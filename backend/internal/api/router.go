// Package api 装配 HTTP 路由并把请求翻译成领域调用。
//
// 这一层刻意保持很薄：解码、校验、调服务、编码。任何"如果 A 则 B"的
// 业务判断都属于领域层 —— 放在 handler 里会让它无法被种子数据、
// 未来的导入功能或测试复用。
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/alkaid/enjoycoffee/internal/bean"
	"github.com/alkaid/enjoycoffee/internal/brew"
	"github.com/alkaid/enjoycoffee/internal/config"
	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/flavor"
	"github.com/alkaid/enjoycoffee/internal/flavorscore"
	"github.com/alkaid/enjoycoffee/internal/goldcup"
	"github.com/alkaid/enjoycoffee/internal/httpx"
	"github.com/alkaid/enjoycoffee/internal/logger"
	"github.com/alkaid/enjoycoffee/internal/store"
	"github.com/alkaid/enjoycoffee/internal/ws"
)

// Handlers 持有全部依赖。
type Handlers struct {
	Cfg     config.Config
	DB      *store.DB
	Beans   *bean.Service
	Brews   *brew.Service
	Flavors *flavor.Service
	Scores  *flavorscore.Service
	Engine  *goldcup.Engine
	Configs *store.ConfigRepo
	Hub     *ws.Hub

	startedAt time.Time
}

// NewHandlers 构造 handler 集合。
func NewHandlers(
	cfg config.Config,
	db *store.DB,
	beans *bean.Service,
	brews *brew.Service,
	flavors *flavor.Service,
	scores *flavorscore.Service,
	engine *goldcup.Engine,
	configs *store.ConfigRepo,
	hub *ws.Hub,
) *Handlers {
	return &Handlers{
		Cfg: cfg, DB: db,
		Beans: beans, Brews: brews, Flavors: flavors, Scores: scores,
		Engine: engine, Configs: configs, Hub: hub,
		startedAt: domain.Now(),
	}
}

// slowRequestThreshold 触发慢请求警告的阈值。
//
// 取 200ms 而非 NFR-01 承诺的 10ms：10ms 是风味树内存筛选的指标，
// 而一次完整的 HTTP 请求还包含数据库往返。用 10ms 当阈值会让
// 每条正常的列表请求都变成一条警告日志，警告就失去意义了。
// 风味树本身的耗时由 /flavors/filter 响应里的 elapsed_micros 单独度量。
const slowRequestThreshold = 200 * time.Millisecond

// requestTimeout 单个 HTTP 请求的上限。
//
// WebSocket 路由不套这个中间件 —— 一条长连接的正常寿命远超任何超时值。
const requestTimeout = 15 * time.Second

// 各路由接受的查询参数。清单与 handler 里的 httpx.Query* 读取点一一对应，
// 由 httpx.StrictQuery 强制执行：写错的参数名会当场 400，而不是被静默忽略
// 然后返回一份未经筛选的全量数据。改 handler 的读取点就要改这里，
// router_query_test.go 会盯着两边别走散。
var (
	pageParams = []string{"page", "page_size"}

	beanListParams = append([]string{
		"keyword", "stages", "roast_levels", "flavor_ids", "flavor_match",
		"exact_flavor", "include_archived", "only_opened", "only_unopened", "sort",
	}, pageParams...)

	brewListParams = append([]string{
		"bean_id", "method", "only_gold", "only_measured", "since",
	}, pageParams...)

	flavorFilterParams = []string{"flavor_ids", "match", "exact", "facets"}
	flavorSearchParams = []string{"q", "limit"}
	flavorDeleteParams = []string{"mode"}

	goldcupChartParams = []string{"method", "bean_id"}
	radarWallParams    = []string{"bean_ids"}
)

// Router 装配完整路由树。
func (h *Handlers) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(httpx.Recoverer)
	r.Use(httpx.RequestLogger(slowRequestThreshold))
	r.Use(httpx.SecurityHeaders)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: h.Cfg.CORSOrigins,
		AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Content-Type", "X-Requested-With"},
		ExposedHeaders: []string{"X-Request-ID"},
		MaxAge:         300,
	}))

	// 健康检查放在超时与 no-cache 中间件之外的独立分支：
	// Docker 的 healthcheck 每 5 秒打一次，让它走完整的中间件链
	// 会在日志里刷出大量噪声，掩盖真正的请求。
	r.Get("/api/v1/health", h.health)
	r.Get("/api/v1/ready", h.ready)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(httpx.NoCache)

		// WebSocket 单独一条子路由，绕开超时中间件
		r.Route("/ws", func(r chi.Router) {
			r.Get("/brews/{id}/pour", h.pourSocket)
		})

		r.Group(func(r chi.Router) {
			r.Use(httpx.Timeout(requestTimeout))

			r.Get("/meta", h.meta)

			// ---- 豆库 ----
			r.Route("/beans", func(r chi.Router) {
				r.Get("/", httpx.StrictQuery(beanListParams, h.listBeans))
				r.Post("/", h.createBean)
				r.Get("/board", h.beanBoard)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", h.getBean)
					r.Put("/", h.updateBean)
					r.Delete("/", h.deleteBean)
					r.Post("/consume", h.consumeBean)
					r.Put("/flavors", h.setBeanFlavors)
				})
			})

			// ---- 萃取记录 ----
			r.Route("/brews", func(r chi.Router) {
				r.Get("/", httpx.StrictQuery(brewListParams, h.listBrews))
				r.Post("/", h.createBrew)
				r.Post("/preview", h.previewBrew)
				r.Route("/{id}", func(r chi.Router) {
					r.Get("/", h.getBrew)
					r.Put("/", h.updateBrew)
					r.Delete("/", h.deleteBrew)
					r.Post("/pour", h.appendPourEvents)
					r.Get("/score", h.getScore)
					r.Put("/score", h.saveScore)
					r.Delete("/score", h.deleteScore)
					r.Post("/simulate", h.startSimulation)
					r.Delete("/simulate", h.stopSimulation)
				})
			})

			// ---- 风味树 ----
			r.Route("/flavors", func(r chi.Router) {
				r.Get("/tree", h.flavorTree)
				r.Get("/filter", httpx.StrictQuery(flavorFilterParams, h.flavorFilter))
				r.Get("/search", httpx.StrictQuery(flavorSearchParams, h.flavorSearch))
				r.Post("/nodes", h.createFlavorNode)
				r.Route("/nodes/{id}", func(r chi.Router) {
					r.Patch("/", h.updateFlavorNode)
					r.Post("/move", h.moveFlavorNode)
					r.Delete("/", httpx.StrictQuery(flavorDeleteParams, h.deleteFlavorNode))
				})
			})

			// ---- 金杯引擎 ----
			r.Route("/goldcup", func(r chi.Router) {
				r.Get("/profiles", h.goldcupProfiles)
				r.Put("/profiles/{method}", h.saveGoldcupProfile)
				r.Delete("/profiles/{method}", h.resetGoldcupProfile)
				r.Get("/chart", httpx.StrictQuery(goldcupChartParams, h.goldcupChart))
				r.Post("/solve", h.solve)
			})

			// ---- 风味雷达墙 ----
			r.Get("/radar/wall", httpx.StrictQuery(radarWallParams, h.radarWall))
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, r, domain.NotFound("接口", r.Method+" "+r.URL.Path))
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(w, r, domain.Validation("METHOD_NOT_ALLOWED",
			r.Method+" 不被该路径支持"))
	})

	return r
}

// health 是存活探针：只要进程能响应就算通过。
//
// 刻意不查数据库：存活探针的语义是"这个进程还活着吗"，
// 把数据库拖进来会让一次短暂的数据库抖动导致容器被重启，
// 而重启一个健康的进程解决不了数据库的问题。数据库状态归 /ready 管。
func (h *Handlers) health(w http.ResponseWriter, r *http.Request) {
	httpx.OK(w, map[string]any{
		"status":     "ok",
		"service":    "enjoycoffee-backend",
		"env":        h.Cfg.Env,
		"time":       domain.Now().Format(time.RFC3339),
		"uptime_sec": int(time.Since(h.startedAt).Seconds()),
	})
}

// ready 是就绪探针：数据库可达、风味树已装载才算就绪。
func (h *Handlers) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	dbOK := true
	dbDetail := "ok"
	if err := h.DB.Pool.Ping(ctx); err != nil {
		dbOK = false
		dbDetail = "数据库不可达"
		logger.Warn("就绪探针：数据库 ping 失败", "error", err.Error())
	}

	snap := h.Flavors.Snapshot()
	treeOK := snap != nil && snap.NodeCount() > 0

	payload := map[string]any{
		"database":    map[string]any{"ok": dbOK, "detail": dbDetail},
		"flavor_tree": map[string]any{"ok": treeOK, "nodes": snapNodeCount(snap)},
		"ws_rooms":    h.Hub.RoomCount(),
		"pour_source": string(h.Hub.Mode()),
		"time":        domain.Now().Format(time.RFC3339),
	}

	if !dbOK || !treeOK {
		// 就绪失败要给 503，让编排系统把流量挪走 —— 但响应体仍要带上
		// 完整的诊断信息，否则运维只知道"没就绪"却不知道是哪一项没起来。
		httpx.FailWithPayload(w, http.StatusServiceUnavailable,
			"NOT_READY", "服务尚未就绪", payload)
		return
	}
	httpx.OK(w, payload)
}

func snapNodeCount(s *flavor.Snapshot) int {
	if s == nil {
		return 0
	}
	return s.NodeCount()
}

// reloadEngineProfiles 在配置变更后热更新引擎的金杯标准。
//
// 不重启服务而是原子替换引擎的 profile 表：用户在设置面板点一下保存
// 就要重启容器是不可接受的。
func (h *Handlers) reloadEngineProfiles(ctx context.Context) error {
	overrides, err := h.Configs.Load(ctx)
	if err != nil {
		return err
	}
	h.Engine.SetProfiles(overrides)
	logger.Info("金杯标准已热更新", "overrides", len(overrides))
	return nil
}

// lifecycleWindows 把各养豆行为组的窗口参数暴露给前端，用于绘制进度条刻度。
//
// 只返回三组去重后的窗口，并另附「六档烘焙度 → 行为组」映射：
// 若让前端自己推导这个映射，就等于把一条业务规则复制到了前端，
// 后端调整分组边界时前端会静默地跟不上。
func (h *Handlers) lifecycleWindows() []bean.Window {
	levels := domain.AllRoastLevels()
	out := make([]bean.Window, 0, 3)
	seen := make(map[domain.RoastBand]struct{}, 3)
	for _, lv := range levels {
		wnd := bean.WindowFor(lv)
		if _, dup := seen[wnd.Band]; dup {
			continue
		}
		seen[wnd.Band] = struct{}{}
		out = append(out, wnd)
	}
	return out
}

func roastLevelBands() []map[string]string {
	levels := domain.AllRoastLevels()
	out := make([]map[string]string, 0, len(levels))
	for _, lv := range levels {
		out = append(out, map[string]string{
			"roast_level": string(lv),
			"band":        string(lv.Band()),
		})
	}
	return out
}

// chiParam 读取路径参数原文，不做数字解析。
func chiParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}
