// Package ws 提供注水实时通道：一个按冲煮记录分房间的 WebSocket Hub，
// 加一个可选的内置流速模拟器。
//
// 为何需要 Hub 而非让每个连接各自处理：一次冲煮很可能同时开着手机（打点）
// 和平板（看曲线）。任一端打的点必须立刻出现在另一端的曲线上，
// 这要求一个按 brew_id 分组的广播中枢。
package ws

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/alkaid/enjoycoffee/internal/brew"
	"github.com/alkaid/enjoycoffee/internal/config"
	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
	"github.com/alkaid/enjoycoffee/internal/logger"
)

// 连接生命周期参数。
//
// pongWait 必须显著大于 pingPeriod，否则网络抖动导致的一次 pong 延迟
// 就会误杀一条健康连接。3:2 的比例是常见取值。
const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = 40 * time.Second
	maxMessageSize = 8 * 1024
	// sendBuffer 给每个连接的出站队列留余量。写慢的客户端（如切到后台的
	// 手机浏览器）不应阻塞广播循环，队列满了就断开它。
	sendBuffer = 64
)

// PourPersister 是 Hub 需要的持久化能力，由 brew.Service 实现。
//
// 定义窄接口而非直接依赖 *brew.Service：Hub 只需要"追加节点并拿回曲线"
// 这一件事，声明成接口后测试里可以塞一个不碰数据库的假实现。
type PourPersister interface {
	AppendPourEvents(ctx context.Context, brewID int64, incoming []brew.PourEvent) (*brew.PourCurve, int, error)
	Get(ctx context.Context, id int64) (*brew.View, error)
}

// Hub 是全局连接中枢。
type Hub struct {
	persister PourPersister
	mode      config.PourSourceMode

	mu    sync.RWMutex
	rooms map[int64]*room

	// simulators 记录每个房间正在运行的模拟器，保证一个房间最多一个，
	// 否则两个模拟器会同时往同一条曲线上灌互相矛盾的累计值。
	simulators map[int64]*simulator
}

// NewHub 构造 Hub。
func NewHub(p PourPersister, mode config.PourSourceMode) *Hub {
	return &Hub{
		persister:  p,
		mode:       mode,
		rooms:      make(map[int64]*room, 4),
		simulators: make(map[int64]*simulator, 2),
	}
}

// room 是一次冲煮对应的广播域。
type room struct {
	brewID  int64
	mu      sync.RWMutex
	clients map[*client]struct{}
}

// broadcast 向房间内全部连接投递一条消息，并顺手清理跟不上的连接。
//
// 关键约束：绝不在这里关闭 c.send。
//
// 曾经的写法是"队列满就 close(c.send)，让写循环自己发现"。它有一个致命缺陷 ——
// 被关闭的客户端仍留在 r.clients 里，直到它自己的读循环退出才被摘除。
// 在这个窗口里，下一次广播会向已关闭的信道发送，触发 panic
// 并带走整个服务进程。一个锁屏的手机足以让服务端崩溃。
//
// 现在的做法是关闭一个独立的 dead 信道并立刻把客户端摘出房间：
// c.send 永不关闭，因此向它发送永远安全；写循环通过 dead 得知该收尾。
func (r *room) broadcast(msg []byte) {
	var stale []*client

	r.mu.RLock()
	for c := range r.clients {
		select {
		case c.send <- msg:
		default:
			stale = append(stale, c)
		}
	}
	r.mu.RUnlock()

	if len(stale) == 0 {
		return
	}

	r.mu.Lock()
	for _, c := range stale {
		delete(r.clients, c)
	}
	r.mu.Unlock()

	for _, c := range stale {
		logger.Warn("WebSocket 客户端出站队列已满，主动断开", "brew_id", r.brewID)
		c.kill()
	}
}

func (h *Hub) roomFor(brewID int64) *room {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[brewID]; ok {
		return r
	}
	r := &room{brewID: brewID, clients: make(map[*client]struct{}, 2)}
	h.rooms[brewID] = r
	return r
}

func (h *Hub) join(r *room, c *client) {
	r.mu.Lock()
	r.clients[c] = struct{}{}
	n := len(r.clients)
	r.mu.Unlock()
	logger.Info("WebSocket 客户端接入", "brew_id", r.brewID, "clients", n)
}

func (h *Hub) leave(r *room, c *client) {
	r.mu.Lock()
	delete(r.clients, c)
	n := len(r.clients)
	r.mu.Unlock()

	if n > 0 {
		logger.Debug("WebSocket 客户端离开", "brew_id", r.brewID, "clients", n)
		return
	}

	// 房间空了：停掉模拟器并回收房间。不回收的话每次冲煮都会留下一个
	// 空 map，长时间运行后是一处缓慢的内存泄漏。
	h.StopSimulator(r.brewID)
	h.mu.Lock()
	// 二次确认：加锁间隙可能有新客户端进来
	r.mu.RLock()
	empty := len(r.clients) == 0
	r.mu.RUnlock()
	if empty {
		delete(h.rooms, r.brewID)
	}
	h.mu.Unlock()
	logger.Info("WebSocket 房间已回收", "brew_id", r.brewID)
}

// Broadcast 向指定冲煮的全部连接推送一条消息。
func (h *Hub) Broadcast(brewID int64, msg outbound) {
	h.mu.RLock()
	r, ok := h.rooms[brewID]
	h.mu.RUnlock()
	if !ok {
		return
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		logger.Error("WebSocket 消息序列化失败", "brew_id", brewID, "error", err.Error())
		return
	}
	r.broadcast(payload)
}

// BroadcastCurve 把一条新算出的曲线推给房间内所有连接。
//
// 供 HTTP 打点路径调用：两条录入路径（HTTP 补录与 WebSocket 实时）
// 必须都能让所有在线端看到同一条曲线，否则同时开着两个设备时
// 一端打的点在另一端看不见，用户会以为数据丢了。
func (h *Hub) BroadcastCurve(brewID int64, curve *brew.PourCurve, accepted int) {
	if curve == nil {
		return
	}
	h.Broadcast(brewID, outbound{Type: outTypeCurve, Curve: curve, Accepted: accepted})
}

// RoomCount 返回当前活跃房间数，供健康检查与调试端点使用。
func (h *Hub) RoomCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms)
}

// ClientCount 返回某房间的连接数。
func (h *Hub) ClientCount(brewID int64) int {
	h.mu.RLock()
	r, ok := h.rooms[brewID]
	h.mu.RUnlock()
	if !ok {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.clients)
}

// Mode 返回当前注水数据源模式。
func (h *Hub) Mode() config.PourSourceMode { return h.mode }

// inboundSource 决定经 WebSocket 进来的注水节点该记成什么来源。
//
// 这里必须看模式，不能一律记成 MANUAL。device 模式下推流的是智能秤，
// 记成"人手打点"就是让来源字段说谎 —— 而这个字段存在的唯一理由，
// 就是区分"秤量出来的"和"人估出来的"。两者的曲线长得一样，
// 事后复盘时再没有别的线索能还原一条数据究竟是怎么来的。
//
// 模拟器不走这条路：它在 simulator.go 里自己标 SourceSimulator。
// HTTP 的手动打点接口固定记 MANUAL 也是对的 —— 那条路上确实是人在敲。
func (h *Hub) inboundSource() brew.PourSource {
	if h.mode.DeviceIngestEnabled() {
		return brew.SourceDevice
	}
	return brew.SourceManual
}

// ---- 消息协议 ----

// boolPtr 用于给 outbound.SimRunning 这类"必须能表达 false"的字段取址。
func boolPtr(v bool) *bool { return &v }

// inbound 是客户端 → 服务端的消息。
type inbound struct {
	Type string `json:"type"`

	// mark 类型字段。累计注水量走字符串以保持定点数精度契约。
	CumulativeG string `json:"cumulative_g,omitempty"`
	OffsetMs    int    `json:"offset_ms,omitempty"`
	Technique   string `json:"technique,omitempty"`
	// Key 是幂等键。断线重连后客户端会重推它认为可能没送达的点，
	// 有了它服务端可以安全去重而不必猜。
	Key string `json:"key,omitempty"`
}

// outbound 是服务端 → 客户端的消息。
type outbound struct {
	Type string `json:"type"`

	Curve    *brew.PourCurve `json:"curve,omitempty"`
	Accepted int             `json:"accepted,omitempty"`
	Message  string          `json:"message,omitempty"`
	Code     string          `json:"code,omitempty"`
	Mode     string          `json:"mode,omitempty"`
	// ServerTimeMs 让前端秒表能与服务端对齐，避免两侧时钟漂移导致
	// 图上的时间轴和实际打点偏一两秒。
	ServerTimeMs int64 `json:"server_time_ms,omitempty"`
	// SimRunning 是指针而不是 bool：bool 配 omitempty 时，"已停止"
	// 会被序列化成"字段不存在"—— 一条 sim_state 消息恰好在状态为停止时
	// 不带状态，自相矛盾。客户端只能靠"缺失即 false"的约定去猜，
	// 而缺失同样可能是版本不匹配或字段改名。用指针后：sim_state 一定
	// 带上真实值，其它消息类型则照常省略。
	SimRunning *bool `json:"sim_running,omitempty"`
}

const (
	msgTypeMark     = "mark"
	msgTypePing     = "ping"
	msgTypeSimStart = "sim_start"
	msgTypeSimStop  = "sim_stop"
	msgTypeSync     = "sync"

	outTypeCurve = "curve"
	outTypePong  = "pong"
	outTypeError = "error"
	outTypeHello = "hello"
	outTypeSim   = "sim_state"
)

// client 是单条 WebSocket 连接。
type client struct {
	conn *websocket.Conn
	// send 是出站队列。它永不被关闭 —— 见 room.broadcast 的说明。
	send chan []byte
	// dead 在连接应当收尾时被关闭，是唯一的终止信号。
	dead      chan struct{}
	closeOnce sync.Once
}

func newClient(conn *websocket.Conn, buffer int) *client {
	return &client{
		conn: conn,
		send: make(chan []byte, buffer),
		dead: make(chan struct{}),
	}
}

// kill 标记连接应当收尾。可从任意 goroutine 重复调用。
func (c *client) kill() { c.closeOnce.Do(func() { close(c.dead) }) }

// Serve 接管一条已升级的 WebSocket 连接，直到它关闭。
//
// 调用方（HTTP handler）在此之后不得再触碰 ResponseWriter。
func (h *Hub) Serve(ctx context.Context, brewID int64, conn *websocket.Conn) {
	r := h.roomFor(brewID)
	c := newClient(conn, sendBuffer)
	h.join(r, c)

	defer func() {
		h.leave(r, c)
		_ = conn.Close()
	}()

	// 握手消息：告诉前端当前数据源模式与服务端时间基准
	h.unicast(c, outbound{
		Type:         outTypeHello,
		Mode:         string(h.mode),
		ServerTimeMs: domain.Now().UnixMilli(),
		SimRunning:   boolPtr(h.simulatorRunning(brewID)),
	})

	// 立即回放已有曲线：重连的客户端不该看到一张空图，
	// 而应当直接接上它断开前的进度。
	if curve, err := h.currentCurve(ctx, brewID); err == nil && curve != nil {
		h.unicast(c, outbound{Type: outTypeCurve, Curve: curve})
	}

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		h.writeLoop(c)
	}()

	h.readLoop(ctx, brewID, r, c)

	// 读循环退出即连接不可用，通知写循环收尾
	c.kill()
	<-writerDone
}

func (h *Hub) unicast(c *client, msg outbound) {
	payload, err := json.Marshal(msg)
	if err != nil {
		logger.Error("WebSocket 单播序列化失败", "error", err.Error())
		return
	}
	select {
	case c.send <- payload:
	case <-c.dead:
		// 连接已在收尾，丢弃这条消息即可
	default:
		logger.Warn("WebSocket 单播失败：出站队列已满")
	}
}

func (h *Hub) writeLoop(c *client) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-c.dead:
			// 正常收尾：发一个 Close 帧让对端知道这不是网络故障
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			_ = c.conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return

		case msg := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				logger.Debug("WebSocket 写失败", "error", err.Error())
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (h *Hub) readLoop(ctx context.Context, brewID int64, r *room, c *client) {
	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		var in inbound
		if err := c.conn.ReadJSON(&in); err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				logger.Debug("WebSocket 异常关闭", "brew_id", brewID, "error", err.Error())
			}
			return
		}
		// 收到任何业务消息也视为连接活跃
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))

		h.handleInbound(ctx, brewID, r, c, in)
	}
}

func (h *Hub) handleInbound(ctx context.Context, brewID int64, r *room, c *client, in inbound) {
	switch in.Type {
	case msgTypePing:
		h.unicast(c, outbound{Type: outTypePong, ServerTimeMs: domain.Now().UnixMilli()})

	case msgTypeSync:
		if curve, err := h.currentCurve(ctx, brewID); err == nil && curve != nil {
			h.unicast(c, outbound{Type: outTypeCurve, Curve: curve})
		}

	case msgTypeMark:
		h.handleMark(ctx, brewID, c, in)

	case msgTypeSimStart:
		h.handleSimStart(brewID, c)

	case msgTypeSimStop:
		h.StopSimulator(brewID)
		h.Broadcast(brewID, outbound{Type: outTypeSim, SimRunning: boolPtr(false)})

	default:
		h.unicast(c, outbound{
			Type:    outTypeError,
			Code:    "UNKNOWN_MESSAGE_TYPE",
			Message: "不认识的消息类型：" + in.Type,
		})
	}
}

func (h *Hub) handleMark(ctx context.Context, brewID int64, c *client, in inbound) {
	mass, err := fixed.ParseGrams(in.CumulativeG)
	if err != nil {
		h.unicast(c, outbound{
			Type:    outTypeError,
			Code:    "INVALID_CUMULATIVE",
			Message: "累计注水量无法解析：" + in.CumulativeG,
		})
		return
	}

	technique := domain.PourTechnique(in.Technique)
	if !technique.Valid() {
		technique = domain.PourCircle
	}

	ev := brew.PourEvent{
		OffsetMs:       in.OffsetMs,
		CumulativeMg:   mass,
		Technique:      technique,
		Source:         h.inboundSource(),
		IdempotencyKey: in.Key,
	}

	curve, accepted, err := h.persister.AppendPourEvents(ctx, brewID, []brew.PourEvent{ev})
	if err != nil {
		de := domain.AsDomain(err)
		h.unicast(c, outbound{Type: outTypeError, Code: de.Code, Message: de.Message})
		return
	}

	// 广播给房间里所有人（含发起者）：让打点端也用服务端返回的曲线
	// 作为唯一真相，避免本地乐观更新与服务端结果分叉。
	h.Broadcast(brewID, outbound{Type: outTypeCurve, Curve: curve, Accepted: accepted})
}

func (h *Hub) currentCurve(ctx context.Context, brewID int64) (*brew.PourCurve, error) {
	// 传空切片：AppendPourEvents 在没有新节点时只重算并返回现有曲线，
	// 于是"取当前曲线"与"追加后取曲线"走的是同一条代码路径，
	// 两者不可能给出不一致的结果。
	curve, _, err := h.persister.AppendPourEvents(ctx, brewID, nil)
	return curve, err
}
