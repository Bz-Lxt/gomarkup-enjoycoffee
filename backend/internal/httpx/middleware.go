package httpx

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/alkaid/enjoycoffee/internal/logger"
)

// requestCounter 生成进程内递增的请求序号，用于把一次请求的多条日志串起来。
var requestCounter atomic.Uint64

// recorder 包裹 ResponseWriter 以捕获状态码与响应字节数。
//
// 它必须实现 http.Hijacker：WebSocket 升级需要拿到底层 TCP 连接，
// 而一个只实现了 ResponseWriter 的包装器会让 gorilla/websocket 的
// Upgrade 直接失败并报 "response does not implement http.Hijacker"。
// 这是给 ResponseWriter 套装饰器时最容易踩的坑 —— 中间件加上去之后
// HTTP 一切正常，唯独 WebSocket 挂掉。
type recorder struct {
	http.ResponseWriter
	status  int
	written int64
	// hijacked 记录连接是否已被劫持。被劫持后底层连接已交给 WebSocket 层，
	// 再往 ResponseWriter 写任何东西都是错误的。
	hijacked bool
}

func (rec *recorder) WriteHeader(code int) {
	if rec.status != 0 {
		// 重复 WriteHeader 是 bug，标准库会打印警告。这里主动拦一下，
		// 避免第二次调用污染已记录的状态码。
		return
	}
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

func (rec *recorder) Write(b []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	n, err := rec.ResponseWriter.Write(b)
	rec.written += int64(n)
	return n, err
}

func (rec *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := rec.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("底层 ResponseWriter 不支持 Hijack")
	}
	conn, rw, err := h.Hijack()
	if err == nil {
		rec.hijacked = true
		rec.status = http.StatusSwitchingProtocols
	}
	return conn, rw, err
}

// Flush 透传给底层，供 SSE 或流式响应使用。
func (rec *recorder) Flush() {
	if f, ok := rec.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// RequestLogger 记录每个请求的方法、路径、状态码与耗时。
//
// 慢请求单独用 warn 级别标出：本项目对风味树筛选承诺了 P99 ≤ 10ms，
// 一条明显的慢日志比事后翻监控更容易发现回归。
func RequestLogger(slowThreshold time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			reqID := strconv.FormatUint(requestCounter.Add(1), 10)
			rec := &recorder{ResponseWriter: w}

			w.Header().Set("X-Request-ID", reqID)
			next.ServeHTTP(rec, r)

			elapsed := time.Since(start)
			if rec.status == 0 {
				rec.status = http.StatusOK
			}

			attrs := []any{
				"req_id", reqID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"bytes", rec.written,
				"took_ms", elapsed.Milliseconds(),
			}
			if q := r.URL.RawQuery; q != "" {
				attrs = append(attrs, "query", q)
			}

			switch {
			case rec.hijacked:
				logger.Info("WebSocket 连接已关闭", attrs...)
			case rec.status >= 500:
				logger.Error("请求返回服务端错误", attrs...)
			case elapsed >= slowThreshold:
				logger.Warn("慢请求", attrs...)
			default:
				logger.Debug("请求完成", attrs...)
			}
		})
	}
}

// Recoverer 捕获 handler panic，转为 500 而非让整个进程退出。
//
// 单用户本地部署下，一个 nil 指针不该让用户正在记录的那杯咖啡连同
// 整个服务一起消失。堆栈进日志，响应里只留一句人话。
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rv := recover()
			if rv == nil {
				return
			}
			// ErrAbortHandler 是标准库约定的"静默放弃"信号，
			// 由 httputil 反向代理等组件使用，不应记为故障。
			if rv == http.ErrAbortHandler {
				panic(rv)
			}

			logger.Error("handler 发生 panic",
				"method", r.Method,
				"path", r.URL.Path,
				"panic", rv,
				"stack", string(debug.Stack()))

			// 若 handler 在 panic 前已写出响应头，再写会被标准库忽略，
			// 这里的尝试是无害的
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"ok":false,"error":{"kind":"INTERNAL","code":"PANIC","message":"服务内部错误，请稍后重试"}}`))
		}()
		next.ServeHTTP(w, r)
	})
}

// Timeout 给请求上下文加超时。
//
// 刻意不使用 chi 的 middleware.Timeout：它会在超时后替换 ResponseWriter，
// 与 WebSocket 的长连接语义冲突。这里只设 context deadline，
// 由下游的数据库查询自行响应取消，WebSocket 路由则整体绕开本中间件。
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// NoCache 对 API 路由统一禁用缓存。
func NoCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		next.ServeHTTP(w, r)
	})
}

// SecurityHeaders 添加基础安全响应头。
//
// 本项目是本地单用户部署，但这些头的成本几乎为零，
// 而一旦有人把它暴露到公网，它们就是第一道防线。
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
