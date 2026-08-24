package bean

import (
	"time"

	"github.com/alkaid/enjoycoffee/internal/domain"
)

// Window 是某一烘焙度档位的养豆窗口参数，单位为烘焙后的民用自然日。
//
// 日期口径必须是民用自然日而非 24 小时倍数：咖啡圈说"烘后第 7 天"指的是
// 日历上的第 7 天，早上 8 点和晚上 8 点都算第 7 天。若按小时差折算，
// 同一支豆在早晨和傍晚查询会得到不同的天数，用户会认为程序在乱跳。
type Window struct {
	Band  domain.RoastBand `json:"band"`
	Label string           `json:"label"`

	// DegassingDays 排气期时长。此期间 CO₂ 大量逸出，会阻碍水与咖啡粉的接触，
	// 导致萃取不稳定、通道效应明显。
	DegassingDays int `json:"degassing_days"`
	// PeakEndDay 最佳风味期的结束日（自烘焙日起算）。
	PeakEndDay int `json:"peak_end_day"`
	// DeclineDay 风味衰退期的起始日。PeakEndDay 与它之间是临期。
	DeclineDay int `json:"decline_day"`
	// OpenedShelfLifeDays 开封后的风味保持上限。
	//
	// 开封后接触氧气，氧化速率上升一个数量级。这个上限与烘焙日龄的窗口
	// 取交集（谁先到算谁），因为一包烘后第 3 天就开封的豆，
	// 到第 17 天时即使还在"最佳期"内，实际上已经被氧化拖垮了。
	OpenedShelfLifeDays int `json:"opened_shelf_life_days"`
}

// defaultWindows 是三个烘焙行为组的出厂窗口参数。
//
// 数值依据：烘焙度越深，豆体结构越疏松、CO₂ 逸出越快，因此排气期更短；
// 但同样因为结构疏松、油脂上浮，氧化也更快，因此整体窗口更短。
// 这两个趋势方向相同，故深烘的三个界点全面前移。
//
// 全部可配置（Roadmap V-07）：不同烘焙商的曲线差异很大，
// 尤其是浅烘的排气期在快炒与慢炒之间能差出一倍。
var defaultWindows = map[domain.RoastBand]Window{
	domain.BandLight: {
		Band: domain.BandLight, Label: "浅烘",
		DegassingDays: 7, PeakEndDay: 35, DeclineDay: 45,
		OpenedShelfLifeDays: 14,
	},
	domain.BandMedium: {
		Band: domain.BandMedium, Label: "中烘",
		DegassingDays: 5, PeakEndDay: 32, DeclineDay: 40,
		OpenedShelfLifeDays: 14,
	},
	domain.BandDark: {
		Band: domain.BandDark, Label: "深烘",
		DegassingDays: 3, PeakEndDay: 24, DeclineDay: 30,
		OpenedShelfLifeDays: 12,
	},
}

// WindowFor 返回给定烘焙度的养豆窗口。
func WindowFor(level domain.RoastLevel) Window {
	if w, ok := defaultWindows[level.Band()]; ok {
		return w
	}
	return defaultWindows[domain.BandMedium]
}

// AllWindows 返回全部窗口配置，供设置页展示与"恢复默认"。
func AllWindows() []Window {
	return []Window{
		defaultWindows[domain.BandLight],
		defaultWindows[domain.BandMedium],
		defaultWindows[domain.BandDark],
	}
}

// Segment 是生命周期进度条上的一段，由后端算出几何量供前端直接绘制。
//
// 为何几何量由后端算（同裁定 C-02 的思路）：进度条的分段边界依赖养豆窗口配置，
// 而窗口配置是可调的业务参数。若前端自己算，配置改动后两处就会不一致，
// 表现为"进度条颜色分界与文字提示对不上"这类难以察觉的 bug。
type Segment struct {
	Stage     domain.FreshnessStage `json:"stage"`
	Label     string                `json:"label"`
	ColorHint string                `json:"color_hint"`
	StartDay  int                   `json:"start_day"`
	EndDay    int                   `json:"end_day"`
	// WidthPercent 是该段占整条进度条的宽度百分比，四段之和为 100。
	WidthPercent float64 `json:"width_percent"`
}

// Freshness 是一支豆的完整新鲜度状态。
type Freshness struct {
	Stage      domain.FreshnessStage `json:"stage"`
	StageLabel string                `json:"stage_label"`
	ColorHint  string                `json:"color_hint"`

	// RoastAgeDays 烘焙日龄。烘焙日当天为 0。
	RoastAgeDays int `json:"roast_age_days"`
	// OpenedAgeDays 开封日龄。未开封时为 -1，前端据此隐藏开封相关展示。
	OpenedAgeDays int  `json:"opened_age_days"`
	Opened        bool `json:"opened"`

	Window Window `json:"window"`

	// ProgressPercent 是在整条生命周期（0 到衰退日）上的位置。
	ProgressPercent float64 `json:"progress_percent"`
	// PeakProgressPercent 是在最佳风味期内部的位置。未进入最佳期时为 0，
	// 已过最佳期时为 100。
	PeakProgressPercent float64 `json:"peak_progress_percent"`

	// EffectiveDeclineDay 是实际的衰退起始日（已计入开封氧化的压缩效应）。
	EffectiveDeclineDay int    `json:"effective_decline_day"`
	EffectiveDeclineOn  string `json:"effective_decline_on"`
	// LimitedBy 说明是烘焙日龄还是开封氧化决定了当前的衰退时点。
	// 这个字段让"为什么我这包才烘后第 20 天就临期了"有了答案。
	LimitedBy string `json:"limited_by"`

	DaysUntilNextStage int    `json:"days_until_next_stage"`
	NextStageLabel     string `json:"next_stage_label"`

	Segments []Segment `json:"segments"`
	Advice   string    `json:"advice"`
}

// EvaluateFreshness 计算一支豆在给定时点的新鲜度状态。
//
// now 由调用方传入而非在函数内取当前时间，是为了让单元测试能确定性地
// 断言"烘后第 7 天应处于最佳期起点"这类边界，而不必操纵系统时钟。
func EvaluateFreshness(level domain.RoastLevel, roastedOn, openedOn domain.CivilDate, now time.Time) Freshness {
	w := WindowFor(level)

	f := Freshness{
		Window:        w,
		OpenedAgeDays: -1,
		Segments:      []Segment{},
	}

	if roastedOn.IsZero() {
		// 没有烘焙日就无法计算任何生命周期。返回一个明确的未知态，
		// 而不是拿今天当烘焙日凑一个看起来合理的结果。
		f.Stage = domain.StagePeak
		f.StageLabel = "烘焙日期未填"
		f.ColorHint = "neutral"
		f.Advice = "补填烘焙日期后即可看到排气期与最佳风味窗口。这是豆库看板最核心的一个字段。"
		return f
	}

	f.RoastAgeDays = roastedOn.DaysSince(now)
	if f.RoastAgeDays < 0 {
		// 烘焙日在未来：可能是用户预填了即将到货的豆子。
		// 按"尚未烘焙"处理，把日龄归零而不是显示负数。
		f.RoastAgeDays = 0
		f.Advice = "烘焙日期晚于今天，看板已按尚未开始排气处理。若是预登记即将到货的豆子，这是正常的。"
	}

	// 开封氧化压缩：实际衰退日取「烘焙窗口衰退日」与「开封日 + 开封保质期」的较早者
	effectiveDecline := w.DeclineDay
	effectivePeakEnd := w.PeakEndDay
	f.LimitedBy = "ROAST"

	if !openedOn.IsZero() {
		f.Opened = true
		f.OpenedAgeDays = openedOn.DaysSince(now)
		if f.OpenedAgeDays < 0 {
			f.OpenedAgeDays = 0
		}

		openedAtRoastAge := roastedOn.DaysSince(openedOn.Time())
		if openedAtRoastAge < 0 {
			openedAtRoastAge = 0
		}
		oxidationDecline := openedAtRoastAge + w.OpenedShelfLifeDays

		if oxidationDecline < effectiveDecline {
			effectiveDecline = oxidationDecline
			f.LimitedBy = "OPENING"
			// 临期段按比例前移，保持"临期占衰退前一段固定比例"的语义。
			// 直接把 PeakEnd 也设为 oxidationDecline 会让临期段消失，
			// 用户就失去了"还剩几天该赶紧喝"的提前预警。
			nearWindow := w.DeclineDay - w.PeakEndDay
			effectivePeakEnd = effectiveDecline - nearWindow
			if effectivePeakEnd < w.DegassingDays {
				effectivePeakEnd = w.DegassingDays
			}
		}
	}

	f.EffectiveDeclineDay = effectiveDecline
	f.EffectiveDeclineOn = roastedOn.AddDays(effectiveDecline).String()

	// 阶段判定。边界采用左闭右开：烘后第 7 天（排气期为 7 天）已进入最佳期。
	switch {
	case f.RoastAgeDays < w.DegassingDays:
		f.Stage = domain.StageDegassing
		f.DaysUntilNextStage = w.DegassingDays - f.RoastAgeDays
		f.NextStageLabel = domain.StagePeak.Label()
	case f.RoastAgeDays < effectivePeakEnd:
		f.Stage = domain.StagePeak
		f.DaysUntilNextStage = effectivePeakEnd - f.RoastAgeDays
		f.NextStageLabel = domain.StageNearExpiry.Label()
	case f.RoastAgeDays < effectiveDecline:
		f.Stage = domain.StageNearExpiry
		f.DaysUntilNextStage = effectiveDecline - f.RoastAgeDays
		f.NextStageLabel = domain.StageDeclined.Label()
	default:
		f.Stage = domain.StageDeclined
		f.DaysUntilNextStage = 0
		f.NextStageLabel = ""
	}

	f.StageLabel = f.Stage.Label()
	f.ColorHint = f.Stage.ColorHint()

	f.ProgressPercent = pct(f.RoastAgeDays, effectiveDecline)
	if f.RoastAgeDays >= w.DegassingDays && effectivePeakEnd > w.DegassingDays {
		f.PeakProgressPercent = pct(f.RoastAgeDays-w.DegassingDays, effectivePeakEnd-w.DegassingDays)
	} else if f.RoastAgeDays >= effectivePeakEnd {
		f.PeakProgressPercent = 100
	}

	f.Segments = buildSegments(w.DegassingDays, effectivePeakEnd, effectiveDecline)
	if f.Advice == "" {
		f.Advice = freshnessAdvice(f, w)
	}
	return f
}

// buildSegments 生成四段进度条几何。
//
// 总宽度基准取 衰退日 × 1.25，让"衰退期"这一段在条上有可见的实际宽度。
// 若以衰退日为 100%，衰退期就会被压成右端的一条线，而已经衰退的豆子
// 恰恰是用户最需要在看板上注意到的那些。
func buildSegments(degasEnd, peakEnd, declineDay int) []Segment {
	total := declineDay * 5 / 4
	if total <= 0 {
		total = 1
	}

	mk := func(stage domain.FreshnessStage, start, end int) Segment {
		if end < start {
			end = start
		}
		return Segment{
			Stage:        stage,
			Label:        stage.Label(),
			ColorHint:    stage.ColorHint(),
			StartDay:     start,
			EndDay:       end,
			WidthPercent: pct(end-start, total),
		}
	}

	return []Segment{
		mk(domain.StageDegassing, 0, degasEnd),
		mk(domain.StagePeak, degasEnd, peakEnd),
		mk(domain.StageNearExpiry, peakEnd, declineDay),
		mk(domain.StageDeclined, declineDay, total),
	}
}

// freshnessAdvice 给出该阶段的具体行动建议。
//
// 每条建议都要能被执行。"注意保存"这种话没有信息量；
// "还有 3 天进入临期，这周内喝掉"才是用户能照着做的。
func freshnessAdvice(f Freshness, w Window) string {
	switch f.Stage {
	case domain.StageDegassing:
		return "还在排气期，再养 " + itoa(f.DaysUntilNextStage) + " 天进入最佳风味期。" +
			"现在冲煮容易出现通道效应与萃取不均 —— CO₂ 会把水从粉层挤开。" +
			"若实在等不了，可以把研磨度调细一档并延长闷蒸时间来部分补偿。"

	case domain.StagePeak:
		s := "处于最佳风味期，距临期还有 " + itoa(f.DaysUntilNextStage) + " 天。这是记录基准配方的最好时机。"
		if f.LimitedBy == "OPENING" {
			s += "注意：由于已开封 " + itoa(f.OpenedAgeDays) + " 天，实际窗口已被氧化压缩，" +
				"比未开封状态提前了 " + itoa(w.DeclineDay-f.EffectiveDeclineDay) + " 天。"
		}
		return s

	case domain.StageNearExpiry:
		s := "已进入临期，" + itoa(f.DaysUntilNextStage) + " 天后进入风味衰退期。建议这几天内用完。" +
			"此阶段的高频酸香会先流失，可以适当提高水温或调细研磨度来把甜感和醇厚度托住。"
		if f.LimitedBy == "OPENING" {
			s += "临期提前的原因是开封氧化，不是烘焙日龄 —— 若还有未开封的同批豆，它们的窗口更长。"
		}
		return s

	default:
		over := f.RoastAgeDays - f.EffectiveDeclineDay
		return "已进入风味衰退期（超出 " + itoa(over) + " 天）。酸质会转钝、可能出现纸味或木质味。" +
			"仍可饮用，但不建议再用它做参数对比实验 —— 衰退豆的萃取表现不稳定，" +
			"会污染你的历史数据与偏好曲线。"
	}
}

func pct(part, whole int) float64 {
	if whole <= 0 {
		return 0
	}
	if part < 0 {
		part = 0
	}
	if part > whole {
		part = whole
	}
	// 保留一位小数，用整数运算避免浮点累加漂移
	return float64(part*1000/whole) / 10
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
