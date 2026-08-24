package bean

import (
	"testing"
	"time"

	"github.com/alkaid/enjoycoffee/internal/domain"
)

func day(y int, m time.Month, d int) domain.CivilDate {
	return domain.CivilDate{Year: y, Month: m, Day: d}
}

func at(y int, m time.Month, d, h int) time.Time {
	return time.Date(y, m, d, h, 0, 0, 0, domain.Beijing)
}

// TestStageBoundariesAreLeftClosed 锁住四个阶段的边界归属。
//
// 边界日归哪一段是个必须钉死的约定：中烘排气期 7 天，那么"第 7 天"
// 是排气期最后一天还是最佳期第一天？本项目取左闭右开，即第 7 天已进入最佳期。
// 这条不测，将来任何人改动判定式的比较符号都不会有人发现。
func TestStageBoundariesAreLeftClosed(t *testing.T) {
	roasted := day(2026, time.August, 1)
	w := WindowFor(domain.RoastMedium)

	cases := []struct {
		ageDays int
		want    domain.FreshnessStage
		why     string
	}{
		{0, domain.StageDegassing, "烘焙当天"},
		{w.DegassingDays - 1, domain.StageDegassing, "排气期最后一天"},
		{w.DegassingDays, domain.StagePeak, "排气期天数当日即进入最佳期（左闭右开）"},
		{w.PeakEndDay - 1, domain.StagePeak, "最佳期最后一天"},
		{w.PeakEndDay, domain.StageNearExpiry, "最佳期结束日当天即临期"},
		{w.DeclineDay - 1, domain.StageNearExpiry, "临期最后一天"},
		{w.DeclineDay, domain.StageDeclined, "衰退日当天即已衰退"},
		{w.DeclineDay + 100, domain.StageDeclined, "早就该扔了"},
	}

	for _, c := range cases {
		now := roasted.AddDays(c.ageDays).Time()
		f := EvaluateFreshness(domain.RoastMedium, roasted, domain.CivilDate{}, now)
		if f.Stage != c.want {
			t.Errorf("烘后第 %d 天（%s）应为 %s，实际 %s",
				c.ageDays, c.why, c.want, f.Stage)
		}
		if f.RoastAgeDays != c.ageDays {
			t.Errorf("烘后第 %d 天的日龄应为 %d，实际 %d",
				c.ageDays, c.ageDays, f.RoastAgeDays)
		}
	}
}

// TestMorningQueryDoesNotShiftStage 是 GMT+8 规范在业务层的落地断言。
//
// 前一条时区测试证明了日期折算正确；这条证明它确实传导到了阶段判定上。
// 取的是最凶的时点：恰好处于阶段边界那一天的清晨。若折算掉到 UTC，
// 这一刻会被算成前一天，阶段随之回退，用户会看到状态在早上八点自己跳变。
func TestMorningQueryDoesNotShiftStage(t *testing.T) {
	roasted := day(2026, time.August, 1)
	w := WindowFor(domain.RoastMedium)
	boundary := roasted.AddDays(w.DegassingDays)

	morning := at(boundary.Year, boundary.Month, boundary.Day, 7)
	evening := at(boundary.Year, boundary.Month, boundary.Day, 22)

	fm := EvaluateFreshness(domain.RoastMedium, roasted, domain.CivilDate{}, morning)
	fe := EvaluateFreshness(domain.RoastMedium, roasted, domain.CivilDate{}, evening)

	if fm.Stage != fe.Stage || fm.RoastAgeDays != fe.RoastAgeDays {
		t.Errorf("同一民用日的早晚查询结果不一致：早上 %s/第%d天，晚上 %s/第%d天。"+
			"这正是日期折算掉到 UTC 的症状", fm.Stage, fm.RoastAgeDays,
			fe.Stage, fe.RoastAgeDays)
	}
	if fm.Stage != domain.StagePeak {
		t.Errorf("排气期第 %d 天当日应已进入最佳期，实际 %s",
			w.DegassingDays, fm.Stage)
	}
}

// TestRoastLevelChangesTheWindow 验证不同烘焙度用不同养豆窗口。
//
// 浅烘排气慢、需要更长养豆期；深烘排气快但衰退也快。若三档共用一套窗口，
// 那"按烘焙度管理"这个功能就只是个摆设。
func TestRoastLevelChangesTheWindow(t *testing.T) {
	light := WindowFor(domain.RoastLight)
	medium := WindowFor(domain.RoastMedium)
	dark := WindowFor(domain.RoastDark)

	if !(light.DegassingDays > medium.DegassingDays &&
		medium.DegassingDays > dark.DegassingDays) {
		t.Errorf("排气期应随烘焙度加深而缩短：浅 %d / 中 %d / 深 %d",
			light.DegassingDays, medium.DegassingDays, dark.DegassingDays)
	}
	if !(light.DeclineDay > dark.DeclineDay) {
		t.Errorf("浅烘的风味保持期应长于深烘：浅 %d / 深 %d",
			light.DeclineDay, dark.DeclineDay)
	}

	// 同一天、同一烘焙日，浅烘还在排气而深烘已进入最佳期
	roasted := day(2026, time.August, 1)
	now := roasted.AddDays(dark.DegassingDays).Time()

	if EvaluateFreshness(domain.RoastDark, roasted, domain.CivilDate{}, now).Stage !=
		domain.StagePeak {
		t.Error("深烘在自己的排气期天数当日应已进入最佳期")
	}
	if EvaluateFreshness(domain.RoastLight, roasted, domain.CivilDate{}, now).Stage !=
		domain.StageDegassing {
		t.Errorf("同一天，浅烘（排气 %d 天）应仍在排气期", light.DegassingDays)
	}
}

// TestOpeningCompressesTheDeclineDate 验证开封氧化会提前衰退日。
//
// 一包烘后第 3 天就开封的豆，不可能和未开封的同一包在第 30 天时状态相同。
// 若忽略开封，看板会给出偏乐观的判断 —— 用户按它的提示去冲，风味已经掉了。
func TestOpeningCompressesTheDeclineDate(t *testing.T) {
	roasted := day(2026, time.August, 1)
	w := WindowFor(domain.RoastMedium)

	// 烘后第 3 天开封
	opened := roasted.AddDays(3)
	now := roasted.AddDays(w.OpenedShelfLifeDays + 5).Time()

	sealed := EvaluateFreshness(domain.RoastMedium, roasted, domain.CivilDate{}, now)
	unsealed := EvaluateFreshness(domain.RoastMedium, roasted, opened, now)

	if unsealed.EffectiveDeclineDay >= sealed.EffectiveDeclineDay {
		t.Errorf("开封后的实际衰退日（第 %d 天）应早于未开封（第 %d 天）",
			unsealed.EffectiveDeclineDay, sealed.EffectiveDeclineDay)
	}
	if unsealed.LimitedBy != "OPENING" {
		t.Errorf("此例中衰退时点由开封氧化决定，LimitedBy 应为 OPENING，实际 %q。"+
			"这个字段就是为了回答「为什么我这包才第 %d 天就临期了」",
			unsealed.LimitedBy, unsealed.RoastAgeDays)
	}
	if sealed.LimitedBy != "ROAST" {
		t.Errorf("未开封时衰退时点由烘焙窗口决定，应为 ROAST，实际 %q",
			sealed.LimitedBy)
	}
	if !unsealed.Opened || unsealed.OpenedAgeDays < 0 {
		t.Errorf("已开封的豆应标记 Opened 并给出开封日龄，实际 Opened=%v 日龄=%d",
			unsealed.Opened, unsealed.OpenedAgeDays)
	}
	if sealed.Opened || sealed.OpenedAgeDays != -1 {
		t.Errorf("未开封应为 Opened=false 且日龄 -1（供前端隐藏相关展示），"+
			"实际 Opened=%v 日龄=%d", sealed.Opened, sealed.OpenedAgeDays)
	}
}

// TestEarlyOpeningStillLeavesANearExpiryWarning 是开封压缩里最容易写错的一处。
//
// 直接把最佳期结束日也设为压缩后的衰退日，临期段就归零了 —— 豆子会从
// "最佳"一步跳到"已衰退"，用户完全没有"还剩几天，赶紧喝"的预警窗口。
// 而预警恰恰是这个看板存在的理由。
func TestEarlyOpeningStillLeavesANearExpiryWarning(t *testing.T) {
	roasted := day(2026, time.August, 1)
	// 烘后第 1 天就开封，压缩效应最强
	opened := roasted.AddDays(1)
	w := WindowFor(domain.RoastMedium)

	var sawNearExpiry bool
	for age := 0; age <= w.DeclineDay+5; age++ {
		now := roasted.AddDays(age).Time()
		f := EvaluateFreshness(domain.RoastMedium, roasted, opened, now)
		if f.Stage == domain.StageNearExpiry {
			sawNearExpiry = true
			break
		}
	}
	if !sawNearExpiry {
		t.Error("即使很早开封，生命周期里也必须存在临期阶段。" +
			"直接从最佳跳到已衰退等于取消了预警，而预警是这个看板的核心价值")
	}
}

// TestMissingRoastDateAsksInsteadOfGuessing 确认缺烘焙日时给出明确的未知态。
//
// 拿今天当烘焙日会算出一个看起来完全合理的"第 0 天，排气期"，
// 用户不会意识到这个结论是凭空造的。
func TestMissingRoastDateAsksInsteadOfGuessing(t *testing.T) {
	f := EvaluateFreshness(domain.RoastMedium, domain.CivilDate{}, domain.CivilDate{},
		at(2026, time.August, 24, 12))

	if f.Advice == "" {
		t.Error("缺烘焙日期时必须给出补填提示")
	}
	if f.RoastAgeDays != 0 {
		t.Errorf("缺烘焙日期时不应编出一个日龄，实际 %d", f.RoastAgeDays)
	}
	if f.ProgressPercent != 0 {
		t.Errorf("缺烘焙日期时进度条应为 0 而不是一个凭空的百分比，实际 %.1f",
			f.ProgressPercent)
	}
	if len(f.Segments) != 0 {
		t.Errorf("缺烘焙日期时不应给出进度条分段，实际 %d 段", len(f.Segments))
	}
}

// TestFutureRoastDateIsHandledNotNegative 确认预登记即将到货的豆子不会显示负日龄。
func TestFutureRoastDateIsHandledNotNegative(t *testing.T) {
	now := at(2026, time.August, 24, 12)
	future := day(2026, time.September, 1)

	f := EvaluateFreshness(domain.RoastMedium, future, domain.CivilDate{}, now)

	if f.RoastAgeDays < 0 {
		t.Errorf("烘焙日在未来时日龄不应为负数，实际 %d", f.RoastAgeDays)
	}
	if f.Stage != domain.StageDegassing {
		t.Errorf("尚未烘焙的豆应按排气期展示，实际 %s", f.Stage)
	}
	if f.Advice == "" {
		t.Error("烘焙日晚于今天是个值得说明的情况，应给出提示")
	}
}

// TestSegmentsCoverTheWholeBarWithoutGaps 检查进度条几何自洽。
//
// 这些几何量由后端算（裁定 C-02），前端直接照着画。若各段宽度之和不是 100，
// 进度条右端会缺一块或溢出容器 —— 而前端没有任何理由去校验它。
func TestSegmentsCoverTheWholeBarWithoutGaps(t *testing.T) {
	roasted := day(2026, time.August, 1)

	for _, level := range []domain.RoastLevel{
		domain.RoastLight, domain.RoastMedium, domain.RoastDark,
	} {
		f := EvaluateFreshness(level, roasted, domain.CivilDate{},
			roasted.AddDays(10).Time())

		if len(f.Segments) != 4 {
			t.Errorf("%s：应有 4 段（排气/最佳/临期/衰退），实际 %d 段",
				level, len(f.Segments))
			continue
		}

		var sum float64
		prevEnd := 0
		for i, seg := range f.Segments {
			sum += seg.WidthPercent
			if seg.WidthPercent <= 0 {
				t.Errorf("%s：第 %d 段（%s）宽度为 %.2f，"+
					"零宽段在界面上等于不存在，用户就看不到这个阶段",
					level, i, seg.Label, seg.WidthPercent)
			}
			if i > 0 && seg.StartDay != prevEnd {
				t.Errorf("%s：第 %d 段起始日 %d 与上一段结束日 %d 不衔接，进度条会有断缝",
					level, i, seg.StartDay, prevEnd)
			}
			prevEnd = seg.EndDay
			if seg.ColorHint == "" {
				t.Errorf("%s：第 %d 段缺少颜色提示，前端只能自己瞎猜配色", level, i)
			}
		}

		// 浮点累加允许极小误差，但不能差到肉眼可见
		if sum < 99.9 || sum > 100.1 {
			t.Errorf("%s：四段宽度之和应为 100%%，实际 %.4f%%", level, sum)
		}
	}
}

// TestProgressPercentIsClampedToBar 确认已严重过期的豆子不会把进度条冲出容器。
func TestProgressPercentIsClampedToBar(t *testing.T) {
	roasted := day(2026, time.January, 1)
	// 一年后才想起来这包豆
	f := EvaluateFreshness(domain.RoastMedium, roasted, domain.CivilDate{},
		roasted.AddDays(365).Time())

	if f.ProgressPercent > 100 {
		t.Errorf("进度百分比应被夹到 100 以内，实际 %.1f%%。溢出会让进度条"+
			"渲染到容器之外", f.ProgressPercent)
	}
	if f.PeakProgressPercent > 100 {
		t.Errorf("最佳期进度应被夹到 100 以内，实际 %.1f%%", f.PeakProgressPercent)
	}
	if f.Stage != domain.StageDeclined {
		t.Errorf("烘后一年应为已衰退，实际 %s", f.Stage)
	}
	if f.DaysUntilNextStage != 0 {
		t.Errorf("已衰退是终态，不应再有「距下一阶段 %d 天」",
			f.DaysUntilNextStage)
	}
}

// TestAdviceIsActionableForEachStage 确认每个阶段都给出可执行的建议。
//
// "该豆处于排气期"是状态复述，不是建议。用户要的是"再等 4 天"或
// "现在就该喝"。这条测的是有没有真的说出下一步动作。
func TestAdviceIsActionableForEachStage(t *testing.T) {
	roasted := day(2026, time.August, 1)
	w := WindowFor(domain.RoastMedium)

	stages := map[domain.FreshnessStage]int{
		domain.StageDegassing:  0,
		domain.StagePeak:       w.DegassingDays,
		domain.StageNearExpiry: w.PeakEndDay,
		domain.StageDeclined:   w.DeclineDay,
	}

	seen := map[string]domain.FreshnessStage{}
	for want, age := range stages {
		f := EvaluateFreshness(domain.RoastMedium, roasted, domain.CivilDate{},
			roasted.AddDays(age).Time())
		if f.Stage != want {
			t.Fatalf("测试前提不成立：第 %d 天应为 %s，实际 %s", age, want, f.Stage)
		}
		if f.Advice == "" {
			t.Errorf("%s 阶段缺少建议文案", want)
			continue
		}
		if prev, dup := seen[f.Advice]; dup {
			t.Errorf("%s 与 %s 的建议文案完全相同，说明建议没有随阶段变化",
				want, prev)
		}
		seen[f.Advice] = want
	}
}

// TestAllWindowsIsCompleteForSettingsPage 确认设置页能拿到全部三档窗口。
func TestAllWindowsIsCompleteForSettingsPage(t *testing.T) {
	all := AllWindows()
	if len(all) != 3 {
		t.Fatalf("应返回浅/中/深三档窗口，实际 %d 档", len(all))
	}
	for _, w := range all {
		if w.DegassingDays <= 0 || w.PeakEndDay <= w.DegassingDays ||
			w.DeclineDay <= w.PeakEndDay {
			t.Errorf("窗口 %+v 的天数不构成递增序列，会算出负宽度的进度条段", w)
		}
		if w.OpenedShelfLifeDays <= 0 {
			t.Errorf("窗口 %+v 缺少开封保质期", w)
		}
	}
}
