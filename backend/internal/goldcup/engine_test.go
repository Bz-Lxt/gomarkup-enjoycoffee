package goldcup

import (
	"math/big"
	"testing"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

func pct(t *testing.T, s string) fixed.Ratio {
	t.Helper()
	v, err := fixed.ParsePercent(s)
	if err != nil {
		t.Fatalf("ParsePercent(%q): %v", s, err)
	}
	return v
}

func g(t *testing.T, s string) fixed.Mass {
	t.Helper()
	v, err := fixed.ParseGrams(s)
	if err != nil {
		t.Fatalf("ParseGrams(%q): %v", s, err)
	}
	return v
}

// TestExtractionYieldMatchesExactRational 是 NFR-03 的核心断言：
// 引擎算出的萃取率与有理数精确解**完全相等**，用 == 判定而非 epsilon 容差。
//
// 之所以能断言相等而不是"足够接近"：ExtractionYield 内部把
// 液重 × TDS ÷ 粉量 写成一条 big.Rat 表达式，只在最后量化一次。
// 因此它与"直接用有理数算"的唯一差别就是那一次量化，
// 而对照组也用同一个量化函数。
func TestExtractionYieldMatchesExactRational(t *testing.T) {
	cases := []struct{ bev, tds, dose string }{
		{"288", "1.25", "18"},
		{"288", "1.30", "18"},
		{"288", "1.11", "18"},
		{"36", "9.5", "18"},
		{"27", "11.5", "18"},
		{"320.4", "1.28", "20.5"},
		{"300", "1.29", "19.3"},
		{"250.3", "1.37", "15.7"},
		{"210.7", "1.42", "14.3"},
		{"333.3", "1.19", "21.7"},
	}

	for _, c := range cases {
		bev, tds, dose := g(t, c.bev), pct(t, c.tds), g(t, c.dose)

		got, err := ExtractionYield(bev, tds, dose)
		if err != nil {
			t.Fatalf("ExtractionYield(%s, %s, %s): %v", c.bev, c.tds, c.dose, err)
		}

		exact := new(big.Rat).Mul(bev.RawRat(), tds.RawRat())
		exact.Quo(exact, dose.RawRat())
		want, err := fixed.RatioFromPPMRat(exact)
		if err != nil {
			t.Fatal(err)
		}

		if got != want {
			t.Errorf("液重 %sg / TDS %s%% / 粉量 %sg：引擎给出 %d PPM，精确解 %d PPM",
				c.bev, c.tds, c.dose, got, want)
		}
	}
}

// TestGoldCupBoundaryIsExact 验证金杯边界的判定是精确的闭区间。
//
// 这是定点数最直接的回报：18.0000% 必须判为"在区间内"，
// 而 17.9999% 必须判为"欠萃"。差一个 PPM 就要给出不同结论，
// 这种判定不能建立在"两个浮点数恰好朝同一方向舍入"之上。
func TestGoldCupBoundaryIsExact(t *testing.T) {
	e := NewEngine(nil)
	p, err := e.ProfileFor(domain.MethodFilter)
	if err != nil {
		t.Fatal(err)
	}

	if p.YieldMin != 180000 || p.YieldMax != 220000 {
		t.Fatalf("出厂手冲萃取率区间应为 18%%–22%%，实际 %d–%d PPM",
			p.YieldMin, p.YieldMax)
	}

	cases := []struct {
		yieldPPM fixed.Ratio
		wantZone YieldZone
		desc     string
	}{
		{179999, YieldUnder, "比下界低 1 PPM"},
		{180000, YieldIdeal, "恰好等于下界"},
		{180001, YieldIdeal, "比下界高 1 PPM"},
		{219999, YieldIdeal, "比上界低 1 PPM"},
		{220000, YieldIdeal, "恰好等于上界"},
		{220001, YieldOver, "比上界高 1 PPM"},
	}

	for _, c := range cases {
		got := classify(p, c.yieldPPM, p.StrengthMidpoint()).Yield
		if got != c.wantZone {
			t.Errorf("%s（%d PPM = %s%%）：判为 %s，期望 %s",
				c.desc, c.yieldPPM, c.yieldPPM.Percent(), got, c.wantZone)
		}
	}
}

// TestFilterAndEspressoHaveDifferentStandards 验证裁定 C-04：
// 手冲与意式必须用各自的浓度区间，不能共用一套。
//
// 若共用手冲的 1.15%–1.35%，任何一杯正常的意式浓缩（TDS 8%–12%）
// 都会被判成"浓度过高"，而它其实完全正常。这是需求里被明确点出的矛盾点。
func TestFilterAndEspressoHaveDifferentStandards(t *testing.T) {
	e := NewEngine(nil)

	filter, err := e.ProfileFor(domain.MethodFilter)
	if err != nil {
		t.Fatal(err)
	}
	espresso, err := e.ProfileFor(domain.MethodEspresso)
	if err != nil {
		t.Fatal(err)
	}

	if filter.StrengthMax >= espresso.StrengthMin {
		t.Errorf("手冲浓度上界（%s%%）应当远低于意式浓度下界（%s%%），"+
			"否则两套标准形同共用",
			filter.StrengthMax.Percent(), espresso.StrengthMin.Percent())
	}

	// 一杯典型意式：TDS 9.5% —— 按意式标准是正常，按手冲标准会被判过浓
	espressoTDS := pct(t, "9.5")
	inEspresso := classify(espresso, espresso.YieldMidpoint(), espressoTDS).Strength
	inFilter := classify(filter, filter.YieldMidpoint(), espressoTDS).Strength
	if inEspresso != StrengthIdeal {
		t.Errorf("TDS 9.5%% 在意式标准下应判为浓度合适，实际 %s", inEspresso)
	}
	if inFilter == StrengthIdeal {
		t.Error("TDS 9.5%% 在手冲标准下不应判为浓度合适 —— " +
			"若判为合适说明两套标准的区间被写成了同一组")
	}

	// LRR 只对手冲有意义
	if !filter.UsesLRR {
		t.Error("手冲必须使用持水系数推导液重")
	}
	if espresso.UsesLRR {
		t.Error("意式不应使用持水系数：粉饼持水量受压力与研磨度影响，" +
			"固定系数的误差大于判定分辨率")
	}
}

// TestMeasuredModeEvaluatesGoldCup 验证实测模式下一杯落在区间内的咖啡
// 被正确判为合格。
func TestMeasuredModeEvaluatesGoldCup(t *testing.T) {
	e := NewEngine(nil)

	res, err := e.Evaluate(Input{
		Method:     domain.MethodFilter,
		Dose:       g(t, "18"),
		TotalWater: g(t, "324"),
		TDS:        pct(t, "1.30"),
	}, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if res.Mode != ModeMeasured {
		t.Errorf("提供了 TDS 应走实测模式，实际 %s", res.Mode)
	}
	// 液重 = 324 − 18×2 = 288g；萃取率 = 288 × 1.30%% ÷ 18 = 20.80%%
	if res.RawBeverage != g(t, "288") {
		t.Errorf("液重应为 288g，实际 %s", res.RawBeverage.Grams())
	}
	if res.RawYield != 208000 {
		t.Errorf("萃取率应为 208000 PPM（20.80%%），实际 %d PPM（%s%%）",
			res.RawYield, res.RawYield.Percent())
	}
	if !res.Zone.InGoldCup {
		t.Errorf("20.80%% 萃取率 / 1.30%% 浓度应落在金杯区间内，实际判定 %s",
			res.Zone.Code)
	}
	if res.Advisory {
		t.Error("实测模式不应被标记为 advisory（推算）")
	}
	if res.Estimation != nil {
		t.Error("实测模式不应携带 Estimation —— 它是推算模式专属的置信度说明")
	}
}

// TestEstimatedModeNeverClaimsGoldCup 验证裁定 C-01 的硬约束：
// 推算模式不得输出"落在金杯区间内"的结论。
//
// 这条约束的意义在于诚实：没有折射仪测量值时，萃取率是从研磨度、水温、
// 时间这些间接参数推出来的，误差远大于金杯区间的宽度。若允许推算结果
// 显示"合格"，用户会以为自己拿到了一个测量结论，而它其实是一个猜测。
func TestEstimatedModeNeverClaimsGoldCup(t *testing.T) {
	e := NewEngine(nil)

	// 刻意构造一组参数，让推算出的萃取率大概率落在 18%–22% 之间
	res, err := e.Evaluate(Input{
		Method:         domain.MethodFilter,
		Dose:           g(t, "18"),
		TotalWater:     g(t, "324"),
		GrindMicron:    700,
		WaterTempC:     93,
		ContactSeconds: 200,
		AgitationCount: 2,
		// 不提供 TDS
	}, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if res.Mode != ModeEstimated {
		t.Fatalf("未提供 TDS 应走推算模式，实际 %s", res.Mode)
	}
	if res.Zone.InGoldCup {
		t.Errorf("推算模式绝不允许声明落在金杯区间内（推算萃取率 %s%%，落区 %s）",
			res.RawYield.Percent(), res.Zone.Code)
	}
	if !res.Advisory {
		t.Error("推算模式必须被标记为 advisory，前端才知道要用虚线与「推算值」角标渲染")
	}
	if res.Estimation == nil {
		t.Fatal("推算模式必须附带 Estimation 说明推算依据")
	}
	// 无历史样本时必须回落到动力学先验，且如实说明
	if res.Estimation.Estimator == EstimatorHistory {
		t.Error("没有历史样本却声称用了回归模型")
	}
}

// TestEstimatedModeLabelsItselfAsGuess 验证推算结果在文案上也做了标注，
// 而不是只在一个布尔字段里藏着。
//
// 用户看的是界面上的文字，不是 JSON 字段。若落区标签写着"理想萃取"
// 而 in_gold_cup 是 false，前端很容易只渲染标签，让用户误读。
func TestEstimatedModeLabelsItselfAsGuess(t *testing.T) {
	e := NewEngine(nil)
	res, err := e.Evaluate(Input{
		Method:         domain.MethodFilter,
		Dose:           g(t, "18"),
		TotalWater:     g(t, "324"),
		GrindMicron:    700,
		WaterTempC:     93,
		ContactSeconds: 200,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsRune(res.Zone.Label, '推') {
		t.Errorf("推算模式的落区标签应当自带「推测」字样，实际 %q", res.Zone.Label)
	}
}

// TestRegressionUsedWhenEnoughSamples 验证有足够历史样本时启用回归模型，
// 且置信度高于无样本的动力学回退。
func TestRegressionUsedWhenEnoughSamples(t *testing.T) {
	e := NewEngine(nil)

	// 五条实测样本，研磨度与萃取率呈清晰的单调关系
	samples := []Sample{
		{BrewID: 1, Yield: 177600, TDS: 11100, Dose: g(t, "18"), Beverage: g(t, "288"),
			GrindMicron: 850, WaterTempC: 90, ContactSeconds: 155, AgitationCount: 1},
		{BrewID: 2, Yield: 192000, TDS: 12000, Dose: g(t, "18"), Beverage: g(t, "288"),
			GrindMicron: 780, WaterTempC: 92, ContactSeconds: 175, AgitationCount: 2},
		{BrewID: 3, Yield: 200000, TDS: 12500, Dose: g(t, "18"), Beverage: g(t, "288"),
			GrindMicron: 740, WaterTempC: 93, ContactSeconds: 190, AgitationCount: 2},
		{BrewID: 4, Yield: 208000, TDS: 13000, Dose: g(t, "18"), Beverage: g(t, "288"),
			GrindMicron: 700, WaterTempC: 94, ContactSeconds: 205, AgitationCount: 3},
		{BrewID: 5, Yield: 216000, TDS: 13500, Dose: g(t, "18"), Beverage: g(t, "288"),
			GrindMicron: 660, WaterTempC: 94, ContactSeconds: 225, AgitationCount: 3},
	}

	withSamples, err := e.Evaluate(Input{
		Method: domain.MethodFilter, Dose: g(t, "18"), TotalWater: g(t, "324"),
		GrindMicron: 720, WaterTempC: 93, ContactSeconds: 198, AgitationCount: 2,
	}, samples)
	if err != nil {
		t.Fatal(err)
	}

	withoutSamples, err := e.Evaluate(Input{
		Method: domain.MethodFilter, Dose: g(t, "18"), TotalWater: g(t, "324"),
		GrindMicron: 720, WaterTempC: 93, ContactSeconds: 198, AgitationCount: 2,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if withSamples.Estimation.Estimator != EstimatorHistory {
		t.Errorf("五条样本应当足以启用回归模型，实际用了 %s",
			withSamples.Estimation.Estimator)
	}
	if withoutSamples.Estimation.Estimator != EstimatorKinetic {
		t.Errorf("无样本时应回落到动力学先验，实际用了 %s",
			withoutSamples.Estimation.Estimator)
	}
	if withSamples.Estimation.Confidence <= withoutSamples.Estimation.Confidence {
		t.Errorf("有历史样本的置信度（%.3f）应高于无样本的动力学回退（%.3f）",
			withSamples.Estimation.Confidence, withoutSamples.Estimation.Confidence)
	}
	if withSamples.Estimation.Disclaimer == "" {
		t.Error("推算结果必须携带免责说明，否则用户会把推测当测量")
	}
	// 即便有样本，推算模式仍不得声明合格
	if withSamples.Zone.InGoldCup {
		t.Error("即便回归置信度较高，推算模式仍不得声明落在金杯区间内")
	}
}

// TestEspressoRequiresMeasuredBeverage 验证意式必须提供实测出液重量。
//
// 拒绝计算而非用固定系数硬推：粉饼持水量受压力、粉量、研磨度多因素影响，
// 推导误差会大于萃取率判定本身的分辨率。给一个看似精确实则无意义的数字
// 比明确拒绝更糟。
func TestEspressoRequiresMeasuredBeverage(t *testing.T) {
	e := NewEngine(nil)
	_, err := e.Evaluate(Input{
		Method: domain.MethodEspresso,
		Dose:   g(t, "18"),
		TDS:    pct(t, "9.5"),
		// 不提供 Beverage
	}, nil)
	if err == nil {
		t.Fatal("意式未提供出液重量时应当报错")
	}
	de := domain.AsDomain(err)
	if de.Code != "ESPRESSO_REQUIRES_BEVERAGE_MASS" {
		t.Errorf("错误码应为 ESPRESSO_REQUIRES_BEVERAGE_MASS，实际 %s：%s",
			de.Code, de.Message)
	}
}

// TestWaterFullyAbsorbedIsDiagnosed 验证"粉量与水量填反"这个高频输入错误
// 得到的是可读的诊断，而不是一个负数液重或一次除零。
func TestWaterFullyAbsorbedIsDiagnosed(t *testing.T) {
	e := NewEngine(nil)
	// 把 18g 粉和 324g 水填反了
	_, err := e.Evaluate(Input{
		Method:     domain.MethodFilter,
		Dose:       g(t, "324"),
		TotalWater: g(t, "18"),
		TDS:        pct(t, "1.30"),
	}, nil)
	if err == nil {
		t.Fatal("注水量不足以产生液体时应当报错")
	}
	de := domain.AsDomain(err)
	if de.Code != "WATER_FULLY_ABSORBED" {
		t.Errorf("错误码应为 WATER_FULLY_ABSORBED，实际 %s：%s", de.Code, de.Message)
	}
	if !containsRune(de.Message, '反') {
		t.Errorf("诊断信息应当提示检查粉量与水量是否填反，实际 %q", de.Message)
	}
}

// TestZeroDoseIsRejectedNotDividedBy 验证粉量为零被前置拦截。
//
// 本包用有理数运算，除零会直接 panic 而非产生 NaN，
// 所以"事后检查结果是否异常"这条路根本不存在，必须前置拦截。
func TestZeroDoseIsRejectedNotDividedBy(t *testing.T) {
	defer func() {
		if rv := recover(); rv != nil {
			t.Fatalf("粉量为零导致了 panic 而非返回错误：%v", rv)
		}
	}()

	e := NewEngine(nil)
	if _, err := e.Evaluate(Input{
		Method:     domain.MethodFilter,
		Dose:       0,
		TotalWater: g(t, "324"),
		TDS:        pct(t, "1.30"),
	}, nil); err == nil {
		t.Error("粉量为零时应当返回校验错误")
	}
}

// TestSolveRoundTrip 验证反解与正算互为逆运算。
//
// 这条不变量是沙盘"目标反推"功能可信的前提：引擎告诉用户
// "接到 36.5g 就正好 20%"，那么按 36.5g 记录下来后正算出的萃取率
// 必须就是 20%，否则这个建议是在骗人。
func TestSolveRoundTrip(t *testing.T) {
	e := NewEngine(nil)
	targetYield := pct(t, "20")
	dose := g(t, "18")
	tds := pct(t, "9.5")

	// 反解：要达到 20% 萃取率，该接出多少液体
	solved, err := e.Solve(SolveRequest{
		Method:      domain.MethodEspresso,
		Target:      SolveTargetBeverage,
		TargetYield: targetYield,
		TDS:         tds,
		Dose:        dose,
	})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	// 走 ValueRaw 而非 ValueText：前者是不带单位的纯十进制串，
	// 正是前端要填回表单、再提交回后端的那个值。这条往返测的就是这条真实链路。
	bev, err := fixed.ParseGrams(solved.ValueRaw)
	if err != nil {
		t.Fatalf("反解结果 %q 无法解析回定点数：%v", solved.ValueRaw, err)
	}

	// 正算：用反解出的液重回代
	back, err := ExtractionYield(bev, tds, dose)
	if err != nil {
		t.Fatal(err)
	}

	// 反解结果被量化到毫克，回代后的萃取率会有一个毫克级的舍入残差。
	// 容差取 100 PPM（0.01 个百分点）—— 远小于金杯区间宽度（4 个百分点），
	// 也小于任何折射仪的重复性误差。
	const tolerancePPM = 100
	diff := int64(back) - int64(targetYield)
	if diff < -tolerancePPM || diff > tolerancePPM {
		t.Errorf("反解-正算往返偏差 %d PPM 超出容差：目标 %s%%，回代得到 %s%%（液重 %sg）",
			diff, targetYield.Percent(), back.Percent(), bev.Grams())
	}
}

// TestAdviceDirectionIsActionable 验证欠萃与过萃给出方向相反的建议。
//
// 一个诊断系统最基本的可信度要求：欠萃时不能建议"磨粗一点"。
func TestAdviceDirectionIsActionable(t *testing.T) {
	e := NewEngine(nil)

	under, err := e.Evaluate(Input{
		Method: domain.MethodFilter, Dose: g(t, "18"),
		TotalWater: g(t, "324"), TDS: pct(t, "1.05"), GrindMicron: 850,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	over, err := e.Evaluate(Input{
		Method: domain.MethodFilter, Dose: g(t, "18"),
		TotalWater: g(t, "324"), TDS: pct(t, "1.45"), GrindMicron: 650,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if under.Zone.Yield != YieldUnder {
		t.Fatalf("TDS 1.05%% 应判为欠萃，实际 %s", under.Zone.Yield)
	}
	if over.Zone.Yield != YieldOver {
		t.Fatalf("TDS 1.45%% 应判为过萃，实际 %s", over.Zone.Yield)
	}
	if len(under.Advice) == 0 || len(over.Advice) == 0 {
		t.Fatal("欠萃与过萃都应给出调整建议")
	}

	underGrind := findAdvice(under.Advice, AdviceGrind)
	overGrind := findAdvice(over.Advice, AdviceGrind)
	if underGrind == nil || overGrind == nil {
		t.Fatal("欠萃与过萃都应包含研磨度调整建议")
	}
	if underGrind.Direction == overGrind.Direction {
		t.Errorf("欠萃与过萃的研磨建议方向相同（都是 %s），这是明显的逻辑错误",
			underGrind.Direction)
	}
}

// TestProfileOverrideIsValidatedAndFallsBack 验证非法的自定义标准被丢弃，
// 而不是让引擎带着一组讲不通的区间继续工作。
func TestProfileOverrideIsValidatedAndFallsBack(t *testing.T) {
	broken, err := DefaultProfile(domain.MethodFilter)
	if err != nil {
		t.Fatal(err)
	}
	// 上下界颠倒
	broken.YieldMin, broken.YieldMax = broken.YieldMax, broken.YieldMin

	e := NewEngine(map[domain.BrewMethod]Profile{domain.MethodFilter: broken})
	got, err := e.ProfileFor(domain.MethodFilter)
	if err != nil {
		t.Fatal(err)
	}
	if got.YieldMin >= got.YieldMax {
		t.Errorf("非法覆盖应被丢弃并回落到出厂标准，实际生效区间 %d–%d",
			got.YieldMin, got.YieldMax)
	}
}

// TestSetProfilesIsFullReplacement 验证热更新是全量替换而非增量合并。
//
// 增量合并会让"恢复出厂标准"失效：删掉数据库里的覆盖行之后，
// 旧值仍留在内存里，用户点了恢复却什么都没变。
func TestSetProfilesIsFullReplacement(t *testing.T) {
	custom, err := DefaultProfile(domain.MethodFilter)
	if err != nil {
		t.Fatal(err)
	}
	custom.YieldMin = pct(t, "19")
	custom.YieldMax = pct(t, "21")

	e := NewEngine(map[domain.BrewMethod]Profile{domain.MethodFilter: custom})
	if p, _ := e.ProfileFor(domain.MethodFilter); p.YieldMin != 190000 {
		t.Fatalf("自定义标准未生效，实际下界 %d", p.YieldMin)
	}

	// 传空 map 模拟"数据库里的覆盖已被删除"
	e.SetProfiles(nil)
	p, err := e.ProfileFor(domain.MethodFilter)
	if err != nil {
		t.Fatal(err)
	}
	if p.YieldMin != 180000 {
		t.Errorf("清空覆盖后应回到出厂下界 18%%（180000 PPM），实际 %d PPM。"+
			"这说明 SetProfiles 做的是增量合并而非全量替换", p.YieldMin)
	}
}

func findAdvice(list []Advice, kind AdviceKind) *Advice {
	for i := range list {
		if list[i].Kind == kind {
			return &list[i]
		}
	}
	return nil
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}
