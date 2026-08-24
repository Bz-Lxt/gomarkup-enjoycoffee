package brew

import (
	"sort"
	"strings"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

// PourSource 标识一条注水节点的数据来源。
//
// 三个来源共享完全相同的数据结构与下游处理逻辑，差异仅在于谁产生了事件。
// 这是"模拟通道不构成对真实实现的替换"的结构性保证：删掉模拟器，
// 手动与设备两条通道照常工作；接入真实设备，也不需要改任何下游代码。
type PourSource string

const (
	// SourceManual 前端秒表手动打点。零依赖，任何环境都能用。
	SourceManual PourSource = "MANUAL"
	// SourceSimulator 内置流速模拟器按物理模型生成。
	SourceSimulator PourSource = "SIMULATOR"
	// SourceDevice 真实智能秤经 WebSocket 推送。
	SourceDevice PourSource = "DEVICE"
)

func (s PourSource) Label() string {
	switch s {
	case SourceManual:
		return "手动打点"
	case SourceSimulator:
		return "模拟器"
	case SourceDevice:
		return "智能秤"
	default:
		return "未知来源"
	}
}

// PourEvent 是一个注水节点：某一时刻的累计注水量。
type PourEvent struct {
	ID     int64
	BrewID int64
	// OffsetMs 是自冲煮开始的毫秒偏移。以偏移量而非绝对时刻存储，
	// 使记录不受设备时钟漂移与时区影响。
	OffsetMs int
	// CumulativeMg 是该时刻的累计注水量（毫克），而非本次注入量。
	//
	// 存累计值而非增量是关键决策：智能秤读到的本来就是累计示数，
	// 存增量需要做一次减法，而任何一条事件丢失都会让后续全部累计值错位。
	// 存累计值时，丢一条事件只影响曲线的局部平滑度，不影响任何后续点的正确性。
	CumulativeMg fixed.Mass
	Technique    domain.PourTechnique
	Source       PourSource
	// IdempotencyKey 由客户端生成，用于断线重连后的去重合并。
	IdempotencyKey string
}

// PourPoint 是流速曲线上的一个采样点，几何量由后端算好供前端直接绘制。
type PourPoint struct {
	OffsetMs    int     `json:"offset_ms"`
	OffsetSec   float64 `json:"offset_sec"`
	TimeLabel   string  `json:"time_label"`
	CumulativeG float64 `json:"cumulative_g"`
	// FlowRate 是从上一个点到本点的平均流速（克/秒）。
	// 第一个点的流速以 0 起算，因为它之前没有区间可计算。
	FlowRate  float64 `json:"flow_rate"`
	Technique string  `json:"technique"`
	TechLabel string  `json:"tech_label"`
	Source    string  `json:"source"`
	// IsPause 标记这一段是断水（流速接近零但时间在走）。
	IsPause bool `json:"is_pause"`
}

// PourSegment 是相邻两个节点之间的一段注水行为。
type PourSegment struct {
	// Ordinal 是给用户看的段序号，从 1 起（"第 1 段"）。
	// 刻意不叫 Index：叫 Index 会诱使前端拿它去索引 segments 数组，
	// 而它比数组下标大 1。
	Ordinal     int     `json:"ordinal"`
	FromMs      int     `json:"from_ms"`
	ToMs        int     `json:"to_ms"`
	DurationSec float64 `json:"duration_sec"`
	PouredG     float64 `json:"poured_g"`
	FlowRate    float64 `json:"flow_rate"`
	// SharePercent 是该段注水量占总注水量的百分比。这就是需求里
	// "注水流速比例"的量化形式 —— 用户想知道的是"闷蒸用了多少水、
	// 第二段又给了多少"，而不只是一条曲线的形状。
	SharePercent float64 `json:"share_percent"`
	Technique    string  `json:"technique"`
	TechLabel    string  `json:"tech_label"`
	IsPause      bool    `json:"is_pause"`
}

// PourCurve 是一次冲煮的完整注水曲线分析。
type PourCurve struct {
	Points   []PourPoint   `json:"points"`
	Segments []PourSegment `json:"segments"`

	TotalWaterG    float64 `json:"total_water_g"`
	TotalDurationS float64 `json:"total_duration_sec"`
	AvgFlowRate    float64 `json:"avg_flow_rate"`
	PeakFlowRate   float64 `json:"peak_flow_rate"`
	PeakAtSec      float64 `json:"peak_at_sec"`

	// BloomWaterG 与 BloomRatio 描述闷蒸阶段。闷蒸水量通常取粉量的 2–3 倍，
	// 过少会有干粉未浸润，过多会提前开始正式萃取。
	BloomWaterG   float64  `json:"bloom_water_g"`
	BloomRatio    float64  `json:"bloom_ratio"`
	BloomSeconds  float64  `json:"bloom_seconds"`
	HasBloom      bool     `json:"has_bloom"`
	PauseCount    int      `json:"pause_count"`
	SourceSummary string   `json:"source_summary"`
	Insights      []string `json:"insights"`
}

// pauseFlowThresholdMgPerSec 是判定"断水"的流速阈值：0.3 g/s。
//
// 取这个值的理由：正常注水流速在 2–6 g/s，滴滤下水阶段约 1–2 g/s，
// 而真正的断水期间秤示数只有极微小的滴落变化。0.3 g/s 足够低到不会
// 把慢速注水误判为断水，又足够高到容纳滴落的噪声。
const pauseFlowThresholdMgPerSec = 300

// AnalyzePourCurve 由注水节点序列推导完整的流速曲线与分析结论。
//
// dose 用于计算闷蒸比例；为 0 时闷蒸相关字段留空而不报错。
func AnalyzePourCurve(events []PourEvent, dose fixed.Mass) PourCurve {
	c := PourCurve{
		Points:   []PourPoint{},
		Segments: []PourSegment{},
		Insights: []string{},
	}
	if len(events) == 0 {
		return c
	}

	sorted := make([]PourEvent, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].OffsetMs < sorted[j].OffsetMs })

	totalMg := sorted[len(sorted)-1].CumulativeMg
	c.TotalWaterG = totalMg.ApproxGramsFloat()
	c.TotalDurationS = float64(sorted[len(sorted)-1].OffsetMs) / 1000

	sourceCount := map[PourSource]int{}
	var peakFlowMilli int64 = 0 // 毫克每秒
	peakAtMs := 0

	prevMs := 0
	prevMg := fixed.Mass(0)

	for i, e := range sorted {
		sourceCount[e.Source]++

		dtMs := e.OffsetMs - prevMs
		dMg := e.CumulativeMg - prevMg

		var flowMgPerSec int64
		if i > 0 && dtMs > 0 {
			// 毫克/秒 = 毫克 × 1000 / 毫秒。用整数运算得到流速，最后再转展示浮点。
			flowMgPerSec = int64(dMg) * 1000 / int64(dtMs)
		}

		isPause := i > 0 && dtMs > 0 && flowMgPerSec < pauseFlowThresholdMgPerSec

		if flowMgPerSec > peakFlowMilli {
			peakFlowMilli = flowMgPerSec
			peakAtMs = e.OffsetMs
		}

		c.Points = append(c.Points, PourPoint{
			OffsetMs:    e.OffsetMs,
			OffsetSec:   float64(e.OffsetMs) / 1000,
			TimeLabel:   formatStopwatch(e.OffsetMs),
			CumulativeG: e.CumulativeMg.ApproxGramsFloat(),
			FlowRate:    float64(flowMgPerSec) / 1000,
			Technique:   string(e.Technique),
			TechLabel:   e.Technique.Label(),
			Source:      string(e.Source),
			IsPause:     isPause,
		})

		if i > 0 {
			share := 0.0
			if totalMg > 0 {
				share = float64(int64(dMg)*1000/int64(totalMg)) / 10
			}
			c.Segments = append(c.Segments, PourSegment{
				Ordinal:      i,
				FromMs:       prevMs,
				ToMs:         e.OffsetMs,
				DurationSec:  float64(dtMs) / 1000,
				PouredG:      dMg.ApproxGramsFloat(),
				FlowRate:     float64(flowMgPerSec) / 1000,
				SharePercent: share,
				Technique:    string(e.Technique),
				TechLabel:    e.Technique.Label(),
				IsPause:      isPause,
			})
			if isPause {
				c.PauseCount++
			}
		}

		prevMs = e.OffsetMs
		prevMg = e.CumulativeMg
	}

	c.PeakFlowRate = float64(peakFlowMilli) / 1000
	c.PeakAtSec = float64(peakAtMs) / 1000
	if c.TotalDurationS > 0 {
		c.AvgFlowRate = c.TotalWaterG / c.TotalDurationS
	}

	// 闷蒸识别：第一个"确实注了水"的节点是闷蒸注水的终点，
	// 其后若存在一段低流速间隔，该间隔就是闷蒸时长。
	//
	// 为何要跳过累计量为 0 的开头节点：秒表与智能秤在归零启动时会打一个
	// t=0、示数 0 的点，它是计时起点而非一次注水。若把它当闷蒸终点，
	// 闷蒸水量会读成 0g，闷蒸比例随之归零 —— 而这恰恰是最常见的记录方式。
	bloomEnd := -1
	for i := range sorted {
		if sorted[i].CumulativeMg > 0 {
			bloomEnd = i
			break
		}
	}
	if bloomEnd >= 0 && bloomEnd+1 < len(sorted) {
		first := sorted[bloomEnd]
		c.BloomWaterG = first.CumulativeMg.ApproxGramsFloat()
		c.BloomSeconds = float64(sorted[bloomEnd+1].OffsetMs-first.OffsetMs) / 1000
		c.HasBloom = first.Technique == domain.PourBloom || c.BloomSeconds >= 15
		if dose > 0 {
			if r, err := fixed.DivMass(first.CumulativeMg, dose); err == nil {
				c.BloomRatio = r.ApproxMultipleFloat()
			}
		}
	}

	c.SourceSummary = summarizeSources(sourceCount)
	c.Insights = pourInsights(c, dose)
	return c
}

func summarizeSources(counts map[PourSource]int) string {
	if len(counts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(counts))
	for _, s := range []PourSource{SourceManual, SourceSimulator, SourceDevice} {
		if n, ok := counts[s]; ok && n > 0 {
			parts = append(parts, s.Label()+" "+itoa(n)+" 个节点")
		}
	}
	return strings.Join(parts, "，")
}

// pourInsights 由曲线形状给出注水手法层面的观察。
//
// 这些结论刻意不带"你做错了"的口吻：注水手法的好坏高度依赖具体豆子与目标风味，
// 引擎能做的是把曲线里客观存在的特征指出来，让用户自己判断是否是他想要的。
func pourInsights(c PourCurve, dose fixed.Mass) []string {
	out := []string{}

	if c.HasBloom && c.BloomRatio > 0 {
		switch {
		case c.BloomRatio < 1.5:
			out = append(out, "闷蒸水量 "+f1(c.BloomWaterG)+"g，只有粉量的 "+
				f1(c.BloomRatio)+" 倍，偏少。粉层可能有部分未被完全浸润，"+
				"后续注水时那部分粉会晚一步开始萃取，容易造成萃取不均。常用比例是 2–3 倍。")
		case c.BloomRatio > 3.5:
			out = append(out, "闷蒸水量 "+f1(c.BloomWaterG)+"g，达到粉量的 "+
				f1(c.BloomRatio)+" 倍，偏多。这个量已经开始正式萃取而不只是排气，"+
				"实际上缩短了后续注水的可控空间。")
		default:
			out = append(out, "闷蒸水量 "+f1(c.BloomWaterG)+"g（粉量的 "+
				f1(c.BloomRatio)+" 倍），落在 2–3 倍的常用区间内。")
		}
	}

	if c.BloomSeconds > 0 {
		switch {
		case c.BloomSeconds < 20:
			out = append(out, "闷蒸 "+f1(c.BloomSeconds)+" 秒偏短。新鲜豆（尤其烘后两周内的浅烘）"+
				"排气旺盛，闷蒸不足会让 CO₂ 在后续注水时持续把水推开。")
		case c.BloomSeconds > 60:
			out = append(out, "闷蒸 "+f1(c.BloomSeconds)+" 秒偏长，粉层可能已经开始降温，"+
				"后段萃取效率会随之下降。")
		}
	}

	if c.PauseCount > 0 {
		out = append(out, "曲线中检测到 "+itoa(c.PauseCount)+" 段断水（流速低于 0.3 g/s）。"+
			"断水法能提高萃取率，但每次断水都会让粉层降温，段数多时建议略微提高水温补偿。")
	}

	if c.PeakFlowRate > 8 {
		out = append(out, "峰值流速 "+f1(c.PeakFlowRate)+" g/s 偏高（在第 "+
			f1(c.PeakAtSec)+" 秒）。过快的注水会冲击粉层形成通道，"+
			"水会沿通道直落而不充分接触咖啡粉。")
	}

	if len(c.Segments) == 1 {
		out = append(out, "全程只有一段注水记录。多打几个节点能让流速曲线反映出真实的手法节奏，"+
			"也让后续复盘时有据可依。")
	}

	if dose > 0 && c.TotalWaterG > 0 {
		out = append(out, "总注水 "+f1(c.TotalWaterG)+"g，全程 "+
			formatStopwatch(int(c.TotalDurationS*1000))+"，平均流速 "+f1(c.AvgFlowRate)+" g/s。")
	}

	return out
}

// MergePourEvents 按幂等键合并注水节点，用于 WebSocket 断线重连后的续传。
//
// 场景：客户端在冲煮过程中断网，本地缓存了若干节点；重连后把缓存全部重发。
// 服务端必须把已经收到过的那些去重，只保留新的。
//
// 幂等键缺失时的兜底规则是「同一毫秒偏移视为同一事件」：这不是随意的选择 ——
// 注水节点的语义是"某一时刻的累计示数"，同一毫秒不可能有两个不同的累计值。
// 若真的收到同偏移不同值的两条，后到的覆盖先到的（更可能是重传的修正值）。
func MergePourEvents(existing, incoming []PourEvent) []PourEvent {
	byKey := make(map[string]int, len(existing)+len(incoming))
	merged := make([]PourEvent, 0, len(existing)+len(incoming))

	add := func(e PourEvent) {
		k := e.IdempotencyKey
		if strings.TrimSpace(k) == "" {
			k = "offset:" + itoa(e.OffsetMs)
		}
		if idx, seen := byKey[k]; seen {
			merged[idx] = e
			return
		}
		byKey[k] = len(merged)
		merged = append(merged, e)
	}

	for _, e := range existing {
		add(e)
	}
	for _, e := range incoming {
		add(e)
	}

	sort.SliceStable(merged, func(i, j int) bool { return merged[i].OffsetMs < merged[j].OffsetMs })
	return merged
}

// ValidatePourEvents 校验注水节点序列的内部一致性。
func ValidatePourEvents(events []PourEvent) error {
	if len(events) == 0 {
		return nil
	}

	sorted := make([]PourEvent, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].OffsetMs < sorted[j].OffsetMs })

	e := domain.Validation("INVALID_POUR_EVENTS", "注水节点序列不一致")
	bad := false

	prevMg := fixed.Mass(-1)
	for i, ev := range sorted {
		if ev.OffsetMs < 0 {
			e.WithField("pour_events["+itoa(i)+"].offset_ms", "时间偏移不能为负")
			bad = true
		}
		// 单次冲煮不可能超过 30 分钟。超出通常是把秒当毫秒填了。
		if ev.OffsetMs > 30*60*1000 {
			e.WithField("pour_events["+itoa(i)+"].offset_ms",
				"时间偏移超过 30 分钟，请确认单位是毫秒")
			bad = true
		}
		if ev.CumulativeMg < 0 {
			e.WithField("pour_events["+itoa(i)+"].cumulative_g", "累计注水量不能为负")
			bad = true
		}
		// 累计量必须单调不减 —— 这是"累计"的定义。递减说明客户端把
		// 增量当累计值发了，是必须拦住的语义错误，否则流速会算出负数。
		if prevMg >= 0 && ev.CumulativeMg < prevMg {
			e.WithField("pour_events["+itoa(i)+"].cumulative_g",
				"累计注水量必须单调不减（上一节点 "+prevMg.Grams()+
					"g，本节点 "+ev.CumulativeMg.Grams()+"g）。请确认发送的是累计值而非单次注入量。")
			bad = true
		}
		if ev.Technique != "" && !ev.Technique.Valid() {
			e.WithField("pour_events["+itoa(i)+"].technique", "未知的注水手法标签")
			bad = true
		}
		prevMg = ev.CumulativeMg
	}

	if bad {
		return e
	}
	return nil
}

// formatStopwatch 把毫秒渲染为 "m:ss.f" 的秒表格式。
func formatStopwatch(ms int) string {
	if ms < 0 {
		ms = 0
	}
	totalTenths := ms / 100
	tenths := totalTenths % 10
	totalSec := totalTenths / 10
	sec := totalSec % 60
	min := totalSec / 60

	s := itoa(min) + ":"
	if sec < 10 {
		s += "0"
	}
	s += itoa(sec) + "." + itoa(tenths)
	return s
}

func f1(v float64) string {
	x := int64(v*10 + 0.5)
	if v < 0 {
		x = int64(v*10 - 0.5)
	}
	return itoa(int(x/10)) + "." + itoa(int(absInt64(x%10)))
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
