package goldcup

import (
	"math/big"
	"sort"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

// AdviceKind 是调整建议的动作类型，决定前端用哪个图标渲染。
type AdviceKind string

const (
	AdviceGrind       AdviceKind = "GRIND"       // 调整研磨度
	AdviceDose        AdviceKind = "DOSE"        // 调整粉量
	AdviceWater       AdviceKind = "WATER"       // 调整水量/液重
	AdviceTemperature AdviceKind = "TEMPERATURE" // 调整水温
	AdviceTime        AdviceKind = "TIME"        // 调整萃取时间
	AdviceTechnique   AdviceKind = "TECHNIQUE"   // 调整手法
	AdviceHold        AdviceKind = "HOLD"        // 无需调整，保持
)

// Direction 是调整方向。
type Direction string

const (
	DirIncrease Direction = "INCREASE"
	DirDecrease Direction = "DECREASE"
	DirFiner    Direction = "FINER"
	DirCoarser  Direction = "COARSER"
	DirHold     Direction = "HOLD"
)

// Advice 是一条可解释的调整建议。
//
// 设计约束（Requirements §3.5）：禁止黑盒数字。每条建议必须携带 Rationale
// 说明为什么这么建议，以及 Confidence 表明这条建议的依据强度。
// 用户有权知道"磨细 2 档"是从哪推出来的 —— 否则这就只是个算命工具。
type Advice struct {
	Kind      AdviceKind `json:"kind"`
	Direction Direction  `json:"direction"`
	// Headline 是一句可直接执行的指令，如"研磨度调细 2 档"。
	Headline string `json:"headline"`
	// Rationale 解释这条建议的推导依据。
	Rationale string `json:"rationale"`
	// TargetText 是调整后的具体目标值（若可计算），如"粉量 16.8g"。
	TargetText string `json:"target_text"`
	// Priority 越小越优先。前端按此排序，第一条是最该先动的那个参数。
	Priority int `json:"priority"`
}

// buildAdvice 根据落区与输入生成有序的调整建议列表。
//
// 排序逻辑背后的冲煮方法论：一次只动一个变量。若同时改研磨度和粉液比，
// 下一杯的结果无法归因到任何单一原因，用户就永远学不会自己的设备。
// 因此建议列表是"按优先级排序的单变量候选"，而非"请同时执行以下三项"。
//
// 优先级原则：
//  1. 浓度偏差优先于萃取率偏差 —— 加水减水是可逆的、当场就能验证的操作，
//     而改研磨度需要重新冲一杯。先修便宜的。
//  2. 萃取率偏差优先动研磨度 —— 它是萃取效率的主控变量，杠杆最大。
//  3. 水温与时间是微调项，只在研磨度已经动过之后才建议。
func buildAdvice(p Profile, z Zone, in Input, bev fixed.Mass, yield, strength fixed.Ratio) []Advice {
	if z.InGoldCup {
		return []Advice{{
			Kind:       AdviceHold,
			Direction:  DirHold,
			Headline:   "保持当前参数",
			Rationale:  "萃取率 " + yield.Percent() + "% 与浓度 " + strength.Percent() + "% 双双落在金杯区间，这组参数值得记录为该豆的基准配方。",
			TargetText: "",
			Priority:   0,
		}}
	}

	var out []Advice

	// ---- 浓度轴：调水量或粉量 ----
	if z.Strength != StrengthIdeal {
		target := p.StrengthMidpoint()
		// 保持粉量不变，调液重使浓度落到区间中心：
		// 由 EY = bev*TDS/dose 且 EY 不随稀释改变（溶解物总量已定），
		// 有 bev_new = bev_old * TDS_old / TDS_target
		if strength > 0 {
			ratio := new(big.Rat).Quo(strength.RawRat(), target.RawRat())
			scaled := new(big.Rat).Mul(bev.RawRat(), ratio)
			if newBev, err := fixed.MassFromMilligramsRat(scaled); err == nil && newBev > 0 {
				delta := newBev - bev
				if z.Strength == StrengthStrong {
					out = append(out, Advice{
						Kind:       AdviceWater,
						Direction:  DirIncrease,
						Headline:   "加水稀释约 " + absMass(delta).Grams() + "g",
						Rationale:  "浓度 " + strength.Percent() + "% 高出理想上限 " + p.StrengthMax.Percent() + "%。稀释只改变浓度、不改变萃取率（可溶物总量已经定了），所以这是唯一不会打乱其他参数的修法。",
						TargetText: "液重目标 " + newBev.Grams() + "g",
						Priority:   1,
					})
				} else {
					out = append(out, Advice{
						Kind:       AdviceWater,
						Direction:  DirDecrease,
						Headline:   "减少液重约 " + absMass(delta).Grams() + "g",
						Rationale:  "浓度 " + strength.Percent() + "% 低于理想下限 " + p.StrengthMin.Percent() + "%，水放多了。下次少接一些，或在粉量上补齐。",
						TargetText: "液重目标 " + newBev.Grams() + "g",
						Priority:   1,
					})
				}
			}
		}

		// 备选路径：保持液重不变，改粉量
		if newDose, err := SolveDose(yield, target, bev); err == nil && newDose > 0 {
			delta := newDose - in.Dose
			dir := DirIncrease
			verb := "增加"
			if delta < 0 {
				dir = DirDecrease
				verb = "减少"
			}
			out = append(out, Advice{
				Kind:       AdviceDose,
				Direction:  dir,
				Headline:   verb + "粉量约 " + absMass(delta).Grams() + "g",
				Rationale:  "若你希望保持出杯量不变，改粉量是另一条路。它会同时轻微影响萃取效率，所以调完需要重新验一次萃取率。",
				TargetText: "粉量目标 " + newDose.Grams() + "g",
				Priority:   3,
			})
		}
	}

	// ---- 萃取率轴：调研磨度 ----
	if z.Yield != YieldIdeal {
		notches := grindNotches(p, yield)
		if z.Yield == YieldUnder {
			out = append(out, Advice{
				Kind:       AdviceGrind,
				Direction:  DirFiner,
				Headline:   "研磨度调细 " + notchLabel(notches),
				Rationale:  "萃取率 " + yield.Percent() + "% 低于金杯下限 " + p.YieldMin.Percent() + "%，差 " + absRatio(yield-p.YieldMin).Percent() + " 个百分点。磨细会增大粉的总表面积并延长水与粉的接触时间，是提升萃取效率杠杆最大的单一变量。",
				TargetText: "",
				Priority:   2,
			})
			out = append(out, Advice{
				Kind:       AdviceTemperature,
				Direction:  DirIncrease,
				Headline:   "水温提高 2–3℃",
				Rationale:  "若不想动研磨度，升温也能提高萃取效率，但杠杆比研磨度小得多。建议先把研磨度调对，再用水温做微调。",
				TargetText: "",
				Priority:   4,
			})
		} else {
			out = append(out, Advice{
				Kind:       AdviceGrind,
				Direction:  DirCoarser,
				Headline:   "研磨度调粗 " + notchLabel(notches),
				Rationale:  "萃取率 " + yield.Percent() + "% 超出金杯上限 " + p.YieldMax.Percent() + "%，超了 " + absRatio(yield-p.YieldMax).Percent() + " 个百分点。磨粗能减少过度萃取的苦涩物溶出。",
				TargetText: "",
				Priority:   2,
			})
			if p.Method == domain.MethodEspresso {
				out = append(out, Advice{
					Kind:       AdviceTime,
					Direction:  DirDecrease,
					Headline:   "缩短萃取时间 2–4 秒",
					Rationale:  "意式的萃取时间与研磨度强耦合。若不想动磨豆机刻度，提前断流也能压低萃取率，代价是流速曲线会偏离原本的设计。",
					TargetText: "",
					Priority:   4,
				})
			} else {
				out = append(out, Advice{
					Kind:       AdviceTechnique,
					Direction:  DirDecrease,
					Headline:   "减少搅拌与断水次数",
					Rationale:  "每一次搅拌都会刷新粉层表面的浓度梯度，加速可溶物溶出。过萃时先把手法简化，往往比动研磨度更快见效。",
					TargetText: "",
					Priority:   4,
				})
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out
}

// grindNotches 估算研磨度需要调整的档数。
//
// 经验依据：在多数手摇与电动磨豆机上，一档研磨度变化对应约 0.7–1.0 个百分点的
// 萃取率变化（不同磨盘差异很大，锥刀普遍更钝感）。此处取 0.8 个百分点/档作为
// 折中，并把结果收敛在 1–4 档之间。
//
// 为何要收敛上界：算出"调细 9 档"这类建议是没有意义的 —— 偏离这么大时
// 一次调到位会直接跨过金杯区间落到另一侧。分次逼近才是可行的操作路径。
func grindNotches(p Profile, yield fixed.Ratio) int {
	var gap fixed.Ratio
	switch {
	case yield < p.YieldMin:
		gap = p.YieldMin - yield
	case yield > p.YieldMax:
		gap = yield - p.YieldMax
	default:
		return 0
	}

	perNotch := fixed.MustRatioPercent("0.8")
	n := int(gap / perNotch)
	// 只要落在区间外就至少调 1 档，否则建议等于"什么都不做"
	if n < 1 {
		n = 1
	}
	if n > 4 {
		n = 4
	}
	return n
}

func notchLabel(n int) string {
	if n <= 0 {
		return "0 档"
	}
	if n >= 4 {
		return "4 档以上（建议分两次逼近，一次调到位容易直接跨到另一侧）"
	}
	return itoa(n) + " 档"
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

func absMass(m fixed.Mass) fixed.Mass {
	if m < 0 {
		return -m
	}
	return m
}

func absRatio(r fixed.Ratio) fixed.Ratio {
	if r < 0 {
		return -r
	}
	return r
}
