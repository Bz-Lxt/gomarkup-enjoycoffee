// Package validate 把 API 层收到的字符串输入转换为定点数与枚举，
// 并把转换过程中的全部失败一次性收集成字段级错误清单。
//
// 为何数值字段走字符串而不走 JSON number：JSON 的 number 在 encoding/json 里
// 落进 float64，而 float64 无法精确表示大多数十进制小数 —— 227.7 实际存的是
// 227.69999999999998863（见 fixed 包的 TestFloat64CannotRepresentTheseDecimals）。
//
// 这个决策的边界需要说清，避免把理由讲得比事实更严重：float64 有约 15 位
// 有效十进制数字，而本项目只需要 7 位（PPM）。单次萃取率计算走 float64
// 得到的 PPM 值与精确解通常是一致的 —— 它不会算错。但"不会算错"依赖
// 具体数值恰好朝同一方向舍入，不是一条能证明的性质。
//
// 精度包（internal/fixed）提供的是构造上的精确：一个十进制输入对应唯一整数，
// "是否 ≥ 18.0000%" 就是一次整数比较。对一个把金杯区间判定当作核心结论
// 输出给用户的系统，这个判定应当由构造保证而非由巧合保证。而这条链要成立，
// 前提是精度包拿到的是原始十进制文本，所以约定必须从 HTTP 边界就开始执行。
package validate

import (
	"strconv"
	"strings"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

// Collector 累积字段级校验失败。
//
// 一次性返回全部错误而非遇错即返：用户填错三个字段时，
// 让他改一次提交一次连报三轮，是很糟糕的体验。
type Collector struct {
	code   string
	msg    string
	fields []domain.FieldError
}

// New 构造收集器。
func New(code, msg string) *Collector {
	return &Collector{code: code, msg: msg}
}

// Add 记录一条字段错误。
func (c *Collector) Add(field, reason string) {
	c.fields = append(c.fields, domain.FieldError{Field: field, Reason: reason})
}

// HasErrors 是否已有失败。
func (c *Collector) HasErrors() bool { return len(c.fields) > 0 }

// Err 返回聚合后的领域错误，无失败时返回 nil。
func (c *Collector) Err() error {
	if len(c.fields) == 0 {
		return nil
	}
	e := domain.Validation(c.code, c.msg)
	e.Fields = c.fields
	return e
}

// ---- 定点数解析 ----

// Grams 解析克数字符串为毫克定点数。空串视为未提供，返回零值且不报错。
func (c *Collector) Grams(field, raw string) fixed.Mass {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if !c.decimalsWithin(field, raw, 3, "克数", "毫克") {
		return 0
	}
	v, err := fixed.ParseGrams(raw)
	if err != nil {
		c.Add(field, "必须是形如 18 或 18.5 的克数，最多三位小数")
		return 0
	}
	if v < 0 {
		c.Add(field, "不能为负数")
		return 0
	}
	return v
}

// RequiredGrams 同 Grams，但空串或零值视为失败。
func (c *Collector) RequiredGrams(field, raw string) fixed.Mass {
	if strings.TrimSpace(raw) == "" {
		c.Add(field, "必填")
		return 0
	}
	v := c.Grams(field, raw)
	if v == 0 && !c.hasField(field) {
		c.Add(field, "必须大于 0")
	}
	return v
}

// Percent 解析百分数字符串为 PPM 定点数。空串视为未提供。
func (c *Collector) Percent(field, raw string) fixed.Ratio {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	// 容忍用户把百分号一起粘进来
	raw = strings.TrimSuffix(raw, "%")
	if !c.decimalsWithin(field, raw, 4, "百分数", "0.0001%") {
		return 0
	}
	v, err := fixed.ParsePercent(raw)
	if err != nil {
		c.Add(field, "必须是形如 1.35 的百分数，最多四位小数")
		return 0
	}
	if v < 0 {
		c.Add(field, "不能为负数")
		return 0
	}
	return v
}

// Multiple 解析倍数字符串（粉液比、持水系数）为 PPM 定点数。
func (c *Collector) Multiple(field, raw string) fixed.Ratio {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	// 支持 "1:16" 这种咖啡圈惯用写法。用户手打粉液比时几乎总会带上前缀 1:，
	// 强迫他删掉是没有必要的摩擦。
	if idx := strings.Index(raw, ":"); idx >= 0 {
		head := strings.TrimSpace(raw[:idx])
		if head != "1" && head != "1.0" {
			c.Add(field, "比例写法只支持 1:N 形式")
			return 0
		}
		raw = strings.TrimSpace(raw[idx+1:])
	}

	if !c.decimalsWithin(field, raw, 6, "倍数", "0.000001 倍") {
		return 0
	}
	v, err := fixed.ParseMultiple(raw)
	if err != nil {
		c.Add(field, "必须是形如 16 或 2.0 的倍数，最多六位小数")
		return 0
	}
	if v < 0 {
		c.Add(field, "不能为负数")
		return 0
	}
	return v
}

// ---- 标量校验 ----

// IntRange 校验整数落在闭区间内。zeroOK 为真时允许 0 表示"未填写"。
func (c *Collector) IntRange(field string, v, min, max int, zeroOK bool) int {
	if v == 0 && zeroOK {
		return 0
	}
	if v < min || v > max {
		c.Add(field, "必须在 "+strconv.Itoa(min)+"–"+strconv.Itoa(max)+" 之间")
		return 0
	}
	return v
}

// NonEmpty 校验必填字符串。
func (c *Collector) NonEmpty(field, raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		c.Add(field, "必填")
	}
	return v
}

// MaxLen 校验字符串长度上限（按 rune 计，中文一字算一个）。
func (c *Collector) MaxLen(field, raw string, max int) string {
	v := strings.TrimSpace(raw)
	if n := len([]rune(v)); n > max {
		c.Add(field, "最长 "+strconv.Itoa(max)+" 个字符，当前 "+strconv.Itoa(n))
	}
	return v
}

// ---- 枚举解析 ----

// BrewMethod 解析冲煮法。空串时返回 def。
func (c *Collector) BrewMethod(field, raw string, def domain.BrewMethod) domain.BrewMethod {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	if raw == "" {
		return def
	}
	m := domain.BrewMethod(raw)
	if !m.Valid() {
		c.Add(field, "只支持 FILTER 或 ESPRESSO")
		return def
	}
	return m
}

// RoastLevel 解析烘焙度。
func (c *Collector) RoastLevel(field, raw string) domain.RoastLevel {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	if raw == "" {
		c.Add(field, "必填")
		return ""
	}
	lv := domain.RoastLevel(raw)
	if !lv.Valid() {
		c.Add(field, "必须是 LIGHT / LIGHT_MEDIUM / MEDIUM / MEDIUM_DARK / DARK / VERY_DARK 之一")
		return ""
	}
	return lv
}

// CivilDate 解析 YYYY-MM-DD 民用日期。空串视为未提供。
func (c *Collector) CivilDate(field, raw string) domain.CivilDate {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return domain.CivilDate{}
	}
	d, err := domain.ParseCivilDate(raw)
	if err != nil {
		c.Add(field, "必须是 YYYY-MM-DD 格式的日期")
		return domain.CivilDate{}
	}
	return d
}

// RequiredCivilDate 同 CivilDate，但空串视为失败。
func (c *Collector) RequiredCivilDate(field, raw string) domain.CivilDate {
	if strings.TrimSpace(raw) == "" {
		c.Add(field, "必填")
		return domain.CivilDate{}
	}
	return c.CivilDate(field, raw)
}

// PourTechnique 解析注水手法。空串返回 CIRCLE，未知值报错。
//
// 与 ws.Hub.handleMark 的宽松回落刻意不同，这个不对称是有意的：
//
//   - 这里是表单提交路径。用户填错了标签，界面上高亮那一行让他改掉，
//     成本是几秒钟。
//   - handleMark 是冲煮进行中的实时打点路径。那一刻的时间与水量一去不返，
//     为一个描述性标签丢掉整条数据是不成比例的，所以那里回落到默认值。
//
// 若哪天要统一，正确的方向是让这里也回落 —— 而不是让实时路径变严。
func (c *Collector) PourTechnique(field, raw string) domain.PourTechnique {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	if raw == "" {
		return domain.PourCircle
	}
	t := domain.PourTechnique(raw)
	if !t.Valid() {
		c.Add(field, "不是受支持的注水手法")
		return domain.PourCircle
	}
	return t
}

// FreshnessStages 解析新鲜度阶段列表。
func (c *Collector) FreshnessStages(field string, raws []string) []domain.FreshnessStage {
	if len(raws) == 0 {
		return nil
	}
	out := make([]domain.FreshnessStage, 0, len(raws))
	for _, raw := range raws {
		s := domain.FreshnessStage(strings.ToUpper(strings.TrimSpace(raw)))
		if !s.Valid() {
			c.Add(field, "包含未知的新鲜度阶段 "+raw)
			continue
		}
		out = append(out, s)
	}
	return out
}

// RoastLevels 解析烘焙度列表。
func (c *Collector) RoastLevels(field string, raws []string) []domain.RoastLevel {
	if len(raws) == 0 {
		return nil
	}
	out := make([]domain.RoastLevel, 0, len(raws))
	for _, raw := range raws {
		lv := domain.RoastLevel(strings.ToUpper(strings.TrimSpace(raw)))
		if !lv.Valid() {
			c.Add(field, "包含未知的烘焙度 "+raw)
			continue
		}
		out = append(out, lv)
	}
	return out
}

// decimalsWithin 拦住小数位数超过存储分辨率的输入。
//
// 为何必须在这一层拦：fixed 层的量化会把 "18.1234" 银行家舍入成 18123 毫克，
// 这对内部计算是正确行为（量化总要发生在某处）。但作为用户输入的应答，
// 静默舍入意味着用户填的第四位小数被吞掉了而界面上毫无提示 ——
// 他保存后回看会发现数字变了，且无从判断是自己看错还是系统改的。
//
// 提示语里带上真实分辨率，用户才知道该填到几位。
func (c *Collector) decimalsWithin(field, raw string, max int, kind, resolution string) bool {
	dot := strings.IndexByte(raw, '.')
	if dot < 0 {
		return true
	}
	if n := len(raw) - dot - 1; n > max {
		c.Add(field, kind+"最多保留 "+strconv.Itoa(max)+" 位小数（存储分辨率为 "+
			resolution+"），当前填了 "+strconv.Itoa(n)+" 位")
		return false
	}
	return true
}

func (c *Collector) hasField(field string) bool {
	for _, f := range c.fields {
		if f.Field == field {
			return true
		}
	}
	return false
}
