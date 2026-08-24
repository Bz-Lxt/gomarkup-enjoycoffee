// Command server 是 EnjoyCoffee 后端的唯一入口。
//
// 它只做三件事：装配依赖、启动 HTTP、优雅关闭。任何业务判断都不属于这里。
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alkaid/enjoycoffee/internal/api"
	"github.com/alkaid/enjoycoffee/internal/bean"
	"github.com/alkaid/enjoycoffee/internal/brew"
	"github.com/alkaid/enjoycoffee/internal/config"
	"github.com/alkaid/enjoycoffee/internal/flavor"
	"github.com/alkaid/enjoycoffee/internal/flavorscore"
	"github.com/alkaid/enjoycoffee/internal/goldcup"
	"github.com/alkaid/enjoycoffee/internal/logger"
	"github.com/alkaid/enjoycoffee/internal/store"
	"github.com/alkaid/enjoycoffee/internal/ws"
)

func main() {
	// 启动失败必须以非零码退出，否则 Docker 会认为容器正常结束而不重启。
	if err := run(); err != nil {
		logger.Error("服务启动失败", "error", err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		// 配置加载失败发生在 logger 初始化之前，只能用标准库兜底
		return err
	}

	logger.Init(cfg.LogLevel, os.Stdout, cfg.LogJSON)
	logger.Info("EnjoyCoffee 后端启动中",
		"env", cfg.Env,
		"addr", cfg.HTTPAddr,
		"pour_source", string(cfg.PourSource),
		"seed_demo", cfg.SeedDemoData)

	// 信号监听要在任何耗时初始化之前就位：数据库起得慢的时候，
	// 用户按 Ctrl-C 应当能立刻退出，而不是干等到连接超时。
	rootCtx, stopSignals := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	db, err := store.Open(rootCtx, cfg.DatabaseURL, cfg.DBMaxConns, cfg.DBConnTimeout)
	if err != nil {
		return err
	}
	defer db.Close()

	if cfg.MigrateOnStart {
		if err := store.Migrate(rootCtx, db); err != nil {
			return err
		}
	}

	app, err := wire(rootCtx, *cfg, db)
	if err != nil {
		return err
	}
	defer app.hub.StopAll()
	defer app.flavorCache.Close()

	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: app.handlers.Router(),
		// ReadHeaderTimeout 防慢速头部攻击。刻意不设 ReadTimeout /
		// WriteTimeout：它们会无差别地掐掉 WebSocket 长连接，
		// 而本项目的注水通道正常就要开几分钟。单个 HTTP 请求的时长
		// 由 httpx.Timeout 中间件按路由控制，粒度比服务器全局设置更合适。
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("HTTP 服务开始监听", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-rootCtx.Done():
		logger.Info("收到终止信号，开始优雅关闭")
	}

	// 关闭超时用独立的 context.Background()：rootCtx 此刻已经被信号取消，
	// 拿它去做 Shutdown 会让所有在途请求立刻被掐断，"优雅"二字就没了。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	// 先停模拟器再关 HTTP：模拟器还在往数据库写，而 Shutdown 会等
	// WebSocket 连接自然结束 —— 一个还在跑的模拟器能让这个等待
	// 一直持续到它的计划表走完。
	app.hub.StopAll()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("优雅关闭超时，强制结束", "error", err.Error())
		_ = srv.Close()
	}
	logger.Info("服务已停止")
	return nil
}

// application 汇总装配后的组件，仅供 run 内部收尾使用。
type application struct {
	handlers    *api.Handlers
	hub         *ws.Hub
	flavorCache *flavor.Cache
}

// wire 手工装配依赖图。
//
// 不引入 wire / dig 之类的注入框架：这个依赖图只有十来个节点，
// 手写出来一眼能看懂谁依赖谁，而框架会把这个信息藏进生成代码或反射里。
//
// 装配顺序被跨域依赖约束着，不能随意调整：
//
//	风味缓存 → 风味服务 → 豆库服务 → 萃取服务 → 评分服务，
//
// 其中豆库与萃取、萃取与评分之间是双向依赖（豆库要展示雷达图，
// 萃取要扣减豆量），因此用 setter 打破循环，而非在构造函数里互相传引用。
func wire(ctx context.Context, cfg config.Config, db *store.DB) (*application, error) {
	// ---- 仓储 ----
	flavorRepo := store.NewFlavorRepo(db)
	beanRepo := store.NewBeanRepo(db)
	brewRepo := store.NewBrewRepo(db)
	scoreRepo := store.NewScoreRepo(db)
	configRepo := store.NewConfigRepo(db)

	// ---- 风味树缓存 ----
	// 首次装载失败要让启动失败：一棵空树会让所有筛选静默返回零结果，
	// 用户看到的是"我的豆子都不见了"，而不是一条错误。
	flavorCache, err := flavor.NewCache(ctx, flavorRepo, cfg.FlavorCacheRefresh)
	if err != nil {
		return nil, err
	}
	flavorCache.StartRefreshLoop(ctx)
	flavorSvc := flavor.NewService(flavorRepo, flavorCache)

	// ---- 金杯引擎 ----
	overrides, cerr := configRepo.Load(ctx)
	if cerr != nil {
		return nil, cerr
	}
	engine := goldcup.NewEngine(overrides)

	// ---- 评分服务 ----
	scoreSvc := flavorscore.NewService(scoreRepo)

	// ---- 豆库与萃取（互相依赖，用两步装配打破循环）----
	beanSvc := bean.NewService(beanRepo, flavorSvc, scoreSvc, nil)
	brewSvc := brew.NewService(brewRepo, engine, beanSvc, scoreSvc)
	beanSvc.SetBrewStats(brewSvc)

	// ---- WebSocket Hub ----
	hub := ws.NewHub(brewSvc, cfg.PourSource)

	// ---- 种子数据 ----
	seeder := store.NewSeeder(flavorRepo, flavorSvc, beanSvc, brewSvc, scoreSvc)
	if err := seeder.Run(ctx, cfg.SeedDemoData); err != nil {
		return nil, err
	}

	handlers := api.NewHandlers(cfg, db, beanSvc, brewSvc, flavorSvc, scoreSvc, engine, configRepo, hub)

	return &application{
		handlers:    handlers,
		hub:         hub,
		flavorCache: flavorCache,
	}, nil
}
