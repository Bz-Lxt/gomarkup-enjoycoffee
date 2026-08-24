package brew

import (
	"math"
	"testing"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

func mg(grams string, t *testing.T) fixed.Mass {
	t.Helper()
	m, err := fixed.ParseGrams(grams)
	if err != nil {
		t.Fatalf("解析 %q 失败: %v", grams, err)
	}
	return m
}

// fourSixEvents 是标准 4:6 手法的注水节点序列：
// 20g 粉、300g 水，前两段各 60g（合计 40%）、后三段各 60g（合计 60%）。
func fourSixEvents(t *testing.T) []PourEvent {
	t.Helper()
	type spec struct {
		ms    int
		total string
		tech  domain.PourTechnique
	}
	specs := []spec{
		{0, "0", domain.PourCenter},
		{10000, "60", domain.PourCenter},   // 闷蒸 60g
		{45000, "120", domain.PourSpiral},  // 第二段
		{75000, "180", domain.PourSpiral},  // 第三段
		{105000, "240", domain.PourSpiral}, // 第四段
		{135000, "300", domain.PourSpiral}, // 第五段
	}
	out := make([]PourEvent, 0, len(specs))
	for i, s := range specs {
		out = append(out, PourEvent{
			ID:             int64(i + 1),
			BrewID:         1,
			OffsetMs:       s.ms,
			CumulativeMg:   mg(s.total, t),
			Technique:      s.tech,
			Source:         SourceManual,
			IdempotencyKey: "k" + string(rune('a'+i)),
		})
	}
	return out
}

func nearly(got, want, tol float64) bool {
	return math.Abs(got-want) <= tol
}

// TestFlowRateIsPerSegmentNotCumulative 是流速曲线最基础的一条。
//
// 存的是累计示数，画的是瞬时流速。若忘了做差分而直接把累计量当流速，
// 曲线会变成一条单调上升的直线 —— 看起来"像条曲线"，但和注水行为无关。
func TestFlowRateIsPerSegmentNotCumulative(t *testing.T) {
	c := AnalyzePourCurve(fourSixEvents(t), mg("20", t))

	if len(c.Points) != 6 {
		t.Fatalf("应有 6 个采样点，实际 %d 个", len(c.Points))
	}

	// 第 2 点：10 秒注入 60g → 6 g/s
	if !nearly(c.Points[1].FlowRate, 6.0, 0.01) {
		t.Errorf("闷蒸段 10 秒注入 60g，流速应为 6 g/s，实际 %.3f", c.Points[1].FlowRate)
	}
	// 第 3 点：35 秒注入 60g → 约 1.714 g/s
	if !nearly(c.Points[2].FlowRate, 60.0/35.0, 0.01) {
		t.Errorf("第二段 35 秒注入 60g，流速应约 %.3f g/s，实际 %.3f",
			60.0/35.0, c.Points[2].FlowRate)
	}
	// 曲线不能单调递增 —— 那正是"把累计值当流速"的症状
	monotonic := true
	for i := 2; i < len(c.Points); i++ {
		if c.Points[i].FlowRate < c.Points[i-1].FlowRate {
			monotonic = false
			break
		}
	}
	if monotonic {
		t.Error("流速序列全程单调不减，可能把累计注水量当成流速了")
	}
}

// TestFirstPointHasNoFlowRate 确认第一个点的流速是 0 而不是一个凭空的数。
//
// 第一个点之前没有区间，任何非零流速都是编的。若拿它去除以偏移 0，
// 还会直接除零。
func TestFirstPointHasNoFlowRate(t *testing.T) {
	c := AnalyzePourCurve(fourSixEvents(t), mg("20", t))

	if c.Points[0].FlowRate != 0 {
		t.Errorf("首个采样点之前没有区间可算流速，应为 0，实际 %.3f",
			c.Points[0].FlowRate)
	}
	if math.IsNaN(c.Points[0].FlowRate) || math.IsInf(c.Points[0].FlowRate, 0) {
		t.Error("首点流速出现 NaN/Inf，说明发生了除零")
	}
}

// TestSegmentSharesSumToOneHundred 验证"注水流速比例"这个需求点。
//
// 用户想知道的是"闷蒸用了多少水、第二段又给了多少"。各段占比之和
// 必须是 100%，否则前端的堆叠条会有缺口。
func TestSegmentSharesSumToOneHundred(t *testing.T) {
	c := AnalyzePourCurve(fourSixEvents(t), mg("20", t))

	if len(c.Segments) != 5 {
		t.Fatalf("6 个节点应产生 5 段，实际 %d 段", len(c.Segments))
	}

	var sum float64
	for i, s := range c.Segments {
		sum += s.SharePercent
		if s.Ordinal != i+1 {
			t.Errorf("第 %d 段的 Ordinal 应为 %d（从 1 起给用户看），实际 %d",
				i, i+1, s.Ordinal)
		}
		if s.DurationSec <= 0 {
			t.Errorf("第 %d 段时长为 %.3f 秒", i, s.DurationSec)
		}
	}
	if !nearly(sum, 100, 0.1) {
		t.Errorf("各段占比之和应为 100%%，实际 %.4f%%", sum)
	}

	// 4:6 手法的前两段应占 40%
	firstTwo := c.Segments[0].SharePercent + c.Segments[1].SharePercent
	if !nearly(firstTwo, 40, 0.5) {
		t.Errorf("4:6 手法前两段应占 40%%，实际 %.2f%%", firstTwo)
	}
}

// TestBloomIsIdentifiedWithRatioToDose 验证闷蒸识别。
//
// 闷蒸水量的判断标准是"粉量的几倍"，而不是绝对克数 —— 20g 粉用 60g 水
// 是标准的 3 倍，40g 粉用 60g 水就只有 1.5 倍，明显不足。
// 若只报绝对值，用户得自己做除法。
func TestBloomIsIdentifiedWithRatioToDose(t *testing.T) {
	c := AnalyzePourCurve(fourSixEvents(t), mg("20", t))

	if !c.HasBloom {
		t.Fatal("首段 60g / 10 秒后有明显停顿，应识别为闷蒸")
	}
	if !nearly(c.BloomWaterG, 60, 0.01) {
		t.Errorf("闷蒸水量应为 60g，实际 %.3f", c.BloomWaterG)
	}
	if !nearly(c.BloomRatio, 3.0, 0.01) {
		t.Errorf("20g 粉配 60g 闷蒸水应为 3.0 倍，实际 %.3f 倍", c.BloomRatio)
	}
}

// TestBloomRatioIsOmittedWithoutDose 确认粉量缺失时不编一个比例出来。
func TestBloomRatioIsOmittedWithoutDose(t *testing.T) {
	c := AnalyzePourCurve(fourSixEvents(t), 0)

	if c.BloomRatio != 0 {
		t.Errorf("粉量为 0 时闷蒸比例应留空而非算出 %.3f 倍（那需要除以 0）",
			c.BloomRatio)
	}
	// 但曲线本身仍要能画
	if len(c.Points) != 6 || c.TotalWaterG == 0 {
		t.Error("粉量缺失只应影响闷蒸比例，不应让整条曲线失效")
	}
}

// TestPauseIsDetectedNotCountedAsSlowPour 验证断水识别。
//
// 断水期间秤示数几乎不动但时间在走。若不识别，这一段会被当成
// "极慢的注水"，算出的平均流速会把整条曲线的判断带偏。
func TestPauseIsDetectedNotCountedAsSlowPour(t *testing.T) {
	events := []PourEvent{
		{OffsetMs: 0, CumulativeMg: mg("0", t), Source: SourceManual},
		{OffsetMs: 10000, CumulativeMg: mg("60", t), Source: SourceManual},
		// 断水 20 秒，只有 0.1g 的滴落
		{OffsetMs: 30000, CumulativeMg: mg("60.1", t), Source: SourceManual},
		{OffsetMs: 60000, CumulativeMg: mg("200", t), Source: SourceManual},
	}

	c := AnalyzePourCurve(events, mg("20", t))

	if c.PauseCount != 1 {
		t.Errorf("应识别出 1 段断水，实际 %d 段", c.PauseCount)
	}
	if !c.Points[2].IsPause {
		t.Error("20 秒只滴了 0.1g 的那一段应标记为断水")
	}
	if c.Points[3].IsPause {
		t.Error("30 秒注入 140g 是正常注水，不应标记为断水")
	}
	if !c.Segments[1].IsPause {
		t.Error("断水标记应同时体现在段上，前端要靠它画虚线")
	}
}

// TestSlowButRealPourIsNotAPause 检查断水阈值没有把慢速注水误杀。
//
// 滴滤末段的下水速度可以低到 1 g/s。若阈值取得太高，这些正常注水
// 会被标成断水，曲线上会出现一堆莫须有的虚线段。
func TestSlowButRealPourIsNotAPause(t *testing.T) {
	events := []PourEvent{
		{OffsetMs: 0, CumulativeMg: mg("0", t), Source: SourceManual},
		// 10 秒注入 10g = 1 g/s，慢但确实在注水
		{OffsetMs: 10000, CumulativeMg: mg("10", t), Source: SourceManual},
	}

	c := AnalyzePourCurve(events, mg("20", t))

	if c.PauseCount != 0 {
		t.Errorf("1 g/s 是慢速注水而非断水，不应计入断水数，实际 %d 段",
			c.PauseCount)
	}
}

// TestPeakFlowRatePointsAtWhenNotJustHowMuch 确认峰值流速带时间戳。
//
// 只报"峰值 6 g/s"没什么用；用户要知道的是"在第 10 秒冲得太猛了"。
func TestPeakFlowRatePointsAtWhenNotJustHowMuch(t *testing.T) {
	c := AnalyzePourCurve(fourSixEvents(t), mg("20", t))

	if !nearly(c.PeakFlowRate, 6.0, 0.01) {
		t.Errorf("峰值流速应为闷蒸段的 6 g/s，实际 %.3f", c.PeakFlowRate)
	}
	if !nearly(c.PeakAtSec, 10, 0.01) {
		t.Errorf("峰值应出现在第 10 秒，实际第 %.2f 秒", c.PeakAtSec)
	}
}

// TestUnsortedEventsAreHandled 确认乱序到达的事件被排好序再分析。
//
// WebSocket 的消息顺序在弱网下并不保证。若直接按到达顺序做差分，
// 会算出负流速。
func TestUnsortedEventsAreHandled(t *testing.T) {
	ordered := fourSixEvents(t)
	shuffled := []PourEvent{ordered[3], ordered[0], ordered[5], ordered[1], ordered[4], ordered[2]}

	a := AnalyzePourCurve(ordered, mg("20", t))
	b := AnalyzePourCurve(shuffled, mg("20", t))

	if !nearly(a.TotalWaterG, b.TotalWaterG, 0.001) ||
		!nearly(a.AvgFlowRate, b.AvgFlowRate, 0.001) ||
		len(a.Points) != len(b.Points) {
		t.Errorf("乱序输入应得到相同分析结果：有序 %+v，乱序 %+v",
			a.TotalWaterG, b.TotalWaterG)
	}
	for i := range a.Points {
		if a.Points[i].OffsetMs != b.Points[i].OffsetMs {
			t.Fatalf("第 %d 点偏移不一致：%d vs %d",
				i, a.Points[i].OffsetMs, b.Points[i].OffsetMs)
		}
		if a.Points[i].FlowRate < 0 {
			t.Fatalf("第 %d 点流速为负 %.3f —— 说明乱序输入没有先排序",
				i, a.Points[i].FlowRate)
		}
	}
}

// TestAnalyzeDoesNotMutateInput 确认分析不改动调用方的切片。
//
// 分析内部要排序。若原地排序，调用方持有的切片顺序会被悄悄改掉 ——
// 而 service 层可能正准备把这批事件按原顺序写库。
func TestAnalyzeDoesNotMutateInput(t *testing.T) {
	events := fourSixEvents(t)
	scrambled := []PourEvent{events[4], events[1], events[0]}
	before := []int{scrambled[0].OffsetMs, scrambled[1].OffsetMs, scrambled[2].OffsetMs}

	_ = AnalyzePourCurve(scrambled, mg("20", t))

	for i := range before {
		if scrambled[i].OffsetMs != before[i] {
			t.Errorf("分析后调用方切片第 %d 项的偏移从 %d 变为 %d —— 发生了原地排序",
				i, before[i], scrambled[i].OffsetMs)
		}
	}
}

// TestEmptyCurveIsRenderableNotNil 确认空序列返回空切片而非 nil。
//
// nil 切片序列化成 JSON 的 null，前端拿到 null 去 .map() 会直接抛异常。
// 一次刚创建、还没开始注水的冲煮记录必然走到这条路径。
func TestEmptyCurveIsRenderableNotNil(t *testing.T) {
	c := AnalyzePourCurve(nil, mg("20", t))

	if c.Points == nil {
		t.Error("空曲线的 Points 应为空切片而非 nil，否则前端会拿到 JSON null")
	}
	if c.Segments == nil {
		t.Error("空曲线的 Segments 应为空切片而非 nil")
	}
	if c.Insights == nil {
		t.Error("空曲线的 Insights 应为空切片而非 nil")
	}
}

// TestMergeDeduplicatesByIdempotencyKey 是断线重连续传的核心。
//
// 客户端断网期间在本地缓存节点，重连后把缓存全部重发（它无法知道
// 哪些已经到了服务端）。服务端必须靠幂等键去重，否则曲线上会出现
// 一堆重叠的重复点。
func TestMergeDeduplicatesByIdempotencyKey(t *testing.T) {
	existing := []PourEvent{
		{OffsetMs: 0, CumulativeMg: mg("0", t), IdempotencyKey: "a"},
		{OffsetMs: 10000, CumulativeMg: mg("60", t), IdempotencyKey: "b"},
	}
	// 客户端重发了 a、b，外加两个新的
	incoming := []PourEvent{
		{OffsetMs: 0, CumulativeMg: mg("0", t), IdempotencyKey: "a"},
		{OffsetMs: 10000, CumulativeMg: mg("60", t), IdempotencyKey: "b"},
		{OffsetMs: 45000, CumulativeMg: mg("120", t), IdempotencyKey: "c"},
		{OffsetMs: 75000, CumulativeMg: mg("180", t), IdempotencyKey: "d"},
	}

	merged := MergePourEvents(existing, incoming)

	if len(merged) != 4 {
		t.Fatalf("2 条已有 + 4 条重发（含 2 条重复）应合成 4 条，实际 %d 条", len(merged))
	}
	for i := 1; i < len(merged); i++ {
		if merged[i].OffsetMs <= merged[i-1].OffsetMs {
			t.Errorf("合并结果应按偏移升序：第 %d 项 %d 未大于前项 %d",
				i, merged[i].OffsetMs, merged[i-1].OffsetMs)
		}
	}
}

// TestMergeIsIdempotentUnderRepetition 确认反复合并同一批数据收敛。
//
// 弱网下同一批缓存可能被重发多次。若每次合并都增长，一次糟糕的网络
// 就能把一条曲线撑成几百个重复点。
func TestMergeIsIdempotentUnderRepetition(t *testing.T) {
	batch := fourSixEvents(t)

	acc := MergePourEvents(nil, batch)
	first := len(acc)
	for i := 0; i < 10; i++ {
		acc = MergePourEvents(acc, batch)
	}

	if len(acc) != first {
		t.Errorf("重复合并同一批 %d 条事件 10 次后应仍为 %d 条，实际 %d 条",
			first, first, len(acc))
	}
}

// TestMergeFallsBackToOffsetWhenKeyMissing 验证幂等键缺失时的兜底。
//
// 手动录入的节点没有客户端生成的幂等键。此时"同一毫秒偏移视为同一事件"
// 是唯一合理的规则 —— 累计示数的语义决定了同一时刻不可能有两个值。
func TestMergeFallsBackToOffsetWhenKeyMissing(t *testing.T) {
	existing := []PourEvent{
		{OffsetMs: 10000, CumulativeMg: mg("60", t)},
	}
	incoming := []PourEvent{
		{OffsetMs: 10000, CumulativeMg: mg("65", t)}, // 同偏移，修正了数值
		{OffsetMs: 20000, CumulativeMg: mg("120", t)},
	}

	merged := MergePourEvents(existing, incoming)

	if len(merged) != 2 {
		t.Fatalf("同偏移应视为同一事件，2 + 1 新 应合成 2 条，实际 %d 条", len(merged))
	}
	// 后到的覆盖先到的：更可能是重传的修正值
	if merged[0].CumulativeMg != mg("65", t) {
		t.Errorf("同偏移冲突时应由后到者覆盖（视为修正值），实际保留了 %s",
			merged[0].CumulativeMg.Grams())
	}
}

// TestMergeCorrectionOverwritesRatherThanAppends 确认带同一幂等键的修正值
// 是覆盖而非追加。
func TestMergeCorrectionOverwritesRatherThanAppends(t *testing.T) {
	existing := []PourEvent{
		{OffsetMs: 10000, CumulativeMg: mg("60", t), IdempotencyKey: "b",
			Technique: domain.PourCenter},
	}
	incoming := []PourEvent{
		{OffsetMs: 10000, CumulativeMg: mg("62", t), IdempotencyKey: "b",
			Technique: domain.PourSpiral},
	}

	merged := MergePourEvents(existing, incoming)

	if len(merged) != 1 {
		t.Fatalf("同幂等键应合成 1 条，实际 %d 条", len(merged))
	}
	if merged[0].CumulativeMg != mg("62", t) || merged[0].Technique != domain.PourSpiral {
		t.Errorf("修正值应完整覆盖旧值，实际 %s / %s",
			merged[0].CumulativeMg.Grams(), merged[0].Technique)
	}
}

// TestValidateRejectsDecreasingCumulative 是最重要的一条输入校验。
//
// 客户端最容易犯的错是把"本次注入量"当"累计量"发。那样每条事件的
// 数值都很小且不单调，流速会算出负数，曲线会向下拐。这个错误必须在
// 入口拦住，而且提示语要直接点出真正的原因。
func TestValidateRejectsDecreasingCumulative(t *testing.T) {
	events := []PourEvent{
		{OffsetMs: 0, CumulativeMg: mg("0", t)},
		{OffsetMs: 10000, CumulativeMg: mg("60", t)},
		{OffsetMs: 20000, CumulativeMg: mg("55", t)}, // 递减
	}

	err := ValidatePourEvents(events)
	if err == nil {
		t.Fatal("累计注水量递减必须被拒绝，否则会算出负流速")
	}

	de := domain.AsDomain(err)
	if de.Code != "INVALID_POUR_EVENTS" {
		t.Errorf("错误码应为 INVALID_POUR_EVENTS，实际 %q", de.Code)
	}
	if len(de.Fields) == 0 {
		t.Error("应定位到具体是哪一条事件出的问题，前端才能高亮那一行")
	}
}

// TestValidateCatchesSecondsMistakenForMillis 检查单位混淆的诊断。
//
// 把秒当毫秒填是个高频错误：一次 135 秒的冲煮填成 135，看起来"是个正常数"，
// 但曲线会挤成起点处的一个点。反过来把毫秒当秒填会得到 135000 秒 = 37 小时，
// 这个方向能靠上限拦住并给出明确提示。
func TestValidateCatchesSecondsMistakenForMillis(t *testing.T) {
	events := []PourEvent{
		{OffsetMs: 0, CumulativeMg: mg("0", t)},
		{OffsetMs: 135 * 1000 * 1000, CumulativeMg: mg("300", t)},
	}

	err := ValidatePourEvents(events)
	if err == nil {
		t.Fatal("偏移 37 小时应被拒绝")
	}

	de := domain.AsDomain(err)
	var mentionsUnit bool
	for _, f := range de.Fields {
		if contains(f.Reason, "毫秒") {
			mentionsUnit = true
		}
	}
	if !mentionsUnit {
		t.Errorf("提示语应点出单位问题而不只说「超出范围」，实际 %v", de.Fields)
	}
}

// TestValidateRejectsNegatives 确认负偏移与负水量被拒绝。
func TestValidateRejectsNegatives(t *testing.T) {
	cases := []struct {
		name  string
		event PourEvent
	}{
		{"负时间偏移", PourEvent{OffsetMs: -1, CumulativeMg: mg("10", t)}},
		{"负累计水量", PourEvent{OffsetMs: 1000, CumulativeMg: -1000}},
	}
	for _, c := range cases {
		if err := ValidatePourEvents([]PourEvent{c.event}); err == nil {
			t.Errorf("%s 应被拒绝", c.name)
		}
	}
}

// TestValidateAccumulatesAllProblems 确认一次返回全部问题而非只报第一个。
//
// 逐个报错会让用户改一处、提交一次、再被拒一次。表单类接口应该一次说完。
func TestValidateAccumulatesAllProblems(t *testing.T) {
	events := []PourEvent{
		{OffsetMs: -5, CumulativeMg: mg("10", t)},
		{OffsetMs: 1000, CumulativeMg: -20},
	}

	de := domain.AsDomain(ValidatePourEvents(events))
	if len(de.Fields) < 2 {
		t.Errorf("两条事件各有问题，应一次性报出至少 2 项，实际 %d 项：%v",
			len(de.Fields), de.Fields)
	}
}

// TestValidAndEmptySequencesPass 确认正常序列与空序列都不报错。
//
// 空序列必须放过：一条刚创建、还没开始注水的记录就是这个状态。
func TestValidAndEmptySequencesPass(t *testing.T) {
	if err := ValidatePourEvents(nil); err != nil {
		t.Errorf("空序列应通过校验（刚创建的冲煮记录就是这样），实际 %v", err)
	}
	if err := ValidatePourEvents(fourSixEvents(t)); err != nil {
		t.Errorf("标准 4:6 序列应通过校验，实际 %v", err)
	}
}

// TestSourceSummaryDistinguishesProvenance 确认来源摘要能区分数据出处。
//
// 模拟器生成的曲线和真实智能秤记录的曲线不能长得一样 —— 用户翻看
// 历史记录时必须能分清哪次是演示数据。
func TestSourceSummaryDistinguishesProvenance(t *testing.T) {
	simulated := fourSixEvents(t)
	for i := range simulated {
		simulated[i].Source = SourceSimulator
	}

	manual := AnalyzePourCurve(fourSixEvents(t), mg("20", t))
	sim := AnalyzePourCurve(simulated, mg("20", t))

	if manual.SourceSummary == sim.SourceSummary {
		t.Errorf("手动录入与模拟器生成的来源摘要不应相同，都是 %q",
			manual.SourceSummary)
	}
	if sim.SourceSummary == "" {
		t.Error("来源摘要不应为空，用户需要知道这条曲线是怎么来的")
	}
}

// TestTimeLabelIsStopwatchFormat 确认时间标签是秒表格式而非裸毫秒。
//
// 前端的横轴刻度直接用这个标签。给它 135000 它只能自己去格式化，
// 而格式化规则（要不要显示小时、保留几位小数）属于展示约定，
// 后端定一次比前端各处各定一次可靠。
func TestTimeLabelIsStopwatchFormat(t *testing.T) {
	c := AnalyzePourCurve(fourSixEvents(t), mg("20", t))

	cases := map[int]string{
		0:      "0:00.0",
		10000:  "0:10.0",
		75000:  "1:15.0",
		135000: "2:15.0",
	}
	for _, p := range c.Points {
		want, ok := cases[p.OffsetMs]
		if !ok {
			continue
		}
		if p.TimeLabel != want {
			t.Errorf("偏移 %dms 的秒表标签应为 %q，实际 %q",
				p.OffsetMs, want, p.TimeLabel)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
