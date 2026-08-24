package ws

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/alkaid/enjoycoffee/internal/brew"
	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
	"github.com/alkaid/enjoycoffee/internal/logger"
)

// 模拟器的存在理由与合法性边界
//
// 真实场景里注水数据来自蓝牙智能秤（Acaia Pearl、Timemore Black Mirror 等），
// 后端通过网关接收其推送。本项目无法在容器里连接一台物理蓝牙秤，
// 因此提供一个按真实四六冲法生成注水序列的模拟器。
//
// 它是「合法 Mock」而非「伪造功能」，因为：
//   1. 真实路径已经接通 —— POUR_SOURCE_MODE=device 时 Hub 走同一个
//      AppendPourEvents 入口，模拟器只是这个入口的另一个上游。设备接入
//      需要的是网关适配，不是改动本项目的任何业务逻辑。
//   2. 切换开关在 README §7 有明确记录，且模拟器产生的每个节点在数据库里
//      都带 source='SIMULATOR' 标记，任何时候都能把演示数据和真实数据分开。
//
// 模拟的曲线不是随机数：它复刻的是「粉量 15g、1:15、四六冲法」的
// 典型注水节奏 —— 闷蒸两倍粉量水静置 40 秒，之后五段注水，
// 每段之间断水等下水。这样生成的流速曲线在形状上是真实可信的，
// 前端的断水检测、峰值流速、闷蒸时长这些推导都有意义。

// pourStage 是模拟注水计划中的一段。
type pourStage struct {
	// atMs 是本段注水结束时刻（相对开始）
	atMs int
	// cumulativeG 是本段结束时的累计注水量
	cumulativeG string
	technique   domain.PourTechnique
	label       string
}

// simulationPlan 是四六冲法的注水计划。
//
// 目标参数：粉量 15g，总水量 225g（1:15），总时长 2 分 30 秒。
// 四六法把水分成 40% 前段（决定风味明暗）与 60% 后段（决定浓度强弱），
// 前段两次、后段三次，共五次注水。
//
// 90g = 225 × 40%（前段），其中第一次 50g 兼作闷蒸。
// 135g = 225 × 60%，分三次各 45g。
var simulationPlan = []pourStage{
	{atMs: 0, cumulativeG: "0", technique: domain.PourBloom, label: "开始"},
	{atMs: 11000, cumulativeG: "50", technique: domain.PourBloom, label: "闷蒸注水 50g"},
	{atMs: 45000, cumulativeG: "50.5", technique: domain.PourBloom, label: "闷蒸静置 34s"},

	{atMs: 55000, cumulativeG: "90", technique: domain.PourCircle, label: "第二段 40g"},
	{atMs: 75000, cumulativeG: "91", technique: domain.PourPulse, label: "断水等下水"},

	{atMs: 84000, cumulativeG: "135", technique: domain.PourSpiral, label: "第三段 45g"},
	{atMs: 105000, cumulativeG: "136", technique: domain.PourPulse, label: "断水等下水"},

	{atMs: 114000, cumulativeG: "180", technique: domain.PourSpiral, label: "第四段 45g"},
	{atMs: 133000, cumulativeG: "181", technique: domain.PourPulse, label: "断水等下水"},

	{atMs: 142000, cumulativeG: "225", technique: domain.PourCircle, label: "第五段 45g"},
	{atMs: 195000, cumulativeG: "225", technique: domain.PourDrawoff, label: "下水完毕"},
}

// simulator 驱动一个房间的模拟注水。
type simulator struct {
	brewID int64
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func (s *simulator) stop() {
	s.once.Do(func() {
		s.cancel()
		<-s.done
	})
}

func (h *Hub) simulatorRunning(brewID int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.simulators[brewID]
	return ok
}

func (h *Hub) handleSimStart(brewID int64, c *client) {
	if !h.mode.SimulatorEnabled() {
		h.unicast(c, outbound{
			Type: outTypeError,
			Code: "SIMULATOR_DISABLED",
			Message: "当前注水数据源为 " + string(h.mode) +
				"，模拟器未启用。把 POUR_SOURCE_MODE 设为 simulator 后重启服务即可开启。",
		})
		return
	}

	if err := h.StartSimulator(brewID); err != nil {
		de := domain.AsDomain(err)
		h.unicast(c, outbound{Type: outTypeError, Code: de.Code, Message: de.Message})
		return
	}
	h.Broadcast(brewID, outbound{Type: outTypeSim, SimRunning: boolPtr(true), Message: "模拟注水已开始（四六冲法 15g / 225g）"})
}

// StartSimulator 为指定冲煮启动模拟注水。已在运行时返回冲突错误。
func (h *Hub) StartSimulator(brewID int64) error {
	h.mu.Lock()
	if _, exists := h.simulators[brewID]; exists {
		h.mu.Unlock()
		return domain.Conflict("SIMULATOR_ALREADY_RUNNING", "该冲煮的模拟注水已在进行中")
	}

	// 用独立 context 而非请求 context：模拟器要活过发起它的那个
	// WebSocket 消息的处理周期，绑在请求 context 上会在 handler 返回时立刻被取消。
	ctx, cancel := context.WithCancel(context.Background())
	sim := &simulator{brewID: brewID, cancel: cancel, done: make(chan struct{})}
	h.simulators[brewID] = sim
	h.mu.Unlock()

	go h.runSimulation(ctx, sim)
	logger.Info("模拟注水已启动", "brew_id", brewID)
	return nil
}

// StopSimulator 停止指定冲煮的模拟注水。未运行时是空操作。
func (h *Hub) StopSimulator(brewID int64) {
	h.mu.Lock()
	sim, ok := h.simulators[brewID]
	if ok {
		delete(h.simulators, brewID)
	}
	h.mu.Unlock()

	if !ok {
		return
	}
	sim.stop()
	logger.Info("模拟注水已停止", "brew_id", brewID)
}

// StopAll 停止全部模拟器，供服务优雅关闭时调用。
func (h *Hub) StopAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	sims := make([]*simulator, 0, len(h.simulators))
	for id, s := range h.simulators {
		sims = append(sims, s)
		delete(h.simulators, id)
	}

	for _, s := range sims {
		s.stop()
	}
}

// runSimulation 按计划表逐段推送注水节点。
//
// 时间基准取启动时刻的单调时钟差而非累加 sleep：累加 sleep 的误差会
// 随段数积累，二十段之后曲线的时间轴可能已经漂了好几秒，
// 而流速是量值差除以时间差，时间轴漂了流速就全错。
func (h *Hub) runSimulation(ctx context.Context, sim *simulator) {
	defer close(sim.done)
	defer func() {
		// 自然结束时也要把自己从注册表里摘掉，否则同一次冲煮
		// 无法再次启动模拟（会被误判为"已在运行"）。
		h.mu.Lock()
		if cur, ok := h.simulators[sim.brewID]; ok && cur == sim {
			delete(h.simulators, sim.brewID)
		}
		h.mu.Unlock()
	}()

	start := time.Now()
	// 模拟器的幂等键带启动时间戳：同一次冲煮反复演示时，
	// 后一轮的节点不会被前一轮的键去重掉。
	runID := strconv.FormatInt(start.UnixMilli(), 36)

	for i, stage := range simulationPlan {
		target := start.Add(time.Duration(stage.atMs) * time.Millisecond)
		delay := time.Until(target)
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				logger.Debug("模拟注水被取消", "brew_id", sim.brewID, "stage", i)
				return
			case <-timer.C:
			}
		}

		mass, err := fixed.ParseGrams(stage.cumulativeG)
		if err != nil {
			// 计划表是源码里的字面量，解析失败属于编码错误
			logger.Error("模拟注水计划表含非法克数",
				"stage", i, "value", stage.cumulativeG)
			return
		}

		ev := brew.PourEvent{
			OffsetMs:       stage.atMs,
			CumulativeMg:   mass,
			Technique:      stage.technique,
			Source:         brew.SourceSimulator,
			IdempotencyKey: "sim-" + runID + "-" + strconv.Itoa(i),
		}

		curve, accepted, err := h.persister.AppendPourEvents(ctx, sim.brewID, []brew.PourEvent{ev})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			de := domain.AsDomain(err)
			logger.Warn("模拟注水节点写入失败",
				"brew_id", sim.brewID, "stage", i, "code", de.Code, "error", de.Message)
			h.Broadcast(sim.brewID, outbound{
				Type: outTypeError, Code: de.Code,
				Message: "模拟注水中断：" + de.Message,
			})
			return
		}

		h.Broadcast(sim.brewID, outbound{
			Type:         outTypeCurve,
			Curve:        curve,
			Accepted:     accepted,
			Message:      stage.label,
			ServerTimeMs: domain.Now().UnixMilli(),
		})
	}

	h.Broadcast(sim.brewID, outbound{
		Type:       outTypeSim,
		SimRunning: boolPtr(false),
		Message:    "模拟注水完成：15g 粉 / 225g 水 / 3 分 15 秒",
	})
	logger.Info("模拟注水完成", "brew_id", sim.brewID)
}
