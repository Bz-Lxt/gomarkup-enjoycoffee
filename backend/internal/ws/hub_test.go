package ws

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alkaid/enjoycoffee/internal/brew"
	"github.com/alkaid/enjoycoffee/internal/config"
	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

// fakePersister 是不碰数据库的假实现。它把收到的节点存在内存里，
// 并复用真实的合并与分析逻辑 —— 这样测的是 Hub 的行为，
// 而合并语义仍与生产一致。
type fakePersister struct {
	mu       sync.Mutex
	events   map[int64][]brew.PourEvent
	calls    int
	failWith error
}

func newFakePersister() *fakePersister {
	return &fakePersister{events: make(map[int64][]brew.PourEvent)}
}

func (f *fakePersister) AppendPourEvents(_ context.Context, brewID int64,
	incoming []brew.PourEvent) (*brew.PourCurve, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failWith != nil {
		return nil, 0, f.failWith
	}
	before := len(f.events[brewID])
	f.events[brewID] = brew.MergePourEvents(f.events[brewID], incoming)
	accepted := len(f.events[brewID]) - before
	curve := brew.AnalyzePourCurve(f.events[brewID], 20000)
	return &curve, accepted, nil
}

func (f *fakePersister) Get(_ context.Context, id int64) (*brew.View, error) {
	return &brew.View{ID: id}, nil
}

func (f *fakePersister) count(brewID int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events[brewID])
}

func mustGrams(t *testing.T, s string) fixed.Mass {
	t.Helper()
	m, err := fixed.ParseGrams(s)
	if err != nil {
		t.Fatalf("解析 %q 失败: %v", s, err)
	}
	return m
}

// TestRoomIsCreatedOnceUnderConcurrentJoins 验证房间创建的竞态安全。
//
// 多个设备同时连上同一次冲煮是这个功能的主场景（手机计时、平板看曲线）。
// 若 roomFor 存在检查-创建的竞态，两台设备会各自拿到一个不同的房间对象，
// 于是彼此的打点都广播不到对方 —— 而单机测试永远发现不了。
func TestRoomIsCreatedOnceUnderConcurrentJoins(t *testing.T) {
	h := NewHub(newFakePersister(), config.PourSourceManual)

	const goroutines = 64
	var wg sync.WaitGroup
	rooms := make([]*room, goroutines)

	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			rooms[idx] = h.roomFor(42)
		}(i)
	}
	close(start)
	wg.Wait()

	for i := 1; i < goroutines; i++ {
		if rooms[i] != rooms[0] {
			t.Fatalf("并发请求同一冲煮的房间时拿到了不同实例（第 %d 个）。"+
				"两台设备会落进不同房间，互相看不到对方的打点", i)
		}
	}
	if h.RoomCount() != 1 {
		t.Errorf("应只创建 1 个房间，实际 %d 个", h.RoomCount())
	}
}

// TestRoomsAreIsolatedPerBrew 确认不同冲煮的广播互不串台。
func TestRoomsAreIsolatedPerBrew(t *testing.T) {
	h := NewHub(newFakePersister(), config.PourSourceManual)

	a := h.roomFor(1)
	b := h.roomFor(2)
	if a == b {
		t.Fatal("不同冲煮必须落在不同房间，否则 A 的曲线会推给正在冲 B 的用户")
	}

	ca := newTestClient(8)
	cb := newTestClient(8)
	h.join(a, ca)
	h.join(b, cb)

	if h.ClientCount(1) != 1 || h.ClientCount(2) != 1 {
		t.Errorf("两个房间各应有 1 个客户端，实际 %d / %d",
			h.ClientCount(1), h.ClientCount(2))
	}

	h.Broadcast(1, outbound{Type: outTypeSim, SimRunning: boolPtr(true)})

	if len(ca.send) != 1 {
		t.Errorf("房间 1 的客户端应收到 1 条消息，实际 %d 条", len(ca.send))
	}
	if len(cb.send) != 0 {
		t.Errorf("房间 2 的客户端不应收到房间 1 的广播，实际收到 %d 条", len(cb.send))
	}
}

// TestBroadcastReachesEverySubscriber 验证广播覆盖房间内全部连接。
//
// 打点端也在接收列表里（服务端返回的曲线是唯一真相），
// 所以这里的期望是 N 个客户端全部收到，没有例外。
func TestBroadcastReachesEverySubscriber(t *testing.T) {
	h := NewHub(newFakePersister(), config.PourSourceManual)
	r := h.roomFor(7)

	const n = 16
	clients := make([]*client, n)
	for i := range clients {
		clients[i] = newTestClient(4)
		h.join(r, clients[i])
	}

	h.Broadcast(7, outbound{Type: outTypePong, ServerTimeMs: 1})

	for i, c := range clients {
		if len(c.send) != 1 {
			t.Errorf("第 %d 个客户端应收到广播，实际队列里 %d 条", i, len(c.send))
		}
	}
}

// TestSlowClientIsDroppedWithoutBlockingOthers 是背压处理的关键。
//
// 一个卡住的客户端（手机锁屏、网络挂起）会让它的出站队列填满。
// 若广播在它上面阻塞，整个房间的推送都会停 —— 一个坏客户端拖垮所有人。
// 正确行为是断开它，让其他人继续。
func TestSlowClientIsDroppedWithoutBlockingOthers(t *testing.T) {
	h := NewHub(newFakePersister(), config.PourSourceManual)
	r := h.roomFor(9)

	// 队列容量 1 且已被填满 —— 模拟一个完全不读的客户端
	stuck := newTestClient(1)
	stuck.send <- []byte("occupied")

	healthy := newTestClient(8)
	h.join(r, stuck)
	h.join(r, healthy)

	done := make(chan struct{})
	go func() {
		h.Broadcast(9, outbound{Type: outTypePong, ServerTimeMs: 1})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("广播被一个卡住的客户端阻塞了。一台锁屏的手机就能让" +
			"整个房间的实时曲线停止推送")
	}

	if len(healthy.send) != 1 {
		t.Errorf("健康客户端应正常收到消息，实际队列 %d 条", len(healthy.send))
	}

	// 卡住的那个应被判死并摘出房间
	select {
	case <-stuck.dead:
	default:
		t.Error("队列满的客户端应被 Broadcast 判死，写循环才知道该收尾")
	}
	if h.ClientCount(9) != 1 {
		t.Errorf("跟不上的客户端应被立刻摘出房间，只留 1 个健康连接，"+
			"实际房间内 %d 个。留在房间里的死连接会让下一次广播"+
			"向已关闭的信道发送", h.ClientCount(9))
	}

	// 再广播一次：这一步在修复前会 panic（send on closed channel）
	h.Broadcast(9, outbound{Type: outTypePong, ServerTimeMs: 2})
	if len(healthy.send) != 2 {
		t.Errorf("摘除死连接后广播应继续正常工作，健康客户端队列应有 2 条，实际 %d 条",
			len(healthy.send))
	}
}

// TestEmptyRoomIsReclaimed 确认房间会被回收。
//
// 每次冲煮都建一个房间，不回收的话长时间运行的服务端会累积成千上万个
// 空 map。这不是会立刻出问题的 bug，而是那种跑一个月后才被发现的内存增长。
func TestEmptyRoomIsReclaimed(t *testing.T) {
	h := NewHub(newFakePersister(), config.PourSourceManual)
	r := h.roomFor(11)
	c := newTestClient(4)

	h.join(r, c)
	if h.RoomCount() != 1 {
		t.Fatalf("加入后应有 1 个房间，实际 %d", h.RoomCount())
	}

	h.leave(r, c)
	if h.RoomCount() != 0 {
		t.Errorf("最后一个客户端离开后房间应被回收，实际仍有 %d 个", h.RoomCount())
	}
	if h.ClientCount(11) != 0 {
		t.Errorf("已回收房间的客户端数应为 0，实际 %d", h.ClientCount(11))
	}
}

// TestRoomSurvivesIfSomeoneStays 确认还有人在时不会误回收。
func TestRoomSurvivesIfSomeoneStays(t *testing.T) {
	h := NewHub(newFakePersister(), config.PourSourceManual)
	r := h.roomFor(13)

	leaving := newTestClient(4)
	staying := newTestClient(4)
	h.join(r, leaving)
	h.join(r, staying)

	h.leave(r, leaving)

	if h.RoomCount() != 1 {
		t.Errorf("仍有 1 个客户端在线，房间不应被回收，实际房间数 %d", h.RoomCount())
	}
	// 回收后仍要能收到广播
	h.Broadcast(13, outbound{Type: outTypePong})
	if len(staying.send) != 1 {
		t.Error("留下的客户端应仍能收到广播")
	}
}

// TestBroadcastToUnknownRoomIsNoop 确认向不存在的房间广播不 panic 也不建房。
//
// HTTP 补录路径会无条件调 BroadcastCurve。如果那次冲煮当前没人在线
// （最常见的情况），这一步必须安静地什么都不做，而不是凭空建出一个空房间。
func TestBroadcastToUnknownRoomIsNoop(t *testing.T) {
	h := NewHub(newFakePersister(), config.PourSourceManual)

	h.Broadcast(999, outbound{Type: outTypePong})
	curve := brew.AnalyzePourCurve(nil, 0)
	h.BroadcastCurve(999, &curve, 0)
	h.BroadcastCurve(999, nil, 0) // nil 曲线也不能 panic

	if h.RoomCount() != 0 {
		t.Errorf("向无人房间广播不应创建房间，实际房间数 %d", h.RoomCount())
	}
}

// TestConcurrentMarksAllLandOnOneCurve 是实时打点的核心并发场景。
//
// 手机和平板同时打点，两条路径都会走 handleMark。若合并不是幂等的，
// 或者房间状态有竞态，会出现节点丢失或重复。这里用不同的幂等键模拟
// 真正不同的打点，期望全部落库。
func TestConcurrentMarksAllLandOnOneCurve(t *testing.T) {
	p := newFakePersister()
	h := NewHub(p, config.PourSourceManual)
	r := h.roomFor(21)
	c := newTestClient(512)
	h.join(r, c)

	const marks = 40
	var wg sync.WaitGroup
	for i := 0; i < marks; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h.handleMark(context.Background(), 21, c, inbound{
				Type:        msgTypeMark,
				OffsetMs:    i * 1000,
				CumulativeG: "10",
				Key:         "k" + itoaTest(i),
			})
		}(i)
	}
	wg.Wait()

	if got := p.count(21); got != marks {
		t.Errorf("%d 次并发打点（各自不同的幂等键）应全部落库，实际 %d 条",
			marks, got)
	}
}

// TestDuplicateMarksAreMergedNotAppended 验证重连续传不会产生重复点。
func TestDuplicateMarksAreMergedNotAppended(t *testing.T) {
	p := newFakePersister()
	h := NewHub(p, config.PourSourceManual)
	r := h.roomFor(23)
	c := newTestClient(64)
	h.join(r, c)

	// 同一条打点重发 10 次（弱网下的典型行为）
	for i := 0; i < 10; i++ {
		h.handleMark(context.Background(), 23, c, inbound{
			Type:        msgTypeMark,
			OffsetMs:    5000,
			CumulativeG: "60",
			Key:         "same-key",
		})
	}

	if got := p.count(23); got != 1 {
		t.Errorf("同一幂等键重发 10 次应只留 1 条，实际 %d 条。"+
			"重复点会让曲线上出现重叠的采样", got)
	}
}

// TestInvalidCumulativeIsRejectedToSenderOnly 确认解析失败只回给发起者。
//
// 一个客户端发了脏数据，不该让房间里其他人看到一条错误消息 ——
// 他们什么都没做错。
func TestInvalidCumulativeIsRejectedToSenderOnly(t *testing.T) {
	p := newFakePersister()
	h := NewHub(p, config.PourSourceManual)
	r := h.roomFor(25)

	sender := newTestClient(8)
	bystander := newTestClient(8)
	h.join(r, sender)
	h.join(r, bystander)

	h.handleMark(context.Background(), 25, sender, inbound{
		Type:        msgTypeMark,
		OffsetMs:    1000,
		CumulativeG: "六十克",
	})

	if len(sender.send) != 1 {
		t.Errorf("发起者应收到 1 条错误反馈，实际 %d 条", len(sender.send))
	}
	if len(bystander.send) != 0 {
		t.Errorf("旁观者不应收到别人的错误消息，实际 %d 条", len(bystander.send))
	}
	if p.count(25) != 0 {
		t.Error("解析失败的打点不应落库")
	}
}

// TestUnknownTechniqueFallsBackInsteadOfRejecting 确认未知手法标签走兜底。
//
// 注水手法是个描述性标签，不是业务约束。因为一个拼错的标签就丢掉整条
// 打点数据是不成比例的 —— 用户丢的是冲煮过程中一去不返的时刻。
func TestUnknownTechniqueFallsBackInsteadOfRejecting(t *testing.T) {
	p := newFakePersister()
	h := NewHub(p, config.PourSourceManual)
	r := h.roomFor(27)
	c := newTestClient(8)
	h.join(r, c)

	h.handleMark(context.Background(), 27, c, inbound{
		Type:        msgTypeMark,
		OffsetMs:    1000,
		CumulativeG: "60",
		Technique:   "螺旋升天大法",
		Key:         "k1",
	})

	if p.count(27) != 1 {
		t.Error("未知手法标签应回落到默认值，而不是丢掉整条打点数据")
	}
}

// TestPersisterErrorIsReportedNotSwallowed 确认落库失败会告知客户端。
//
// 静默失败在这里代价极高：用户以为打点成功了，继续冲煮，
// 结束后才发现曲线是空的 —— 而那次冲煮已经无法重现。
func TestPersisterErrorIsReportedNotSwallowed(t *testing.T) {
	p := newFakePersister()
	p.failWith = domain.Validation("DB_DOWN", "数据库连接失败")
	h := NewHub(p, config.PourSourceManual)
	r := h.roomFor(29)
	c := newTestClient(8)
	h.join(r, c)

	h.handleMark(context.Background(), 29, c, inbound{
		Type:        msgTypeMark,
		OffsetMs:    1000,
		CumulativeG: "60",
		Key:         "k1",
	})

	if len(c.send) != 1 {
		t.Fatalf("落库失败必须回报给客户端，实际收到 %d 条消息", len(c.send))
	}
}

// TestInternalErrorDoesNotLeakDetails 确认非领域错误不会把内部细节吐给客户端。
func TestInternalErrorDoesNotLeakDetails(t *testing.T) {
	p := newFakePersister()
	p.failWith = errors.New("pq: password authentication failed for user \"coffee\"")
	h := NewHub(p, config.PourSourceManual)
	r := h.roomFor(31)
	c := newTestClient(8)
	h.join(r, c)

	h.handleMark(context.Background(), 31, c, inbound{
		Type:        msgTypeMark,
		OffsetMs:    1000,
		CumulativeG: "60",
		Key:         "k1",
	})

	msg := <-c.send
	if containsBytes(msg, "password") {
		t.Errorf("推给客户端的错误消息里出现了底层细节：%s", msg)
	}
}

// TestUnknownMessageTypeGetsAnAnswer 确认不认识的消息类型有回应。
//
// 静默忽略会让前端的一个拼写错误变成"服务端好像挂了"，
// 调试时完全没有线索。
func TestUnknownMessageTypeGetsAnAnswer(t *testing.T) {
	h := NewHub(newFakePersister(), config.PourSourceManual)
	r := h.roomFor(33)
	c := newTestClient(8)
	h.join(r, c)

	h.handleInbound(context.Background(), 33, r, c, inbound{Type: "TELEPORT"})

	if len(c.send) != 1 {
		t.Fatalf("未知消息类型应得到一条错误回应，实际 %d 条", len(c.send))
	}
	msg := <-c.send
	if !containsBytes(msg, "TELEPORT") {
		t.Errorf("错误回应里应回显那个不认识的类型名，便于前端定位，实际 %s", msg)
	}
}

// TestPingGetsPong 确认心跳有应答且带服务端时间。
//
// 服务端时间戳是给前端做时钟校正用的：秒表跑在客户端，
// 而累计偏移量必须和服务端一致，否则两台设备的曲线横轴对不齐。
func TestPingGetsPong(t *testing.T) {
	h := NewHub(newFakePersister(), config.PourSourceManual)
	r := h.roomFor(35)
	c := newTestClient(8)
	h.join(r, c)

	h.handleInbound(context.Background(), 35, r, c, inbound{Type: msgTypePing})

	if len(c.send) != 1 {
		t.Fatalf("ping 应得到 pong，实际 %d 条回应", len(c.send))
	}
	msg := <-c.send
	if !containsBytes(msg, "server_time_ms") {
		t.Errorf("pong 应带服务端时间用于时钟校正，实际 %s", msg)
	}
}

// TestSyncReplaysCurrentCurve 确认重连后能拉回当前曲线。
//
// 这是断线重连的另一半：客户端补发本地缓存（靠幂等键去重），
// 同时拉回服务端的完整曲线作为真相。
func TestSyncReplaysCurrentCurve(t *testing.T) {
	p := newFakePersister()
	h := NewHub(p, config.PourSourceManual)
	r := h.roomFor(37)
	c := newTestClient(16)
	h.join(r, c)

	// 先打两个点
	for i, g := range []string{"60", "120"} {
		h.handleMark(context.Background(), 37, c, inbound{
			Type: msgTypeMark, OffsetMs: (i + 1) * 10000,
			CumulativeG: g, Key: "k" + itoaTest(i),
		})
	}
	// 清空广播产生的消息
	for len(c.send) > 0 {
		<-c.send
	}

	h.handleInbound(context.Background(), 37, r, c, inbound{Type: msgTypeSync})

	if len(c.send) != 1 {
		t.Fatalf("sync 应回一条当前曲线，实际 %d 条", len(c.send))
	}
	msg := <-c.send
	if !containsBytes(msg, "points") {
		t.Errorf("sync 回的应是完整曲线，实际 %s", msg)
	}
}

// TestSyncOnEmptyBrewDoesNotFail 确认对还没打点的冲煮做 sync 不出错。
func TestSyncOnEmptyBrewDoesNotFail(t *testing.T) {
	h := NewHub(newFakePersister(), config.PourSourceManual)
	r := h.roomFor(39)
	c := newTestClient(8)
	h.join(r, c)

	h.handleInbound(context.Background(), 39, r, c, inbound{Type: msgTypeSync})

	// 回一条空曲线是对的：前端拿到六个空数组能正常渲染出坐标系
	if len(c.send) != 1 {
		t.Errorf("对空冲煮 sync 应回一条空曲线而非静默，实际 %d 条", len(c.send))
	}
}

// TestOnlyOneSimulatorPerRoom 确认一个房间最多一个模拟器。
//
// 两个模拟器同时往同一条曲线灌数据会产生互相矛盾的累计值 ——
// 而累计值必须单调不减，冲突会直接触发校验失败。
func TestOnlyOneSimulatorPerRoom(t *testing.T) {
	h := NewHub(newFakePersister(), config.PourSourceSimulator)
	r := h.roomFor(41)
	c := newTestClient(64)
	h.join(r, c)

	for i := 0; i < 5; i++ {
		_ = h.StartSimulator(41)
	}
	defer h.StopSimulator(41)

	h.mu.RLock()
	n := len(h.simulators)
	h.mu.RUnlock()

	if n != 1 {
		t.Errorf("同一房间重复启动模拟器应只保留 1 个，实际 %d 个。"+
			"两个模拟器会往同一条曲线灌互相矛盾的累计值", n)
	}
}

// TestStopSimulatorIsIdempotent 确认重复停止不 panic。
//
// 停止会从两条路径被调用：客户端显式停止，以及房间回收时的自动清理。
// 两者可能几乎同时发生。
func TestStopSimulatorIsIdempotent(t *testing.T) {
	h := NewHub(newFakePersister(), config.PourSourceSimulator)

	h.StopSimulator(43) // 从未启动过
	_ = h.StartSimulator(43)
	h.StopSimulator(43)
	h.StopSimulator(43)
	h.StopSimulator(43)
}

// TestModeIsExposedForFrontend 确认注水来源模式可读。
//
// 前端要靠它决定是否显示"启动模拟"按钮 —— 生产环境不该出现这个按钮。
func TestModeIsExposedForFrontend(t *testing.T) {
	if got := NewHub(newFakePersister(), config.PourSourceManual).Mode(); got != config.PourSourceManual {
		t.Errorf("模式应为 %s，实际 %s", config.PourSourceManual, got)
	}
	if got := NewHub(newFakePersister(), config.PourSourceSimulator).Mode(); got != config.PourSourceSimulator {
		t.Errorf("模式应为 %s，实际 %s", config.PourSourceSimulator, got)
	}
}

// TestConcurrentJoinLeaveBroadcast 是给 -race 用的混合压力场景。
//
// 单独测每个操作都可能通过，而真实的崩溃发生在它们交错时：
// 一个客户端正在离开、房间正在被回收，同时另一个打点在广播。
func TestConcurrentJoinLeaveBroadcast(t *testing.T) {
	h := NewHub(newFakePersister(), config.PourSourceManual)

	var wg sync.WaitGroup
	const workers = 24

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			brewID := int64(i%4 + 1)
			for k := 0; k < 50; k++ {
				r := h.roomFor(brewID)
				c := newTestClient(4)
				h.join(r, c)
				h.Broadcast(brewID, outbound{Type: outTypePong})
				h.handleMark(context.Background(), brewID, c, inbound{
					Type: msgTypeMark, OffsetMs: k * 100,
					CumulativeG: "10", Key: "k" + itoaTest(i*100+k),
				})
				_ = h.ClientCount(brewID)
				_ = h.RoomCount()
				h.leave(r, c)
			}
		}(i)
	}
	wg.Wait()

	if h.RoomCount() != 0 {
		t.Errorf("全部客户端离开后房间应清空，实际残留 %d 个", h.RoomCount())
	}
}

func newTestClient(buffer int) *client {
	return &client{
		send: make(chan []byte, buffer),
		dead: make(chan struct{}),
	}
}

func containsBytes(b []byte, sub string) bool {
	s := string(b)
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestInboundSourceReflectsMode 验证 WebSocket 打点的来源标记随模式变化。
//
// 这个字段是整个应用「可信测量」这条主线上的一环：推算模式会不惜代价地
// 把每个推算值都标注出来，就是为了不让用户把推算当实测。如果 device 模式下
// 智能秤推来的数据被记成 MANUAL，同一条主线在这里断了 —— 曲线本身看不出
// 差别（都是一串累计克数），事后复盘时再没有别的线索能还原它是秤量的还是
// 人估的。修复前 SourceDevice 有常量、有 Label()、有汇总分支，唯独没有写入方。
func TestInboundSourceReflectsMode(t *testing.T) {
	cases := []struct {
		mode config.PourSourceMode
		want brew.PourSource
	}{
		{config.PourSourceManual, brew.SourceManual},
		{config.PourSourceSimulator, brew.SourceManual},
		{config.PourSourceDevice, brew.SourceDevice},
	}

	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			p := newFakePersister()
			h := NewHub(p, tc.mode)
			c := newTestClient(16)
			h.join(h.roomFor(91), c)

			h.handleMark(context.Background(), 91, c, inbound{
				Type:        msgTypeMark,
				OffsetMs:    1000,
				CumulativeG: "30",
				Key:         "src-1",
			})

			p.mu.Lock()
			evs := p.events[91]
			p.mu.Unlock()

			if len(evs) != 1 {
				t.Fatalf("应落库 1 条节点，实际 %d 条", len(evs))
			}
			if evs[0].Source != tc.want {
				t.Errorf("%s 模式下的入站打点应标记为 %s，实际 %s —— 来源字段在说谎",
					tc.mode, tc.want, evs[0].Source)
			}
		})
	}
}

// TestSimulatorStampsItsOwnSource 验证模拟器产生的节点不会被误标成人手打点。
//
// 与上一条互补：模拟器不走 handleMark，它自己标 SourceSimulator。
// 若两条路径将来被合并，这条测试会拦住"模拟数据混进实测里"的退化 ——
// 那会让演示数据和真实冲煮记录再也分不开。
func TestSimulatorStampsItsOwnSource(t *testing.T) {
	p := newFakePersister()
	h := NewHub(p, config.PourSourceSimulator)
	c := newTestClient(256)
	h.join(h.roomFor(92), c)

	h.handleSimStart(92, c)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && p.count(92) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	h.StopSimulator(92)

	p.mu.Lock()
	evs := p.events[92]
	p.mu.Unlock()

	if len(evs) == 0 {
		t.Fatal("模拟器应至少产生一个注水节点")
	}
	for i, e := range evs {
		if e.Source != brew.SourceSimulator {
			t.Errorf("第 %d 个模拟节点应标记为 %s，实际 %s",
				i, brew.SourceSimulator, e.Source)
		}
	}
}

// TestModePredicatesAreExhaustive 验证三种模式的语义判定互斥且完备。
//
// 判定挂在 PourSourceMode 上而不是散落在各处，加第四种模式时这条会提醒
// 补齐语义；若某个模式两个判定都为假，说明它没有被任何一处代码识别。
func TestModePredicatesAreExhaustive(t *testing.T) {
	all := []config.PourSourceMode{
		config.PourSourceManual,
		config.PourSourceSimulator,
		config.PourSourceDevice,
	}
	for _, m := range all {
		sim, dev := m.SimulatorEnabled(), m.DeviceIngestEnabled()
		if sim && dev {
			t.Errorf("%s 同时是模拟器与真实设备，语义冲突", m)
		}
		if m != config.PourSourceManual && !sim && !dev {
			t.Errorf("%s 的两个判定都为假，说明没有代码识别这个模式", m)
		}
	}
}
