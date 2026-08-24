package domain

import "strings"

// BrewMethod 是冲煮法。它不是一个装饰性标签 —— 金杯判定的浓度轴完全由它决定
// （见 Requirements 裁定 C-04），因此任何计算路径都必须显式携带此值。
type BrewMethod string

const (
	// MethodFilter 手冲 / 滤泡。浓度轴 1.15%–1.35%，走 SCA 冲煮控制图。
	MethodFilter BrewMethod = "FILTER"
	// MethodEspresso 意式浓缩。浓度轴 8%–12%，走 Espresso Compass 双轴。
	MethodEspresso BrewMethod = "ESPRESSO"
)

// Valid 报告冲煮法是否为已知值。
func (m BrewMethod) Valid() bool {
	return m == MethodFilter || m == MethodEspresso
}

// Label 返回中文展示名。
func (m BrewMethod) Label() string {
	switch m {
	case MethodFilter:
		return "手冲/滤泡"
	case MethodEspresso:
		return "意式浓缩"
	default:
		return "未知"
	}
}

// ParseBrewMethod 宽松解析冲煮法，容忍大小写与常见别名。
func ParseBrewMethod(s string) (BrewMethod, bool) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "FILTER", "POUROVER", "POUR_OVER", "DRIP", "手冲":
		return MethodFilter, true
	case "ESPRESSO", "ESP", "意式":
		return MethodEspresso, true
	default:
		return "", false
	}
}

// RoastLevel 是烘焙度。它决定养豆窗口的默认时长（见 Requirements §3.1），
// 因此是豆库生命周期计算的必需输入。
type RoastLevel string

const (
	RoastLight       RoastLevel = "LIGHT"        // 极浅
	RoastLightMedium RoastLevel = "LIGHT_MEDIUM" // 浅
	RoastMedium      RoastLevel = "MEDIUM"       // 中浅
	RoastMediumDark  RoastLevel = "MEDIUM_DARK"  // 中
	RoastDark        RoastLevel = "DARK"         // 中深
	RoastVeryDark    RoastLevel = "VERY_DARK"    // 深
)

// AllRoastLevels 按由浅到深排列，前端下拉框顺序依赖此顺序。
func AllRoastLevels() []RoastLevel {
	return []RoastLevel{RoastLight, RoastLightMedium, RoastMedium, RoastMediumDark, RoastDark, RoastVeryDark}
}

func (r RoastLevel) Valid() bool {
	for _, v := range AllRoastLevels() {
		if v == r {
			return true
		}
	}
	return false
}

func (r RoastLevel) Label() string {
	switch r {
	case RoastLight:
		return "极浅烘"
	case RoastLightMedium:
		return "浅烘"
	case RoastMedium:
		return "中浅烘"
	case RoastMediumDark:
		return "中烘"
	case RoastDark:
		return "中深烘"
	case RoastVeryDark:
		return "深烘"
	default:
		return "未知"
	}
}

// RoastBand 把六档烘焙度归并为三个养豆行为组。
//
// 归并依据：排气速率主要由烘焙终温决定，相邻档位的排气差异小于测量噪声，
// 分六套窗口参数是过度拟合，反而让用户无从校准。
type RoastBand string

const (
	BandLight  RoastBand = "LIGHT_BAND"
	BandMedium RoastBand = "MEDIUM_BAND"
	BandDark   RoastBand = "DARK_BAND"
)

// Band 返回该烘焙度所属的养豆行为组。
func (r RoastLevel) Band() RoastBand {
	switch r {
	case RoastLight, RoastLightMedium:
		return BandLight
	case RoastMedium, RoastMediumDark:
		return BandMedium
	default:
		return BandDark
	}
}

// ProcessMethod 是生豆处理法。设为开放字符串而非闭合枚举：
// 精品咖啡的处理法在持续发明（厌氧日晒、酵母发酵、碳酸浸渍…），
// 写死枚举会在半年内过时并迫使用户造假数据。
type ProcessMethod string

// CommonProcessMethods 供前端下拉框预填，用户仍可自由输入其他值。
func CommonProcessMethods() []string {
	return []string{"日晒", "水洗", "蜜处理", "厌氧日晒", "厌氧水洗", "湿刨", "酵母发酵", "碳酸浸渍"}
}

// FreshnessStage 是豆子在生命周期曲线上的所处阶段，驱动前端进度条配色。
type FreshnessStage string

const (
	// StageDegassing 排气期：CO₂ 大量逸出，萃取不稳定，不建议冲煮。
	StageDegassing FreshnessStage = "DEGASSING"
	// StagePeak 最佳风味窗口。
	StagePeak FreshnessStage = "PEAK"
	// StageNearExpiry 临期：仍可饮用但风味开始收敛，建议尽快用完。
	StageNearExpiry FreshnessStage = "NEAR_EXPIRY"
	// StageDeclined 衰退期：氧化主导，酸质转钝、出现纸味。
	StageDeclined FreshnessStage = "DECLINED"
)

// Valid 判断是否为已知阶段。
func (s FreshnessStage) Valid() bool {
	switch s {
	case StageDegassing, StagePeak, StageNearExpiry, StageDeclined:
		return true
	default:
		return false
	}
}

// AllFreshnessStages 按生命周期先后顺序返回全部阶段，供前端筛选器渲染。
func AllFreshnessStages() []FreshnessStage {
	return []FreshnessStage{StageDegassing, StagePeak, StageNearExpiry, StageDeclined}
}

func (s FreshnessStage) Label() string {
	switch s {
	case StageDegassing:
		return "排气期"
	case StagePeak:
		return "最佳风味期"
	case StageNearExpiry:
		return "临期"
	case StageDeclined:
		return "风味衰退期"
	default:
		return "未知"
	}
}

// ColorHint 给前端一个语义化配色键。具体色值由前端设计系统决定，
// 后端只表达语义，避免把十六进制色值硬编码在业务层。
func (s FreshnessStage) ColorHint() string {
	switch s {
	case StageDegassing:
		return "neutral"
	case StagePeak:
		return "success"
	case StageNearExpiry:
		return "warning"
	case StageDeclined:
		return "danger"
	default:
		return "neutral"
	}
}

// PourTechnique 是注水手法标签，用于在流速曲线上标注每段的操作意图。
type PourTechnique string

const (
	PourBloom   PourTechnique = "BLOOM"   // 闷蒸
	PourCircle  PourTechnique = "CIRCLE"  // 绕圈
	PourCenter  PourTechnique = "CENTER"  // 中心注水
	PourSpiral  PourTechnique = "SPIRAL"  // 螺旋
	PourStir    PourTechnique = "STIR"    // 搅拌
	PourPulse   PourTechnique = "PULSE"   // 断水
	PourDrawoff PourTechnique = "DRAWOFF" // 下水/滴滤
)

func (p PourTechnique) Label() string {
	switch p {
	case PourBloom:
		return "闷蒸"
	case PourCircle:
		return "绕圈"
	case PourCenter:
		return "中心"
	case PourSpiral:
		return "螺旋"
	case PourStir:
		return "搅拌"
	case PourPulse:
		return "断水"
	case PourDrawoff:
		return "下水"
	default:
		return "注水"
	}
}

// AllPourTechniques 供前端打点按钮渲染。
func AllPourTechniques() []PourTechnique {
	return []PourTechnique{PourBloom, PourCircle, PourCenter, PourSpiral, PourStir, PourPulse, PourDrawoff}
}

func (p PourTechnique) Valid() bool {
	for _, v := range AllPourTechniques() {
		if v == p {
			return true
		}
	}
	return false
}

// FlavorAxis 是六维风味评分的维度。顺序固定，前端雷达图的顶点顺序依赖它。
type FlavorAxis string

const (
	AxisAcidity   FlavorAxis = "ACIDITY"   // 酸
	AxisBitter    FlavorAxis = "BITTER"    // 苦
	AxisSweet     FlavorAxis = "SWEET"     // 甜
	AxisBody      FlavorAxis = "BODY"      // 醇厚
	AxisAroma     FlavorAxis = "AROMA"     // 香气
	AxisAftertone FlavorAxis = "AFTERTONE" // 余韵
)

// FlavorAxes 按雷达图顶点的顺时针顺序返回六个维度。
//
// 顺序不是随意的：把「酸」与「苦」放在相邻位置，能让过萃（苦升酸降）在雷达图上
// 表现为一条明显的斜边塌陷，比随机排列更容易被肉眼识别。
func FlavorAxes() []FlavorAxis {
	return []FlavorAxis{AxisAcidity, AxisSweet, AxisAroma, AxisAftertone, AxisBody, AxisBitter}
}

func (a FlavorAxis) Label() string {
	switch a {
	case AxisAcidity:
		return "酸"
	case AxisBitter:
		return "苦"
	case AxisSweet:
		return "甜"
	case AxisBody:
		return "醇厚"
	case AxisAroma:
		return "香气"
	case AxisAftertone:
		return "余韵"
	default:
		return "未知"
	}
}

func (a FlavorAxis) Valid() bool {
	for _, v := range FlavorAxes() {
		if v == a {
			return true
		}
	}
	return false
}
