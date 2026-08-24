package goldcup

import (
	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

// Profile 是某一冲煮法的金杯判定标准。
//
// 存在动因（Requirements 裁定 C-04）：原始需求把"18%–22% 金杯区间"同时施加于
// 手冲与意式，这是专业错误。SCA 金杯标准的完整定义是「萃取率 18%–22% 且
// 浓度 TDS 1.15%–1.35%」，后半句只适用于滤泡咖啡。意式浓缩的 TDS 正常落在
// 8%–12%，若套用 1.15%–1.35% 的浓度轴，每一杯合格的 Espresso 都会被判为过浓。
//
// 因此本类型让两条轴各自独立：萃取率轴两种冲煮法共享（原需求的 18%–22% 完整保留），
// 浓度轴按冲煮法分流。
type Profile struct {
	Method domain.BrewMethod `json:"method"`
	Label  string            `json:"label"`

	// 萃取率区间。两种冲煮法默认相同（18%–22%），但仍分别可配，
	// 因为浅烘豆的甜感峰值常出现在 20%–22% 上半区，店主可能想收窄。
	YieldMin fixed.Ratio `json:"-"`
	YieldMax fixed.Ratio `json:"-"`

	// 浓度区间。这是两种冲煮法的本质差异所在。
	StrengthMin fixed.Ratio `json:"-"`
	StrengthMax fixed.Ratio `json:"-"`

	// 粉液比参考区间。对意式而言它是主控参数（与浓度强相关），
	// 对手冲而言它是浓度的间接推手。
	RatioMin fixed.Ratio `json:"-"`
	RatioMax fixed.Ratio `json:"-"`

	// LRR 是咖啡粉持水系数（Liquid Retained Ratio，克水/克粉）。
	// 手冲的液重无法直接称量（水留在滤纸和粉层里），必须由总注水量减去吸水量推得。
	// 意式直接称出液重量，此值不参与计算。
	LRR fixed.Ratio `json:"-"`

	// UsesLRR 标记该冲煮法是否需要用 LRR 推导液重。
	UsesLRR bool `json:"uses_lrr"`

	// ChartKind 告诉前端该用哪种控制图渲染。
	ChartKind string `json:"chart_kind"`
}

// 默认 Profile 的数值来源与依据：
//
//   - 滤泡萃取率 18%–22%：SCA Gold Cup Standard，源自 1950 年代 MIT / Lockhart
//     的咖啡冲煮控制图研究，至今仍是行业基准。
//   - 滤泡浓度 1.15%–1.35%：SCA 北美口味区间。欧洲标准偏浓（约 1.20%–1.45%），
//     故此值必须可配。
//   - 意式浓度 8%–12%：现代意式浓缩的实测 TDS 常见范围。传统意式偏上限，
//     现代浅烘 filter-style espresso 偏下限。
//   - 意式粉液比 1:1.5–1:2.5：涵盖 ristretto 偏上到 lungo 偏下的主流区间。
//   - LRR 2.0：滤泡咖啡粉的湿粉持水量约为干粉重的 2 倍。实测值随滤杯、
//     粉层厚度、研磨度在 1.8–2.2 间浮动，故开放校准（Roadmap V-08）。
var (
	filterProfile = Profile{
		Method:      domain.MethodFilter,
		Label:       "手冲/滤泡 · SCA 冲煮控制图",
		YieldMin:    fixed.MustRatioPercent("18"),
		YieldMax:    fixed.MustRatioPercent("22"),
		StrengthMin: fixed.MustRatioPercent("1.15"),
		StrengthMax: fixed.MustRatioPercent("1.35"),
		RatioMin:    fixed.MustRatioMultiple("15"),
		RatioMax:    fixed.MustRatioMultiple("17"),
		LRR:         fixed.MustRatioMultiple("2.0"),
		UsesLRR:     true,
		ChartKind:   "SCA_BREWING_CONTROL_CHART",
	}

	espressoProfile = Profile{
		Method:      domain.MethodEspresso,
		Label:       "意式浓缩 · Espresso Compass",
		YieldMin:    fixed.MustRatioPercent("18"),
		YieldMax:    fixed.MustRatioPercent("22"),
		StrengthMin: fixed.MustRatioPercent("8"),
		StrengthMax: fixed.MustRatioPercent("12"),
		RatioMin:    fixed.MustRatioMultiple("1.5"),
		RatioMax:    fixed.MustRatioMultiple("2.5"),
		// 意式直接称量出液重，无需持水系数推导
		LRR:       0,
		UsesLRR:   false,
		ChartKind: "ESPRESSO_COMPASS",
	}
)

// DefaultProfile 返回给定冲煮法的出厂标准。
func DefaultProfile(m domain.BrewMethod) (Profile, error) {
	switch m {
	case domain.MethodFilter:
		return filterProfile, nil
	case domain.MethodEspresso:
		return espressoProfile, nil
	default:
		return Profile{}, domain.Validation("UNKNOWN_BREW_METHOD",
			"未知冲煮法，可选 FILTER 或 ESPRESSO")
	}
}

// DefaultProfiles 返回全部出厂标准，供设置页初始化与"恢复默认"使用。
func DefaultProfiles() []Profile {
	return []Profile{filterProfile, espressoProfile}
}

// Validate 校验 Profile 自身的一致性。
//
// 这是配置面板（Roadmap V-07）的守门人：店主可以按自家出品标准调整区间，
// 但不能给出下界大于上界这类自相矛盾的配置，否则所有落区判定都会失效。
func (p Profile) Validate() error {
	if !p.Method.Valid() {
		return domain.Validation("UNKNOWN_BREW_METHOD", "冲煮法非法").
			WithField("method", "必须为 FILTER 或 ESPRESSO")
	}

	e := domain.Validation("INVALID_PROFILE", "金杯标准配置自相矛盾")
	bad := false

	if p.YieldMin <= 0 || p.YieldMax <= 0 || p.YieldMin >= p.YieldMax {
		e.WithField("yield_range", "萃取率下界必须为正且小于上界")
		bad = true
	}
	if p.StrengthMin <= 0 || p.StrengthMax <= 0 || p.StrengthMin >= p.StrengthMax {
		e.WithField("strength_range", "浓度下界必须为正且小于上界")
		bad = true
	}
	if p.RatioMin <= 0 || p.RatioMax <= 0 || p.RatioMin >= p.RatioMax {
		e.WithField("ratio_range", "粉液比下界必须为正且小于上界")
		bad = true
	}
	if p.UsesLRR && (p.LRR < fixed.MustRatioMultiple("1.0") || p.LRR > fixed.MustRatioMultiple("4.0")) {
		// 1.0 以下意味着湿粉比干粉还轻，物理上不可能；
		// 4.0 以上意味着粉吸走的水是自身重量 4 倍，超出任何实测记录。
		e.WithField("lrr", "持水系数应落在 1.0–4.0 之间")
		bad = true
	}
	// 萃取率超过 30% 在物理上不可达：咖啡豆中可溶物总量约占 28%–30%，
	// 允许配置到这个数以上说明用户填错了单位（比如把 0.20 当成 20 填）。
	if p.YieldMax > fixed.MustRatioPercent("30") {
		e.WithField("yield_max", "萃取率上界不应超过 30%（咖啡豆可溶物总量上限）")
		bad = true
	}

	if bad {
		return e
	}
	return nil
}

// YieldMidpoint 返回萃取率区间中心，用于计算偏移向量。
func (p Profile) YieldMidpoint() fixed.Ratio { return (p.YieldMin + p.YieldMax) / 2 }

// StrengthMidpoint 返回浓度区间中心。
func (p Profile) StrengthMidpoint() fixed.Ratio { return (p.StrengthMin + p.StrengthMax) / 2 }

// ProfileView 是 Profile 的 API 输出形态，把定点数展开为可读字符串与
// 供前端绘图的数值，避免前端自行解析 PPM 标度。
type ProfileView struct {
	Method    domain.BrewMethod `json:"method"`
	Label     string            `json:"label"`
	ChartKind string            `json:"chart_kind"`
	UsesLRR   bool              `json:"uses_lrr"`

	YieldMinPercent    float64 `json:"yield_min_percent"`
	YieldMaxPercent    float64 `json:"yield_max_percent"`
	StrengthMinPercent float64 `json:"strength_min_percent"`
	StrengthMaxPercent float64 `json:"strength_max_percent"`
	RatioMin           float64 `json:"ratio_min"`
	RatioMax           float64 `json:"ratio_max"`
	LRR                float64 `json:"lrr"`

	YieldMinText    string `json:"yield_min_text"`
	YieldMaxText    string `json:"yield_max_text"`
	StrengthMinText string `json:"strength_min_text"`
	StrengthMaxText string `json:"strength_max_text"`
	RatioMinText    string `json:"ratio_min_text"`
	RatioMaxText    string `json:"ratio_max_text"`
	LRRText         string `json:"lrr_text"`
}

// View 把 Profile 转为 API 输出形态。
func (p Profile) View() ProfileView {
	return ProfileView{
		Method:    p.Method,
		Label:     p.Label,
		ChartKind: p.ChartKind,
		UsesLRR:   p.UsesLRR,

		YieldMinPercent:    p.YieldMin.ApproxPercentFloat(),
		YieldMaxPercent:    p.YieldMax.ApproxPercentFloat(),
		StrengthMinPercent: p.StrengthMin.ApproxPercentFloat(),
		StrengthMaxPercent: p.StrengthMax.ApproxPercentFloat(),
		RatioMin:           p.RatioMin.ApproxMultipleFloat(),
		RatioMax:           p.RatioMax.ApproxMultipleFloat(),
		LRR:                p.LRR.ApproxMultipleFloat(),

		YieldMinText:    p.YieldMin.Percent(),
		YieldMaxText:    p.YieldMax.Percent(),
		StrengthMinText: p.StrengthMin.Percent(),
		StrengthMaxText: p.StrengthMax.Percent(),
		RatioMinText:    p.RatioMin.Multiple(),
		RatioMaxText:    p.RatioMax.Multiple(),
		LRRText:         p.LRR.Multiple(),
	}
}
