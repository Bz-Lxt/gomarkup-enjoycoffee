package goldcup

import (
	"math/big"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

// Sample 是一条历史测量记录，作为推算模式的训练数据。
// 只有 MEASURED 模式产生的记录才有资格成为 Sample —— 用推算结果去训练推算模型
// 会导致误差自我放大，这条边界必须由调用方（仓储层查询）把守。
type Sample struct {
	// BrewID 让调用方能在更新场景下把"待重算的那条记录"从训练集中排除，
	// 否则会用一条记录的旧值来推算它自己的新值，形成自我强化的循环引用。
	BrewID         int64
	Yield          fixed.Ratio
	TDS            fixed.Ratio
	Dose           fixed.Mass
	Beverage       fixed.Mass
	GrindMicron    int
	WaterTempC     int
	ContactSeconds int
	AgitationCount int
}

// EstimatorKind 标识推算所用的模型。
type EstimatorKind string

const (
	// EstimatorHistory 基于用户自己的历史测量样本做过原点最小二乘回归。
	EstimatorHistory EstimatorKind = "HISTORY_REGRESSION"
	// EstimatorKinetic 历史样本不足时回落到萃取动力学经验先验。
	EstimatorKinetic EstimatorKind = "KINETIC_PRIOR"
)

// ConfidenceTier 是置信度分档，供前端决定渲染强度。
type ConfidenceTier string

const (
	ConfHigh   ConfidenceTier = "HIGH"
	ConfMedium ConfidenceTier = "MEDIUM"
	ConfLow    ConfidenceTier = "LOW"
)

// Estimation 是推算模式的输出，携带完整的不确定性描述。
type Estimation struct {
	Estimator      EstimatorKind  `json:"estimator"`
	EstimatorLabel string         `json:"estimator_label"`
	SampleSize     int            `json:"sample_size"`
	Confidence     float64        `json:"confidence"`
	ConfidenceTier ConfidenceTier `json:"confidence_tier"`

	YieldPercent      float64 `json:"yield_percent"`
	YieldLowerPercent float64 `json:"yield_lower_percent"`
	YieldUpperPercent float64 `json:"yield_upper_percent"`
	YieldRangeText    string  `json:"yield_range_text"`
	TDSPercent        float64 `json:"tds_percent"`

	// Basis 逐条列出推导依据，是"可解释输出"承诺的落地点。
	Basis []string `json:"basis"`
	// Disclaimer 是必须展示给用户的免责说明。
	Disclaimer string `json:"disclaimer"`

	RawYield      fixed.Ratio `json:"-"`
	RawTDS        fixed.Ratio `json:"-"`
	RawYieldLower fixed.Ratio `json:"-"`
	RawYieldUpper fixed.Ratio `json:"-"`
}

// minRegressionSamples 是启用历史回归所需的最小样本数。
//
// 为何是 3：过原点单参数回归在理论上 1 个点就能定斜率，但 1–2 个点无法估计
// 残差离散度，也就无法给出诚实的置信区间。3 个点是"能算出离散度"的最小值。
// 低于此数一律回落到动力学先验并明确标记低置信度。
const minRegressionSamples = 3

// 萃取动力学经验系数。
//
// 重要声明：以下系数是经验先验，不是从第一性原理推导的物理常数。它们的来源是
// 公开的冲煮研究与从业者共识的中位数，实际值随磨盘类型、滤杯几何、水质硬度
// 大幅浮动。因此使用这些系数的路径（EstimatorKinetic）一律标记为低置信度，
// 并给出 ±3 个百分点的宽区间 —— 宽到足以诚实地表达"我们其实不太确定"。
//
// 一旦用户积累了 3 条以上实测记录，引擎立刻切换到基于其自身设备的回归，
// 这些先验就退居为兜底。这是本模块的核心设计意图：先验只用来填补冷启动，
// 不用来长期替代测量。
var (
	// 研磨度：每微米粒径变化带来的萃取率变化。磨细（微米数减小）提升萃取率。
	// 意式的敏感度显著高于手冲 —— 250µm 附近粒径每变化 10µm，
	// 流速与接触时间都会明显改变，双重作用叠加。
	kGrindFilter   = fixed.MustRatioPercent("0.012")
	kGrindEspresso = fixed.MustRatioPercent("0.040")

	// 水温：每摄氏度带来的萃取率变化。溶解速率随温度上升。
	kTempPerCelsius = fixed.MustRatioPercent("0.150")

	// 接触时间：每秒带来的萃取率变化。意式总时长仅 25–30 秒，
	// 单位时间的边际影响远大于三分钟的手冲。
	kTimeFilter   = fixed.MustRatioPercent("0.030")
	kTimeEspresso = fixed.MustRatioPercent("0.250")

	// 搅拌：每次搅拌或断水带来的萃取率变化。搅拌刷新粉层表面的浓度梯度，
	// 恢复溶出驱动力。
	kAgitationPerEvent = fixed.MustRatioPercent("0.300")

	// 粉液比：每增加 1 倍水量带来的萃取率变化。更多溶剂意味着更大的
	// 饱和容量，可溶物溶出得更彻底。
	kRatioPerUnit = fixed.MustRatioPercent("0.150")
)

// 参考工况：动力学先验的锚点。选取的是"教科书标准冲煮"，在此工况下
// 一支中烘豆的萃取率通常落在 20% 附近（金杯区间中心）。
type referencePoint struct {
	yield          fixed.Ratio
	grindMicron    int
	waterTempC     int
	contactSeconds int
	agitationCount int
	brewRatio      fixed.Ratio
}

var (
	filterReference = referencePoint{
		yield:          fixed.MustRatioPercent("20"),
		grindMicron:    700, // 手冲典型中度研磨
		waterTempC:     93,
		contactSeconds: 180, // 3 分钟
		agitationCount: 2,   // 闷蒸搅拌 + 一次中段搅拌
		brewRatio:      fixed.MustRatioMultiple("16"),
	}
	espressoReference = referencePoint{
		yield:          fixed.MustRatioPercent("20"),
		grindMicron:    250,
		waterTempC:     93,
		contactSeconds: 27,
		agitationCount: 0,
		brewRatio:      fixed.MustRatioMultiple("2"),
	}
)

func referenceFor(m domain.BrewMethod) referencePoint {
	if m == domain.MethodEspresso {
		return espressoReference
	}
	return filterReference
}

func grindCoefficient(m domain.BrewMethod) fixed.Ratio {
	if m == domain.MethodEspresso {
		return kGrindEspresso
	}
	return kGrindFilter
}

func timeCoefficient(m domain.BrewMethod) fixed.Ratio {
	if m == domain.MethodEspresso {
		return kTimeEspresso
	}
	return kTimeFilter
}

// estimate 在缺少实测 TDS 时推断萃取率与浓度。
//
// 推导链条（这是本函数的核心，也是它区别于"编个数"的地方）：
//
// 第一步，把待求量从"萃取率"改写为"TDS"。由定义式 EY = 液重×TDS/粉量 可知，
// 一旦知道 TDS，萃取率就是精确的代数结果 —— 不需要任何近似。所以真正需要
// 推断的只有 TDS 这一个量，推断完之后仍然走同一条精确公式。这一步把
// 建模误差限制在单个变量上。
//
// 第二步，利用同一定义式的另一种读法：TDS = EY × (粉量/液重)。
// 若某支豆在某组研磨/水温/时间下的萃取率大致稳定，那么 TDS 就与
// 粉液比的倒数成正比，且这条直线必须过原点（粉量为零时浓度为零，
// 这是物理约束而非统计假设）。因此对历史样本做「过原点最小二乘」拟合
// TDS ~ (粉量/液重)，得到的斜率就是该豆在该设备上的典型萃取率。
//
// 第三步，用动力学系数修正本次与历史均值在研磨度、水温、时间、搅拌上的偏差。
//
// 第四步，用历史残差的平均绝对偏差给出区间，而非给一个假装精确的点值。
func estimate(p Profile, in Input, bev fixed.Mass, ratio fixed.Ratio, samples []Sample) (*Estimation, error) {
	if in.Dose <= 0 || bev <= 0 {
		return nil, domain.Validation("INVALID_INPUT_FOR_ESTIMATE",
			"推算萃取率需要有效的粉量与液重")
	}

	usable := filterUsableSamples(samples)
	est := &Estimation{
		SampleSize: len(usable),
		Basis:      []string{},
	}

	var baseYield fixed.Ratio
	var spread fixed.Ratio // 平均绝对偏差，作为区间半宽的基础
	var extrapolation fixed.Ratio

	if len(usable) >= minRegressionSamples {
		est.Estimator = EstimatorHistory
		est.EstimatorLabel = "基于你自己的 " + itoa(len(usable)) + " 条实测记录回归"

		slope, mad, err := fitThroughOrigin(usable)
		if err != nil {
			return nil, err
		}
		baseYield = slope
		spread = mad

		est.Basis = append(est.Basis,
			"对同豆同冲煮法的 "+itoa(len(usable))+" 条实测记录做过原点最小二乘拟合 "+
				"TDS ~ 粉量/液重，斜率即为你这套设备上的典型萃取率 "+slope.Percent()+"%。"+
				"「过原点」不是统计假设而是物理约束：粉量为零时浓度必然为零。")
		est.Basis = append(est.Basis,
			"历史样本萃取率的平均绝对偏差为 "+mad.Percent()+" 个百分点，"+
				"它决定了下面给出的区间宽度。")

		delta, notes := kineticDelta(p.Method, in, ratio, sampleCentroid(usable))
		if delta != 0 {
			adjusted := baseYield + delta
			est.Basis = append(est.Basis,
				"本次参数与历史均值存在差异，按萃取动力学系数修正 "+signedPercent(delta)+
					" 个百分点，得到 "+adjusted.Percent()+"%。")
			baseYield = adjusted
			extrapolation = absRatio(delta)
		}
		est.Basis = append(est.Basis, notes...)
	} else {
		est.Estimator = EstimatorKinetic
		if len(usable) == 0 {
			est.EstimatorLabel = "萃取动力学经验先验（暂无实测记录）"
			est.Basis = append(est.Basis,
				"这支豆还没有任何带 TDS 的实测记录，因此只能用行业经验先验推算。"+
					"积累 "+itoa(minRegressionSamples)+" 条实测记录后，引擎会自动切换到"+
					"基于你自己设备的回归模型，准确度会有实质提升。")
		} else {
			est.EstimatorLabel = "萃取动力学经验先验（实测记录仅 " + itoa(len(usable)) + " 条，不足以回归）"
			est.Basis = append(est.Basis,
				"现有 "+itoa(len(usable))+" 条实测记录不足 "+itoa(minRegressionSamples)+
					" 条，无法估计残差离散度，因此暂不启用回归。"+
					"单凭一两个点拟合出的斜率会给出虚假的精确感。")
		}

		ref := referenceFor(p.Method)
		baseYield = ref.yield
		delta, notes := kineticDeltaFromReference(p.Method, in, ratio, ref)
		baseYield += delta
		extrapolation = absRatio(delta)

		est.Basis = append(est.Basis,
			"以标准工况（研磨 "+itoa(ref.grindMicron)+"µm、水温 "+itoa(ref.waterTempC)+
				"℃、接触 "+itoa(ref.contactSeconds)+"s、粉液比 "+ref.brewRatio.BrewRatioLabel()+
				"）下的 "+ref.yield.Percent()+"% 为锚点，按本次参数偏差修正 "+
				signedPercent(delta)+" 个百分点。")
		est.Basis = append(est.Basis, notes...)

		// 经验先验的固有不确定性。设为 1.5pp 是有意的保守选择：
		// 它使得区间半宽（1.5×2 + 外推项）几乎必然横跨金杯区间的边界，
		// 从而在视觉上就让用户明白"这个推算不足以判定是否合格"。
		spread = fixed.MustRatioPercent("1.5")
	}

	if baseYield <= 0 {
		return nil, domain.Computation("ESTIMATE_NONPOSITIVE",
			"推算得到的萃取率非正，通常意味着输入参数组合在物理上不成立")
	}
	if baseYield > maxYield {
		baseYield = maxYield
	}

	// 区间半宽 = 2 × 离散度 + 外推惩罚。
	// 外推惩罚的含义：本次参数离历史越远，动力学系数的线性假设越不可靠，
	// 因此把外推幅度的一半计入不确定性。
	halfWidth := spread*2 + extrapolation/2
	if minHW := fixed.MustRatioPercent("0.3"); halfWidth < minHW {
		// 即使历史极其一致，也不给出比 ±0.3pp 更窄的区间。
		// 折射仪自身的测量重复性就在这个量级，声称比仪器更准是不诚实的。
		halfWidth = minHW
	}

	lower := baseYield - halfWidth
	if lower < 0 {
		lower = 0
	}
	upper := fixed.Clamp(baseYield+halfWidth, 0, maxYield)

	// 由推算出的萃取率反解 TDS，走的仍是精确公式
	tds, err := SolveTDS(baseYield, in.Dose, bev)
	if err != nil {
		return nil, err
	}

	est.RawYield = baseYield
	est.RawTDS = tds
	est.RawYieldLower = lower
	est.RawYieldUpper = upper
	est.YieldPercent = baseYield.ApproxPercentFloat()
	est.YieldLowerPercent = lower.ApproxPercentFloat()
	est.YieldUpperPercent = upper.ApproxPercentFloat()
	est.YieldRangeText = lower.Percent() + "% ~ " + upper.Percent() + "%"
	est.TDSPercent = tds.ApproxPercentFloat()

	conf, tier := scoreConfidence(est.Estimator, len(usable), spread)
	est.Confidence = conf
	est.ConfidenceTier = tier

	est.Disclaimer = "本次未提供 TDS，萃取率 " + baseYield.Percent() +
		"% 与浓度 " + tds.Percent() + "% 均为统计推算值（可能区间 " + est.YieldRangeText +
		"），不能替代折射仪测量，也不构成金杯合格判定。" +
		"若要得到确定结论，请用折射仪测量 TDS 后重新计算。"

	return est, nil
}

// filterUsableSamples 剔除无法参与回归的样本。
func filterUsableSamples(samples []Sample) []Sample {
	out := make([]Sample, 0, len(samples))
	for _, s := range samples {
		if s.Yield > 0 && s.TDS > 0 && s.Dose > 0 && s.Beverage > 0 && s.Yield <= maxYield {
			out = append(out, s)
		}
	}
	return out
}

// fitThroughOrigin 对样本做过原点最小二乘拟合 TDS = b · (粉量/液重)，返回斜率与残差平均绝对偏差。
//
// 过原点最小二乘的闭式解：b = Σ(xᵢyᵢ) / Σ(xᵢ²)
//
// 由于每条样本都精确满足 yᵢ = EYᵢ·xᵢ，代入后得 b = Σ(EYᵢ·xᵢ²) / Σ(xᵢ²) ——
// 也就是以 xᵢ² 为权重的历史萃取率加权平均。这个权重不是人为设定的：
// 它是最小二乘准则的自然产物，效果是让粉液比更小（更浓、TDS 读数信噪比更高）
// 的样本获得更大话语权，这恰好符合"浓样本的 TDS 测量更可靠"的实际情况。
//
// 全程 big.Rat，无浮点。
func fitThroughOrigin(samples []Sample) (slope, mad fixed.Ratio, err error) {
	sumXY := new(big.Rat)
	sumXX := new(big.Rat)

	for _, s := range samples {
		// x = 粉量/液重（无量纲），y = TDS（PPM 标度的有理数）
		x := new(big.Rat).Quo(s.Dose.RawRat(), s.Beverage.RawRat())
		y := s.TDS.RawRat()

		sumXY.Add(sumXY, new(big.Rat).Mul(x, y))
		sumXX.Add(sumXX, new(big.Rat).Mul(x, x))
	}

	if sumXX.Sign() == 0 {
		return 0, 0, domain.Computation("DEGENERATE_REGRESSION",
			"历史样本的粉液比全为零，回归退化")
	}

	b := new(big.Rat).Quo(sumXY, sumXX)
	slope, err = fixed.RatioFromPPMRat(b)
	if err != nil {
		return 0, 0, domain.Computation("REGRESSION_OVERFLOW", "回归斜率溢出").WithCause(err)
	}

	// 残差的平均绝对偏差。用 MAD 而非标准差是刻意选择：
	// 标准差需要开平方，而平方根在有理数域内一般无解，会迫使引入浮点。
	// MAD 在有理数域内封闭，且在小样本（n=3~5）下对离群点更稳健。
	absSum := new(big.Rat)
	for _, s := range samples {
		diff := new(big.Rat).Sub(s.Yield.RawRat(), b)
		absSum.Add(absSum, new(big.Rat).Abs(diff))
	}
	meanAbs := new(big.Rat).Quo(absSum, new(big.Rat).SetInt64(int64(len(samples))))
	mad, err = fixed.RatioFromPPMRat(meanAbs)
	if err != nil {
		return 0, 0, domain.Computation("MAD_OVERFLOW", "残差离散度计算溢出").WithCause(err)
	}

	return slope, mad, nil
}

// centroid 是历史样本在动力学特征上的均值，作为动力学修正的基准点。
type centroid struct {
	grindMicron    int
	waterTempC     int
	contactSeconds int
	agitationCount int
	brewRatio      fixed.Ratio
}

// sampleCentroid 计算历史样本的特征均值。
//
// 每个特征独立统计有效样本数：用户可能只在部分记录里填了水温，
// 不该因为水温缺失就丢掉整条记录的研磨度信息。
func sampleCentroid(samples []Sample) centroid {
	var c centroid
	var nGrind, nTemp, nTime, nAgit, nRatio int
	var sumGrind, sumTemp, sumTime, sumAgit int
	sumRatio := new(big.Rat)

	for _, s := range samples {
		if s.GrindMicron > 0 {
			sumGrind += s.GrindMicron
			nGrind++
		}
		if s.WaterTempC > 0 {
			sumTemp += s.WaterTempC
			nTemp++
		}
		if s.ContactSeconds > 0 {
			sumTime += s.ContactSeconds
			nTime++
		}
		if s.AgitationCount > 0 {
			sumAgit += s.AgitationCount
			nAgit++
		}
		if s.Dose > 0 && s.Beverage > 0 {
			sumRatio.Add(sumRatio, new(big.Rat).Quo(s.Beverage.RawRat(), s.Dose.RawRat()))
			nRatio++
		}
	}

	if nGrind > 0 {
		c.grindMicron = sumGrind / nGrind
	}
	if nTemp > 0 {
		c.waterTempC = sumTemp / nTemp
	}
	if nTime > 0 {
		c.contactSeconds = sumTime / nTime
	}
	if nAgit > 0 {
		c.agitationCount = sumAgit / nAgit
	}
	if nRatio > 0 {
		avg := new(big.Rat).Quo(sumRatio, new(big.Rat).SetInt64(int64(nRatio)))
		if r, err := fixed.RatioFromRat(avg); err == nil {
			c.brewRatio = r
		}
	}
	return c
}

// kineticDelta 计算本次参数相对历史均值的萃取率修正量。
//
// 只对"本次与基准都已知"的特征做修正。缺失特征一律跳过而非填默认值 ——
// 用臆测的默认值去修正，会把一个不确定性伪装成一次精确调整。
func kineticDelta(m domain.BrewMethod, in Input, ratio fixed.Ratio, c centroid) (fixed.Ratio, []string) {
	var total fixed.Ratio
	notes := []string{}

	if in.GrindMicron > 0 && c.grindMicron > 0 && in.GrindMicron != c.grindMicron {
		// 磨细（微米数减小）提升萃取率，故用 基准 − 本次
		d := applyCoefficient(grindCoefficient(m), c.grindMicron-in.GrindMicron)
		total += d
		notes = append(notes, "研磨度 "+itoa(in.GrindMicron)+"µm 相对历史均值 "+
			itoa(c.grindMicron)+"µm "+finerOrCoarser(in.GrindMicron, c.grindMicron)+
			"，贡献 "+signedPercent(d)+" 个百分点。")
	}

	if in.WaterTempC > 0 && c.waterTempC > 0 && in.WaterTempC != c.waterTempC {
		d := applyCoefficient(kTempPerCelsius, in.WaterTempC-c.waterTempC)
		total += d
		notes = append(notes, "水温 "+itoa(in.WaterTempC)+"℃ 相对历史均值 "+
			itoa(c.waterTempC)+"℃，贡献 "+signedPercent(d)+" 个百分点。")
	}

	if in.ContactSeconds > 0 && c.contactSeconds > 0 && in.ContactSeconds != c.contactSeconds {
		d := applyCoefficient(timeCoefficient(m), in.ContactSeconds-c.contactSeconds)
		total += d
		notes = append(notes, "接触时间 "+itoa(in.ContactSeconds)+"s 相对历史均值 "+
			itoa(c.contactSeconds)+"s，贡献 "+signedPercent(d)+" 个百分点。")
	}

	if in.AgitationCount != c.agitationCount && (in.AgitationCount > 0 || c.agitationCount > 0) {
		d := applyCoefficient(kAgitationPerEvent, in.AgitationCount-c.agitationCount)
		total += d
		notes = append(notes, "搅拌次数 "+itoa(in.AgitationCount)+" 相对历史均值 "+
			itoa(c.agitationCount)+"，贡献 "+signedPercent(d)+" 个百分点。")
	}

	if ratio > 0 && c.brewRatio > 0 && ratio != c.brewRatio {
		d := applyRatioCoefficient(kRatioPerUnit, ratio-c.brewRatio)
		total += d
		notes = append(notes, "粉液比 "+ratio.BrewRatioLabel()+" 相对历史均值 "+
			c.brewRatio.BrewRatioLabel()+"，贡献 "+signedPercent(d)+" 个百分点。")
	}

	return total, notes
}

// kineticDeltaFromReference 与 kineticDelta 同构，但基准是标准工况而非历史均值。
func kineticDeltaFromReference(m domain.BrewMethod, in Input, ratio fixed.Ratio, ref referencePoint) (fixed.Ratio, []string) {
	return kineticDelta(m, in, ratio, centroid{
		grindMicron:    ref.grindMicron,
		waterTempC:     ref.waterTempC,
		contactSeconds: ref.contactSeconds,
		agitationCount: ref.agitationCount,
		brewRatio:      ref.brewRatio,
	})
}

// applyCoefficient 计算 系数 × 整数偏差，结果为萃取率修正量。
func applyCoefficient(k fixed.Ratio, delta int) fixed.Ratio {
	product := new(big.Rat).Mul(k.RawRat(), new(big.Rat).SetInt64(int64(delta)))
	v, err := fixed.RatioFromPPMRat(product)
	if err != nil {
		return 0
	}
	return v
}

// applyRatioCoefficient 计算 系数 × 比值偏差。
func applyRatioCoefficient(k, delta fixed.Ratio) fixed.Ratio {
	// delta 是倍数量纲（如粉液比差 1.5），需先降回无量纲再乘系数
	product := new(big.Rat).Mul(k.RawRat(), delta.Rat())
	v, err := fixed.RatioFromPPMRat(product)
	if err != nil {
		return 0
	}
	return v
}

func finerOrCoarser(now, base int) string {
	if now < base {
		return "更细"
	}
	return "更粗"
}

// scoreConfidence 把样本量与离散度映射为置信度。
//
// 采用离散分档而非连续函数：连续函数会给出 0.6231 这类看似精确的数字，
// 而它的第三位小数没有任何实际含义。分档表达的是"我们对这个推断有多少把握"
// 这个本身就粗粒度的判断，也更容易向用户解释。
func scoreConfidence(kind EstimatorKind, n int, spread fixed.Ratio) (float64, ConfidenceTier) {
	if kind == EstimatorKinetic {
		// 经验先验永远是低置信度，无论输入多完整。
		// 它描述的是"一般人的一般设备"，而用户关心的是自己的那台。
		return 0.25, ConfLow
	}

	var base float64
	switch {
	case n >= 10:
		base = 0.90
	case n >= 6:
		base = 0.75
	default:
		base = 0.55
	}

	// 离散度惩罚：历史样本本身就散乱时，回归斜率的可信度随之下降
	switch {
	case spread <= fixed.MustRatioPercent("0.5"):
		// 保持
	case spread <= fixed.MustRatioPercent("1.0"):
		base *= 0.85
	case spread <= fixed.MustRatioPercent("2.0"):
		base *= 0.65
	default:
		base *= 0.45
	}

	tier := ConfLow
	switch {
	case base >= 0.75:
		tier = ConfHigh
	case base >= 0.50:
		tier = ConfMedium
	}
	return base, tier
}
