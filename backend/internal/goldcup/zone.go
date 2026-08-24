package goldcup

import (
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

// YieldZone 是萃取率轴上的位置。它回答"萃取得够不够"。
type YieldZone string

const (
	// YieldUnder 欠萃：可溶物还没充分溶出。感官表现为尖酸、青涩、咸感、余韵短。
	YieldUnder YieldZone = "UNDER_EXTRACTED"
	// YieldIdeal 萃取率落在金杯区间。
	YieldIdeal YieldZone = "IDEAL"
	// YieldOver 过萃：把不该溶出的苦涩物也带了出来。感官表现为干苦、木质、涩感。
	YieldOver YieldZone = "OVER_EXTRACTED"
)

// StrengthZone 是浓度轴上的位置。它回答"喝起来浓不浓"。
//
// 关键认知：浓度与萃取率是两个独立维度。一杯咖啡完全可以「萃取充分但很淡」
// （粉液比太大），也可以「萃取不足但很浓」（粉液比太小）。把两者混为一谈
// 是咖啡新手最常见的误解，也是本引擎坚持输出九宫格而非单一"好/坏"结论的原因。
type StrengthZone string

const (
	// StrengthWeak 过淡。
	StrengthWeak StrengthZone = "TOO_WEAK"
	// StrengthIdeal 浓度落在金杯区间。
	StrengthIdeal StrengthZone = "IDEAL"
	// StrengthStrong 过浓。
	StrengthStrong StrengthZone = "TOO_STRONG"
)

// Zone 是萃取结果在冲煮控制图上的九宫格落区。
type Zone struct {
	Yield    YieldZone    `json:"yield_zone"`
	Strength StrengthZone `json:"strength_zone"`

	// Code 是九宫格的机器可读标识，形如 "UNDER_WEAK"。
	Code string `json:"code"`
	// Label 是中文短标签，供图表角标使用。
	Label string `json:"label"`
	// Diagnosis 是感官层面的诊断，把数字翻译成用户喝得出来的东西。
	Diagnosis string `json:"diagnosis"`
	// InGoldCup 仅当两轴同时落在理想区间时为真。
	InGoldCup bool `json:"in_gold_cup"`

	// YieldOffsetPercent 与 StrengthOffsetPercent 是相对区间中心的偏移量（百分点）。
	// 正值表示偏高。前端用它绘制从当前点指向理想中心的箭头。
	YieldOffsetPercent    float64 `json:"yield_offset_percent"`
	StrengthOffsetPercent float64 `json:"strength_offset_percent"`
	YieldOffsetText       string  `json:"yield_offset_text"`
	StrengthOffsetText    string  `json:"strength_offset_text"`
}

// classify 判定萃取结果落在九宫格的哪一格。
//
// 边界处理：采用闭区间 [min, max]。恰好等于 18.00% 的萃取率判为理想而非欠萃 ——
// SCA 标准本身给的是包含边界的区间，且折射仪的读数精度（±0.01%）使得
// 在边界上纠缠开闭区间没有实际意义。
func classify(p Profile, yield, strength fixed.Ratio) Zone {
	z := Zone{}

	switch {
	case yield < p.YieldMin:
		z.Yield = YieldUnder
	case yield > p.YieldMax:
		z.Yield = YieldOver
	default:
		z.Yield = YieldIdeal
	}

	switch {
	case strength < p.StrengthMin:
		z.Strength = StrengthWeak
	case strength > p.StrengthMax:
		z.Strength = StrengthStrong
	default:
		z.Strength = StrengthIdeal
	}

	z.InGoldCup = z.Yield == YieldIdeal && z.Strength == StrengthIdeal
	z.Code = zoneCode(z.Yield, z.Strength)
	z.Label, z.Diagnosis = zoneNarrative(z.Yield, z.Strength)

	yOff := yield - p.YieldMidpoint()
	sOff := strength - p.StrengthMidpoint()
	z.YieldOffsetPercent = yOff.ApproxPercentFloat()
	z.StrengthOffsetPercent = sOff.ApproxPercentFloat()
	z.YieldOffsetText = signedPercent(yOff)
	z.StrengthOffsetText = signedPercent(sOff)

	return z
}

func zoneCode(y YieldZone, s StrengthZone) string {
	yPart := map[YieldZone]string{
		YieldUnder: "UNDER",
		YieldIdeal: "IDEAL",
		YieldOver:  "OVER",
	}[y]
	sPart := map[StrengthZone]string{
		StrengthWeak:   "WEAK",
		StrengthIdeal:  "BALANCED",
		StrengthStrong: "STRONG",
	}[s]
	return yPart + "_" + sPart
}

// zoneNarrative 把九宫格坐标翻译为人话。
//
// 这是引擎"可解释性"承诺的落地点：用户看到 "19.2% / 1.28%" 这两个数字时
// 未必知道意味着什么，但看到"萃取充分且浓度合宜 —— 这是金杯区间"就懂了。
// 每一格的诊断都描述具体的感官后果，而不是重复数字。
func zoneNarrative(y YieldZone, s StrengthZone) (label, diagnosis string) {
	switch {
	case y == YieldIdeal && s == StrengthIdeal:
		return "金杯", "萃取充分且浓度合宜，酸甜苦三者处于平衡带。这一杯落在 SCA 金杯区间内。"

	case y == YieldUnder && s == StrengthWeak:
		return "欠萃偏淡", "既没萃够也不够浓，整体寡淡且带尖酸。这通常是研磨过粗叠加粉液比过大，两个方向都需要收紧。"
	case y == YieldUnder && s == StrengthIdeal:
		return "欠萃", "浓度合宜但萃取不足，入口有明显的尖酸与青涩感，甜味迟迟不来、余韵偏短。可溶物还留在粉里。"
	case y == YieldUnder && s == StrengthStrong:
		return "欠萃偏浓", "又浓又酸涩，是粉下得太多而萃取效率不够的典型组合。减粉或磨细都能改善，但方向不同。"

	case y == YieldIdeal && s == StrengthWeak:
		return "萃取充分偏淡", "萃取率本身很健康，只是水放多了，风味被稀释。这是最容易修的一种偏差：保持研磨度，减水或加粉即可。"
	case y == YieldIdeal && s == StrengthStrong:
		return "萃取充分偏浓", "萃取率健康但浓度偏高，风味浓郁到有些压迫感。加水稀释就能落回金杯，无需改研磨度。"

	case y == YieldOver && s == StrengthWeak:
		return "过萃偏淡", "萃得太狠却又不浓，喝起来是空洞的苦涩。往往是研磨过细配上过大粉液比，或萃取时间拖得太长。"
	case y == YieldOver && s == StrengthIdeal:
		return "过萃", "浓度合宜但萃过了头，苦味与涩感盖住了甜。粉层里不该溶出的物质也被带了出来。"
	case y == YieldOver && s == StrengthStrong:
		return "过萃偏浓", "又浓又苦涩，是最难喝的一格。研磨过细、粉液比过小、萃取时间过长可能同时存在。"
	}
	return "未知", "无法判定落区。"
}

// signedPercent 把偏移量渲染为带符号的百分点字符串，如 "+1.20" / "-0.35"。
func signedPercent(v fixed.Ratio) string {
	s := v.Percent()
	if v > 0 {
		return "+" + s
	}
	return s
}

// ZoneMatrix 返回九宫格的完整定义，供前端渲染控制图的图例与色块。
//
// 由后端下发而非前端硬编码：九宫格的诊断文案是业务知识，
// 若在前端复制一份，两处会在需求演进中不可避免地漂移。
func ZoneMatrix() []ZoneCell {
	yields := []YieldZone{YieldUnder, YieldIdeal, YieldOver}
	strengths := []StrengthZone{StrengthWeak, StrengthIdeal, StrengthStrong}

	cells := make([]ZoneCell, 0, 9)
	for _, s := range strengths {
		for _, y := range yields {
			label, diag := zoneNarrative(y, s)
			cells = append(cells, ZoneCell{
				Code:        zoneCode(y, s),
				Yield:       y,
				Strength:    s,
				Label:       label,
				Diagnosis:   diag,
				InGoldCup:   y == YieldIdeal && s == StrengthIdeal,
				SeverityHue: severityHue(y, s),
			})
		}
	}
	return cells
}

// ZoneCell 是九宫格中的一格。
type ZoneCell struct {
	Code        string       `json:"code"`
	Yield       YieldZone    `json:"yield_zone"`
	Strength    StrengthZone `json:"strength_zone"`
	Label       string       `json:"label"`
	Diagnosis   string       `json:"diagnosis"`
	InGoldCup   bool         `json:"in_gold_cup"`
	SeverityHue string       `json:"severity_hue"`
}

// severityHue 给每格一个语义化的严重度键，前端据此上色。
//
// 分级依据是"距离可饮用的远近"而非简单的偏离格数：
// 单轴偏离（如仅浓度偏淡）加水或加粉就能救回，属轻度；
// 双轴同向偏离（又苦又浓）需要同时改研磨与配比，属重度。
func severityHue(y YieldZone, s StrengthZone) string {
	yOff := y != YieldIdeal
	sOff := s != StrengthIdeal
	switch {
	case !yOff && !sOff:
		return "gold"
	case yOff && sOff:
		return "danger"
	case yOff:
		// 萃取率偏离需要动研磨度，比单纯调水量麻烦
		return "warning"
	default:
		return "info"
	}
}
