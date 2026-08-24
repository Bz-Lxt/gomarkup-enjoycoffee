package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/alkaid/enjoycoffee/internal/brew"
	"github.com/alkaid/enjoycoffee/internal/httpx"
	"github.com/alkaid/enjoycoffee/internal/logger"
)

// upgrader 的 CheckOrigin 必须显式实现。
//
// 不能简单地 return true：同源策略不适用于 WebSocket 握手，那等于
// 关闭了 CSRF 防护，任何网站都能向本机的 WS 端点发起连接。也不能用
// gorilla 的默认实现 —— 它要求 Origin 与 Host 完全一致，开发期前端
// 与后端端口不同就会被全部拒掉。
//
// 判定顺序是「同源 → 白名单 → 本机任意端口」，其中同源这一条是主路径：
//
// 自从前端改成经 nginx 同源代理（location /api/v1/ws/），浏览器发起的
// 握手里 Origin 与 Host 天然相同，所以"同源即放行"覆盖了全部正常访问，
// 且不需要预先知道部署在哪个主机哪个端口上。
//
// 这一条是补上一个真实缺陷：在此之前只认 CORS 白名单加 localhost，
// 于是从局域网 IP 访问（比如把手机架在电子秤旁边看实时流速 —— 恰恰是
// 这个功能最典型的用法）会被静默拒绝，用户只看到一条画不出来的曲线，
// 拒绝原因只留在服务端日志里。同源代理改造本该消掉"把主机和端口写进
// 配置"这个耦合，当时漏掉了 WS 这一处。
//
// 安全性没有放松：第三方站点的 Origin 永远不等于本服务的 Host，
// 因此跨站发起的握手依旧被拒。
func (h *Handlers) newUpgrader() *websocket.Upgrader {
	allowed := make(map[string]struct{}, len(h.Cfg.CORSOrigins))
	wildcard := false
	for _, o := range h.Cfg.CORSOrigins {
		if o == "*" {
			wildcard = true
			continue
		}
		allowed[strings.ToLower(strings.TrimRight(o, "/"))] = struct{}{}
	}

	return &websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 4096,
		// 注水曲线的 JSON 有几 KB，压缩能显著减少手机热点下的流量
		EnableCompression: true,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				// 非浏览器客户端（Playwright 的 API 测试、curl、
				// 未来的设备网关）不发 Origin。它们不受同源策略约束，
				// 因此 Origin 缺失本身不是攻击信号。
				return true
			}
			if wildcard {
				return true
			}

			// 同源即放行。经 nginx 同源代理进来的握手全部走这一条。
			// 比较的是 host:port 整体，因为端口不同就是不同源。
			if sameOrigin(origin, r.Host) {
				return true
			}

			if _, ok := allowed[strings.ToLower(strings.TrimRight(origin, "/"))]; ok {
				return true
			}

			// 放行 localhost / 127.0.0.1 的任意端口：本项目是本地部署，
			// 开发期前端直连后端时两侧端口不同，把每个端口都写进白名单不现实。
			if u, err := url.Parse(origin); err == nil {
				host := u.Hostname()
				if host == "localhost" || host == "127.0.0.1" || host == "::1" {
					return true
				}
			}

			logger.Warn("WebSocket 握手被拒：Origin 既非同源也不在白名单",
				"origin", origin, "host", r.Host)
			return false
		},
	}
}

// sameOrigin 判断 Origin 头与请求的 Host 是否指向同一来源。
//
// 只比 host:port，不比 scheme：反向代理后面拿不到浏览器侧的真实 scheme
// （Host 头里也不含 scheme），拿 X-Forwarded-Proto 去比会把没配这个头的
// 部署全部拒掉。降级的风险很小 —— 攻击者要利用 scheme 不匹配，得先能在
// 同一个 host:port 上以另一种协议提供服务，那时已经是中间人了。
func sameOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

// pourSocket 升级连接并交给 Hub。
func (h *Handlers) pourSocket(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	// 先确认冲煮记录存在再升级。升级之后就只能通过 WebSocket 的
	// Close 帧报错了，而浏览器对握手后立刻关闭的连接给不出有用的信息，
	// 前端只会看到一个语焉不详的 "connection closed"。
	if _, err := h.Brews.Get(r.Context(), id); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	conn, err := h.newUpgrader().Upgrade(w, r, nil)
	if err != nil {
		// Upgrade 失败时它已经自行写过响应，这里只记录
		logger.Warn("WebSocket 升级失败", "brew_id", id, "error", err.Error())
		return
	}

	// 用 context.WithoutCancel 语义：连接的生命周期由 Hub 的读写循环控制，
	// 而不是由这个 handler 的返回控制。传 r.Context() 会在 handler 返回时
	// 立刻取消，导致所有数据库写入失败。
	h.Hub.Serve(detachContext(r), id, conn)
}

// startSimulation 通过 HTTP 启动模拟注水。
//
// 与 WebSocket 的 sim_start 消息等价，存在的理由是让 QA 的 API 冒烟测试
// 能在不建立 WebSocket 连接的情况下触发模拟器。
func (h *Handlers) startSimulation(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if _, err := h.Brews.Get(r.Context(), id); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	if err := h.Hub.StartSimulator(id); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.OK(w, map[string]any{
		"brew_id":     id,
		"sim_running": true,
		"mode":        string(h.Hub.Mode()),
		"message":     "模拟注水已启动。连接 WebSocket 或轮询记录详情可看到曲线逐步生成。",
	})
}

func (h *Handlers) stopSimulation(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	h.Hub.StopSimulator(id)
	httpx.OK(w, map[string]any{"brew_id": id, "sim_running": false})
}

// broadcastCurve 让 HTTP 打点的结果也推给 WebSocket 端。
func (h *Handlers) broadcastCurve(brewID int64, curve *brew.PourCurve, accepted int) {
	h.Hub.BroadcastCurve(brewID, curve, accepted)
}

// detachContext 剥离请求 context 的取消信号，保留其携带的值。
//
// WebSocket 连接的寿命远超发起升级的那次 HTTP handler 调用。
// 直接用 r.Context() 会让连接在 handler 返回的瞬间失去有效 context，
// 后续每一次数据库写入都会以 context canceled 失败 —— 而且症状很隐蔽：
// 连接看起来是通的，只是打的点全部存不进去。
func detachContext(r *http.Request) context.Context {
	return context.WithoutCancel(r.Context())
}
