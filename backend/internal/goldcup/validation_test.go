package goldcup

import (
	"errors"
	"strings"
	"testing"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

// 本文件测两件事：配置校验与输入校验。
//
// 两者的共同要求是「一次报全部错」而不是逐个报。设置页有十来个数字输入框，
// 若每次提交只告诉用户一个错，改十个字段要来回提交十次 —— 而每次提交
// 之间他都不知道还剩几个坑。收集式校验把这变成一轮。

// fieldsOf 取出错误里的字段名集合，便于断言"该报的都报了"。
func fieldsOf(t *testing.T, err error) map[string]string {
	t.Helper()
	if err == nil {
		t.Fatal("期望一个校验错误，实际为 nil")
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		t.Fatalf("期望 domain.Error，实际 %T: %v", err, err)
	}
	out := make(map[string]string, len(de.Fields))
	for _, f := range de.Fields {
		out[f.Field] = f.Reason
	}
	return out
}

// ---------------------------------------------------------------------------
// 金杯标准配置校验
// ---------------------------------------------------------------------------

// TestValidProfilesPassValidation 验证出厂标准自身是合法的。
//
// 若出厂配置过不了自己的校验，任何"恢复默认"的操作都会失败。
func TestValidProfilesPassValidation(t *testing.T) {
	for _, p := range []Profile{filterP(t), espressoP(t)} {
		if err := p.Validate(); err != nil {
			t.Errorf("%s 的出厂标准过不了自己的校验: %v", p.Method, err)
		}
	}
}

// TestProfileValidationReportsEveryProblemAtOnce 验证配置校验一次报全。
func TestProfileValidationReportsEveryProblemAtOnce(t *testing.T) {
	p := filterP(t)
	// 同时弄坏四处：萃取率区间倒置、浓度区间倒置、粉液比倒置、持水系数越界。
	p.YieldMin, p.YieldMax = fixed.MustRatioPercent("22"), fixed.MustRatioPercent("18")
	p.StrengthMin, p.StrengthMax = fixed.MustRatioPercent("1.35"), fixed.MustRatioPercent("1.15")
	p.RatioMin, p.RatioMax = fixed.MustRatioMultiple("17"), fixed.MustRatioMultiple("15")
	p.LRR = fixed.MustRatioMultiple("9.0")

	fields := fieldsOf(t, p.Validate())
	for _, want := range []string{"yield_range", "strength_range", "ratio_range", "lrr"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("四处同时出错时应报出 %s，实际只报了 %v", want, keysOf(fields))
		}
	}
}

// TestProfileRejectsImpossibleYieldCeiling 验证萃取率上界不能超过物理上限。
//
// 最常见的成因是单位填错：把 0.20 当成 20、把 20 当成 2000。
// 若放过去，整张控制图的坐标轴会被撑到荒谬的范围，所有历史点挤成一团。
func TestProfileRejectsImpossibleYieldCeiling(t *testing.T) {
	p := filterP(t)
	p.YieldMax = fixed.MustRatioPercent("95")

	fields := fieldsOf(t, p.Validate())
	if _, ok := fields["yield_max"]; !ok {
		t.Errorf("95%% 的萃取率上界应被拒绝，实际报出的字段为 %v", keysOf(fields))
	}
	if !strings.Contains(fields["yield_max"], "30") {
		t.Errorf("说明里应给出 30%% 这个上限依据，实际: %s", fields["yield_max"])
	}
}

// TestProfileRejectsThirtyPercentPlusEvenWhenRangeIsOrdered 验证区间合法但上界越界时仍被拒。
func TestProfileRejectsThirtyPercentPlusEvenWhenRangeIsOrdered(t *testing.T) {
	p := filterP(t)
	p.YieldMin, p.YieldMax = fixed.MustRatioPercent("31"), fixed.MustRatioPercent("35")
	if err := p.Validate(); err == nil {
		t.Error("31%–35% 虽然区间顺序正确，但整段都在物理上限之上，应被拒绝")
	}
}

// TestProfileRejectsUnknownMethod 验证未知冲煮法被拒。
func TestProfileRejectsUnknownMethod(t *testing.T) {
	p := filterP(t)
	p.Method = "FRENCH_PRESS"
	err := p.Validate()
	if err == nil {
		t.Fatal("未知冲煮法应被拒绝")
	}
	// 冲煮法非法时其余字段的校验没有意义，应当直接返回而非继续收集。
	if fields := fieldsOf(t, err); len(fields) != 1 {
		t.Errorf("冲煮法非法时只需报这一项，实际报了 %v", keysOf(fields))
	}
}

// TestLRROnlyValidatedWhenUsed 验证意式不校验持水系数。
//
// 意式直接称液重，LRR 不参与计算。对一个用不上的字段做区间校验，
// 会让意式的配置因为一个无关字段而保存失败。
func TestLRROnlyValidatedWhenUsed(t *testing.T) {
	p := espressoP(t)
	if p.UsesLRR {
		t.Skip("意式配置已改为使用 LRR，本测试的前提不再成立")
	}
	p.LRR = fixed.MustRatioMultiple("99")
	if err := p.Validate(); err != nil {
		t.Errorf("意式用不到持水系数，不该因它而校验失败: %v", err)
	}
}

// TestZeroBoundsAreRejected 验证零值边界被拒。
func TestZeroBoundsAreRejected(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*Profile)
		field string
	}{
		{"萃取率下界为零", func(p *Profile) { p.YieldMin = 0 }, "yield_range"},
		{"浓度上界为零", func(p *Profile) { p.StrengthMax = 0 }, "strength_range"},
		{"粉液比下界为零", func(p *Profile) { p.RatioMin = 0 }, "ratio_range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filterP(t)
			tc.mut(&p)
			fields := fieldsOf(t, p.Validate())
			if _, ok := fields[tc.field]; !ok {
				t.Errorf("应报出 %s，实际 %v", tc.field, keysOf(fields))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 萃取输入的物理合理性校验
// ---------------------------------------------------------------------------

// TestUnitConfusionIsCaughtWithAnExplanation 验证单位填错被拦下并说明。
//
// 这是真实用户最容易犯的错：把 20g 粉填成 20000（毫克当克），
// 或把 TDS 1.35% 填成 135。数值本身"能算"，算出来的结论却荒谬。
// 拦下来还不够 —— 报错必须点出"你可能搞错了单位"，否则用户只会
// 反复确认自己确实输入了 135。
func TestUnitConfusionIsCaughtWithAnExplanation(t *testing.T) {
	e := NewEngine(nil)
	cases := []struct {
		name     string
		in       Input
		field    string
		mentions string
	}{
		{
			name:     "粉量按毫克填",
			in:       Input{Method: domain.MethodFilter, Dose: g(t, "20000"), MeasuredBeverage: g(t, "300"), TDS: fixed.MustRatioPercent("1.30")},
			field:    "dose_g",
			mentions: "克",
		},
		{
			name:     "粉量小到不像克",
			in:       Input{Method: domain.MethodFilter, Dose: fixed.Mass(1), MeasuredBeverage: g(t, "300"), TDS: fixed.MustRatioPercent("1.30")},
			field:    "dose_g",
			mentions: "克",
		},
		{
			name:     "TDS 漏了百分号",
			in:       Input{Method: domain.MethodFilter, Dose: g(t, "20"), MeasuredBeverage: g(t, "300"), TDS: fixed.MustRatioPercent("135")},
			field:    "tds_percent",
			mentions: "百分数",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.Evaluate(tc.in, nil)
			fields := fieldsOf(t, err)
			reason, ok := fields[tc.field]
			if !ok {
				t.Fatalf("应报出 %s，实际 %v", tc.field, keysOf(fields))
			}
			if !strings.Contains(reason, tc.mentions) {
				t.Errorf("说明里应提到 %q 以提示单位问题，实际: %s", tc.mentions, reason)
			}
		})
	}
}

// TestNegativeMassesAreRejected 验证负数质量被拒。
func TestNegativeMassesAreRejected(t *testing.T) {
	e := NewEngine(nil)
	cases := []struct {
		name  string
		in    Input
		field string
	}{
		{"粉量为负", Input{Method: domain.MethodFilter, Dose: fixed.Mass(-1000), MeasuredBeverage: g(t, "300")}, "dose_g"},
		{"总注水量为负", Input{Method: domain.MethodFilter, Dose: g(t, "20"), TotalWater: fixed.Mass(-1000)}, "total_water_g"},
		{"液重为负", Input{Method: domain.MethodFilter, Dose: g(t, "20"), MeasuredBeverage: fixed.Mass(-1000)}, "beverage_mass_g"},
		{"TDS 为负", Input{Method: domain.MethodFilter, Dose: g(t, "20"), MeasuredBeverage: g(t, "300"), TDS: fixed.Ratio(-100)}, "tds_percent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.Evaluate(tc.in, nil)
			fields := fieldsOf(t, err)
			if _, ok := fields[tc.field]; !ok {
				t.Errorf("应报出 %s，实际 %v", tc.field, keysOf(fields))
			}
		})
	}
}

// TestOversizedInputsAreRejected 验证超大输入被拒。
func TestOversizedInputsAreRejected(t *testing.T) {
	e := NewEngine(nil)
	cases := []struct {
		name  string
		in    Input
		field string
	}{
		{"总注水量超上限", Input{Method: domain.MethodFilter, Dose: g(t, "20"), TotalWater: g(t, "60000")}, "total_water_g"},
		{"液重超上限", Input{Method: domain.MethodFilter, Dose: g(t, "20"), MeasuredBeverage: g(t, "60000")}, "beverage_mass_g"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := e.Evaluate(tc.in, nil)
			fields := fieldsOf(t, err)
			if _, ok := fields[tc.field]; !ok {
				t.Errorf("应报出 %s，实际 %v", tc.field, keysOf(fields))
			}
		})
	}
}

// TestMultipleInputProblemsAreReportedTogether 验证输入校验也一次报全。
func TestMultipleInputProblemsAreReportedTogether(t *testing.T) {
	e := NewEngine(nil)
	_, err := e.Evaluate(Input{
		Method:           domain.MethodFilter,
		Dose:             g(t, "20000"),                 // 超上限
		TotalWater:       fixed.Mass(-5000),             // 负数
		MeasuredBeverage: g(t, "60000"),                 // 超上限
		TDS:              fixed.MustRatioPercent("135"), // 超上限
	}, nil)

	fields := fieldsOf(t, err)
	for _, want := range []string{"dose_g", "total_water_g", "beverage_mass_g", "tds_percent"} {
		if _, ok := fields[want]; !ok {
			t.Errorf("四处同时出错时应报出 %s，实际 %v", want, keysOf(fields))
		}
	}
}

// TestZeroTDSIsLegalAndMeansEstimated 验证 TDS 缺省是合法输入而非错误。
//
// 大多数用户没有折射仪。若把"没填 TDS"当成校验错误，这个应用就只服务
// 有设备的少数人了 —— 推算模式的存在就是为了服务其余人。
func TestZeroTDSIsLegalAndMeansEstimated(t *testing.T) {
	e := NewEngine(nil)
	res, err := e.Evaluate(Input{
		Method:      domain.MethodFilter,
		Dose:        g(t, "20"),
		TotalWater:  g(t, "300"),
		GrindMicron: 700, WaterTempC: 92, ContactSeconds: 150,
	}, nil)
	if err != nil {
		t.Fatalf("不填 TDS 应走推算模式，而不是报错: %v", err)
	}
	if res.Mode != ModeEstimated {
		t.Errorf("未提供 TDS 时应为推算模式，实际 %s", res.Mode)
	}
	if !res.Advisory {
		t.Error("推算结果必须标注 advisory")
	}
	if res.Estimation == nil {
		t.Fatal("推算模式应给出推算说明块")
	}
	if res.Estimation.Disclaimer == "" {
		t.Error("推算说明必须带免责声明")
	}
}

// ---------------------------------------------------------------------------
// 推算置信度
// ---------------------------------------------------------------------------

// histSample 造一条自洽的历史样本。
//
// 「自洽」在这里是硬要求：回归拟合的是 TDS ~ 粉量/液重，斜率即萃取率。
// 若样本的 Yield 字段与 TDS×液重/粉量 算出来的不一致，残差里就混进了
// 我编造的矛盾，离散度惩罚会被这份矛盾主导，测出来的是我的笔误而不是
// 引擎的行为。我第一版就是这么写的，结果紧密组和离散组的置信度一模一样。
//
// 固定 粉量 20g / 液重 250g，于是 粉量/液重 = 0.08，TDS = 萃取率 × 0.08。
func histSample(t *testing.T, id int64, yieldPercent float64) Sample {
	t.Helper()
	const dose, bev = 20.0, 250.0
	tds := yieldPercent * dose / bev
	return Sample{
		BrewID:      id,
		Yield:       fixed.MustRatioPercent(ftoa(yieldPercent)),
		TDS:         fixed.MustRatioPercent(ftoa(tds)),
		Dose:        g(t, "20"),
		Beverage:    g(t, "250"),
		GrindMicron: 700, WaterTempC: 92, ContactSeconds: 150,
	}
}

// ftoa 把浮点百分数写成四位小数的十进制串（fixed.Ratio 的分辨率上限）。
func ftoa(v float64) string {
	x := int64(v*10000 + 0.5)
	whole, frac := x/10000, x%10000
	s := itoa(int(frac))
	for len(s) < 4 {
		s = "0" + s
	}
	return itoa(int(whole)) + "." + s
}

// TestConfidenceRisesWithSampleCount 验证样本越多置信度越高。
//
// 置信度是用户决定"要不要相信这个推算值"的唯一依据。若它不随样本量变化，
// 那它就只是一个装饰性的数字。
func TestConfidenceRisesWithSampleCount(t *testing.T) {
	e := NewEngine(nil)
	in := Input{
		Method: domain.MethodFilter, Dose: g(t, "20"), TotalWater: g(t, "300"),
		GrindMicron: 700, WaterTempC: 92, ContactSeconds: 150,
	}

	mk := func(n int) []Sample {
		out := make([]Sample, 0, n)
		for i := 0; i < n; i++ {
			// 紧密聚集（19.9–20.1）→ 低离散度 → 不触发惩罚
			out = append(out, histSample(t, int64(i+1), 20.0+float64(i%3-1)*0.1))
		}
		return out
	}

	var prev float64
	for _, n := range []int{0, 3, 6, 12} {
		res, err := e.Evaluate(in, mk(n))
		if err != nil {
			t.Fatalf("%d 个样本时推算失败: %v", n, err)
		}
		if res.Estimation == nil {
			t.Fatalf("%d 个样本时缺少推算说明", n)
		}
		got := res.Estimation.Confidence
		if got < 0 || got > 1 {
			t.Errorf("%d 个样本的置信度 %g 越出 [0,1]", n, got)
		}
		if n > 0 && got < prev {
			t.Errorf("样本从上一档增至 %d 个，置信度反而从 %g 降到 %g", n, prev, got)
		}
		prev = got
	}
}

// TestNoSamplesFallsBackToLowConfidenceKinetics 验证零样本时回落到低置信度经验模型。
//
// 这条对应"样本不足时不得编造"：引擎仍给出一个数（用户总得有个起点），
// 但必须同时把置信度压到最低并说明依据是通用经验而非他自己的数据。
func TestNoSamplesFallsBackToLowConfidenceKinetics(t *testing.T) {
	e := NewEngine(nil)
	res, err := e.Evaluate(Input{
		Method: domain.MethodFilter, Dose: g(t, "20"), TotalWater: g(t, "300"),
		GrindMicron: 700, WaterTempC: 92, ContactSeconds: 150,
	}, nil)
	if err != nil {
		t.Fatalf("零样本推算失败: %v", err)
	}
	est := res.Estimation
	if est == nil {
		t.Fatal("缺少推算说明")
	}
	if est.ConfidenceTier != ConfLow {
		t.Errorf("零样本时置信度档位应为 %s，实际 %s", ConfLow, est.ConfidenceTier)
	}
	if est.SampleSize != 0 {
		t.Errorf("零样本时样本数应为 0，实际 %d", est.SampleSize)
	}
	if len(est.Basis) == 0 {
		t.Error("必须说明推算依据，否则用户无从判断该不该相信")
	}
}

// TestScatteredSamplesLowerConfidence 验证样本离散时置信度被惩罚。
//
// 十条互相矛盾的历史记录不比三条一致的记录更可信。若只看数量不看离散度，
// 一个每次都换参数乱试的用户会得到"高置信度"的推算 —— 恰好相反。
func TestScatteredSamplesLowerConfidence(t *testing.T) {
	e := NewEngine(nil)
	in := Input{
		Method: domain.MethodFilter, Dose: g(t, "20"), TotalWater: g(t, "300"),
		GrindMicron: 700, WaterTempC: 92, ContactSeconds: 150,
	}

	mk := func(yields []float64) []Sample {
		out := make([]Sample, 0, len(yields))
		for i, y := range yields {
			out = append(out, histSample(t, int64(i+1), y))
		}
		return out
	}

	tight := []float64{20.0, 20.1, 19.9, 20.0, 20.1, 19.9, 20.0, 20.1, 19.9, 20.0}
	loose := []float64{14.0, 24.0, 16.0, 23.0, 15.0, 25.0, 17.0, 22.0, 13.0, 26.0}

	tightRes, err := e.Evaluate(in, mk(tight))
	if err != nil {
		t.Fatalf("紧密样本推算失败: %v", err)
	}
	looseRes, err := e.Evaluate(in, mk(loose))
	if err != nil {
		t.Fatalf("离散样本推算失败: %v", err)
	}
	if tightRes.Estimation == nil || looseRes.Estimation == nil {
		t.Fatal("两组都应给出推算说明")
	}

	if looseRes.Estimation.Confidence >= tightRes.Estimation.Confidence {
		t.Errorf("同为 10 个样本，离散组的置信度 %g 不应高于紧密组的 %g —— "+
			"数量不能替代一致性",
			looseRes.Estimation.Confidence, tightRes.Estimation.Confidence)
	}
}

// TestEstimationRangeWidensWithLowerConfidence 验证置信度低时区间更宽。
//
// 置信度和区间宽度必须同向变化。若低置信度还给出一个很窄的区间，
// 那两个数字互相矛盾，用户不知道该信哪个。
func TestEstimationRangeWidensWithLowerConfidence(t *testing.T) {
	e := NewEngine(nil)
	in := Input{
		Method: domain.MethodFilter, Dose: g(t, "20"), TotalWater: g(t, "300"),
		GrindMicron: 700, WaterTempC: 92, ContactSeconds: 150,
	}

	var samples []Sample
	for i := 0; i < 12; i++ {
		samples = append(samples, histSample(t, int64(i+1), 20.0+float64(i%3-1)*0.1))
	}

	high, err := e.Evaluate(in, samples)
	if err != nil {
		t.Fatalf("高置信度推算失败: %v", err)
	}
	low, err := e.Evaluate(in, nil)
	if err != nil {
		t.Fatalf("低置信度推算失败: %v", err)
	}

	width := func(r *Result) float64 {
		return r.Estimation.YieldUpperPercent - r.Estimation.YieldLowerPercent
	}
	if width(low) <= width(high) {
		t.Errorf("零样本（置信度 %g）的区间宽度 %g 应大于 12 样本（置信度 %g）的 %g",
			low.Estimation.Confidence, width(low),
			high.Estimation.Confidence, width(high))
	}
	for _, r := range []*Result{high, low} {
		if r.Estimation.YieldLowerPercent > r.Estimation.YieldUpperPercent {
			t.Errorf("区间上下界颠倒: [%g, %g]",
				r.Estimation.YieldLowerPercent, r.Estimation.YieldUpperPercent)
		}
		if r.Estimation.YieldRangeText == "" {
			t.Error("区间必须有可展示的文案")
		}
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
