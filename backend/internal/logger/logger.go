// Package logger 提供全项目统一的结构化日志出口。
//
// 存在动因：禁止 fmt.Println 散落各处（knowledge-base/global.md [Logging]）。
// 所有日志必须经由本包，以获得统一的 level 控制与生产环境 debug 屏蔽。
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	mu      sync.RWMutex
	current *slog.Logger
)

// Level 是对外暴露的日志级别别名，避免调用方直接 import log/slog。
type Level = slog.Level

const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

// beijing 是全项目统一的展示时区。
// 日志时间戳若落在 UTC，排查线上问题时需要人脑做 +8 换算，极易看错跨零点的事件顺序。
var beijing = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		// 容器镜像缺 tzdata 时的兜底：固定偏移量在中国大陆无夏令时，语义等价
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}()

// Init 按给定级别与输出目标初始化全局 Logger。
// 重复调用是安全的，后一次覆盖前一次（便于测试中切换输出）。
func Init(levelName string, out io.Writer, jsonFormat bool) {
	if out == nil {
		out = os.Stdout
	}
	lvl := ParseLevel(levelName)

	opts := &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// 把时间戳统一改写为 GMT+8 的可读格式
			if a.Key == slog.TimeKey {
				if t, ok := a.Value.Any().(time.Time); ok {
					return slog.String(slog.TimeKey, t.In(beijing).Format("2006-01-02 15:04:05.000"))
				}
			}
			return a
		},
	}

	var h slog.Handler
	if jsonFormat {
		h = slog.NewJSONHandler(out, opts)
	} else {
		h = slog.NewTextHandler(out, opts)
	}

	mu.Lock()
	current = slog.New(h)
	mu.Unlock()
}

// ParseLevel 把配置字符串映射为日志级别，无法识别时回落到 info。
func ParseLevel(name string) Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// L 返回全局 Logger。
//
// 关键保证：即使 Init 从未被调用（典型场景是单元测试直接构造被测组件），
// L 也返回一个可用的 Logger 而非 nil。曾有教训是 WebSocket Hub 的清理路径
// 在未 Init 的测试中直接 nil panic，故此处必须惰性兜底。
func L() *slog.Logger {
	mu.RLock()
	lg := current
	mu.RUnlock()
	if lg != nil {
		return lg
	}

	mu.Lock()
	defer mu.Unlock()
	if current == nil {
		current = slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: LevelError}))
	}
	return current
}

// 以下为便捷入口，全部经由 L() 从而继承 nil 兜底保护。

func Debug(msg string, args ...any) { L().Debug(msg, args...) }
func Info(msg string, args ...any)  { L().Info(msg, args...) }
func Warn(msg string, args ...any)  { L().Warn(msg, args...) }
func Error(msg string, args ...any) { L().Error(msg, args...) }

// With 返回携带固定属性的子 Logger，用于给某个请求或组件打标。
func With(args ...any) *slog.Logger { return L().With(args...) }

// FromContext 预留请求级 Logger 提取点。当前实现返回全局 Logger，
// 中间件注入 request_id 后可在此处升级为按请求取值，调用方代码无需改动。
func FromContext(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if lg, ok := ctx.Value(loggerCtxKey{}).(*slog.Logger); ok && lg != nil {
			return lg
		}
	}
	return L()
}

type loggerCtxKey struct{}

// IntoContext 把子 Logger 存入 context，供 FromContext 提取。
func IntoContext(ctx context.Context, lg *slog.Logger) context.Context {
	if lg == nil {
		return ctx
	}
	return context.WithValue(ctx, loggerCtxKey{}, lg)
}
