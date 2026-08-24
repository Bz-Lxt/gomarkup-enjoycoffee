package goldcup

import (
	"math"
	"strings"
	"testing"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

// 本文件测控制图与偏好曲线的几何量。
//
// 为什么值得单独测：裁定 C-02 把全部几何计算放在后端，前端只把点集画成线。
// 这个选择消除了前后端数值不一致的可能，代价是几何错误再没有第二道防线 ——
// 一个算错的落区矩形会让页面上的九宫格与读数里的落区判定互相矛盾，
// 而两者恰恰是用户判断"这杯该怎么调"的唯一依据。
//
// 几何 bug 又特别容易逃过肉眼：轴范围差 0.5 个百分点、矩形边界错一格，
// 图看起来都很正常，只有把点画到框外时才会露馅。所以这里测的是不变量
// （矩形铺满且不重叠、点必在轴内、等比线斜率确为 1/R），而不是快照。

func filterP(t *testing.T) Profile {
	t.Helper()
	p, err := DefaultProfile(domain.MethodFilter)
	if err != nil {
		t.Fatalf("取手冲出厂标准失败: %v", err)
	}
	return p
}

func espressoP(t *testing.T) Profile {
	t.Helper()
	p, err := DefaultProfile(domain.MethodEspresso)
	if err != nil {
		t.Fatalf("取意式出厂标准失败: %v", err)
	}
	return p
}

// sample 造一条历史样本。yield/tds 为百分数字符串，score 为总分（0–60）。
func sample(t *testing.T, id int64, yield, tds string, score float64, mode Mode) ScoredSample {
	t.Helper()
	return ScoredSample{
		BrewID:         id,
		Yield:          fixed.MustRatioPercent(yield),
		TDS:            fixed.MustRatioPercent(tds),
		Ratio:          fixed.MustRatioMultiple("15"),
		Mode:           mode,
		Label:          "样本" + itoa(int(id)),
		TotalScoreX100: int(score * 100),
		SweetScoreX100: int(score * 100 / 6),
	}
}

// ---------------------------------------------------------------------------
// 落区矩形
// ---------------------------------------------------------------------------

// TestZoneRectsTileTheAxisBoxWithoutGaps 验证九个矩形正好铺满坐标系，不留缝也不重叠。
//
// 缝隙会让某些落区在图上根本画不出来，用户点进去看到一片空白；
// 重叠则会让两个矩形争同一片像素，颜色叠加后落区看起来像第三种状态。
func TestZoneRectsTileTheAxisBoxWithoutGaps(t *testing.T) {
	p := filterP(t)
	ch := BuildChart(p, nil)

	if len(ch.Zones) != 9 {
		t.Fatalf("九宫格应有 9 格，实际 %d 格", len(ch.Zones))
	}

	// 每格的面积之和必须等于整个坐标框的面积。相等即证明既无缝隙也无重叠 ——
	// 有缝会小于，有重叠会大于。
	var sum float64
	for _, z := range ch.Zones {
		if z.XMax < z.XMin || z.YMax < z.YMin {
			t.Errorf("%s 矩形边界颠倒: x[%g,%g] y[%g,%g]",
				z.Code, z.XMin, z.XMax, z.YMin, z.YMax)
		}
		sum += (z.XMax - z.XMin) * (z.YMax - z.YMin)
	}
	whole := (ch.AxisX.Max - ch.AxisX.Min) * (ch.AxisY.Max - ch.AxisY.Min)
	if math.Abs(sum-whole) > 1e-9 {
		t.Errorf("九格面积之和 %g 应等于坐标框面积 %g —— 不等说明有缝隙或重叠",
			sum, whole)
	}
}

// TestExactlyOneZoneIsGoldCup 验证金杯格唯一，且其边界就是配置的区间。
//
// 若金杯格的边界与 Profile 的区间不一致，图上的理想区就和读数的判定标准
// 对不上：用户会看到点落在绿框里，读数却说"浓度偏高"。
func TestExactlyOneZoneIsGoldCup(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Profile
	}{
		{"手冲", filterP(t)},
		{"意式", espressoP(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := BuildChart(tc.p, nil)

			var gold []ZoneRect
			for _, z := range ch.Zones {
				if z.InGoldCup {
					gold = append(gold, z)
				}
			}
			if len(gold) != 1 {
				t.Fatalf("应恰好一格是金杯区，实际 %d 格", len(gold))
			}

			g := gold[0]
			want := [4]float64{
				tc.p.YieldMin.ApproxPercentFloat(),
				tc.p.YieldMax.ApproxPercentFloat(),
				tc.p.StrengthMin.ApproxPercentFloat(),
				tc.p.StrengthMax.ApproxPercentFloat(),
			}
			got := [4]float64{g.XMin, g.XMax, g.YMin, g.YMax}
			for i := range want {
				if math.Abs(got[i]-want[i]) > 1e-9 {
					t.Errorf("金杯格边界[%d] = %g，应为配置值 %g —— "+
						"图上的理想区必须与读数用的判定区间是同一个", i, got[i], want[i])
				}
			}
		})
	}
}

// TestEveryZoneCodeIsDistinct 验证九格编码互不重复。
//
// 前端按 code 做图例联动与 hover 高亮，重复的 code 会让两格一起亮。
func TestEveryZoneCodeIsDistinct(t *testing.T) {
	ch := BuildChart(filterP(t), nil)
	seen := map[string]bool{}
	for _, z := range ch.Zones {
		if seen[z.Code] {
			t.Errorf("落区编码 %s 重复", z.Code)
		}
		seen[z.Code] = true
		if z.Label == "" || z.Diagnosis == "" || z.SeverityHue == "" {
			t.Errorf("%s 缺少展示文案: label=%q diagnosis=%q hue=%q",
				z.Code, z.Label, z.Diagnosis, z.SeverityHue)
		}
	}
}

// TestZoneMatrixAgreesWithChartZones 验证 /meta 下发的九宫格图例与控制图一致。
//
// 两处若不一致，图例上的说明就会解释错格子 —— 用户照着图例读图会得出反向结论。
func TestZoneMatrixAgreesWithChartZones(t *testing.T) {
	cells := ZoneMatrix()
	ch := BuildChart(filterP(t), nil)

	if len(cells) != len(ch.Zones) {
		t.Fatalf("图例 %d 格与控制图 %d 格数量不一致", len(cells), len(ch.Zones))
	}

	byCode := map[string]ZoneRect{}
	for _, z := range ch.Zones {
		byCode[z.Code] = z
	}
	for _, c := range cells {
		z, ok := byCode[c.Code]
		if !ok {
			t.Errorf("图例里的 %s 在控制图中不存在", c.Code)
			continue
		}
		if c.Label != z.Label || c.Diagnosis != z.Diagnosis {
			t.Errorf("%s 的文案两处不一致：图例 %q / %q，控制图 %q / %q",
				c.Code, c.Label, c.Diagnosis, z.Label, z.Diagnosis)
		}
		if c.InGoldCup != z.InGoldCup {
			t.Errorf("%s 的金杯判定两处不一致", c.Code)
		}
		if c.SeverityHue != z.SeverityHue {
			t.Errorf("%s 的严重度色两处不一致: %q vs %q",
				c.Code, c.SeverityHue, z.SeverityHue)
		}
	}
}

// TestSeverityHueIsAssignedToEveryCell 验证每格都有严重度色，且金杯格与众不同。
func TestSeverityHueIsAssignedToEveryCell(t *testing.T) {
	var goldHue string
	other := map[string]bool{}
	for _, c := range ZoneMatrix() {
		if c.SeverityHue == "" {
			t.Errorf("%s 没有严重度色", c.Code)
		}
		if c.InGoldCup {
			goldHue = c.SeverityHue
		} else {
			other[c.SeverityHue] = true
		}
	}
	if goldHue == "" {
		t.Fatal("金杯格没有严重度色")
	}
	if other[goldHue] {
		t.Errorf("金杯格用了和非金杯格相同的色键 %q —— "+
			"合格与不合格在图上必须一眼可分", goldHue)
	}
}

// ---------------------------------------------------------------------------
// 坐标轴
// ---------------------------------------------------------------------------

// TestTicksAreMonotonicAndFreeOfFloatDrift 验证刻度递增且没有浮点毛刺。
//
// buildTicks 刻意用整数步进而非浮点累加，就是为了避免刻度上出现
// 18.000000000000004 这种值 —— 它会被直接渲染成轴标签，一眼假。
func TestTicksAreMonotonicAndFreeOfFloatDrift(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Profile
	}{
		{"手冲", filterP(t)},
		{"意式", espressoP(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := BuildChart(tc.p, nil)
			for _, ax := range []struct {
				name string
				a    Axis
			}{{"X", ch.AxisX}, {"Y", ch.AxisY}} {
				if len(ax.a.Ticks) < 2 {
					t.Errorf("%s 轴只有 %d 个刻度，画不出可读的网格",
						ax.name, len(ax.a.Ticks))
				}
				for i, v := range ax.a.Ticks {
					if i > 0 && v <= ax.a.Ticks[i-1] {
						t.Errorf("%s 轴刻度非递增: %v", ax.name, ax.a.Ticks)
						break
					}
					// 毛刺检测：刻度乘 1000 后应当极接近整数。
					if d := math.Abs(v*1000 - math.Round(v*1000)); d > 1e-6 {
						t.Errorf("%s 轴刻度 %v 带浮点毛刺（×1000 后偏离整数 %g）",
							ax.name, v, d)
					}
				}
				if ax.a.Label == "" || ax.a.Unit == "" {
					t.Errorf("%s 轴缺少标签或单位", ax.name)
				}
			}
		})
	}
}

// TestAxisAlwaysContainsTheGoldCupBox 验证金杯区永远画得进坐标系。
func TestAxisAlwaysContainsTheGoldCupBox(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Profile
	}{
		{"手冲", filterP(t)},
		{"意式", espressoP(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := BuildChart(tc.p, nil)
			if ch.AxisX.Min > tc.p.YieldMin.ApproxPercentFloat() ||
				ch.AxisX.Max < tc.p.YieldMax.ApproxPercentFloat() {
				t.Errorf("X 轴 [%g,%g] 装不下萃取率金杯区间 [%g,%g]",
					ch.AxisX.Min, ch.AxisX.Max,
					tc.p.YieldMin.ApproxPercentFloat(), tc.p.YieldMax.ApproxPercentFloat())
			}
			if ch.AxisY.Min > tc.p.StrengthMin.ApproxPercentFloat() ||
				ch.AxisY.Max < tc.p.StrengthMax.ApproxPercentFloat() {
				t.Errorf("Y 轴 [%g,%g] 装不下浓度金杯区间 [%g,%g]",
					ch.AxisY.Min, ch.AxisY.Max,
					tc.p.StrengthMin.ApproxPercentFloat(), tc.p.StrengthMax.ApproxPercentFloat())
			}
		})
	}
}

// TestAxisNeverGoesNegative 验证坐标轴不会伸到负数区。
//
// 萃取率和浓度都不可能为负。负的轴范围会浪费掉画布左下角一大片，
// 把实际数据挤到右上角一小块里。
func TestAxisNeverGoesNegative(t *testing.T) {
	// 意式的浓度下限减去留白后本就容易探到负数，是最容易触发的一档。
	for _, p := range []Profile{filterP(t), espressoP(t)} {
		ch := BuildChart(p, nil)
		if ch.AxisX.Min < 0 || ch.AxisY.Min < 0 {
			t.Errorf("%s 的坐标轴下界出现负数: x=%g y=%g",
				p.Method, ch.AxisX.Min, ch.AxisY.Min)
		}
	}
}

// TestOutlierSamplesExpandTheAxisInsteadOfBeingClipped 验证越界的历史点会撑开坐标轴。
//
// 这是整个几何逻辑里最要紧的一条。用户翻看历史时，一次失败的萃取
// （比如萃取率 28%）恰恰是最想看的那个点。若坐标轴不跟着扩张，
// 这个点会被画到框外或被裁掉 —— 用户会以为那次记录丢了。
func TestOutlierSamplesExpandTheAxisInsteadOfBeingClipped(t *testing.T) {
	p := filterP(t)
	base := BuildChart(p, nil)

	samples := []ScoredSample{
		sample(t, 1, "28.5", "2.10", 0, ModeMeasured), // 远超上界
		sample(t, 2, "9.20", "0.60", 0, ModeMeasured), // 远低于下界
	}
	ch := BuildChart(p, samples)

	if ch.AxisX.Max <= base.AxisX.Max {
		t.Errorf("28.5%% 的点未撑开 X 轴上界（仍为 %g）", ch.AxisX.Max)
	}
	if ch.AxisX.Min >= base.AxisX.Min {
		t.Errorf("9.2%% 的点未压低 X 轴下界（仍为 %g）", ch.AxisX.Min)
	}

	for _, pt := range ch.Points {
		if pt.YieldPercent < ch.AxisX.Min || pt.YieldPercent > ch.AxisX.Max {
			t.Errorf("点 %d 的萃取率 %g 落在 X 轴 [%g,%g] 之外 —— 画不出来",
				pt.BrewID, pt.YieldPercent, ch.AxisX.Min, ch.AxisX.Max)
		}
		if pt.TDSPercent < ch.AxisY.Min || pt.TDSPercent > ch.AxisY.Max {
			t.Errorf("点 %d 的浓度 %g 落在 Y 轴 [%g,%g] 之外 —— 画不出来",
				pt.BrewID, pt.TDSPercent, ch.AxisY.Min, ch.AxisY.Max)
		}
	}
}

// TestZoneRectsStillTileAfterAxisExpansion 验证轴被撑开后矩形仍然铺满。
//
// 扩张轴范围和生成矩形是两处独立的计算，很容易只更新一处。
func TestZoneRectsStillTileAfterAxisExpansion(t *testing.T) {
	p := filterP(t)
	ch := BuildChart(p, []ScoredSample{
		sample(t, 1, "27.0", "1.90", 0, ModeMeasured),
	})

	var sum float64
	for _, z := range ch.Zones {
		sum += (z.XMax - z.XMin) * (z.YMax - z.YMin)
	}
	whole := (ch.AxisX.Max - ch.AxisX.Min) * (ch.AxisY.Max - ch.AxisY.Min)
	if math.Abs(sum-whole) > 1e-9 {
		t.Errorf("轴扩张后九格面积和 %g 不再等于坐标框 %g —— "+
			"矩形没跟着轴一起更新", sum, whole)
	}
}

// ---------------------------------------------------------------------------
// 等粉液比参考线
// ---------------------------------------------------------------------------

// TestIsoRatioLinesReallyHaveSlopeOneOverRatio 验证等比线的斜率确为 1/R。
//
// 这些线的用途是把"改配比"变成沿线滑动、"改研磨度"变成跨线移动。
// 斜率错了，用户照着图调参会调反方向 —— 一条画错的辅助线比没有更糟。
func TestIsoRatioLinesReallyHaveSlopeOneOverRatio(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Profile
	}{
		{"手冲", filterP(t)},
		{"意式", espressoP(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := BuildChart(tc.p, nil)
			if len(ch.IsoRatios) == 0 {
				t.Fatal("没有生成任何等粉液比参考线")
			}
			for _, l := range ch.IsoRatios {
				if l.Ratio <= 0 {
					t.Errorf("%s 的比值 %g 非正", l.Label, l.Ratio)
					continue
				}
				// TDS = EY / R，故两端都应满足 y == x / R。
				for i, pt := range [][2]float64{{l.X1, l.Y1}, {l.X2, l.Y2}} {
					want := pt[0] / l.Ratio
					if math.Abs(pt[1]-want) > 1e-6 {
						t.Errorf("%s 端点%d (%g,%g) 不在 y=x/%g 上（应为 y=%g）—— "+
							"斜率错的辅助线会把调参方向指反",
							l.Label, i+1, pt[0], pt[1], l.Ratio, want)
					}
				}
				if l.X1 > l.X2 {
					t.Errorf("%s 的端点顺序颠倒: x1=%g > x2=%g", l.Label, l.X1, l.X2)
				}
			}
		})
	}
}

// TestIsoRatioLinesAreClippedToTheAxisBox 验证参考线被裁进坐标框内。
func TestIsoRatioLinesAreClippedToTheAxisBox(t *testing.T) {
	ch := BuildChart(filterP(t), nil)
	const eps = 1e-9
	for _, l := range ch.IsoRatios {
		if l.X1 < ch.AxisX.Min-eps || l.X2 > ch.AxisX.Max+eps {
			t.Errorf("%s 的 x 范围 [%g,%g] 超出 X 轴 [%g,%g]",
				l.Label, l.X1, l.X2, ch.AxisX.Min, ch.AxisX.Max)
		}
		if l.Y1 < ch.AxisY.Min-eps || l.Y2 > ch.AxisY.Max+eps {
			t.Errorf("%s 的 y 范围 [%g,%g] 超出 Y 轴 [%g,%g]",
				l.Label, l.Y1, l.Y2, ch.AxisY.Min, ch.AxisY.Max)
		}
	}
}

// TestEmphasizedIsoRatiosAreExactlyThoseInProfileRange 验证高亮的正是参考区间内的档位。
func TestEmphasizedIsoRatiosAreExactlyThoseInProfileRange(t *testing.T) {
	p := filterP(t)
	ch := BuildChart(p, nil)

	lo := p.RatioMin.ApproxMultipleFloat()
	hi := p.RatioMax.ApproxMultipleFloat()
	var emphasized int
	for _, l := range ch.IsoRatios {
		inRange := l.Ratio >= lo-1e-9 && l.Ratio <= hi+1e-9
		if l.Emphasize != inRange {
			t.Errorf("%s（比值 %g）的高亮标记为 %v，但参考区间是 [%g,%g]",
				l.Label, l.Ratio, l.Emphasize, lo, hi)
		}
		if l.Emphasize {
			emphasized++
		}
	}
	if emphasized == 0 {
		t.Error("没有任何一条参考线被高亮 —— 用户看不出哪几档是推荐范围")
	}
}

// TestEspressoAndFilterUseDifferentRatioSeries 验证两种冲煮法的档位不混用。
//
// 意式的粉液比在 1:1–1:3，手冲在 1:13–1:19。若混用，其中一张图上的
// 参考线会全部挤在角落或整体跑到框外。
func TestEspressoAndFilterUseDifferentRatioSeries(t *testing.T) {
	f := BuildChart(filterP(t), nil)
	e := BuildChart(espressoP(t), nil)

	maxOf := func(ls []IsoRatioLine) float64 {
		m := 0.0
		for _, l := range ls {
			if l.Ratio > m {
				m = l.Ratio
			}
		}
		return m
	}
	if maxOf(e.IsoRatios) >= maxOf(f.IsoRatios) {
		t.Errorf("意式的最大粉液比 %g 应远小于手冲的 %g",
			maxOf(e.IsoRatios), maxOf(f.IsoRatios))
	}
	if len(e.IsoRatios) == 0 || len(f.IsoRatios) == 0 {
		t.Error("两种冲煮法都必须有参考线")
	}
}

// ---------------------------------------------------------------------------
// 历史点
// ---------------------------------------------------------------------------

// TestEstimatedPointsAreNeverMarkedGoldCup 验证推算点不会被判为金杯合格。
//
// 这是"推算值不得冒充实测值"这条主线在控制图上的落点。推算出的萃取率
// 带着模型误差，拿它宣布"这杯达到金杯标准"是在给一个没有依据的结论；
// 用户会据此认定参数已调对，从而停止调整。
func TestEstimatedPointsAreNeverMarkedGoldCup(t *testing.T) {
	p := filterP(t)
	// 刻意取一个正落在金杯区中心的数值：若实现只看数值不看模式，这里必然漏判。
	mid := p.YieldMidpoint().Percent()
	strengthMid := ((p.StrengthMin + p.StrengthMax) / 2).Percent()

	ch := BuildChart(p, []ScoredSample{
		sample(t, 1, mid, strengthMid, 48, ModeMeasured),
		sample(t, 2, mid, strengthMid, 48, ModeEstimated),
	})

	if len(ch.Points) != 2 {
		t.Fatalf("应有 2 个点，实际 %d 个", len(ch.Points))
	}
	measured, estimated := ch.Points[0], ch.Points[1]

	if !measured.InGoldCup {
		t.Errorf("实测点落在区间中心 (%s%%, %s%%) 却未判为金杯合格",
			mid, strengthMid)
	}
	if measured.Advisory {
		t.Error("实测点被标成了推算")
	}
	if !estimated.Advisory {
		t.Error("推算点没有标注 advisory —— 前端无法用空心点区分它")
	}
	if estimated.InGoldCup {
		t.Error("推算点被判为金杯合格 —— 推算值不足以支撑'达标'这个结论")
	}
}

// TestPointsCarryZoneAndRatioLabels 验证每个点都带落区与配比的可读标签。
func TestPointsCarryZoneAndRatioLabels(t *testing.T) {
	ch := BuildChart(filterP(t), []ScoredSample{
		sample(t, 7, "20.0", "1.25", 45, ModeMeasured),
	})
	pt := ch.Points[0]
	if pt.ZoneCode == "" || pt.ZoneLabel == "" {
		t.Errorf("点缺少落区信息: code=%q label=%q", pt.ZoneCode, pt.ZoneLabel)
	}
	if pt.BrewRatioText == "" {
		t.Error("点缺少粉液比文案")
	}
	if !pt.HasScore || pt.TotalScore <= 0 {
		t.Errorf("有评分的点应带分数，实际 has=%v score=%g", pt.HasScore, pt.TotalScore)
	}
}

// TestUnscoredPointIsMarkedAsSuch 验证没评分的点不会显示成 0 分。
//
// 0 分和"没打分"在图上必须区分：前者是"这杯很糟"，后者是"还没评价"。
func TestUnscoredPointIsMarkedAsSuch(t *testing.T) {
	ch := BuildChart(filterP(t), []ScoredSample{
		sample(t, 8, "20.0", "1.25", 0, ModeMeasured),
	})
	if ch.Points[0].HasScore {
		t.Error("未评分的点被标成有评分")
	}
}

// TestEmptyChartStillHasDrawableGeometry 验证零历史记录时图仍可渲染。
//
// 新用户的第一次打开就是这个状态。切片字段若为 nil，前端会崩在 undefined.map 上。
func TestEmptyChartStillHasDrawableGeometry(t *testing.T) {
	ch := BuildChart(filterP(t), nil)
	if ch.Points == nil {
		t.Error("Points 为 nil，前端会崩在 undefined.map 上；应为空数组")
	}
	if len(ch.Zones) != 9 || len(ch.IsoRatios) == 0 || len(ch.AxisX.Ticks) == 0 {
		t.Error("空数据时几何量也必须完整下发")
	}
	if ch.Preference == nil {
		t.Error("Preference 为 nil，前端无法区分'不可用'与'字段缺失'")
	}
	if ch.Title == "" || ch.ChartKind == "" {
		t.Errorf("图缺少标题或类型: title=%q kind=%q", ch.Title, ch.ChartKind)
	}
}

// ---------------------------------------------------------------------------
// 个人偏好曲线
// ---------------------------------------------------------------------------

// TestPreferenceCurveRefusesToGuessBelowThreshold 验证样本不足时明确拒绝而非编造。
//
// 对应 Requirements §3.5。这条是需求里少见的"必须拒绝回答"的条款：
// 三条记录也能画出一个"峰"，但那个峰只是随机波动。给出它比不给更糟 ——
// 用户会照着一个假靶心调参。
// 阈值在这里写成字面量 4，而不是引用 minScoredSamplesForCurve。
//
// 引用常量会让这条测试变成同义反复：把常量改成 1，循环边界也跟着变成 1，
// 测试照样全绿 —— 我第一版就是这么写的，变异测试当场抓出它抓不住阈值下调。
// 阈值是需求层面的判断（4 = 峰值 + 两侧各一 + 余量），改它必须改测试，
// 于是"为什么是 4"这个问题会被重新过一遍，而不是悄悄溜过去。
const wantMinScoredSamples = 4

func TestPreferenceCurveRefusesToGuessBelowThreshold(t *testing.T) {
	p := filterP(t)

	if minScoredSamplesForCurve != wantMinScoredSamples {
		t.Fatalf("偏好曲线阈值被改成了 %d。这是需求层面的取值（%d = 峰值 + 两侧各一 + 余量），"+
			"改动请连同 curve.go 里的理由一并复核后再更新本测试",
			minScoredSamplesForCurve, wantMinScoredSamples)
	}

	for n := 0; n < wantMinScoredSamples; n++ {
		samples := make([]ScoredSample, 0, n)
		for i := 0; i < n; i++ {
			samples = append(samples, sample(t, int64(i+1), "20.0", "1.25", 45, ModeMeasured))
		}
		pc := BuildPreferenceCurve(p, samples)

		if pc.Available {
			t.Errorf("仅 %d 条评分样本就给出了曲线，阈值应为 %d",
				n, wantMinScoredSamples)
		}
		if pc.Reason == "" {
			t.Errorf("%d 条样本时拒绝了但没说明原因", n)
		}
		if want := itoa(wantMinScoredSamples - n); !strings.Contains(pc.Reason, want) {
			t.Errorf("%d 条样本时的说明应告诉用户还差 %s 条，实际文案: %s",
				n, want, pc.Reason)
		}
		if pc.Points == nil || pc.Basis == nil {
			t.Error("不可用时切片字段也不能为 nil")
		}
	}
}

// TestPreferenceCurveBecomesAvailableExactlyAtThreshold 从另一侧钉住阈值。
//
// 只测"不足时拒绝"是不够的：把阈值调高到 99 也能让那条测试通过，
// 而那样偏好曲线就永远不会出现，功能等于被静默删掉。
func TestPreferenceCurveBecomesAvailableExactlyAtThreshold(t *testing.T) {
	p := filterP(t)
	mk := func(n int) []ScoredSample {
		out := make([]ScoredSample, 0, n)
		for i := 0; i < n; i++ {
			// 萃取率略微散开，避免全落一箱而看不出曲线形状。
			out = append(out, sample(t, int64(i+1),
				[]string{"19.2", "20.2", "21.2", "22.2", "20.7"}[i%5],
				"1.25", float64(40+i), ModeMeasured))
		}
		return out
	}

	if pc := BuildPreferenceCurve(p, mk(wantMinScoredSamples-1)); pc.Available {
		t.Errorf("%d 条样本（差一条到阈值）不应给出曲线", wantMinScoredSamples-1)
	}
	pc := BuildPreferenceCurve(p, mk(wantMinScoredSamples))
	if !pc.Available {
		t.Errorf("恰好 %d 条样本应给出曲线，实际被拒: %s",
			wantMinScoredSamples, pc.Reason)
	}
	if pc.PeakLabel == "" || len(pc.Points) == 0 {
		t.Error("刚达阈值时也必须给出完整的峰值与分箱")
	}
}

// TestEstimatedSamplesDoNotCountTowardTheCurve 验证推算样本不参与偏好曲线。
//
// 偏好曲线的横坐标是萃取率。若拿推算值当横坐标，峰值位置会被模型误差
// 系统性带偏 —— 而这条曲线的全部价值就在于峰值的位置。
func TestEstimatedSamplesDoNotCountTowardTheCurve(t *testing.T) {
	p := filterP(t)
	var samples []ScoredSample
	for i := 0; i < minScoredSamplesForCurve+2; i++ {
		samples = append(samples, sample(t, int64(i+1), "20.0", "1.25", 45, ModeEstimated))
	}

	pc := BuildPreferenceCurve(p, samples)
	if pc.Available {
		t.Error("全部为推算样本时不应给出偏好曲线")
	}
	if pc.ScoredSampleCount != 0 {
		t.Errorf("推算样本被计入了有效样本数（%d）", pc.ScoredSampleCount)
	}
}

// TestUnscoredSamplesDoNotCountTowardTheCurve 验证没评分的记录不参与推导。
func TestUnscoredSamplesDoNotCountTowardTheCurve(t *testing.T) {
	p := filterP(t)
	var samples []ScoredSample
	for i := 0; i < minScoredSamplesForCurve+2; i++ {
		samples = append(samples, sample(t, int64(i+1), "20.0", "1.25", 0, ModeMeasured))
	}
	if pc := BuildPreferenceCurve(p, samples); pc.Available {
		t.Error("全部未评分时不应给出偏好曲线")
	}
}

// TestPeakLandsOnTheHighestScoringBin 验证峰值落在评分均值最高的分箱上。
func TestPeakLandsOnTheHighestScoringBin(t *testing.T) {
	p := filterP(t)
	// 21.2% 一档给高分，其余给低分。峰值必须落在 21.0–21.5 这个箱。
	samples := []ScoredSample{
		sample(t, 1, "18.2", "1.20", 30, ModeMeasured),
		sample(t, 2, "19.2", "1.22", 36, ModeMeasured),
		sample(t, 3, "21.2", "1.28", 54, ModeMeasured),
		sample(t, 4, "21.3", "1.29", 52, ModeMeasured),
		sample(t, 5, "22.6", "1.33", 33, ModeMeasured),
	}
	pc := BuildPreferenceCurve(p, samples)

	if !pc.Available {
		t.Fatalf("5 条实测评分样本应能推导曲线，实际被拒: %s", pc.Reason)
	}
	if pc.PeakYieldPercent < 21.0 || pc.PeakYieldPercent > 21.5 {
		t.Errorf("峰值应落在 21.0–21.5 的箱内，实际 %g", pc.PeakYieldPercent)
	}
	if pc.ScoredSampleCount != len(samples) {
		t.Errorf("有效样本数应为 %d，实际 %d", len(samples), pc.ScoredSampleCount)
	}
	if len(pc.Basis) == 0 || pc.Insight == "" || pc.PeakLabel == "" {
		t.Error("可用时必须给出依据、结论文案与峰值标签")
	}

	// 分箱必须按萃取率递增，否则前端连成的折线会自我交叉。
	for i := 1; i < len(pc.Points); i++ {
		if pc.Points[i].YieldPercent <= pc.Points[i-1].YieldPercent {
			t.Errorf("分箱未按萃取率递增: %+v", pc.Points)
			break
		}
	}
	// 每个箱都必须有样本，空箱会在折线上画出一个假的谷底。
	for _, pt := range pc.Points {
		if pt.SampleCount < 1 {
			t.Errorf("萃取率 %g 的箱样本数为 %d", pt.YieldPercent, pt.SampleCount)
		}
	}
}

// TestSamplesInSameBinAreAveragedNotDuplicated 验证同箱样本取均值。
func TestSamplesInSameBinAreAveragedNotDuplicated(t *testing.T) {
	p := filterP(t)
	// 四条全落在同一个 0.5 宽的箱里（20.0–20.5），分数 40/42/44/46，均值 43。
	samples := []ScoredSample{
		sample(t, 1, "20.05", "1.25", 40, ModeMeasured),
		sample(t, 2, "20.15", "1.25", 42, ModeMeasured),
		sample(t, 3, "20.25", "1.25", 44, ModeMeasured),
		sample(t, 4, "20.35", "1.25", 46, ModeMeasured),
	}
	pc := BuildPreferenceCurve(p, samples)
	if !pc.Available {
		t.Fatalf("应能推导，实际被拒: %s", pc.Reason)
	}
	if len(pc.Points) != 1 {
		t.Fatalf("四条同箱样本应聚成 1 个箱，实际 %d 个", len(pc.Points))
	}
	pt := pc.Points[0]
	if pt.SampleCount != 4 {
		t.Errorf("箱内样本数应为 4，实际 %d", pt.SampleCount)
	}
	if math.Abs(pt.AvgScore-43) > 1e-9 {
		t.Errorf("箱内均分应为 43，实际 %g", pt.AvgScore)
	}
}

// TestDeltaSignMatchesPeakPosition 验证偏移量的符号与峰值位置一致。
//
// 结论文案分"几乎一致 / 偏高 / 偏低"三支，符号错了会给出反向建议：
// 把偏好更透的人劝去收住萃取。
func TestDeltaSignMatchesPeakPosition(t *testing.T) {
	p := filterP(t)
	mid := p.YieldMidpoint().ApproxPercentFloat()

	cases := []struct {
		name       string
		yields     []string
		wantSign   int // 1 = 正, -1 = 负, 0 = 近似零
		insightHas string
	}{
		{"峰值偏高", []string{"21.6", "21.7", "21.8", "21.9"}, 1, "更透"},
		{"峰值偏低", []string{"18.1", "18.2", "18.3", "18.4"}, -1, "前段风味"},
		{"峰值居中", []string{"20.1", "20.2", "20.1", "20.2"}, 0, "高度一致"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var samples []ScoredSample
			for i, y := range tc.yields {
				samples = append(samples, sample(t, int64(i+1), y, "1.25", 45, ModeMeasured))
			}
			pc := BuildPreferenceCurve(p, samples)
			if !pc.Available {
				t.Fatalf("应能推导，实际被拒: %s", pc.Reason)
			}

			d := pc.DeltaFromSCACenter
			switch tc.wantSign {
			case 1:
				if d <= 0 {
					t.Errorf("峰值 %g 高于中心 %g，偏移量应为正，实际 %g",
						pc.PeakYieldPercent, mid, d)
				}
			case -1:
				if d >= 0 {
					t.Errorf("峰值 %g 低于中心 %g，偏移量应为负，实际 %g",
						pc.PeakYieldPercent, mid, d)
				}
			case 0:
				if math.Abs(d) > 0.6 {
					t.Errorf("峰值 %g 应近似落在中心 %g 上，偏移量 %g 过大",
						pc.PeakYieldPercent, mid, d)
				}
			}
			if !strings.Contains(pc.Insight, tc.insightHas) {
				t.Errorf("结论文案应提到 %q，实际: %s", tc.insightHas, pc.Insight)
			}
		})
	}
}

// TestPreferenceCurveMarksWhichBinsAreInGoldCup 验证分箱标注是否落在金杯区。
func TestPreferenceCurveMarksWhichBinsAreInGoldCup(t *testing.T) {
	p := filterP(t)
	samples := []ScoredSample{
		sample(t, 1, "15.2", "1.10", 30, ModeMeasured), // 区间外
		sample(t, 2, "20.2", "1.25", 50, ModeMeasured), // 区间内
		sample(t, 3, "20.7", "1.26", 48, ModeMeasured), // 区间内
		sample(t, 4, "25.2", "1.40", 25, ModeMeasured), // 区间外
	}
	pc := BuildPreferenceCurve(p, samples)
	if !pc.Available {
		t.Fatalf("应能推导，实际被拒: %s", pc.Reason)
	}

	lo, hi := p.YieldMin.ApproxPercentFloat(), p.YieldMax.ApproxPercentFloat()
	for _, pt := range pc.Points {
		want := pt.YieldPercent >= lo && pt.YieldPercent <= hi
		if pt.InGoldCup != want {
			t.Errorf("萃取率 %g 的箱标记为 in_gold_cup=%v，但金杯区间是 [%g,%g]",
				pt.YieldPercent, pt.InGoldCup, lo, hi)
		}
	}
}

// ---------------------------------------------------------------------------
// 其余出网结构
// ---------------------------------------------------------------------------

// TestProfilesExposesBothMethodsInStableOrder 验证设置页拿到的标准列表稳定。
//
// 顺序不稳会让设置页上两块配置卡片每次刷新都换位置。
func TestProfilesExposesBothMethodsInStableOrder(t *testing.T) {
	e := NewEngine(nil)
	for i := 0; i < 5; i++ {
		got := e.Profiles()
		if len(got) != 2 {
			t.Fatalf("应有手冲与意式两套标准，实际 %d 套", len(got))
		}
		if got[0].Method != domain.MethodFilter || got[1].Method != domain.MethodEspresso {
			t.Errorf("第 %d 次调用的顺序是 %v/%v，应恒为 FILTER/ESPRESSO",
				i, got[0].Method, got[1].Method)
		}
	}
}

// TestSolveTargetsDocumentEveryRequiredField 验证四个反解方向都声明了必填字段。
//
// 前端与 QA 都按这份声明构造请求。少写一个字段，调用方会收到一个
// 它无法预料的 4xx；多写一个，调用方会白填一个用不上的值。
func TestSolveTargetsDocumentEveryRequiredField(t *testing.T) {
	targets := SolveTargets()
	if len(targets) < 4 {
		t.Fatalf("应声明至少 4 个反解方向，实际 %d 个", len(targets))
	}

	known := map[string]bool{
		"target_yield_percent": true,
		"tds_percent":          true,
		"dose_g":               true,
		"beverage_g":           true,
	}
	seen := map[string]bool{}

	for _, tgt := range targets {
		val, _ := tgt["value"].(string)
		if val == "" {
			t.Errorf("反解方向缺少 value: %+v", tgt)
			continue
		}
		if seen[val] {
			t.Errorf("反解方向 %s 重复声明", val)
		}
		seen[val] = true

		if lbl, _ := tgt["label"].(string); lbl == "" {
			t.Errorf("%s 缺少 label，设置页无法渲染选项", val)
		}
		if h, _ := tgt["hint"].(string); h == "" {
			t.Errorf("%s 缺少 hint，用户不知道这个方向解的是什么", val)
		}

		req, ok := tgt["requires"].([]string)
		if !ok || len(req) == 0 {
			t.Errorf("%s 没有声明必填字段", val)
			continue
		}
		for _, f := range req {
			if !known[f] {
				t.Errorf("%s 声明的必填字段 %q 不是引擎认识的入参名", val, f)
			}
			// 反解目标本身不该出现在自己的必填项里 —— 那是要求用户
			// 先填上他正想求解的那个值。
			if strings.HasPrefix(f, val) {
				t.Errorf("%s 把自己列进了必填字段 (%s)", val, f)
			}
		}
	}
}

// TestSolveTotalWaterAddsBackTheAbsorbedWater 验证总注水量反解把吸水量加了回来。
//
// 手冲的总注水量必然大于液重，差额就是粉层与滤纸留住的水。若少加这一项，
// 用户照着数字注水会得到一杯明显偏少的咖啡。
func TestSolveTotalWaterAddsBackTheAbsorbedWater(t *testing.T) {
	p := filterP(t)
	beverage := g(t, "300")
	dose := g(t, "20")

	total, err := SolveTotalWater(p, beverage, dose, p.LRR)
	if err != nil {
		t.Fatalf("反解总注水量失败: %v", err)
	}
	if total <= beverage {
		t.Errorf("总注水量 %s 应大于液重 %s —— 粉层要留住一部分水",
			total.Grams(), beverage.Grams())
	}

	// 差额应恰等于 粉量 × LRR，且是精确相等而非近似。
	absorbed := total - beverage
	want, err := fixed.MulMassRatio(dose, p.LRR)
	if err != nil {
		t.Fatalf("计算吸水量失败: %v", err)
	}
	if absorbed != want {
		t.Errorf("吸水量 %s 应精确等于 粉量×LRR = %s",
			absorbed.Grams(), want.Grams())
	}
}

// TestSolveTotalWaterRejectsNonPositiveInput 验证非正输入被拒而非静默算出怪值。
func TestSolveTotalWaterRejectsNonPositiveInput(t *testing.T) {
	p := filterP(t)
	cases := []struct {
		name     string
		beverage fixed.Mass
		dose     fixed.Mass
	}{
		{"液重为零", 0, g(t, "20")},
		{"粉量为零", g(t, "300"), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := SolveTotalWater(p, tc.beverage, tc.dose, p.LRR); err == nil {
				t.Error("应返回校验错误，而不是算出一个无意义的数")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 几何辅助函数
// ---------------------------------------------------------------------------

// TestFloorToAndCeilToHandleExactMultiples 验证正好落在步长上的值不被多推一格。
//
// 这是经典的 off-by-one：18.0 以 1 为步长向下取整仍应是 18.0，
// 若实现无条件减一，坐标轴会白多出一格空白。
func TestFloorToAndCeilToHandleExactMultiples(t *testing.T) {
	cases := []struct {
		v, step, floor, ceil float64
	}{
		{18.0, 1, 18.0, 18.0},
		{18.4, 1, 18.0, 19.0},
		{18.6, 1, 18.0, 19.0},
		{1.30, 0.1, 1.30, 1.30},
		{1.34, 0.1, 1.30, 1.40},
		{0.0, 1, 0.0, 0.0},
	}
	for _, c := range cases {
		if got := floorTo(c.v, c.step); math.Abs(got-c.floor) > 1e-9 {
			t.Errorf("floorTo(%g, %g) = %g，应为 %g", c.v, c.step, got, c.floor)
		}
		if got := ceilTo(c.v, c.step); math.Abs(got-c.ceil) > 1e-9 {
			t.Errorf("ceilTo(%g, %g) = %g，应为 %g", c.v, c.step, got, c.ceil)
		}
	}
}

// TestBuildTicksHonoursStepAndBounds 验证刻度生成遵守步长与边界。
func TestBuildTicksHonoursStepAndBounds(t *testing.T) {
	ticks := buildTicks(14, 26, 1)
	if len(ticks) == 0 {
		t.Fatal("14–26 步长 1 应生成刻度")
	}
	if ticks[0] < 14-0.5 {
		t.Errorf("首个刻度 %g 远低于下界 14", ticks[0])
	}
	if last := ticks[len(ticks)-1]; last > 26 {
		t.Errorf("末个刻度 %g 超出上界 26", last)
	}
	for i := 1; i < len(ticks); i++ {
		if d := ticks[i] - ticks[i-1]; math.Abs(d-1) > 1e-9 {
			t.Errorf("刻度间距 %g 不等于步长 1", d)
		}
	}

	// 步长非正时必须回落到一个可用值，而不是死循环或返回空。
	if got := buildTicks(0, 5, 0); len(got) == 0 {
		t.Error("步长为 0 时应回落到默认步长，而不是返回空刻度")
	}
	if got := buildTicks(0, 5, -1); len(got) == 0 {
		t.Error("步长为负时应回落到默认步长")
	}
}

// TestFormatScoreFRoundsToOneDecimal 验证评分展示保留一位小数。
func TestFormatScoreFRoundsToOneDecimal(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{43.0, "43.0"},
		{43.24, "43.2"},
		{43.25, "43.3"}, // 四舍五入进位
		{0, "0.0"},
		{60, "60.0"},
	}
	for _, c := range cases {
		if got := formatScoreF(c.in); got != c.want {
			t.Errorf("formatScoreF(%g) = %q，应为 %q", c.in, got, c.want)
		}
	}
}

// TestStrengthTickStepScalesWithMethod 验证浓度轴步长随冲煮法缩放。
//
// 意式浓度约 8%–12%，手冲约 1.15%–1.35%。两者共用一个步长的话，
// 其中一张图的网格会密到糊成一片，另一张则一根线都画不出来。
func TestStrengthTickStepScalesWithMethod(t *testing.T) {
	f, e := strengthTickStep(filterP(t)), strengthTickStep(espressoP(t))
	if !(e > f) {
		t.Errorf("意式浓度轴步长 %g 应大于手冲的 %g", e, f)
	}
	if f <= 0 || e <= 0 {
		t.Errorf("步长必须为正: 手冲 %g，意式 %g", f, e)
	}
}

// ---------------------------------------------------------------------------
// 三向反解的调度与自洽
// ---------------------------------------------------------------------------

// TestSolveRoundTripsThroughTheForwardEngine 验证反解与正算互为逆运算。
//
// 这是反解功能唯一真正重要的性质：把反解出的值填回正算，必须还原出
// 当初设定的目标。若两条路径的代数不一致，用户会照着"称 21.3g 粉"去做，
// 做完一测发现萃取率并不是他要的那个数 —— 而他没有任何办法知道是哪一步错了。
//
// 四个方向逐一往返，等于用正算给反解做了一次独立复核。
func TestSolveRoundTripsThroughTheForwardEngine(t *testing.T) {
	e := NewEngine(nil)
	const (
		targetYield = "20.00"
		tds         = "1.3000"
	)
	ty := fixed.MustRatioPercent(targetYield)

	t.Run("反解粉量", func(t *testing.T) {
		bev := g(t, "300")
		out, err := e.Solve(SolveRequest{
			Method: domain.MethodFilter, Target: SolveTargetDose,
			TargetYield: ty, TDS: fixed.MustRatioPercent(tds), Beverage: bev,
		})
		if err != nil {
			t.Fatalf("反解粉量失败: %v", err)
		}
		dose, err := fixed.ParseGrams(out.ValueRaw)
		if err != nil {
			t.Fatalf("反解结果 %q 无法回填表单: %v", out.ValueRaw, err)
		}
		res, err := e.Evaluate(Input{
			Method: domain.MethodFilter, Dose: dose,
			MeasuredBeverage: bev, TDS: fixed.MustRatioPercent(tds),
		}, nil)
		if err != nil {
			t.Fatalf("正算失败: %v", err)
		}
		if res.YieldText != targetYield {
			t.Errorf("反解出 %s 粉，正算回来的萃取率是 %s%%，应为 %s%% —— "+
				"反解与正算的代数不一致", out.ValueRaw, res.YieldText, targetYield)
		}
	})

	t.Run("反解液重", func(t *testing.T) {
		dose := g(t, "20")
		out, err := e.Solve(SolveRequest{
			Method: domain.MethodFilter, Target: SolveTargetBeverage,
			TargetYield: ty, TDS: fixed.MustRatioPercent(tds), Dose: dose,
		})
		if err != nil {
			t.Fatalf("反解液重失败: %v", err)
		}
		bev, err := fixed.ParseGrams(out.ValueRaw)
		if err != nil {
			t.Fatalf("反解结果 %q 无法回填表单: %v", out.ValueRaw, err)
		}
		res, err := e.Evaluate(Input{
			Method: domain.MethodFilter, Dose: dose,
			MeasuredBeverage: bev, TDS: fixed.MustRatioPercent(tds),
		}, nil)
		if err != nil {
			t.Fatalf("正算失败: %v", err)
		}
		if res.YieldText != targetYield {
			t.Errorf("反解出 %s 液重，正算回来的萃取率是 %s%%，应为 %s%%",
				out.ValueRaw, res.YieldText, targetYield)
		}
	})

	t.Run("反解浓度", func(t *testing.T) {
		// 这里刻意取 320g 而非 300g：20% × 20/320 = 1.25% 是有限小数，
		// 而 20% × 20/300 = 1.3333…% 不是。往返要严格相等，中间那个
		// 展示串就不能是被截断的循环小数 —— 那种情况单独在下一条测试里量化。
		dose, bev := g(t, "20"), g(t, "320")
		out, err := e.Solve(SolveRequest{
			Method: domain.MethodFilter, Target: SolveTargetTDS,
			TargetYield: ty, Dose: dose, Beverage: bev,
		})
		if err != nil {
			t.Fatalf("反解浓度失败: %v", err)
		}
		got, err := fixed.ParsePercent(out.ValueRaw)
		if err != nil {
			t.Fatalf("反解结果 %q 无法回填表单: %v", out.ValueRaw, err)
		}
		res, err := e.Evaluate(Input{
			Method: domain.MethodFilter, Dose: dose,
			MeasuredBeverage: bev, TDS: got,
		}, nil)
		if err != nil {
			t.Fatalf("正算失败: %v", err)
		}
		if res.YieldText != targetYield {
			t.Errorf("反解出 TDS %s%%，正算回来的萃取率是 %s%%，应为 %s%%",
				out.ValueRaw, res.YieldText, targetYield)
		}
	})

	t.Run("反解总注水量", func(t *testing.T) {
		dose, bev := g(t, "20"), g(t, "300")
		out, err := e.Solve(SolveRequest{
			Method: domain.MethodFilter, Target: SolveTargetTotalWater,
			Dose: dose, Beverage: bev,
		})
		if err != nil {
			t.Fatalf("反解总注水量失败: %v", err)
		}
		total, err := fixed.ParseGrams(out.ValueRaw)
		if err != nil {
			t.Fatalf("反解结果 %q 无法回填表单: %v", out.ValueRaw, err)
		}
		// 只给总注水量、不给实测液重，让引擎自己用 LRR 推回液重，
		// 结果应当正好是当初设定的 300g。
		res, err := e.Evaluate(Input{
			Method: domain.MethodFilter, Dose: dose,
			TotalWater: total, TDS: fixed.MustRatioPercent(tds),
		}, nil)
		if err != nil {
			t.Fatalf("正算失败: %v", err)
		}
		if res.BeverageText != bev.GramsPrecise() {
			t.Errorf("反解出总注水量 %s，正算推回的液重是 %s，应为 %s —— "+
				"持水系数在两条路径上用得不一致",
				out.ValueRaw, res.BeverageText, bev.GramsPrecise())
		}
	})
}

// TestSolveResultCarriesBothDisplayAndRefillableValues 验证结果同时给出展示串与可回填串。
//
// ValueText 带单位供展示，ValueRaw 不带单位供一键回填表单。若只给前者，
// 前端得自己剥 "g" 后缀 —— 单位一变就会悄悄剥错。
func TestSolveResultCarriesBothDisplayAndRefillableValues(t *testing.T) {
	e := NewEngine(nil)
	out, err := e.Solve(SolveRequest{
		Method: domain.MethodFilter, Target: SolveTargetDose,
		TargetYield: fixed.MustRatioPercent("20"),
		TDS:         fixed.MustRatioPercent("1.30"),
		Beverage:    g(t, "300"),
	})
	if err != nil {
		t.Fatalf("反解失败: %v", err)
	}
	if out.ValueText == "" || out.ValueRaw == "" {
		t.Fatalf("展示串或回填串为空: text=%q raw=%q", out.ValueText, out.ValueRaw)
	}
	if out.ValueText == out.ValueRaw {
		t.Errorf("展示串与回填串相同（%q）—— 展示串应带单位", out.ValueText)
	}
	if !strings.HasSuffix(out.ValueText, "g") {
		t.Errorf("粉量的展示串 %q 应带 g 单位", out.ValueText)
	}
	if strings.ContainsAny(out.ValueRaw, "g%") {
		t.Errorf("回填串 %q 不应含单位，否则填回表单会被校验拒绝", out.ValueRaw)
	}
	if _, err := fixed.ParseGrams(out.ValueRaw); err != nil {
		t.Errorf("回填串 %q 无法被自己的解析器接受: %v", out.ValueRaw, err)
	}
	if out.Explanation == "" {
		t.Error("反解结果没有解释文案 —— 用户不知道这个数字是怎么来的")
	}
	if out.Target != SolveTargetDose || out.Method != domain.MethodFilter {
		t.Errorf("结果没有回带请求上下文: target=%s method=%s", out.Target, out.Method)
	}
}

// TestSolveRejectsUnknownTarget 验证未知反解方向被明确拒绝。
func TestSolveRejectsUnknownTarget(t *testing.T) {
	e := NewEngine(nil)
	_, err := e.Solve(SolveRequest{
		Method: domain.MethodFilter, Target: SolveTarget("DOSE"), // 大写，非法
		TargetYield: fixed.MustRatioPercent("20"),
		TDS:         fixed.MustRatioPercent("1.30"),
		Beverage:    g(t, "300"),
	})
	if err == nil {
		t.Fatal("大写的 DOSE 应被拒绝，而不是当成某个默认方向静默处理")
	}
	if !strings.Contains(err.Error(), "tds") {
		t.Errorf("错误信息应列出合法取值，实际: %v", err)
	}
}

// TestSolvePropagatesFormulaErrors 验证公式层的拒绝不会被调度层吞掉。
//
// 反解层若把错误吞成零值，物理上不可达的目标会以"0g"的形式返回，
// 看起来像个正常答案。
func TestSolvePropagatesFormulaErrors(t *testing.T) {
	e := NewEngine(nil)
	impossible := fixed.MustRatioPercent("95") // 远超咖啡可溶物上限

	for _, tc := range []struct {
		name string
		req  SolveRequest
	}{
		{"粉量", SolveRequest{Target: SolveTargetDose, TDS: fixed.MustRatioPercent("1.30"), Beverage: g(t, "300")}},
		{"液重", SolveRequest{Target: SolveTargetBeverage, TDS: fixed.MustRatioPercent("1.30"), Dose: g(t, "20")}},
		{"浓度", SolveRequest{Target: SolveTargetTDS, Dose: g(t, "20"), Beverage: g(t, "300")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := tc.req
			req.Method = domain.MethodFilter
			req.TargetYield = impossible
			out, err := e.Solve(req)
			if err == nil {
				t.Fatalf("95%% 萃取率不可达，应报错；实际返回 %+v", out)
			}
			if out != nil {
				t.Error("出错时不应同时返回结果对象")
			}
		})
	}
}

// TestSolveBeverageMentionsTotalWaterForFilter 验证手冲的液重反解会顺带给出总注水量。
//
// 手冲用户手上只有总注水量这个可操作的量 —— 液重是接出来才知道的。
// 只告诉他"接到 308g 就停"而不说"总共注 348g"，等于没给可执行的指令。
func TestSolveBeverageMentionsTotalWaterForFilter(t *testing.T) {
	e := NewEngine(nil)
	out, err := e.Solve(SolveRequest{
		Method: domain.MethodFilter, Target: SolveTargetBeverage,
		TargetYield: fixed.MustRatioPercent("20"),
		TDS:         fixed.MustRatioPercent("1.30"),
		Dose:        g(t, "20"),
	})
	if err != nil {
		t.Fatalf("反解失败: %v", err)
	}
	if !strings.Contains(out.Explanation, "总注水量") {
		t.Errorf("手冲的液重反解应顺带给出总注水量，实际文案: %s", out.Explanation)
	}

	// 意式直接称液重，不需要也不应该出现持水换算。
	esp, err := e.Solve(SolveRequest{
		Method: domain.MethodEspresso, Target: SolveTargetBeverage,
		TargetYield: fixed.MustRatioPercent("20"),
		TDS:         fixed.MustRatioPercent("10.0"),
		Dose:        g(t, "18"),
	})
	if err != nil {
		t.Fatalf("意式反解失败: %v", err)
	}
	if strings.Contains(esp.Explanation, "持水") {
		t.Errorf("意式不经过粉层持水推导，文案不应提到它: %s", esp.Explanation)
	}
}

// TestSolveTDSRoundTripLossStaysWithinRefractometerNoise 量化循环小数被截断后的往返误差。
//
// 20% 萃取率、20g 粉、300g 液所需的浓度是 1.3333…%，展示串按折射仪的
// 读数精度截到两位小数（1.33%）。用户按 1.33% 冲出来的实际萃取率是
// 19.95%，与目标差 0.05 个百分点。
//
// 这不是精度缺陷，而是精度契约的边界：多给几位小数没有意义 —— 折射仪
// 本身的重复性约 ±0.01% TDS，换算到萃取率约 ±0.15 个百分点，比这个
// 截断误差还大三倍。这条测试的作用是把"误差落在测量噪声以内"钉住，
// 若哪天误差涨到肉眼可辨（比如展示串退化成一位小数），它会失败。
func TestSolveTDSRoundTripLossStaysWithinRefractometerNoise(t *testing.T) {
	e := NewEngine(nil)
	dose, bev := g(t, "20"), g(t, "300")
	target := fixed.MustRatioPercent("20.00")

	out, err := e.Solve(SolveRequest{
		Method: domain.MethodFilter, Target: SolveTargetTDS,
		TargetYield: target, Dose: dose, Beverage: bev,
	})
	if err != nil {
		t.Fatalf("反解浓度失败: %v", err)
	}

	// 内部值应当只受定点数分辨率限制，而不是被提前截成两位小数。
	//
	// fixed.Ratio 以 PPM 为单位，即百分数的分辨率是 1e-4。所以精确解
	// 1.333333…% 在内部落成 1.3333%，这是设计好的量化底噪，不是精度丢失；
	// 而若这里读到 1.33，说明两位小数的截断被提前带进了计算层。
	const exact = 4.0 / 3.0 // 20% × 20/300 = 1.3333…%
	const ppmInPercent = 1e-4
	if d := math.Abs(out.ValuePercent - exact); d > ppmInPercent {
		t.Errorf("内部浓度值 %g 偏离精确解 %.6f 达 %g，超过定点数分辨率 %g —— "+
			"展示用的两位小数截断被带进了计算层",
			out.ValuePercent, exact, d, ppmInPercent)
	}

	got, err := fixed.ParsePercent(out.ValueRaw)
	if err != nil {
		t.Fatalf("展示串 %q 无法回填: %v", out.ValueRaw, err)
	}
	res, err := e.Evaluate(Input{
		Method: domain.MethodFilter, Dose: dose,
		MeasuredBeverage: bev, TDS: got,
	}, nil)
	if err != nil {
		t.Fatalf("正算失败: %v", err)
	}

	drift := math.Abs(res.YieldPercent - 20.0)
	// 0.15 个百分点是折射仪重复性换算到萃取率的量级，见 curve.go 里
	// preferenceBinWidth 的注释。往返误差必须显著小于它。
	if drift > 0.15 {
		t.Errorf("往返误差 %.3f 个百分点已超过折射仪本身的重复性 0.15，"+
			"展示精度不够用了（浓度串为 %q）", drift, out.ValueRaw)
	}
	if drift == 0 {
		t.Log("注意：本例本应存在截断误差，误差为零说明展示精度或取值变了，" +
			"这条测试可能已失去意义")
	}
}
