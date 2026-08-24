package goldcup

import (
	"math/big"
	"sort"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

// 本文件是裁定 C-02 的落地：金杯曲线与控制图的全部几何量由后端算出并下发，
// 前端只负责把点集画成线。前端不做任何业务计算，因此不存在前后端数值不一致的可能。

// ScoredSample 是一条带风味评分的历史记录，用于推导个人偏好曲线。
type ScoredSample struct {
	BrewID   int64
	Yield    fixed.Ratio
	TDS      fixed.Ratio
	Ratio    fixed.Ratio
	Dose     fixed.Mass
	Beverage fixed.Mass
	Mode     Mode
	Label    string

	// TotalScore 是六维评分之和（0–60），乘以 100 存为整数以避免小数。
	// 为 0 表示这次冲煮没有评分。
	TotalScoreX100 int
	// SweetScoreX100 单独拎出甜感，因为它是判断萃取平衡度最灵敏的单一维度：
	// 欠萃时甜感被酸掩盖，过萃时被苦掩盖，只有萃取到位时甜感才立得住。
	SweetScoreX100 int
}

// ChartPoint 是控制图上的一个历史冲煮点。
type ChartPoint struct {
	BrewID        int64   `json:"brew_id"`
	Label         string  `json:"label"`
	YieldPercent  float64 `json:"yield_percent"`
	TDSPercent    float64 `json:"tds_percent"`
	BrewRatio     float64 `json:"brew_ratio"`
	BrewRatioText string  `json:"brew_ratio_text"`
	ZoneCode      string  `json:"zone_code"`
	ZoneLabel     string  `json:"zone_label"`
	InGoldCup     bool    `json:"in_gold_cup"`
	// Advisory 为真表示该点的萃取率是推算值，前端须以空心点或虚线边框渲染，
	// 与实测点在视觉上区分开。
	Advisory   bool    `json:"advisory"`
	TotalScore float64 `json:"total_score"`
	HasScore   bool    `json:"has_score"`
}

// IsoRatioLine 是控制图上的等粉液比参考线。
//
// 几何依据：在「萃取率 x 浓度」平面上，由 TDS = EY × 粉量/液重 = EY / R
// 可知固定粉液比 R 对应一条过原点、斜率为 1/R 的直线。
// 这些线是控制图最实用的部分 —— 它们把"改配比"这个操作在图上变成了
// 沿直线滑动，而"改研磨度"变成了跨越直线，两种调整的效果一目了然。
type IsoRatioLine struct {
	Ratio     float64 `json:"ratio"`
	Label     string  `json:"label"`
	X1        float64 `json:"x1"`
	Y1        float64 `json:"y1"`
	X2        float64 `json:"x2"`
	Y2        float64 `json:"y2"`
	Emphasize bool    `json:"emphasize"`
}

// Axis 描述一条坐标轴。
type Axis struct {
	Min   float64   `json:"min"`
	Max   float64   `json:"max"`
	Ticks []float64 `json:"ticks"`
	Label string    `json:"label"`
	Unit  string    `json:"unit"`
}

// ZoneRect 是九宫格中一格在图上的矩形范围。
type ZoneRect struct {
	Code        string  `json:"code"`
	Label       string  `json:"label"`
	Diagnosis   string  `json:"diagnosis"`
	SeverityHue string  `json:"severity_hue"`
	InGoldCup   bool    `json:"in_gold_cup"`
	XMin        float64 `json:"x_min"`
	XMax        float64 `json:"x_max"`
	YMin        float64 `json:"y_min"`
	YMax        float64 `json:"y_max"`
}

// Chart 是一张完整的冲煮控制图，包含前端绘制所需的全部几何量。
type Chart struct {
	Method    domain.BrewMethod `json:"method"`
	ChartKind string            `json:"chart_kind"`
	Title     string            `json:"title"`
	AxisX     Axis              `json:"axis_x"`
	AxisY     Axis              `json:"axis_y"`
	Zones     []ZoneRect        `json:"zones"`
	IsoRatios []IsoRatioLine    `json:"iso_ratios"`
	Points    []ChartPoint      `json:"points"`
	// 切片字段一律序列化为 []，禁止 omitempty：前端拿不到键会直接崩在
	// undefined.map 上，而一个空数组是完全可渲染的。
	Preference *PreferenceCurve `json:"preference_curve"`
}

// BuildChart 生成给定冲煮法的控制图几何与历史点集。
func BuildChart(p Profile, samples []ScoredSample) Chart {
	// 坐标轴范围：以金杯区间为核心向外扩张，保证边界外的点也画得进去，
	// 同时不至于把理想区压缩成一个看不清的小方块。
	xMin, xMax := axisSpan(p.YieldMin, p.YieldMax, fixed.MustRatioPercent("4"))
	yMin, yMax := axisSpan(p.StrengthMin, p.StrengthMax, strengthPadding(p))

	for _, s := range samples {
		if v := s.Yield.ApproxPercentFloat(); v < xMin {
			xMin = floorTo(v, 1)
		} else if v > xMax {
			xMax = ceilTo(v, 1)
		}
		if v := s.TDS.ApproxPercentFloat(); v < yMin {
			yMin = floorTo(v, strengthTickStep(p))
		} else if v > yMax {
			yMax = ceilTo(v, strengthTickStep(p))
		}
	}
	if xMin < 0 {
		xMin = 0
	}
	if yMin < 0 {
		yMin = 0
	}

	ch := Chart{
		Method:    p.Method,
		ChartKind: p.ChartKind,
		Title:     p.Label,
		AxisX: Axis{
			Min: xMin, Max: xMax,
			Ticks: buildTicks(xMin, xMax, 1),
			Label: "萃取率", Unit: "%",
		},
		AxisY: Axis{
			Min: yMin, Max: yMax,
			Ticks: buildTicks(yMin, yMax, strengthTickStep(p)),
			Label: "浓度 TDS", Unit: "%",
		},
		Zones:     buildZoneRects(p, xMin, xMax, yMin, yMax),
		IsoRatios: buildIsoRatios(p, xMin, xMax, yMin, yMax),
		Points:    make([]ChartPoint, 0, len(samples)),
	}

	for _, s := range samples {
		z := classify(p, s.Yield, s.TDS)
		advisory := s.Mode == ModeEstimated
		if advisory {
			z.InGoldCup = false
		}
		ch.Points = append(ch.Points, ChartPoint{
			BrewID:        s.BrewID,
			Label:         s.Label,
			YieldPercent:  s.Yield.ApproxPercentFloat(),
			TDSPercent:    s.TDS.ApproxPercentFloat(),
			BrewRatio:     s.Ratio.ApproxMultipleFloat(),
			BrewRatioText: s.Ratio.BrewRatioLabel(),
			ZoneCode:      z.Code,
			ZoneLabel:     z.Label,
			InGoldCup:     z.InGoldCup,
			Advisory:      advisory,
			TotalScore:    float64(s.TotalScoreX100) / 100,
			HasScore:      s.TotalScoreX100 > 0,
		})
	}

	ch.Preference = BuildPreferenceCurve(p, samples)
	return ch
}

// PreferenceCurve 是用户个人的「风味评分 ~ 萃取率」响应曲线。
//
// 这是本项目对"推导最优萃取金杯曲线"这句需求的实现，也是它区别于
// 一个通用萃取率计算器的地方：SCA 的 18%–22% 是跨人群的统计区间，
// 而每个人、每支豆的实际甜蜜点都不同。浅烘埃塞常在 21% 以上才出花香，
// 深烘拼配可能 18.5% 就已经足够甜。把用户自己的评分按萃取率分箱平均，
// 峰值所在的箱就是他自己的最优萃取率 —— 这个数字只有他自己的数据能给出。
type PreferenceCurve struct {
	// Available 为假时 Reason 说明为何无法推导，前端据此渲染引导文案而非空图。
	Available bool   `json:"available"`
	Reason    string `json:"reason"`

	Points []PreferencePoint `json:"points"`

	// PeakYieldPercent 是评分峰值所在的萃取率。
	PeakYieldPercent float64 `json:"peak_yield_percent"`
	PeakScore        float64 `json:"peak_score"`
	PeakLabel        string  `json:"peak_label"`
	// DeltaFromSCACenter 是个人最优点相对 SCA 区间中心的偏移（百分点）。
	DeltaFromSCACenter float64  `json:"delta_from_sca_center"`
	Insight            string   `json:"insight"`
	Basis              []string `json:"basis"`

	ScoredSampleCount int `json:"scored_sample_count"`
}

// PreferencePoint 是偏好曲线上的一个分箱。
type PreferencePoint struct {
	YieldPercent float64 `json:"yield_percent"`
	AvgScore     float64 `json:"avg_score"`
	AvgSweet     float64 `json:"avg_sweet"`
	SampleCount  int     `json:"sample_count"`
	InGoldCup    bool    `json:"in_gold_cup"`
}

// preferenceBinWidth 是分箱宽度，取 0.5 个百分点。
//
// 为何是 0.5：折射仪读数的重复性约 ±0.01% TDS，换算到萃取率约 ±0.15 个百分点；
// 而人的味觉对萃取率变化的分辨阈约在 0.5–1 个百分点。分箱窄于感官分辨力
// 只会制造噪声峰值，宽于 1 个百分点又会把甜蜜点糊掉。
var preferenceBinWidth = fixed.MustRatioPercent("0.5")

// minScoredSamplesForCurve 是推导偏好曲线所需的最小评分样本数。
//
// 为何是 4：要说"峰值在这里"，至少需要峰值本身加上两侧各一个更低的点来
// 构成一个可辨认的峰形，再留一个余量。少于此数时曲线上的"峰"很可能只是
// 单条记录的随机波动，此时引擎明确拒绝给出结论 —— 这是 Requirements §3.5
// "样本不足时明确返回数据不足，不得编造建议"的落地。
const minScoredSamplesForCurve = 4

// BuildPreferenceCurve 由带评分的历史记录推导个人偏好曲线。
func BuildPreferenceCurve(p Profile, samples []ScoredSample) *PreferenceCurve {
	pc := &PreferenceCurve{
		Points: []PreferencePoint{},
		Basis:  []string{},
	}

	scored := make([]ScoredSample, 0, len(samples))
	for _, s := range samples {
		// 只用实测样本推导偏好曲线。若把推算样本也算进来，
		// 曲线的横坐标本身就带着模型误差，峰值位置会失去意义。
		if s.TotalScoreX100 > 0 && s.Yield > 0 && s.Mode == ModeMeasured {
			scored = append(scored, s)
		}
	}
	pc.ScoredSampleCount = len(scored)

	if len(scored) < minScoredSamplesForCurve {
		pc.Available = false
		need := minScoredSamplesForCurve - len(scored)
		pc.Reason = "还需 " + itoa(need) + " 条「带 TDS 实测 + 六维评分」的记录才能推导你的个人最优萃取率。" +
			"当前符合条件的记录 " + itoa(len(scored)) + " 条。" +
			"偏好曲线必须建立在实测萃取率之上 —— 若用推算值做横坐标，峰值位置会被模型误差带偏。"
		return pc
	}

	// 按萃取率分箱聚合
	type bin struct {
		sumScore int
		sumSweet int
		count    int
	}
	bins := make(map[int64]*bin)
	for _, s := range scored {
		key := int64(s.Yield) / int64(preferenceBinWidth)
		b := bins[key]
		if b == nil {
			b = &bin{}
			bins[key] = b
		}
		b.sumScore += s.TotalScoreX100
		b.sumSweet += s.SweetScoreX100
		b.count++
	}

	keys := make([]int64, 0, len(bins))
	for k := range bins {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	var peakKey int64
	var peakAvg *big.Rat
	for _, k := range keys {
		b := bins[k]
		// 分箱中心的萃取率
		center := fixed.Ratio(k)*preferenceBinWidth + preferenceBinWidth/2
		avgScore := new(big.Rat).SetFrac64(int64(b.sumScore), int64(b.count)*100)
		avgSweet := new(big.Rat).SetFrac64(int64(b.sumSweet), int64(b.count)*100)

		af, _ := avgScore.Float64()
		sf, _ := avgSweet.Float64()
		pc.Points = append(pc.Points, PreferencePoint{
			YieldPercent: center.ApproxPercentFloat(),
			AvgScore:     af,
			AvgSweet:     sf,
			SampleCount:  b.count,
			InGoldCup:    center >= p.YieldMin && center <= p.YieldMax,
		})

		if peakAvg == nil || avgScore.Cmp(peakAvg) > 0 {
			peakAvg = avgScore
			peakKey = k
		}
	}

	peakCenter := fixed.Ratio(peakKey)*preferenceBinWidth + preferenceBinWidth/2
	peakScoreF, _ := peakAvg.Float64()

	pc.Available = true
	pc.PeakYieldPercent = peakCenter.ApproxPercentFloat()
	pc.PeakScore = peakScoreF
	pc.PeakLabel = peakCenter.Percent() + "%"

	delta := peakCenter - p.YieldMidpoint()
	pc.DeltaFromSCACenter = delta.ApproxPercentFloat()

	pc.Basis = append(pc.Basis,
		"取 "+itoa(len(scored))+" 条实测记录，按 "+preferenceBinWidth.Percent()+
			" 个百分点为一箱聚合萃取率，箱内取六维总分均值。")
	pc.Basis = append(pc.Basis,
		"评分均值最高的箱中心在 "+peakCenter.Percent()+"%，总分 "+
			formatScoreF(peakScoreF)+"/60。")
	pc.Basis = append(pc.Basis,
		"分箱宽度取 "+preferenceBinWidth.Percent()+" 个百分点，是因为人的味觉对萃取率的"+
			"分辨阈约在 0.5–1 个百分点，分得更细只会引入噪声峰值。")

	switch {
	case absRatio(delta) <= fixed.MustRatioPercent("0.5"):
		pc.Insight = "你的个人最优萃取率 " + peakCenter.Percent() +
			"% 几乎正落在 SCA 区间中心 " + p.YieldMidpoint().Percent() +
			"% 上。这说明你的口味与行业基准高度一致，直接照金杯区间调参就行。"
	case delta > 0:
		pc.Insight = "你的个人最优萃取率 " + peakCenter.Percent() +
			"% 比 SCA 区间中心高 " + absRatio(delta).Percent() +
			" 个百分点。你偏好萃取得更透一些的杯子 —— 这在浅烘豆上很常见，" +
			"更高的萃取率能把花果调性和甜感一并带出来。下次调参时，可以把 " +
			peakCenter.Percent() + "% 当成你自己的靶心，而不是 " + p.YieldMidpoint().Percent() + "%。"
	default:
		pc.Insight = "你的个人最优萃取率 " + peakCenter.Percent() +
			"% 比 SCA 区间中心低 " + absRatio(delta).Percent() +
			" 个百分点。你偏好保留更多前段风味的杯子 —— 深烘豆或高苦感豆常有这个规律，" +
			"稍稍收住萃取能避开焙烤苦。把 " + peakCenter.Percent() + "% 当成你的靶心会比照搬 " +
			p.YieldMidpoint().Percent() + "% 更贴合你的口味。"
	}

	return pc
}

// ---------------------------------------------------------------------------
// 几何辅助
// ---------------------------------------------------------------------------

func strengthPadding(p Profile) fixed.Ratio {
	// 意式浓度轴的量级是手冲的六倍以上，留白必须按比例缩放，
	// 否则手冲图的留白在意式图上会小到看不见。
	span := p.StrengthMax - p.StrengthMin
	return span / 2
}

func strengthTickStep(p Profile) float64 {
	if p.Method == domain.MethodEspresso {
		return 1
	}
	return 0.1
}

func axisSpan(lo, hi, pad fixed.Ratio) (float64, float64) {
	return (lo - pad).ApproxPercentFloat(), (hi + pad).ApproxPercentFloat()
}

func buildTicks(min, max, step float64) []float64 {
	if step <= 0 {
		step = 1
	}
	ticks := []float64{}
	// 用整数步进循环，避免浮点累加漂移导致刻度值出现 18.000000000000004
	stepMilli := int64(step * 1000)
	startMilli := (int64(min*1000) / stepMilli) * stepMilli
	endMilli := int64(max * 1000)
	for v := startMilli; v <= endMilli; v += stepMilli {
		if float64(v)/1000 >= min-step/2 {
			ticks = append(ticks, float64(v)/1000)
		}
	}
	return ticks
}

func buildZoneRects(p Profile, xMin, xMax, yMin, yMax float64) []ZoneRect {
	xs := [][2]float64{
		{xMin, p.YieldMin.ApproxPercentFloat()},
		{p.YieldMin.ApproxPercentFloat(), p.YieldMax.ApproxPercentFloat()},
		{p.YieldMax.ApproxPercentFloat(), xMax},
	}
	ys := [][2]float64{
		{yMin, p.StrengthMin.ApproxPercentFloat()},
		{p.StrengthMin.ApproxPercentFloat(), p.StrengthMax.ApproxPercentFloat()},
		{p.StrengthMax.ApproxPercentFloat(), yMax},
	}
	yieldZones := []YieldZone{YieldUnder, YieldIdeal, YieldOver}
	strengthZones := []StrengthZone{StrengthWeak, StrengthIdeal, StrengthStrong}

	out := make([]ZoneRect, 0, 9)
	for si, s := range strengthZones {
		for yi, y := range yieldZones {
			label, diag := zoneNarrative(y, s)
			out = append(out, ZoneRect{
				Code:        zoneCode(y, s),
				Label:       label,
				Diagnosis:   diag,
				SeverityHue: severityHue(y, s),
				InGoldCup:   y == YieldIdeal && s == StrengthIdeal,
				XMin:        xs[yi][0], XMax: xs[yi][1],
				YMin: ys[si][0], YMax: ys[si][1],
			})
		}
	}
	return out
}

// buildIsoRatios 生成等粉液比参考线。
func buildIsoRatios(p Profile, xMin, xMax, yMin, yMax float64) []IsoRatioLine {
	ratios := isoRatioSeries(p)
	out := make([]IsoRatioLine, 0, len(ratios))

	for _, r := range ratios {
		rf := r.ApproxMultipleFloat()
		if rf <= 0 {
			continue
		}
		// TDS = EY / R，在两端 x 处求 y，再按 y 轴范围裁剪
		x1, y1 := xMin, xMin/rf
		x2, y2 := xMax, xMax/rf

		if y1 < yMin {
			y1 = yMin
			x1 = y1 * rf
		}
		if y2 > yMax {
			y2 = yMax
			x2 = y2 * rf
		}
		if x1 > x2 || y1 > y2 {
			continue
		}

		out = append(out, IsoRatioLine{
			Ratio: rf,
			Label: r.BrewRatioLabel(),
			X1:    x1, Y1: y1, X2: x2, Y2: y2,
			Emphasize: r >= p.RatioMin && r <= p.RatioMax,
		})
	}
	return out
}

// isoRatioSeries 给出该冲煮法值得画的粉液比档位。
func isoRatioSeries(p Profile) []fixed.Ratio {
	if p.Method == domain.MethodEspresso {
		return []fixed.Ratio{
			fixed.MustRatioMultiple("1.0"),
			fixed.MustRatioMultiple("1.5"),
			fixed.MustRatioMultiple("2.0"),
			fixed.MustRatioMultiple("2.5"),
			fixed.MustRatioMultiple("3.0"),
		}
	}
	return []fixed.Ratio{
		fixed.MustRatioMultiple("13"),
		fixed.MustRatioMultiple("14"),
		fixed.MustRatioMultiple("15"),
		fixed.MustRatioMultiple("16"),
		fixed.MustRatioMultiple("17"),
		fixed.MustRatioMultiple("18"),
		fixed.MustRatioMultiple("19"),
	}
}

func floorTo(v, step float64) float64 {
	n := int64(v / step)
	if float64(n)*step > v {
		n--
	}
	return float64(n) * step
}

func ceilTo(v, step float64) float64 {
	n := int64(v / step)
	if float64(n)*step < v {
		n++
	}
	return float64(n) * step
}

func formatScoreF(v float64) string {
	// 评分展示只需一位小数，且值域固定在 0–60，用整数运算即可
	x := int64(v*10 + 0.5)
	return itoa(int(x/10)) + "." + itoa(int(x%10))
}
