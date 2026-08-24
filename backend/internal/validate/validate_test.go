package validate

import (
	"strings"
	"testing"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

func newC() *Collector { return New("TEST", "测试校验") }

// TestAllFieldErrorsAreReportedAtOnce 是这个包存在的理由。
//
// 表单类接口若逐个报错，用户要"改一处、提交、被拒、再改一处"地循环。
// 收集器的意义就是一次说完全部问题。这条测试如果失败，说明某处提前
// return 了，用户体验会退回到逐轮试错。
func TestAllFieldErrorsAreReportedAtOnce(t *testing.T) {
	c := newC()
	c.RequiredGrams("dose", "")
	c.Percent("tds", "很浓")
	c.Multiple("ratio", "2:16")
	c.IntRange("temp_c", 200, 80, 100, false)
	c.NonEmpty("name", "   ")

	err := c.Err()
	if err == nil {
		t.Fatal("五个字段全有问题，应返回错误")
	}
	de := domain.AsDomain(err)
	if len(de.Fields) != 5 {
		t.Errorf("应一次性报出 5 项字段错误，实际 %d 项：%v", len(de.Fields), de.Fields)
	}

	// 每条错误都要指名字段，否则前端无法定位到具体输入框
	for _, f := range de.Fields {
		if f.Field == "" || f.Reason == "" {
			t.Errorf("字段错误缺少字段名或原因：%+v", f)
		}
	}
}

// TestNoErrorsMeansNilNotEmptyError 确认无错误时返回 nil。
//
// 返回一个"字段列表为空的错误"会让调用方的 if err != nil 全部误判，
// 每个合法请求都会被拒。
func TestNoErrorsMeansNilNotEmptyError(t *testing.T) {
	c := newC()
	c.Grams("dose", "18")
	c.Percent("tds", "1.35")

	if c.HasErrors() {
		t.Error("全部输入合法时不应有错误")
	}
	if err := c.Err(); err != nil {
		t.Errorf("无错误时应返回 nil，实际 %v", err)
	}
}

// TestGramsParsingIsExactNotApproximate 是 NFR-03 在 API 边界上的落地。
//
// 精度承诺始于这里：若入口就把 "18.5" 变成了 18.499999，
// 后面用有理数算得再准也没有意义。
func TestGramsParsingIsExactNotApproximate(t *testing.T) {
	cases := map[string]int64{
		"18":     18000,
		"18.5":   18500,
		"0.001":  1,
		"227.7":  227700,
		"1000":   1000000,
		"18.123": 18123,
	}
	for raw, wantMg := range cases {
		c := newC()
		got := c.Grams("dose", raw)
		if c.HasErrors() {
			t.Errorf("%q 应解析成功，实际报错 %v", raw, domain.AsDomain(c.Err()).Fields)
			continue
		}
		if int64(got) != wantMg {
			t.Errorf("%q 应精确解析为 %d 毫克，实际 %d", raw, wantMg, int64(got))
		}
	}
}

// TestGramsRejectsGarbageInsteadOfDefaultingToZero 确认脏输入被拒而不是静默归零。
//
// 静默归零最坏：粉量变成 0 会让萃取率计算除零，或者算出一个荒谬但"看起来
// 是个数"的结果。用户完全不知道自己填的值被丢了。
func TestGramsRejectsGarbageInsteadOfDefaultingToZero(t *testing.T) {
	bad := []string{
		"十八克",
		"18g",    // 带单位
		"18.5.5", // 两个小数点
		"1e3",    // 科学计数法
		"18,5",   // 逗号当小数点
		"--18",
		"0x12",
		"18.1234", // 超过三位小数
		"Infinity",
		"NaN",
	}
	for _, raw := range bad {
		c := newC()
		c.Grams("dose", raw)
		if !c.HasErrors() {
			t.Errorf("%q 应被拒绝，实际静默接受了", raw)
		}
	}
}

// TestNegativeValuesAreRejected 确认负数被拒。
//
// 负粉量、负浓度在物理上无意义，但在数学上会算出"看起来正常"的萃取率。
func TestNegativeValuesAreRejected(t *testing.T) {
	c := newC()
	c.Grams("dose", "-18")
	c.Percent("tds", "-1.35")
	c.Multiple("ratio", "-16")

	de := domain.AsDomain(c.Err())
	if len(de.Fields) != 3 {
		t.Errorf("三个负值都应被拒，实际报出 %d 项：%v", len(de.Fields), de.Fields)
	}
}

// TestEmptyIsTreatedAsUnprovidedNotZero 区分"没填"与"填了 0"。
//
// 可选字段（如 TDS）留空是正常的，此时应走推算模式；
// 而把它当 0 会让引擎以为用户测出了 0% 浓度。
func TestEmptyIsTreatedAsUnprovidedNotZero(t *testing.T) {
	c := newC()
	tds := c.Percent("tds", "")
	dose := c.Grams("dose", "   ")

	if c.HasErrors() {
		t.Errorf("可选字段留空不应报错，实际 %v", domain.AsDomain(c.Err()).Fields)
	}
	if tds != 0 || dose != 0 {
		t.Error("留空应返回零值供调用方判断「未提供」")
	}
}

// TestRequiredGramsRejectsBothEmptyAndZero 确认必填字段两种缺失都拦住。
func TestRequiredGramsRejectsBothEmptyAndZero(t *testing.T) {
	for _, raw := range []string{"", "   ", "0", "0.0", "0.000"} {
		c := newC()
		c.RequiredGrams("dose", raw)
		if !c.HasErrors() {
			t.Errorf("必填克数为 %q 应被拒绝（0 克无法作为分母）", raw)
		}
	}
}

// TestRequiredGramsReportsOneReasonNotTwo 确认一个字段不会被报两次。
//
// 空串既触发"必填"又可能触发"必须大于 0"。前端若按字段名渲染错误，
// 同一个输入框下会出现两条红字。
func TestRequiredGramsReportsOneReasonNotTwo(t *testing.T) {
	c := newC()
	c.RequiredGrams("dose", "abc")

	de := domain.AsDomain(c.Err())
	count := 0
	for _, f := range de.Fields {
		if f.Field == "dose" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("同一字段应只报一条原因，实际 %d 条：%v", count, de.Fields)
	}
}

// TestPercentToleratesTrailingSign 确认粘进来的百分号被容忍。
//
// 用户从别处复制 "1.35%" 是极常见的操作。因为一个百分号拒绝整个表单，
// 属于没必要的摩擦。
func TestPercentToleratesTrailingSign(t *testing.T) {
	c := newC()
	withSign := c.Percent("tds", "1.35%")
	plain := c.Percent("tds2", "1.35")

	if c.HasErrors() {
		t.Fatalf("带百分号的输入应被容忍，实际 %v", domain.AsDomain(c.Err()).Fields)
	}
	if withSign != plain {
		t.Errorf("\"1.35%%\" 与 \"1.35\" 应解析为同一值，实际 %d vs %d",
			int64(withSign), int64(plain))
	}
}

// TestRatioAcceptsCoffeeShorthand 确认粉液比支持 1:16 写法。
//
// 咖啡圈几乎没人写 "16"，都写 "1:16"。强迫用户删掉前缀是把领域惯例
// 让给了解析器的方便。
func TestRatioAcceptsCoffeeShorthand(t *testing.T) {
	c := newC()
	shorthand := c.Multiple("ratio", "1:16")
	plain := c.Multiple("ratio2", "16")

	if c.HasErrors() {
		t.Fatalf("1:16 写法应被接受，实际 %v", domain.AsDomain(c.Err()).Fields)
	}
	if shorthand != plain {
		t.Errorf("\"1:16\" 与 \"16\" 应解析为同一值，实际 %d vs %d",
			int64(shorthand), int64(plain))
	}

	// 带小数与空格的变体也要能用
	for _, raw := range []string{"1:16.5", "1 : 16.5", "1.0:16.5"} {
		cc := newC()
		v := cc.Multiple("ratio", raw)
		if cc.HasErrors() {
			t.Errorf("%q 应被接受，实际 %v", raw, domain.AsDomain(cc.Err()).Fields)
			continue
		}
		if v != fixed.MustRatioMultiple("16.5") {
			t.Errorf("%q 应解析为 16.5 倍，实际 %s", raw, v.Multiple())
		}
	}
}

// TestRatioRejectsNonUnitNumerator 确认 2:16 这类写法被明确拒绝。
//
// 静默把它当 16 会让用户以为自己填的 2:16（等价 1:8）生效了，
// 而实际算的是 1:16 —— 一倍的差距，且界面上看不出来。
func TestRatioRejectsNonUnitNumerator(t *testing.T) {
	for _, raw := range []string{"2:16", "3:20", "16:1"} {
		c := newC()
		c.Multiple("ratio", raw)
		if !c.HasErrors() {
			t.Errorf("%q 的分子不是 1，应被明确拒绝而不是当成 1:N 处理", raw)
		}
	}
}

// TestIntRangeDistinguishesZeroFromOutOfRange 确认 0 的两种含义分开处理。
//
// 水温 0℃ 是"没填"（可选字段），而水温 200℃ 是"填错了"。
// 同一个校验函数要能表达这两种语义。
func TestIntRangeDistinguishesZeroFromOutOfRange(t *testing.T) {
	// zeroOK：0 表示未填写，放过
	c := newC()
	if got := c.IntRange("temp_c", 0, 80, 100, true); got != 0 || c.HasErrors() {
		t.Errorf("zeroOK 时 0 应被放过表示未填写，实际值 %d 错误 %v",
			got, c.HasErrors())
	}

	// 不允许 0 时，0 是越界
	c2 := newC()
	c2.IntRange("temp_c", 0, 80, 100, false)
	if !c2.HasErrors() {
		t.Error("不允许 0 时，0 低于下限 80，应被拒绝")
	}

	// 边界值合法
	for _, v := range []int{80, 100} {
		c3 := newC()
		if got := c3.IntRange("temp_c", v, 80, 100, false); got != v || c3.HasErrors() {
			t.Errorf("%d 是合法边界值，实际被拒", v)
		}
	}
	// 越界
	for _, v := range []int{79, 101, 200, -5} {
		c4 := newC()
		c4.IntRange("temp_c", v, 80, 100, false)
		if !c4.HasErrors() {
			t.Errorf("%d 越出 80–100，应被拒绝", v)
		}
	}
}

// TestIntRangeMessageStatesTheBounds 确认提示语给出具体范围。
//
// "水温超出范围"没有信息量，用户只能试。"必须在 80–100 之间"是可执行的。
func TestIntRangeMessageStatesTheBounds(t *testing.T) {
	c := newC()
	c.IntRange("temp_c", 200, 80, 100, false)

	de := domain.AsDomain(c.Err())
	if len(de.Fields) == 0 {
		t.Fatal("应报错")
	}
	reason := de.Fields[0].Reason
	if !strings.Contains(reason, "80") || !strings.Contains(reason, "100") {
		t.Errorf("提示语应给出具体上下限，实际 %q", reason)
	}
}

// TestMaxLenCountsRunesNotBytes 确认长度按字符而非字节计。
//
// 风味笔记几乎全是中文。若按字节计，1000 字节的上限只够写 333 个汉字，
// 用户会莫名其妙地被拦住。
func TestMaxLenCountsRunesNotBytes(t *testing.T) {
	// 400 个汉字 = 1200 字节
	note := strings.Repeat("酸", 400)

	c := newC()
	c.MaxLen("note", note, 1000)
	if c.HasErrors() {
		t.Errorf("400 个汉字在 1000 字符上限内应通过，实际被拒：%v",
			domain.AsDomain(c.Err()).Fields)
	}

	c2 := newC()
	c2.MaxLen("note", strings.Repeat("酸", 1001), 1000)
	if !c2.HasErrors() {
		t.Error("1001 个汉字应超出 1000 字符上限")
	}
}

// TestEnumParsingIsCaseInsensitive 确认枚举解析容忍大小写。
//
// 前端传 "filter" 还是 "FILTER" 属于实现细节，不该成为一次 400。
func TestEnumParsingIsCaseInsensitive(t *testing.T) {
	for _, raw := range []string{"FILTER", "filter", "Filter", " filter "} {
		c := newC()
		got := c.BrewMethod("method", raw, domain.MethodEspresso)
		if c.HasErrors() {
			t.Errorf("%q 应被接受，实际 %v", raw, domain.AsDomain(c.Err()).Fields)
			continue
		}
		if got != domain.MethodFilter {
			t.Errorf("%q 应解析为 FILTER，实际 %s", raw, got)
		}
	}
}

// TestEnumDefaultAppliesOnlyToEmpty 确认默认值只对空串生效。
//
// 若非法值也悄悄落到默认值，用户填错的 "pourover" 会被当成 "FILTER"
// 处理 —— 这次恰好对，下次填错别的就不对了，而且永远不会有报错。
func TestEnumDefaultAppliesOnlyToEmpty(t *testing.T) {
	c := newC()
	if got := c.BrewMethod("method", "", domain.MethodFilter); got != domain.MethodFilter {
		t.Errorf("空串应落到默认值 FILTER，实际 %s", got)
	}
	if c.HasErrors() {
		t.Error("空串走默认值不应报错")
	}

	c2 := newC()
	c2.BrewMethod("method", "pourover", domain.MethodFilter)
	if !c2.HasErrors() {
		t.Error("不认识的冲煮法应报错而不是静默落到默认值")
	}
}

// TestUnknownEnumMessageListsValidOptions 确认枚举报错列出合法取值。
//
// "未知的烘焙度" 让用户去猜；列出 LIGHT/MEDIUM/DARK 让他直接改对。
func TestUnknownEnumMessageListsValidOptions(t *testing.T) {
	c := newC()
	c.RoastLevel("roast_level", "超级深烘")

	de := domain.AsDomain(c.Err())
	if len(de.Fields) == 0 {
		t.Fatal("应报错")
	}
	reason := de.Fields[0].Reason
	if !strings.Contains(strings.ToUpper(reason), "LIGHT") {
		t.Errorf("提示语应列出合法取值，实际 %q", reason)
	}
}

// TestCivilDateGoesThroughBeijingTimezone 确认日期解析走北京时区。
//
// 这是 GMT+8 规范在 API 入口的落点：用户填的 "2026-08-24" 是北京的
// 8 月 24 日，不是 UTC 的。
func TestCivilDateGoesThroughBeijingTimezone(t *testing.T) {
	c := newC()
	d := c.CivilDate("roasted_on", "2026-08-24")

	if c.HasErrors() {
		t.Fatalf("合法日期应解析成功，实际 %v", domain.AsDomain(c.Err()).Fields)
	}
	if d.Year != 2026 || int(d.Month) != 8 || d.Day != 24 {
		t.Errorf("应解析为 2026-08-24，实际 %v", d)
	}
	// 还原成时刻应是北京零点
	ts := d.Time()
	if _, offset := ts.Zone(); offset != 8*3600 {
		t.Errorf("还原的时刻应在 +8 时区，实际偏移 %d 秒", offset)
	}
}

// TestCivilDateRejectsWrongFormat 确认日期格式被严格约束。
func TestCivilDateRejectsWrongFormat(t *testing.T) {
	for _, raw := range []string{"2026/08/24", "24-08-2026", "2026-13-01", "昨天"} {
		c := newC()
		c.CivilDate("roasted_on", raw)
		if !c.HasErrors() {
			t.Errorf("%q 应被拒绝", raw)
		}
	}

	// 可选日期留空是合法的（未开封的豆没有开封日）
	c := newC()
	if d := c.CivilDate("opened_on", ""); !d.IsZero() || c.HasErrors() {
		t.Error("可选日期留空应返回零值且不报错")
	}
}

// TestRequiredCivilDateRejectsEmpty 确认必填日期不能留空。
func TestRequiredCivilDateRejectsEmpty(t *testing.T) {
	c := newC()
	c.RequiredCivilDate("roasted_on", "")
	if !c.HasErrors() {
		t.Error("烘焙日期是必填的 —— 没有它整个生命周期看板都算不出来")
	}
}

// TestFreshnessStagesFilterRejectsUnknown 确认筛选参数里的未知枚举被拒。
//
// 静默丢弃一个筛选条件会让结果集变宽，用户以为筛选生效了。
func TestFreshnessStagesFilterRejectsUnknown(t *testing.T) {
	c := newC()
	got := c.FreshnessStages("stages", []string{"PEAK", "DEGASSING"})
	if c.HasErrors() {
		t.Fatalf("合法阶段应通过，实际 %v", domain.AsDomain(c.Err()).Fields)
	}
	if len(got) != 2 {
		t.Errorf("应解析出 2 个阶段，实际 %d 个", len(got))
	}

	c2 := newC()
	c2.FreshnessStages("stages", []string{"PEAK", "SUPER_FRESH"})
	if !c2.HasErrors() {
		t.Error("未知阶段应报错，静默丢弃会让筛选结果比用户预期的宽")
	}
}

// TestEmptyEnumListIsNotAnError 确认不传筛选条件是合法的。
func TestEmptyEnumListIsNotAnError(t *testing.T) {
	c := newC()
	stages := c.FreshnessStages("stages", nil)
	levels := c.RoastLevels("roast_levels", []string{})

	if c.HasErrors() {
		t.Errorf("不传筛选条件应合法（表示不过滤），实际 %v",
			domain.AsDomain(c.Err()).Fields)
	}
	if len(stages) != 0 || len(levels) != 0 {
		t.Error("空筛选条件应返回空切片")
	}
}

// TestPourTechniqueIsStrictOnTheFormPath 锁住表单路径与实时路径的有意不对称。
//
// 表单提交时报错是对的：用户能看到高亮并改掉，成本几秒钟。
// 而 ws.Hub.handleMark 在冲煮进行中收到未知手法时会静默回落 ——
// 那一刻的时间与水量一去不返，为一个描述性标签丢掉整条数据不成比例。
//
// 两处行为不同容易被后来者当成 bug"修平"。这条测试和 validate.go 里的
// 注释一起，把它记录为一个决定而不是一处疏漏。
func TestPourTechniqueIsStrictOnTheFormPath(t *testing.T) {
	c := newC()
	c.PourTechnique("technique", "螺旋升天大法")
	if !c.HasErrors() {
		t.Error("表单路径上的未知注水手法应报错，让用户当场改掉")
	}

	// 空串走默认值，不算错误：手法是可选标注
	c2 := newC()
	if got := c2.PourTechnique("technique", ""); !got.Valid() || c2.HasErrors() {
		t.Errorf("留空应落到合法默认值且不报错，实际 %q / 错误 %v",
			got, c2.HasErrors())
	}
}

// TestOverPreciseInputIsRejectedNotSilentlyRounded 确认超出存储分辨率的
// 输入被拒，而不是被悄悄舍入。
//
// 毫克是克数的存储分辨率。用户填 18.1234 时，量化会把它变成 18.123 ——
// 对内部计算是正确的，但作为对用户输入的应答就是"吞掉了一位数字且不说"。
// 他保存后回看发现数字变了，无从判断是自己看错还是系统改的。
func TestOverPreciseInputIsRejectedNotSilentlyRounded(t *testing.T) {
	cases := []struct {
		field string
		raw   string
		parse func(*Collector, string, string)
	}{
		{"dose", "18.1234", func(c *Collector, f, r string) { c.Grams(f, r) }},
		{"tds", "1.35001", func(c *Collector, f, r string) { c.Percent(f, r) }},
		{"ratio", "16.1234567", func(c *Collector, f, r string) { c.Multiple(f, r) }},
	}
	for _, tc := range cases {
		c := newC()
		tc.parse(c, tc.field, tc.raw)
		if !c.HasErrors() {
			t.Errorf("%s=%q 的精度超过存储分辨率，应被拒绝而非静默舍入",
				tc.field, tc.raw)
			continue
		}
		reason := domain.AsDomain(c.Err()).Fields[0].Reason
		if !strings.Contains(reason, "小数") {
			t.Errorf("%s 的提示语应说明小数位数限制，实际 %q", tc.field, reason)
		}
	}

	// 恰好在分辨率上的输入必须放过
	c := newC()
	c.Grams("dose", "18.123")
	c.Percent("tds", "1.3500")
	c.Multiple("ratio", "16.123456")
	if c.HasErrors() {
		t.Errorf("恰好在分辨率上的输入应通过，实际 %v",
			domain.AsDomain(c.Err()).Fields)
	}
}
