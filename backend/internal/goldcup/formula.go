package goldcup

import (
	"math/big"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

// 本文件是金杯引擎的纯函数公式层。
//
// 全部计算在 math/big.Rat 上完成，仅在返回前量化一次为定点数。
// "只量化一次"不是风格偏好：中间量化的误差会被后续的除法放大，
// fixed 包的 TestChainedQuantizationIsLossy 记录了具体幅度。
//
// 本文件不出现任何 float64 参与的运算，这条约束由本包的
// TestFormulaLayerHasNoFloat 用 AST 扫描把守，而不是靠人工 review 记得住。

// 物理边界常量。这些不是防御性的随手一写，每个都对应一个真实的错误输入模式。
var (
	// 粉量下限 0.1g：低于此值的输入几乎总是把单位当成了千克或漏填。
	minDose = fixed.Mass(100)
	// 粉量上限 5kg：家用与独立店的单次冲煮不可能超过，超出即单位错误。
	maxDose = fixed.Mass(5_000_000)
	// 液重上限 50kg：同上。
	maxBeverage = fixed.Mass(50_000_000)
	// TDS 上限 30%：咖啡豆可溶物总量约 28%–30%，浓度不可能超过它。
	maxTDS = fixed.MustRatioPercent("30")
	// 萃取率上限 30%：同上，这是物理天花板而非口味偏好。
	maxYield = fixed.MustRatioPercent("30")
)

// BeverageMass 推导饮品液重（即真正进入杯中的液体质量）。
//
// 这一步是手冲萃取率计算里最容易被忽略的环节，也是新手算错萃取率的首因：
// 手冲的液重无法直接称量 —— 一部分水永久留在湿粉层和滤纸里。若直接拿总注水量
// 当液重，萃取率会被系统性高估约 12%–15%（相对值），足以把一杯欠萃的咖啡
// 误判进金杯区间。
//
//	手冲：液重 = 总注水量 − 粉量 × LRR
//	意式：液重 = 实测出液重量（直接称量，无需推导）
//
// 当手冲也提供了实测液重时（有人会称量滤杯前后重量差），优先采用实测值，
// 因为它绕过了 LRR 这个经验系数带来的不确定性。
func BeverageMass(p Profile, dose, totalWater, measuredBeverage fixed.Mass, lrr fixed.Ratio) (fixed.Mass, error) {
	if measuredBeverage > 0 {
		if measuredBeverage > maxBeverage {
			return 0, domain.Validation("BEVERAGE_OUT_OF_RANGE", "液重超出合理范围").
				WithField("beverage_mass_g", "应小于 50000g")
		}
		return measuredBeverage, nil
	}

	if !p.UsesLRR {
		// 意式必须提供实测出液重量：粉饼的持水量受压力、粉量、研磨度多因素影响，
		// 用固定系数推导的误差会大于萃取率判定本身的分辨率。宁可拒绝计算，
		// 也不返回一个看似精确实则无意义的数字。
		return 0, domain.Precondition("ESPRESSO_REQUIRES_BEVERAGE_MASS",
			"意式浓缩必须提供实测出液重量（粉饼持水量无法用固定系数可靠推导）")
	}

	if totalWater <= 0 {
		return 0, domain.Validation("MISSING_TOTAL_WATER", "手冲需要提供总注水量或实测液重").
			WithField("total_water_g", "必须为正数")
	}

	effectiveLRR := lrr
	if effectiveLRR <= 0 {
		effectiveLRR = p.LRR
	}

	absorbed, err := fixed.MulMassRatio(dose, effectiveLRR)
	if err != nil {
		return 0, domain.Computation("LRR_OVERFLOW", "持水量计算溢出").WithCause(err)
	}

	bev, err := fixed.SubMass(totalWater, absorbed)
	if err != nil {
		return 0, domain.Computation("BEVERAGE_OVERFLOW", "液重计算溢出").WithCause(err)
	}

	if bev <= 0 {
		// 注水量还不够被粉吸干，说明用户把粉量和水量填反了，或水量填成了粉量的单位。
		// 这是高频输入错误，值得给出明确的诊断而非一个负数。
		return 0, domain.Validation("WATER_FULLY_ABSORBED",
			"总注水量不足以产生液体：按持水系数 "+effectiveLRR.Multiple()+
				" 计算，"+dose.Grams()+"g 粉会吸走 "+absorbed.Grams()+"g 水，"+
				"而总注水量仅 "+totalWater.Grams()+"g。请检查粉量与水量是否填反。").
			WithField("total_water_g", "必须大于 粉量 × 持水系数")
	}

	return bev, nil
}

// ExtractionYield 计算萃取率（Extraction Yield）。
//
//	EY = (液重 × TDS) / 粉量
//
// 物理含义：溶解进液体的咖啡固体质量，占干粉质量的比例。
// 分子 液重×TDS 就是杯中溶解物的绝对质量（克），除以粉量得到"这些粉里有多少
// 被萃取出来了"。
//
// 实现要点：在 PPM 标度下，(mg × ppm) / mg 直接就是 ppm 量纲，
// 无需额外的标度换算，也就少了一次量化舍入。
func ExtractionYield(beverage fixed.Mass, tds fixed.Ratio, dose fixed.Mass) (fixed.Ratio, error) {
	if dose <= 0 {
		return 0, domain.Validation("ZERO_DOSE", "粉量必须为正数，否则萃取率无定义").
			WithField("dose_g", "必须大于 0")
	}
	if tds <= 0 {
		return 0, domain.Precondition("MISSING_TDS",
			"未提供 TDS，无法计算真实萃取率（需折射仪测量值）")
	}

	// EY_ppm = beverage_mg * tds_ppm / dose_mg，全程精确有理数
	numerator := new(big.Rat).Mul(beverage.RawRat(), tds.RawRat())
	quotient := new(big.Rat).Quo(numerator, dose.RawRat())

	ey, err := fixed.RatioFromPPMRat(quotient)
	if err != nil {
		return 0, domain.Computation("YIELD_OVERFLOW", "萃取率计算溢出").WithCause(err)
	}
	return ey, nil
}

// BrewRatio 计算粉液比（液重 ÷ 粉量），返回值语义为"1 份粉对应几份液"。
// 1:16 的手冲返回 Ratio(16_000_000)。
func BrewRatio(beverage, dose fixed.Mass) (fixed.Ratio, error) {
	r, err := fixed.DivMass(beverage, dose)
	if err != nil {
		return 0, domain.Computation("RATIO_ERROR", "粉液比计算失败").WithCause(err)
	}
	return r, nil
}

// SolveTDS 反解：已知目标萃取率、液重、粉量，求需要达到的 TDS。
//
//	TDS = EY × 粉量 / 液重
//
// 用途：沙盘的"目标反推"功能 —— 用户说"我想让这支豆萃到 20%"，
// 引擎告诉他折射仪应该读到多少，从而在冲煮中途就能判断进度。
func SolveTDS(targetYield fixed.Ratio, dose, beverage fixed.Mass) (fixed.Ratio, error) {
	if beverage <= 0 {
		return 0, domain.Validation("ZERO_BEVERAGE", "液重必须为正数").
			WithField("beverage_mass_g", "必须大于 0")
	}
	if targetYield <= 0 || targetYield > maxYield {
		return 0, domain.Validation("YIELD_OUT_OF_RANGE",
			"目标萃取率应落在 0% 与 30% 之间（30% 是咖啡豆可溶物总量上限）")
	}

	// TDS_ppm = EY_ppm * dose_mg / beverage_mg
	numerator := new(big.Rat).Mul(targetYield.RawRat(), dose.RawRat())
	quotient := new(big.Rat).Quo(numerator, beverage.RawRat())

	tds, err := fixed.RatioFromPPMRat(quotient)
	if err != nil {
		return 0, domain.Computation("TDS_OVERFLOW", "TDS 反解溢出").WithCause(err)
	}
	return tds, nil
}

// SolveBeverage 反解：已知目标萃取率、实测 TDS、粉量，求应该接出多少液体。
//
//	液重 = EY × 粉量 / TDS
//
// 用途：这是沙盘里最实用的一条 —— 意式萃取过程中，用户测得当前 TDS 后，
// 引擎立刻告出"再接到 36.5g 停手就正好落在 20% 萃取率"。
func SolveBeverage(targetYield, tds fixed.Ratio, dose fixed.Mass) (fixed.Mass, error) {
	if tds <= 0 {
		return 0, domain.Precondition("MISSING_TDS", "反解液重需要实测 TDS")
	}
	if targetYield <= 0 || targetYield > maxYield {
		return 0, domain.Validation("YIELD_OUT_OF_RANGE", "目标萃取率应落在 0% 与 30% 之间")
	}
	if dose <= 0 {
		return 0, domain.Validation("ZERO_DOSE", "粉量必须为正数")
	}

	// beverage_mg = EY_ppm * dose_mg / TDS_ppm
	numerator := new(big.Rat).Mul(targetYield.RawRat(), dose.RawRat())
	quotient := new(big.Rat).Quo(numerator, tds.RawRat())

	bev, err := fixed.MassFromMilligramsRat(quotient)
	if err != nil {
		return 0, domain.Computation("BEVERAGE_OVERFLOW", "液重反解溢出").WithCause(err)
	}
	if bev > maxBeverage {
		return 0, domain.Validation("BEVERAGE_OUT_OF_RANGE",
			"反解得到的液重超出合理范围，请检查 TDS 与目标萃取率是否填错单位")
	}
	return bev, nil
}

// SolveDose 反解：已知目标萃取率、实测 TDS、液重，求应该用多少粉。
//
//	粉量 = 液重 × TDS / EY
//
// 用途：配方设计 —— "我要做一杯 250g 的手冲，落在 20% 萃取率、1.3% 浓度，该称多少粉"。
func SolveDose(targetYield, tds fixed.Ratio, beverage fixed.Mass) (fixed.Mass, error) {
	if targetYield <= 0 {
		return 0, domain.Validation("ZERO_TARGET_YIELD", "目标萃取率必须为正数")
	}
	// 上界和 SolveTDS / SolveBeverage 用的是同一条物理约束，但这里更容易漏：
	// 目标萃取率越离谱，反解出的粉量越**小**，于是下面的 maxDose 上限拦不住它。
	// 缺了这一行，请求 95% 萃取率会得到一句"称取 3.42g 粉"的确定答复 ——
	// 一个不可能达成的目标，被当成可执行的配方给了用户。
	if targetYield > maxYield {
		return 0, domain.Validation("YIELD_OUT_OF_RANGE",
			"目标萃取率应落在 0% 与 30% 之间（30% 是咖啡豆可溶物总量上限）").
			WithField("target_yield_percent", "收到 "+targetYield.Percent()+"%")
	}
	if tds <= 0 {
		return 0, domain.Precondition("MISSING_TDS", "反解粉量需要目标 TDS")
	}
	if beverage <= 0 {
		return 0, domain.Validation("ZERO_BEVERAGE", "液重必须为正数")
	}

	// dose_mg = beverage_mg * TDS_ppm / EY_ppm
	numerator := new(big.Rat).Mul(beverage.RawRat(), tds.RawRat())
	quotient := new(big.Rat).Quo(numerator, targetYield.RawRat())

	dose, err := fixed.MassFromMilligramsRat(quotient)
	if err != nil {
		return 0, domain.Computation("DOSE_OVERFLOW", "粉量反解溢出").WithCause(err)
	}
	if dose > maxDose {
		return 0, domain.Validation("DOSE_OUT_OF_RANGE",
			"反解得到的粉量超出合理范围，请检查输入单位")
	}
	return dose, nil
}

// SolveTotalWater 反解：已知目标液重与粉量，求手冲需要注入的总水量。
//
//	总注水量 = 液重 + 粉量 × LRR
//
// 这是 BeverageMass 的逆运算，用于把"我要接 250g 咖啡液"翻译成
// "你得注 286g 水"这种可执行的操作指令。
func SolveTotalWater(p Profile, targetBeverage, dose fixed.Mass, lrr fixed.Ratio) (fixed.Mass, error) {
	if !p.UsesLRR {
		return 0, domain.Precondition("NOT_APPLICABLE",
			"意式浓缩不经过总注水量推导，直接称量出液重即可")
	}
	if targetBeverage <= 0 || dose <= 0 {
		return 0, domain.Validation("INVALID_INPUT", "目标液重与粉量都必须为正数")
	}

	effectiveLRR := lrr
	if effectiveLRR <= 0 {
		effectiveLRR = p.LRR
	}

	absorbed, err := fixed.MulMassRatio(dose, effectiveLRR)
	if err != nil {
		return 0, domain.Computation("LRR_OVERFLOW", "持水量计算溢出").WithCause(err)
	}

	total, err := fixed.AddMass(targetBeverage, absorbed)
	if err != nil {
		return 0, domain.Computation("WATER_OVERFLOW", "总注水量计算溢出").WithCause(err)
	}
	return total, nil
}

// validatePhysicalInput 在进入任何除法之前拦截非法量值。
//
// 把校验放在运算之前而不是事后检查 NaN：本包用的是有理数，除零会直接 panic
// 而不是产生 NaN，所以"事后检查"这条路根本不存在。必须前置拦截。
func validatePhysicalInput(dose, totalWater, beverage fixed.Mass, tds fixed.Ratio) error {
	e := domain.Validation("INVALID_BREW_INPUT", "萃取参数超出物理合理范围")
	bad := false

	switch {
	case dose <= 0:
		e.WithField("dose_g", "粉量必须为正数")
		bad = true
	case dose < minDose:
		e.WithField("dose_g", "粉量小于 0.1g，请确认单位是克")
		bad = true
	case dose > maxDose:
		e.WithField("dose_g", "粉量超过 5000g，请确认单位是克")
		bad = true
	}

	if totalWater < 0 {
		e.WithField("total_water_g", "总注水量不能为负")
		bad = true
	}
	if totalWater > maxBeverage {
		e.WithField("total_water_g", "总注水量超过 50000g，请确认单位")
		bad = true
	}
	if beverage < 0 {
		e.WithField("beverage_mass_g", "液重不能为负")
		bad = true
	}
	if beverage > maxBeverage {
		e.WithField("beverage_mass_g", "液重超过 50000g，请确认单位")
		bad = true
	}

	// TDS 为 0 是合法的（代表"未提供"，走推算模式），但负值与超物理上限不合法
	if tds < 0 {
		e.WithField("tds_percent", "TDS 不能为负")
		bad = true
	}
	if tds > maxTDS {
		e.WithField("tds_percent",
			"TDS 超过 30%，已超出咖啡豆可溶物总量上限。若你填的是 12 而非 12%，请注意本字段单位是百分数")
		bad = true
	}

	if bad {
		return e
	}
	return nil
}
