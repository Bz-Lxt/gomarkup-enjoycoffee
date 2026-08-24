package fixed

import (
	"errors"
	"math/big"
	"testing"
)

// TestParseGramsExact 验证十进制文本 → 毫克定点数的转换是精确的。
func TestParseGramsExact(t *testing.T) {
	cases := []struct {
		in   string
		want Mass
	}{
		{"0", 0},
		{"1", 1000},
		{"18", 18000},
		{"18.5", 18500},
		{"0.1", 100},
		{"0.3", 300},
		{"0.001", 1},
		{"227.7", 227700},
		{"324", 324000},
		{"8.7", 8700},
		{"29.7", 29700},
		{"1000.999", 1000999},
		{"018.500", 18500},
		{"-5.25", -5250},
	}

	for _, c := range cases {
		got, err := ParseGrams(c.in)
		if err != nil {
			t.Fatalf("ParseGrams(%q) 返回错误: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseGrams(%q) = %d, 期望 %d", c.in, got, c.want)
		}
	}
}

// TestFloat64CannotRepresentTheseDecimals 记录 float64 与定点数在本项目里的
// 真实差别，并把这个差别的边界诚实地写下来。
//
// 需要先纠正一个常见的过度说法：float64 有约 15–16 位有效十进制数字，
// 而本项目需要的精度只有 7 位（PPM）。因此对单次萃取率计算，
// float64 给出的 PPM 值与精确解通常完全一致 —— 说"float64 会算错萃取率"
// 是不成立的，实测下来一次都不会。
//
// 定点数真正买到的是两件事：
//
//  1. **表示是精确的，而非近似到看不出来。** 下面断言的正是这一点：
//     float64 存的 227.7 在数值上并不等于十进制 227.7。它离得足够近，
//     所以格式化输出看不出差别，但"足够近"是一个依赖具体值的经验事实，
//     不是一条能证明的性质。
//  2. **边界判定由构造保证，而非碰巧成立。** 判断"萃取率是否 ≥ 18.0000%"
//     在整数下就是 `yield >= 180000`，精确且可证明。在 float64 下它
//     依赖被比较的两个值恰好朝同一方向舍入 —— 目前的确如此，
//     但没有任何东西保证换一组参数后仍然如此。
//
// 对一个把"是否落在金杯区间"作为核心结论输出给用户的系统，
// 判定的正确性应当来自构造而不是来自运气。这就是精度包存在的理由。
func TestFloat64CannotRepresentTheseDecimals(t *testing.T) {
	samples := []string{"227.7", "0.3", "8.7", "29.7", "0.29", "1.35", "18.5"}

	inexact := 0
	for _, s := range samples {
		want, ok := new(big.Rat).SetString(s)
		if !ok {
			t.Fatalf("样本 %q 不是合法十进制", s)
		}

		f, _, err := big.ParseFloat(s, 10, 53, big.ToNearestEven)
		if err != nil {
			t.Fatalf("ParseFloat(%q): %v", s, err)
		}
		f64, _ := f.Float64()
		// float64 实际承载的那个有理数
		actual := new(big.Rat).SetFloat64(f64)

		if actual.Cmp(want) != 0 {
			inexact++
			t.Logf("float64 无法精确表示 %s：实际存的是 %s",
				s, actual.FloatString(20))
		}

		// 定点数路径必须精确
		got, err := ParseGrams(s)
		if err != nil {
			t.Fatalf("ParseGrams(%q): %v", s, err)
		}
		exactMg := new(big.Rat).Mul(want, new(big.Rat).SetInt64(MassScale))
		if !exactMg.IsInt() {
			t.Fatalf("样本 %q 的毫克值不是整数，样本设计有误", s)
		}
		if exactMg.Num().Int64() != int64(got) {
			t.Errorf("ParseGrams(%q) = %d, 精确值 %s", s, got, exactMg.Num())
		}
	}

	if inexact == 0 {
		t.Error("这批样本本应包含 float64 无法精确表示的十进制值，" +
			"却一个都没有 —— 样本被改动过，这条论据已经失效")
	}
}

// TestParsePercentExact 验证百分数 → PPM 的转换。
func TestParsePercentExact(t *testing.T) {
	cases := []struct {
		in   string
		want Ratio
	}{
		{"0", 0},
		{"1", 10000},
		{"1.35", 13500},
		{"1.1125", 11125},
		{"18", 180000},
		{"20.75", 207500},
		{"22", 220000},
		{"9.5", 95000},
		{"11.5", 115000},
		{"1.2345", 12345},
		// 带百分号的输入应被容忍
		{"1.35%", 13500},
	}
	for _, c := range cases {
		got, err := ParsePercent(c.in)
		if err != nil {
			t.Fatalf("ParsePercent(%q) 错误: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParsePercent(%q) = %d, 期望 %d", c.in, got, c.want)
		}
	}
}

// TestParseMultipleExact 验证倍数 → PPM 的转换。
func TestParseMultipleExact(t *testing.T) {
	cases := []struct {
		in   string
		want Ratio
	}{
		{"1", 1000000},
		{"2", 2000000},
		{"2.0", 2000000},
		{"16", 16000000},
		{"16.5", 16500000},
		{"15.75", 15750000},
		{"1.85", 1850000},
	}
	for _, c := range cases {
		got, err := ParseMultiple(c.in)
		if err != nil {
			t.Fatalf("ParseMultiple(%q) 错误: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseMultiple(%q) = %d, 期望 %d", c.in, got, c.want)
		}
	}
}

// TestParseRejectsGarbage 验证非法输入被拒绝而非静默返回零。
//
// 静默返回零是这类解析函数最危险的失败模式：用户把粉量填成 "18g"，
// 系统当 0 处理，接着除零、或算出一个荒谬的萃取率却不报错。
func TestParseRejectsGarbage(t *testing.T) {
	bad := []string{
		"", " ", "abc", "18g", "1.2.3", "-", ".", "18.5.0",
		"1e3", "1E3", "0x12", "3/4", "１８", "18 5", "NaN", "Inf",
	}
	for _, s := range bad {
		if got, err := ParseGrams(s); err == nil {
			t.Errorf("ParseGrams(%q) 应当报错，却返回了 %d", s, got)
		} else if !errors.Is(err, ErrParse) {
			t.Errorf("ParseGrams(%q) 的错误应当可用 errors.Is(err, ErrParse) 判定，实际: %v", s, err)
		}
	}
}

// TestParseRejectsAstronomicalValues 验证超大输入在量化时被识别为溢出
// 而非静默回绕成一个荒谬的小数。
func TestParseRejectsAstronomicalValues(t *testing.T) {
	// 30 位数的克数乘以 1000 后远超 int64
	if got, err := ParseGrams("999999999999999999999999999999"); err == nil {
		t.Errorf("天文数字应当报溢出，却返回了 %d", got)
	} else if !errors.Is(err, ErrOverflow) {
		t.Errorf("应当是溢出错误，实际: %v", err)
	}
}

// TestSubMilligramInputRounds 记录亚毫克输入的行为：银行家舍入到毫克。
//
// 这是量化而非拒绝，理由是 0.1 毫克低于任何家用或专业咖啡秤的分辨率，
// 拒绝它只会让用户困惑；但舍入必须是银行家式的，否则大批数据会整体偏高。
func TestSubMilligramInputRounds(t *testing.T) {
	cases := []struct {
		in   string
		want Mass
	}{
		{"18.5001", 18500}, // 18500.1 mg → 18500
		{"18.5004", 18500},
		{"18.5006", 18501},
		{"18.5005", 18500}, // 恰好半点，18500.5 → 偶数 18500
		{"18.5015", 18502}, // 恰好半点，18501.5 → 偶数 18502
	}
	for _, c := range cases {
		got, err := ParseGrams(c.in)
		if err != nil {
			t.Fatalf("ParseGrams(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseGrams(%q) = %d, 期望 %d（银行家舍入到毫克）", c.in, got, c.want)
		}
	}
}

// TestBankersRounding 验证量化用的是银行家舍入而非四舍五入。
//
// 为何不用四舍五入：四舍五入对 .5 一律向上，在大量数据上产生系统性偏高。
// 本项目的偏好曲线会把几十条记录的萃取率聚合平均，一个恒定向上的偏差
// 会让"个人最优萃取率"整体偏高，而这个偏差是不可见的 —— 每个单值看起来都对。
func TestBankersRounding(t *testing.T) {
	cases := []struct {
		num, den int64
		want     Mass
	}{
		{5, 2, 2},   // 2.5 → 2（不是 3）
		{7, 2, 4},   // 3.5 → 4
		{9, 2, 4},   // 4.5 → 4（不是 5）
		{11, 2, 6},  // 5.5 → 6
		{-5, 2, -2}, // -2.5 → -2
		{-7, 2, -4}, // -3.5 → -4
		{7, 3, 2},   // 2.333 → 2
		{8, 3, 3},   // 2.667 → 3
	}

	for _, c := range cases {
		got, err := MassFromMilligramsRat(new(big.Rat).SetFrac64(c.num, c.den))
		if err != nil {
			t.Fatalf("MassFromMilligramsRat(%d/%d): %v", c.num, c.den, err)
		}
		if got != c.want {
			t.Errorf("量化 %d/%d = %d, 期望 %d（银行家舍入）",
				c.num, c.den, got, c.want)
		}
	}
}

// TestBankersRoundingHasNoBias 在统计上验证银行家舍入不引入系统性偏差。
//
// 对 x.5 形式的连续序列，四舍五入会让全部值都向上偏 0.5（总偏差 = n/2），
// 而银行家舍入的偏差应当在正负之间完全抵消。
func TestBankersRoundingHasNoBias(t *testing.T) {
	const n = 1000

	var sumRounded int64
	var sumExactX2 int64
	for i := int64(0); i < n; i++ {
		// 构造 i + 0.5，即 (2i+1)/2
		v, err := MassFromMilligramsRat(new(big.Rat).SetFrac64(2*i+1, 2))
		if err != nil {
			t.Fatalf("i=%d: %v", i, err)
		}
		sumRounded += int64(v)
		sumExactX2 += 2*i + 1
	}

	if 2*sumRounded != sumExactX2 {
		bias := sumRounded - sumExactX2/2
		t.Errorf("银行家舍入引入了 %d 的累积偏差（%d 个样本）。"+
			"四舍五入在此处的偏差会是 %d", bias, n, n/2)
	}
}

// TestFormatting 验证格式化输出的位数与可读性。
func TestFormatting(t *testing.T) {
	if got := Mass(18500).Grams(); got != "18.5" {
		t.Errorf("Mass(18500).Grams() = %q, 期望 \"18.5\"", got)
	}
	if got := Mass(18500).GramsPrecise(); got != "18.50" {
		t.Errorf("Mass(18500).GramsPrecise() = %q, 期望 \"18.50\"", got)
	}
	if got := Mass(227700).GramsPrecise(); got != "227.70" {
		t.Errorf("Mass(227700).GramsPrecise() = %q, 期望 \"227.70\"", got)
	}
	if got := Ratio(207500).Percent(); got != "20.75" {
		t.Errorf("Ratio(207500).Percent() = %q, 期望 \"20.75\"", got)
	}
	if got := Ratio(13500).Percent(); got != "1.35" {
		t.Errorf("Ratio(13500).Percent() = %q, 期望 \"1.35\"", got)
	}
	if got := Ratio(16000000).BrewRatioLabel(); got != "1:16.0" {
		t.Errorf("Ratio(16000000).BrewRatioLabel() = %q, 期望 \"1:16.0\"", got)
	}
	if got := Mass(-5250).GramsPrecise(); got != "-5.25" {
		t.Errorf("Mass(-5250).GramsPrecise() = %q, 期望 \"-5.25\"", got)
	}
}

// TestRoundTripStability 验证「解析 → 格式化 → 再解析」得到同一个值。
//
// 这条不变量保证前端把后端返回的格式化字符串再提交回来时不会漂移，
// 否则用户每保存一次记录，粉量就会掉一点小数。注意只对两位小数以内的
// 值成立 —— GramsPrecise 保留两位，第三位会被舍入掉，这是格式的固有损失，
// 因此测试样本限定在秤的实际分辨率范围内。
func TestRoundTripStability(t *testing.T) {
	inputs := []string{"18", "18.5", "227.7", "324", "0.01", "15.25", "-5.25"}
	for _, in := range inputs {
		m1, err := ParseGrams(in)
		if err != nil {
			t.Fatalf("ParseGrams(%q): %v", in, err)
		}
		m2, err := ParseGrams(m1.GramsPrecise())
		if err != nil {
			t.Fatalf("ParseGrams(%q): %v", m1.GramsPrecise(), err)
		}
		if m1 != m2 {
			t.Errorf("往返不稳定：%q → %d → %q → %d",
				in, m1, m1.GramsPrecise(), m2)
		}
	}
}

// TestArithmeticIsExact 验证四则运算不引入误差。
//
// 用一组会让浮点栽跟头的值走完「相除 → 相乘」的往返：
// 若中间经过 float64，回来的值几乎必然不等于起点。
func TestArithmeticIsExact(t *testing.T) {
	dose := Mass(18000)      // 18g
	beverage := Mass(288000) // 288g

	// 粉液比 = 液重 / 粉量 = 16
	ratio, err := DivMass(beverage, dose)
	if err != nil {
		t.Fatalf("DivMass: %v", err)
	}
	if ratio != 16000000 {
		t.Fatalf("288g / 18g = %d, 期望 16000000 PPM（即 16 倍）", ratio)
	}

	// 反向：粉量 × 粉液比应精确回到液重
	back, err := MulMassRatio(dose, ratio)
	if err != nil {
		t.Fatalf("MulMassRatio: %v", err)
	}
	if back != beverage {
		t.Errorf("往返不精确：18g × %s = %d mg, 期望 %d mg",
			ratio.Multiple(), back, beverage)
	}
}

// TestSingleExpressionYieldIsExact 是 NFR-03 的基础断言：
// 萃取率在「一整条 big.Rat 表达式算完再量化一次」的路径下，
// 与有理数精确解完全相等（断言 ==，不是 epsilon 比较）。
func TestSingleExpressionYieldIsExact(t *testing.T) {
	cases := []struct {
		name       string
		doseG      string
		beverageG  string
		tdsPercent string
		wantPPM    Ratio
	}{
		{"18g / 288g / 1.25%", "18", "288", "1.25", 200000},
		{"18g / 288g / 1.30%", "18", "288", "1.30", 208000},
		{"18g / 288g / 1.35%", "18", "288", "1.35", 216000},
		{"18g / 288g / 1.11%", "18", "288", "1.11", 177600},
		{"18g / 36g / 9.5% 意式", "18", "36", "9.5", 190000},
		{"18g / 27g / 11.5% ristretto", "18", "27", "11.5", 172500},
		// 下面两组的精确解是无限小数，量化结果必须与银行家舍入一致
		{"20.5g / 320.4g / 1.28%", "20.5", "320.4", "1.28", 200055},
		{"19.3g / 300g / 1.29%", "19.3", "300", "1.29", 200518},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dose, err := ParseGrams(c.doseG)
			if err != nil {
				t.Fatal(err)
			}
			bev, err := ParseGrams(c.beverageG)
			if err != nil {
				t.Fatal(err)
			}
			tds, err := ParsePercent(c.tdsPercent)
			if err != nil {
				t.Fatal(err)
			}

			// 一条表达式算到底，只在最后量化一次
			exact := new(big.Rat).Mul(bev.Rat(), tds.Rat())
			exact.Quo(exact, dose.Rat())
			got, err := RatioFromRat(exact)
			if err != nil {
				t.Fatal(err)
			}

			if got != c.wantPPM {
				t.Errorf("萃取率 = %d PPM (%s%%), 期望 %d PPM",
					got, got.Percent(), c.wantPPM)
			}
		})
	}
}

// TestChainedQuantizationIsLossy 证明中间量化会引入误差，
// 把「引擎必须一条 big.Rat 表达式算完再量化一次」这条约束
// 变成一个有测试守着的规则，而不只是注释里的一句建议。
//
// 反面写法是把公式拆成两次公开 API 调用：
//
//	dissolved := MulMassRatio(beverage, tds)  // 量化到毫克
//	yield := DivMass(dissolved, dose)         // 再量化到 PPM
//
// 第一步把溶解物质量截断到毫克，丢掉的部分在第二步被除以粉量放大。
// 18g 粉的情况下 1 毫克的截断会变成约 55 PPM 的萃取率偏差。
// 值不大，但它是系统性的，而且刚好足以让恰好落在 18% 边界上的记录判定翻转。
func TestChainedQuantizationIsLossy(t *testing.T) {
	dose := Mass(18000) // 18g
	bev := Mass(288000) // 288g
	tds := Ratio(11100) // 1.11%

	dissolved, err := MulMassRatio(bev, tds)
	if err != nil {
		t.Fatal(err)
	}
	chained, err := DivMass(dissolved, dose)
	if err != nil {
		t.Fatal(err)
	}

	exactRat := new(big.Rat).Mul(bev.Rat(), tds.Rat())
	exactRat.Quo(exactRat, dose.Rat())
	exact, err := RatioFromRat(exactRat)
	if err != nil {
		t.Fatal(err)
	}

	if exact != 177600 {
		t.Fatalf("精确解应为 177600 PPM（17.76%%），实际 %d", exact)
	}

	if chained == exact {
		t.Fatalf("这组参数本应暴露中间量化的误差，但两条路径都给出 %d PPM。"+
			"样本失效了，需要换一组能触发毫克截断的参数", exact)
	}

	drift := int64(chained) - int64(exact)
	t.Logf("中间量化引入了 %+d PPM（%+.4f 个百分点）的偏差：分步 %d vs 精确 %d",
		drift, float64(drift)/10000, chained, exact)
}

// TestClamp 验证区间钳制的边界行为。
func TestClamp(t *testing.T) {
	lo, hi := Ratio(180000), Ratio(220000)
	cases := []struct{ in, want Ratio }{
		{170000, 180000},
		{180000, 180000},
		{200000, 200000},
		{220000, 220000},
		{230000, 220000},
	}
	for _, c := range cases {
		if got := Clamp(c.in, lo, hi); got != c.want {
			t.Errorf("Clamp(%d, %d, %d) = %d, 期望 %d", c.in, lo, hi, got, c.want)
		}
	}
}
