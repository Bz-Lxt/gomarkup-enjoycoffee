package flavorscore

import (
	"testing"
	"time"

	"github.com/alkaid/enjoycoffee/internal/domain"
)

func score(acidity, sweet, aroma, aftertone, body, bitter int) *Score {
	return &Score{
		BrewID:       1,
		AcidityX10:   acidity,
		SweetX10:     sweet,
		AromaX10:     aroma,
		AftertoneX10: aftertone,
		BodyX10:      body,
		BitterX10:    bitter,
	}
}

func axisOf(t *testing.T, r *domain.RadarSummary, a domain.FlavorAxis) domain.RadarAxisValue {
	t.Helper()
	for _, v := range r.Axes {
		if v.Axis == a {
			return v
		}
	}
	t.Fatalf("雷达图缺少 %s 轴", a)
	return domain.RadarAxisValue{}
}

// TestValidateEnforcesHalfPointSteps 锁住 0.5 分步进。
//
// 为什么这条必须拦在后端：前端滑块的刻度是 0.5，若后端接受 7.3 分入库，
// 回显时滑块会跳到 7.5，用户会以为自己的输入被篡改了 —— 而且无从申诉，
// 因为界面上看不出发生了什么。
func TestValidateEnforcesHalfPointSteps(t *testing.T) {
	ok := score(75, 80, 70, 65, 60, 35)
	if err := ok.Validate(); err != nil {
		t.Errorf("全部为 0.5 的倍数应通过校验，实际 %v", err)
	}

	bad := score(73, 80, 70, 65, 60, 35)
	err := bad.Validate()
	if err == nil {
		t.Fatal("7.3 分不是 0.5 的倍数，应被拒绝")
	}
	de := domain.AsDomain(err)
	if de.Code != "INVALID_FLAVOR_SCORE" {
		t.Errorf("错误码应为 INVALID_FLAVOR_SCORE，实际 %q", de.Code)
	}
	if len(de.Fields) == 0 {
		t.Error("应指明是哪个维度越界，前端才能定位到那个滑块")
	}
}

// TestValidateRejectsOutOfRange 确认 0–10 的值域边界。
func TestValidateRejectsOutOfRange(t *testing.T) {
	cases := []struct {
		name string
		sc   *Score
	}{
		{"负分", score(-5, 80, 70, 65, 60, 35)},
		{"超过满分", score(105, 80, 70, 65, 60, 35)},
	}
	for _, c := range cases {
		if err := c.sc.Validate(); err == nil {
			t.Errorf("%s 应被拒绝", c.name)
		}
	}

	// 边界值本身合法
	for _, v := range []int{0, 100} {
		if err := score(v, v, v, v, v, v).Validate(); err != nil {
			t.Errorf("%d（即 %.1f 分）是合法边界值，实际被拒: %v", v, float64(v)/10, err)
		}
	}
}

// TestValidateAccumulatesAllAxes 确认六个维度的问题一次报完。
func TestValidateAccumulatesAllAxes(t *testing.T) {
	bad := score(-1, 101, 73, 77, 999, 3)
	de := domain.AsDomain(bad.Validate())

	if len(de.Fields) < 5 {
		t.Errorf("六个维度里有 6 个越界或步进错误，应一次性报出，实际只报了 %d 项：%v",
			len(de.Fields), de.Fields)
	}
}

// TestValidateRequiresBrewID 确认评分必须挂在一次冲煮上。
//
// 这是本包最核心的设计决策的守卫：评分挂在萃取记录而非豆子上。
// 一条没有 brew_id 的评分等于"这支豆好喝"，丢掉了参数与风味的对应关系 ——
// 也就丢掉了整个项目的意义。
func TestValidateRequiresBrewID(t *testing.T) {
	orphan := score(75, 80, 70, 65, 60, 35)
	orphan.BrewID = 0

	if err := orphan.Validate(); err == nil {
		t.Error("评分必须绑定萃取记录。允许孤立评分等于放弃「参数 ↔ 风味」这条主线")
	}
}

// TestSingleScoreRadarIsExact 确认单次评分转雷达图没有丢失或换算错误。
func TestSingleScoreRadarIsExact(t *testing.T) {
	sc := score(75, 80, 70, 65, 60, 35)
	r := radarFromScore(sc)

	if len(r.Axes) != 6 {
		t.Fatalf("应有六个维度，实际 %d 个", len(r.Axes))
	}
	if got := axisOf(t, r, domain.AxisAcidity); got.Value != 7.5 || got.ValueText != "7.5" {
		t.Errorf("酸质 75（×10）应展示为 7.5，实际 %.2f / %q", got.Value, got.ValueText)
	}
	if r.TotalScore != 38.5 {
		t.Errorf("总分应为 (75+80+70+65+60+35)/10 = 38.5，实际 %.2f", r.TotalScore)
	}
	if r.SampleCount != 1 {
		t.Errorf("单次评分的样本数应为 1，实际 %d", r.SampleCount)
	}
	if r.MaxScore != domain.MaxAxisScore {
		t.Errorf("应下发单维满分供前端确定坐标轴上限，实际 %.1f", r.MaxScore)
	}
	if r.Weighting == "" {
		t.Error("应说明聚合口径，否则用户不知道这个雷达图是单次还是均值")
	}
}

// TestEmptyRadarIsRenderableNotNil 确认无评分时返回可渲染的零值雷达。
//
// 前端的雷达图组件需要六个顶点才能画出坐标系。给它 null 会导致组件崩溃，
// 给它一个空数组会导致坐标轴消失 —— 而"还没评分"是每支新豆的初始状态。
func TestEmptyRadarIsRenderableNotNil(t *testing.T) {
	r := domain.NewEmptyRadar()

	if r == nil {
		t.Fatal("空态应返回零值雷达而非 nil")
	}
	if len(r.Axes) != 6 {
		t.Errorf("空态雷达也必须有六个顶点，否则前端画不出坐标系，实际 %d 个",
			len(r.Axes))
	}
	for _, a := range r.Axes {
		if a.Label == "" {
			t.Errorf("%s 轴缺少标签，坐标轴上会是一片空白", a.Axis)
		}
		if a.Value != 0 || a.ValueText != "0.0" {
			t.Errorf("%s 轴的空态值应为 0/0.0，实际 %.1f/%q", a.Axis, a.Value, a.ValueText)
		}
	}
	if r.SampleCount != 0 {
		t.Errorf("空态样本数应为 0，实际 %d", r.SampleCount)
	}
	if r.MaxScore != domain.MaxAxisScore {
		t.Error("空态也要下发满分，否则前端第一次渲染就没有坐标轴上限")
	}
}

// TestAxisOrderIsStableAcrossBeans 是"多款豆子风味重叠对比"的前提。
//
// 雷达墙把多支豆的六边形叠在一张图上。若各支豆的轴顺序不一致，
// 叠出来的图形毫无意义 —— 而这种错误在视觉上并不明显，
// 只会让用户对着一张"看起来很专业"的错图做决策。
func TestAxisOrderIsStableAcrossBeans(t *testing.T) {
	a := radarFromScore(score(75, 80, 70, 65, 60, 35))
	b := radarFromScore(score(20, 90, 30, 100, 55, 15))
	empty := domain.NewEmptyRadar()

	for i := range a.Axes {
		if a.Axes[i].Axis != b.Axes[i].Axis || a.Axes[i].Axis != empty.Axes[i].Axis {
			t.Fatalf("第 %d 个顶点的维度不一致：%s / %s / %s（空态）。"+
				"雷达墙叠图会把不同维度画在同一根轴上",
				i, a.Axes[i].Axis, b.Axes[i].Axis, empty.Axes[i].Axis)
		}
	}
}

// TestRecentScoresWeighMoreThanOldOnes 验证时间加权的方向。
//
// 一支豆半年前的评分和上周的评分不该等权：豆子本身在变化，
// 用户的冲煮技术也在进步。若等权平均，一支豆刚到手时的糟糕记录
// 会永久拖累它的雷达图。
func TestRecentScoresWeighMoreThanOldOnes(t *testing.T) {
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, domain.Beijing)

	old := score(20, 20, 20, 20, 20, 20)
	old.ScoredAt = now.AddDate(0, 0, -365)
	recent := score(90, 90, 90, 90, 90, 90)
	recent.ScoredAt = now.AddDate(0, 0, -1)

	r := aggregateRadar([]*Score{old, recent}, now)
	acidity := axisOf(t, r, domain.AxisAcidity).Value

	// 等权平均会得到 5.5
	if acidity <= 6.0 {
		t.Errorf("一年前的 2.0 分与昨天的 9.0 分加权后应显著偏向近期（>6.0），"+
			"实际 %.2f。若接近 5.5 说明是等权平均，时间加权没生效", acidity)
	}
	if acidity > 9.0 {
		t.Errorf("加权结果 %.2f 超过了最高的那条评分 9.0，说明权重归一化有问题",
			acidity)
	}
}

// TestAggregateIsIndependentOfInputOrder 确认聚合结果与输入顺序无关。
//
// 仓储层的排序若某天变了（比如加了个索引导致 SQL 走了不同的扫描顺序），
// 雷达图的数值不该跟着变。
func TestAggregateIsIndependentOfInputOrder(t *testing.T) {
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, domain.Beijing)

	a := score(75, 80, 70, 65, 60, 35)
	a.ScoredAt = now.AddDate(0, 0, -100)
	b := score(60, 90, 55, 70, 80, 20)
	b.ScoredAt = now.AddDate(0, 0, -10)
	c := score(85, 70, 90, 60, 50, 45)
	c.ScoredAt = now.AddDate(0, 0, -200)

	forward := aggregateRadar([]*Score{a, b, c}, now)
	backward := aggregateRadar([]*Score{c, b, a}, now)

	for i := range forward.Axes {
		if forward.Axes[i].Value != backward.Axes[i].Value {
			t.Errorf("%s 轴在正序与逆序输入下得到 %.2f 与 %.2f，聚合不应依赖输入顺序",
				forward.Axes[i].Axis, forward.Axes[i].Value, backward.Axes[i].Value)
		}
	}
	if forward.TotalScore != backward.TotalScore {
		t.Errorf("总分在两种顺序下为 %.2f 与 %.2f", forward.TotalScore, backward.TotalScore)
	}
}

// TestAggregateIsDeterministic 确认同样输入永远给同样输出。
//
// 权重刻意用整数实现就是为了这个 —— 若用 math.Pow，不同平台的浮点
// 实现差异可能让同一支豆在开发机与服务器上显示不同的雷达图。
func TestAggregateIsDeterministic(t *testing.T) {
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, domain.Beijing)

	scores := make([]*Score, 0, 50)
	for i := 0; i < 50; i++ {
		s := score(5*(i%20), 5*((i+3)%20), 5*((i+7)%20),
			5*((i+11)%20), 5*((i+13)%20), 5*((i+17)%20))
		s.ScoredAt = now.AddDate(0, 0, -i*7)
		scores = append(scores, s)
	}

	first := aggregateRadar(scores, now)
	for run := 0; run < 20; run++ {
		again := aggregateRadar(scores, now)
		for i := range first.Axes {
			if first.Axes[i].Value != again.Axes[i].Value {
				t.Fatalf("第 %d 次重算时 %s 轴从 %.4f 变为 %.4f",
					run, first.Axes[i].Axis, first.Axes[i].Value, again.Axes[i].Value)
			}
		}
	}
}

// TestAggregateStaysInRange 确认聚合值不会越界。
//
// 加权求和再除以权重和，若归一化写错（比如除以样本数而非权重和），
// 结果会溢出到 10 分以上，雷达图的顶点会画到坐标系外面。
func TestAggregateStaysInRange(t *testing.T) {
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, domain.Beijing)

	// 全部满分，任意时间分布
	scores := make([]*Score, 0, 30)
	for i := 0; i < 30; i++ {
		s := score(100, 100, 100, 100, 100, 100)
		s.ScoredAt = now.AddDate(0, 0, -i*40)
		scores = append(scores, s)
	}

	r := aggregateRadar(scores, now)
	for _, a := range r.Axes {
		if a.Value != 10 {
			t.Errorf("全部满分的加权结果应恰好是 10.0，实际 %s 轴 %.4f",
				a.Axis, a.Value)
		}
	}
	if r.TotalScore != 60 {
		t.Errorf("六维满分总计应为 60.0，实际 %.2f", r.TotalScore)
	}
	if r.SampleCount != 30 {
		t.Errorf("样本数应为 30，实际 %d", r.SampleCount)
	}
}

// TestVeryOldScoresAreDimmedNotDiscarded 确认极老的评分权重降到最低但不归零。
//
// 静默丢弃会让"这支豆我只在三年前喝过一次"变成"这支豆没有评分"，
// 用户看到空雷达图会以为数据丢了。
func TestVeryOldScoresAreDimmedNotDiscarded(t *testing.T) {
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, domain.Beijing)

	ancient := score(85, 85, 85, 85, 85, 85)
	ancient.ScoredAt = now.AddDate(-5, 0, 0)

	r := aggregateRadar([]*Score{ancient}, now)
	if r.SampleCount != 1 {
		t.Errorf("唯一的一条五年前评分不应被丢弃，实际样本数 %d", r.SampleCount)
	}
	if got := axisOf(t, r, domain.AxisAcidity).Value; got != 8.5 {
		t.Errorf("只有一条评分时，加权结果就应等于它本身 8.5，实际 %.2f", got)
	}

	if w := recencyWeight(100000); w < 1 {
		t.Errorf("权重应有 1 的下限，实际 %d。降到 0 会让老评分被静默丢弃", w)
	}
}

// TestFutureScoreDoesNotBreakWeighting 确认时钟偏差不会算出负权重。
//
// 客户端时钟快几分钟就会产生"未来"的评分时间。若 ageDays 为负导致
// 位移量为负，权重会变成一个巨大的数或直接 panic。
func TestFutureScoreDoesNotBreakWeighting(t *testing.T) {
	now := time.Date(2026, time.August, 24, 12, 0, 0, 0, domain.Beijing)

	future := score(75, 75, 75, 75, 75, 75)
	future.ScoredAt = now.AddDate(0, 0, 30)

	r := aggregateRadar([]*Score{future}, now)
	if got := axisOf(t, r, domain.AxisAcidity).Value; got != 7.5 {
		t.Errorf("时间在未来的评分应按最新处理，得到 7.5，实际 %.2f", got)
	}
	if w := recencyWeight(-10); w <= 0 {
		t.Errorf("负日龄的权重应为正数，实际 %d", w)
	}
}

// TestNoScoresAggregatesToEmptyRadar 确认空输入走空态而不是除零。
func TestNoScoresAggregatesToEmptyRadar(t *testing.T) {
	now := domain.Now()
	r := aggregateRadar(nil, now)

	if r == nil || len(r.Axes) != 6 {
		t.Fatal("空输入应返回可渲染的零值雷达")
	}
	if r.SampleCount != 0 {
		t.Errorf("空输入的样本数应为 0，实际 %d", r.SampleCount)
	}
}

// TestBalanceDiagnosisNamesTheCause 确认平衡度诊断说的是原因而非现象。
//
// "酸 8.5 甜 5.0" 是把数字念一遍；用户要的是"这是欠萃，磨细一档"。
// 这条测的是诊断文案里有没有真的指向萃取方向和可执行动作。
func TestBalanceDiagnosisNamesTheCause(t *testing.T) {
	cases := []struct {
		name       string
		acidity    int
		sweet      int
		bitter     int
		body       int
		wantSubstr string
	}{
		{"酸压过甜 → 欠萃", 85, 50, 30, 60, "欠萃"},
		{"苦压过甜 → 过萃", 40, 45, 80, 60, "过萃"},
		{"三轴俱低 → 浓度不足", 30, 25, 30, 40, "粉液比"},
		{"平衡", 70, 80, 30, 70, "平衡"},
	}

	seen := map[string]bool{}
	for _, c := range cases {
		got := diagnoseBalance(c.acidity, c.sweet, c.bitter, c.body)
		if got == "" {
			t.Errorf("%s：诊断文案为空", c.name)
			continue
		}
		if !contains(got, c.wantSubstr) {
			t.Errorf("%s：诊断应点出「%s」，实际文案是 %q",
				c.name, c.wantSubstr, got)
		}
		if seen[got] {
			t.Errorf("%s 的诊断与之前某个用例完全相同，说明分支没有区分开", c.name)
		}
		seen[got] = true
	}
}

// TestBalanceDiagnosisHandlesAllZero 确认全零走"还没评分"而不是误判为欠萃。
func TestBalanceDiagnosisHandlesAllZero(t *testing.T) {
	got := diagnoseBalance(0, 0, 0, 0)
	if !contains(got, "还没有") {
		t.Errorf("全零应识别为「还没有评分」而不是给出萃取诊断，实际 %q", got)
	}
}

// TestFmtX10AlwaysKeepsOneDecimal 确认展示串格式统一。
//
// 前端把这个串直接贴在雷达图顶点旁。若 8 分渲染成 "8" 而 8.5 渲染成 "8.5"，
// 六个顶点的标签宽度不一，图会歪。
func TestFmtX10AlwaysKeepsOneDecimal(t *testing.T) {
	cases := map[int]string{
		0:   "0.0",
		5:   "0.5",
		80:  "8.0",
		85:  "8.5",
		100: "10.0",
	}
	for in, want := range cases {
		if got := fmtX10(in); got != want {
			t.Errorf("fmtX10(%d) 应为 %q，实际 %q", in, want, got)
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
