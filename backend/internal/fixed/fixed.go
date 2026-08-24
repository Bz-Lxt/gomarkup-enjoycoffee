// Package fixed 提供咖啡萃取计算所需的定点数与精确有理数运算原语。
//
// 设计动因（源自 docs/Requirements.md §4.2）：萃取率公式是连续除法与乘法的链式组合，
// 若以 float64 承载，1.35% 这类十进制小数本身无法被二进制浮点精确表示，链式运算会
// 累积可观测误差；更糟的是误差方向不确定，导致同一杯咖啡在不同调用顺序下落到金杯
// 区间的不同侧。
//
// 本包的契约：
//   - 对外暴露的量值一律为定点整数（int64），无浮点。
//   - 一切中间运算在 math/big.Rat 上进行，全程零精度损失。
//   - 仅在最终量化为定点整数时舍入一次，采用银行家舍入（round-half-to-even）以
//     避免 round-half-up 在大样本上引入的系统性正偏移。
//
// 本包禁止 import 任何浮点转换函数用于业务路径。float64 仅允许出现在
// ApproxFloat 这类明确标注"仅供展示/日志"的出口。
package fixed

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
)

// PPMScale 是 Ratio 的定点标度：1 个单位 Ratio 代表百万分之一。
//
// 选择百万分率而非万分率，是为了让浓度这类小量值保留足够有效位：
// 手冲 TDS 典型值 1.15%–1.35%，即 11500–13500 PPM，仍有 4 位以上有效数字；
// 若用万分率则只剩 115–135，链式运算后的量化误差会吃掉判定精度。
const PPMScale = 1_000_000

// MassScale 是 Mass 的定点标度：1 个单位 Mass 代表 1 毫克。
//
// 咖啡秤的实际分辨率是 0.1g（家用）到 0.01g（专业），毫克级留出两个数量级余量，
// 保证"总注水量 - 粉量×持水系数"这类减法不会因标度不足而丢失尾数。
const MassScale = 1000

var (
	// ErrOverflow 表示运算结果超出 int64 定点范围。
	ErrOverflow = errors.New("fixed: 定点数溢出 int64 值域")
	// ErrDivideByZero 表示除数为零。这在萃取计算中通常意味着粉量为 0，属业务非法输入。
	ErrDivideByZero = errors.New("fixed: 除零")
	// ErrNegative 表示出现了业务上不允许的负值（如负的液重）。
	ErrNegative = errors.New("fixed: 出现业务非法负值")
	// ErrParse 表示字符串无法解析为定点数。
	ErrParse = errors.New("fixed: 无法解析为定点数")
)

// Mass 是以毫克为单位的质量定点数。用于粉量、注水量、液重、出液量。
type Mass int64

// Ratio 是以百万分率（PPM）为单位的无量纲比值定点数。
//
// 它承载两类语义完全不同但量纲相同的量：
//   - 百分比类：TDS 浓度、萃取率。1.35% → 13_500
//   - 倍数类：粉液比、持水系数。1:16 → 16_000_000；LRR 2.0 → 2_000_000
//
// 两类语义共用一个类型是有意的：它们在公式中会直接相乘相除，
// 分成两个类型会迫使调用方反复转换，反而更容易出错。
// 语义区分由变量命名与 Percent()/Multiple() 两个不同的格式化出口承担。
type Ratio int64

// ---------------------------------------------------------------------------
// 有理数互转：所有精确运算的入口与出口
// ---------------------------------------------------------------------------

// Rat 把质量转为以克为单位的精确有理数。
func (m Mass) Rat() *big.Rat {
	return big.NewRat(int64(m), MassScale)
}

// RawRat 把质量转为以毫克为单位的精确有理数。
// 公式推导中若两侧量纲一致，用 RawRat 可减少一次标度乘除。
func (m Mass) RawRat() *big.Rat {
	return new(big.Rat).SetInt64(int64(m))
}

// Rat 把比值转为无量纲的精确有理数。1.35% 的 Ratio(13500) 返回 27/2000。
func (r Ratio) Rat() *big.Rat {
	return big.NewRat(int64(r), PPMScale)
}

// RawRat 把比值转为以 PPM 为单位的精确有理数。
func (r Ratio) RawRat() *big.Rat {
	return new(big.Rat).SetInt64(int64(r))
}

// MassFromGramsRat 把以克为单位的有理数量化为 Mass。
func MassFromGramsRat(v *big.Rat) (Mass, error) {
	scaled := new(big.Rat).Mul(v, new(big.Rat).SetInt64(MassScale))
	n, err := quantize(scaled)
	if err != nil {
		return 0, err
	}
	return Mass(n), nil
}

// MassFromMilligramsRat 把以毫克为单位的有理数量化为 Mass。
func MassFromMilligramsRat(v *big.Rat) (Mass, error) {
	n, err := quantize(v)
	if err != nil {
		return 0, err
	}
	return Mass(n), nil
}

// RatioFromRat 把无量纲有理数量化为 Ratio。
func RatioFromRat(v *big.Rat) (Ratio, error) {
	scaled := new(big.Rat).Mul(v, new(big.Rat).SetInt64(PPMScale))
	n, err := quantize(scaled)
	if err != nil {
		return 0, err
	}
	return Ratio(n), nil
}

// RatioFromPPMRat 把以 PPM 为单位的有理数量化为 Ratio。
func RatioFromPPMRat(v *big.Rat) (Ratio, error) {
	n, err := quantize(v)
	if err != nil {
		return 0, err
	}
	return Ratio(n), nil
}

// ---------------------------------------------------------------------------
// 银行家舍入
// ---------------------------------------------------------------------------

// quantize 把有理数舍入到最近的整数，平局时舍向偶数，并检查 int64 溢出。
//
// 为何是银行家舍入：萃取记录是长期累积的数据集，用户可能有上千条冲煮记录。
// round-half-up 在每个 .5 平局点都向上偏移，统计上会把萃取率均值系统性推高，
// 长期看会让用户误判自己的冲煮偏过萃。舍向偶数使平局的偏移在正负方向上抵消。
func quantize(v *big.Rat) (int64, error) {
	num := v.Num()
	den := v.Denom()

	quo, rem := new(big.Int).QuoRem(num, den, new(big.Int))

	if rem.Sign() != 0 {
		// 比较 2*|rem| 与 den（big.Rat 的分母恒为正），判断落在半点的哪一侧
		twiceRem := new(big.Int).Abs(rem)
		twiceRem.Lsh(twiceRem, 1)

		roundAway := false
		switch twiceRem.Cmp(den) {
		case 1:
			roundAway = true
		case -1:
			roundAway = false
		case 0:
			// 恰好落在半点：向偶数取整。quo 与 quo±1 奇偶必然相反，
			// 故 quo 为奇数时远离零舍入即可得到偶数。
			roundAway = quo.Bit(0) == 1
		}

		if roundAway {
			if v.Sign() < 0 {
				quo.Sub(quo, big.NewInt(1))
			} else {
				quo.Add(quo, big.NewInt(1))
			}
		}
	}

	if !quo.IsInt64() {
		return 0, fmt.Errorf("%w: %s", ErrOverflow, quo.String())
	}
	return quo.Int64(), nil
}

// ---------------------------------------------------------------------------
// 解析：用户输入 → 定点数
// ---------------------------------------------------------------------------

// ParseGrams 解析形如 "18.5" / "18" / "-0.25" 的克数字符串为 Mass。
//
// 使用 big.Rat.SetString 而非 strconv.ParseFloat：后者会先落入 float64，
// 而 float64 存不下大多数十进制小数（"18.3" 实际是 18.300000000000000711）。
// 在毫克这个标度上单次转换通常仍能回到正确的整数，但那是数值恰好朝
// 同一方向舍入的结果，不是有保证的行为。走有理数则精确性由构造给出。
func ParseGrams(s string) (Mass, error) {
	r, err := parseDecimal(s)
	if err != nil {
		return 0, err
	}
	return MassFromGramsRat(r)
}

// ParsePercent 解析形如 "1.35" / "18" / "1.35%" 的百分数字符串为 Ratio。
// 输入语义是"百分号前面的数"，即 "1.35" 表示 1.35%，对应 Ratio(13500)。
func ParsePercent(s string) (Ratio, error) {
	s = strings.TrimSuffix(strings.TrimSpace(s), "%")
	r, err := parseDecimal(s)
	if err != nil {
		return 0, err
	}
	// 百分数 → 无量纲比值：除以 100
	r.Quo(r, new(big.Rat).SetInt64(100))
	return RatioFromRat(r)
}

// ParseMultiple 解析形如 "16" / "2.0" / "1.85" 的倍数字符串为 Ratio。
// 用于粉液比与持水系数，"16" 表示 16 倍，对应 Ratio(16_000_000)。
func ParseMultiple(s string) (Ratio, error) {
	r, err := parseDecimal(s)
	if err != nil {
		return 0, err
	}
	return RatioFromRat(r)
}

func parseDecimal(s string) (*big.Rat, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("%w: 空字符串", ErrParse)
	}
	// 拒绝科学计数法与十六进制：用户输入场景下它们几乎总是笔误或注入尝试，
	// big.Rat.SetString 却会接受 "1e400" 并构造出天文数字，量化时才报溢出。
	if strings.ContainsAny(s, "eExXpP/") {
		return nil, fmt.Errorf("%w: 不接受科学计数法或分数形式 %q", ErrParse, s)
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrParse, s)
	}
	return r, nil
}

// ---------------------------------------------------------------------------
// 格式化：定点数 → 展示字符串
// ---------------------------------------------------------------------------

// Grams 以克为单位格式化，保留 1 位小数（对齐家用咖啡秤分辨率）。
func (m Mass) Grams() string { return formatScaled(int64(m), MassScale, 1) }

// GramsPrecise 以克为单位格式化，保留 2 位小数（对齐专业咖啡秤分辨率）。
func (m Mass) GramsPrecise() string { return formatScaled(int64(m), MassScale, 2) }

// Percent 把比值格式化为百分数字符串（不含 % 号），保留 2 位小数。
// Ratio(13500) → "1.35"
func (r Ratio) Percent() string {
	// 无量纲 → 百分数需乘 100，等价于把标度从 1e6 降到 1e4
	return formatScaled(int64(r), PPMScale/100, 2)
}

// Multiple 把比值格式化为倍数字符串，保留 2 位小数。Ratio(16_000_000) → "16.00"
func (r Ratio) Multiple() string { return formatScaled(int64(r), PPMScale, 2) }

// BrewRatioLabel 把粉液比渲染为咖啡圈惯用的 "1:16.0" 形式。
func (r Ratio) BrewRatioLabel() string {
	return "1:" + formatScaled(int64(r), PPMScale, 1)
}

// formatScaled 把定点整数按给定标度渲染为固定小数位的十进制字符串。
// 全程整数运算，不经过 float。
func formatScaled(v int64, scale int64, decimals int) string {
	neg := v < 0

	// 用 big.Int 承载，避免 v 接近 int64 边界时取绝对值溢出
	abs := new(big.Int).Abs(big.NewInt(v))
	scaleBig := big.NewInt(scale)

	// 先把值放大到目标小数位，再一次性舍入，避免两次舍入叠加
	pow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	abs.Mul(abs, pow)

	quo, rem := new(big.Int).QuoRem(abs, scaleBig, new(big.Int))
	twice := new(big.Int).Lsh(rem, 1)
	if c := twice.Cmp(scaleBig); c > 0 || (c == 0 && quo.Bit(0) == 1) {
		quo.Add(quo, big.NewInt(1))
	}

	digits := quo.String()
	var sb strings.Builder
	if neg && quo.Sign() != 0 {
		sb.WriteByte('-')
	}
	if decimals == 0 {
		sb.WriteString(digits)
		return sb.String()
	}
	for len(digits) <= decimals {
		digits = "0" + digits
	}
	sb.WriteString(digits[:len(digits)-decimals])
	sb.WriteByte('.')
	sb.WriteString(digits[len(digits)-decimals:])
	return sb.String()
}

// ---------------------------------------------------------------------------
// 仅供展示与日志的浮点出口
// ---------------------------------------------------------------------------

// ApproxPercentFloat 返回百分数的 float64 近似值。
//
// 严格限定用途：JSON 输出中供前端直接绘图的冗余字段、日志可读性。
// 禁止把返回值再喂回任何计算路径 —— 那会把本包规避掉的精度问题重新引入。
func (r Ratio) ApproxPercentFloat() float64 {
	return float64(r) / float64(PPMScale/100)
}

// ApproxMultipleFloat 返回倍数的 float64 近似值，用途限制同 ApproxPercentFloat。
// 典型消费方是前端绘制粉液比坐标轴。
func (r Ratio) ApproxMultipleFloat() float64 {
	return float64(r) / float64(PPMScale)
}

// ApproxGramsFloat 返回克数的 float64 近似值，用途限制同 ApproxPercentFloat。
func (m Mass) ApproxGramsFloat() float64 {
	return float64(m) / float64(MassScale)
}

// ---------------------------------------------------------------------------
// 定点数基础算术：显式溢出检查，不静默回绕
// ---------------------------------------------------------------------------

// AddMass 返回两个质量之和，溢出时报错而非静默回绕。
func AddMass(a, b Mass) (Mass, error) {
	s := a + b
	// 同号相加而结果异号即溢出
	if (a > 0 && b > 0 && s < 0) || (a < 0 && b < 0 && s > 0) {
		return 0, ErrOverflow
	}
	return s, nil
}

// SubMass 返回两个质量之差。
func SubMass(a, b Mass) (Mass, error) {
	if b == math.MinInt64 {
		return 0, ErrOverflow
	}
	return AddMass(a, -b)
}

// MulMassRatio 计算 质量 × 无量纲比值，结果仍为质量。
// 典型用途：粉量 × 持水系数 = 咖啡粉吸走的水量。
func MulMassRatio(m Mass, r Ratio) (Mass, error) {
	product := new(big.Rat).Mul(m.RawRat(), r.Rat())
	return MassFromMilligramsRat(product)
}

// DivMass 计算 被除质量 ÷ 除质量，结果为无量纲比值。
// 典型用途：液重 ÷ 粉量 = 粉液比。
func DivMass(num, den Mass) (Ratio, error) {
	if den == 0 {
		return 0, ErrDivideByZero
	}
	q := new(big.Rat).Quo(num.RawRat(), den.RawRat())
	return RatioFromRat(q)
}

// MulRatio 计算两个无量纲比值之积。
func MulRatio(a, b Ratio) (Ratio, error) {
	return RatioFromRat(new(big.Rat).Mul(a.Rat(), b.Rat()))
}

// DivRatio 计算两个无量纲比值之商。
func DivRatio(num, den Ratio) (Ratio, error) {
	if den == 0 {
		return 0, ErrDivideByZero
	}
	return RatioFromRat(new(big.Rat).Quo(num.Rat(), den.Rat()))
}

// Clamp 把比值限制在 [lo, hi] 闭区间内。
func Clamp(v, lo, hi Ratio) Ratio {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// MustRatioPercent 是仅供包内常量初始化使用的便捷构造器。
// 它在解析失败时 panic —— 这只会在源码写错常量时触发，属于编译期级别的错误。
func MustRatioPercent(s string) Ratio {
	r, err := ParsePercent(s)
	if err != nil {
		panic("fixed: 常量百分数字面量非法: " + s + ": " + err.Error())
	}
	return r
}

// MustRatioMultiple 同 MustRatioPercent，用于倍数类常量。
func MustRatioMultiple(s string) Ratio {
	r, err := ParseMultiple(s)
	if err != nil {
		panic("fixed: 常量倍数字面量非法: " + s + ": " + err.Error())
	}
	return r
}
