package domain

import (
	"testing"
	"time"
)

// TestDayCountIsCivilNotElapsedHours 是本项目最容易被写错的一条时间语义。
//
// "烘后第 N 天"是民用自然日之差，不是"经过了多少个 24 小时"。
// 昨天 23:00 烘的豆，今天 01:00 就是"第 1 天"，尽管只过了 2 小时。
// 若用 time.Since()/24h 实现，这里会得到 0，整条养豆判定跟着错一天。
func TestDayCountIsCivilNotElapsedHours(t *testing.T) {
	roasted := CivilDate{2026, time.August, 23}

	// 只过了 2 小时，但跨了一个民用日
	nextDayEarly := time.Date(2026, time.August, 24, 1, 0, 0, 0, Beijing)
	if got := roasted.DaysSince(nextDayEarly); got != 1 {
		t.Errorf("8/23 到 8/24 01:00 应为第 1 天（跨了一个自然日），实际 %d 天。"+
			"若为 0，说明用的是经过小时数除以 24 而不是自然日之差", got)
	}

	// 过了将近 24 小时，但仍是同一民用日
	sameDayLate := time.Date(2026, time.August, 23, 23, 59, 0, 0, Beijing)
	if got := roasted.DaysSince(sameDayLate); got != 0 {
		t.Errorf("8/23 当天 23:59 仍应为第 0 天，实际 %d 天", got)
	}
}

// TestMorningQueryDoesNotLoseADay 直接锁住 GMT+8 规范想防的那个 bug。
//
// 北京时间 00:00–07:59 对应的是前一个 UTC 日。若日期折算走了 UTC，
// 用户在早上八点前打开豆库看板，每一支豆的日龄都会少一天 ——
// 而且到了八点自己就"好了"，是最难复现的一类缺陷。
func TestMorningQueryDoesNotLoseADay(t *testing.T) {
	roasted := CivilDate{2026, time.August, 1}

	// 北京 8/24 07:00 == UTC 8/23 23:00
	beijingMorning := time.Date(2026, time.August, 24, 7, 0, 0, 0, Beijing)
	if beijingMorning.UTC().Day() != 23 {
		t.Fatalf("测试前提不成立：北京 8/24 07:00 应对应 UTC 8/23，实际 UTC %v",
			beijingMorning.UTC())
	}

	got := roasted.DaysSince(beijingMorning)
	if got != 23 {
		t.Errorf("北京时间 8/24 早上查询，8/1 烘的豆应为第 23 天，实际 %d 天。"+
			"差一天说明日期折算掉到了 UTC 日", got)
	}

	// 同一天的晚上必须得到同一个答案
	beijingEvening := time.Date(2026, time.August, 24, 22, 0, 0, 0, Beijing)
	if evening := roasted.DaysSince(beijingEvening); evening != got {
		t.Errorf("同一民用日的早晚查询必须一致：早上 %d 天，晚上 %d 天", got, evening)
	}
}

// TestCivilDateIgnoresIncomingTimezone 确认从任意时区的时刻折算民用日期时
// 都先归一到北京时区。
//
// 数据库驱动返回的 time.Time 常常带着 UTC 位置，而不是写入时的时区。
// 折算若不显式 In(Beijing)，同一条记录读回来的日期就会和写进去的不同。
func TestCivilDateIgnoresIncomingTimezone(t *testing.T) {
	// 同一个物理时刻的三种表示
	instant := time.Date(2026, time.August, 24, 2, 30, 0, 0, Beijing)

	cases := []struct {
		name string
		t    time.Time
	}{
		{"北京时区", instant},
		{"UTC", instant.UTC()},
		{"纽约", instant.In(time.FixedZone("EDT", -4*3600))},
	}

	want := CivilDate{2026, time.August, 24}
	for _, c := range cases {
		if got := ToCivilDate(c.t); got != want {
			t.Errorf("%s 表示的同一时刻应折算为 %v，实际 %v", c.name, want, got)
		}
	}
}

// TestRoundTripThroughDatabaseTimezone 模拟"写入北京日期 → 数据库以 UTC 返回
// → 再折算回民用日期"的完整往返。
func TestRoundTripThroughDatabaseTimezone(t *testing.T) {
	original := CivilDate{2026, time.August, 24}

	// CivilDate.Time() 给出北京零点，即 UTC 前一日 16:00
	stored := original.Time()
	if stored.UTC().Day() != 23 || stored.UTC().Hour() != 16 {
		t.Fatalf("北京 8/24 00:00 应为 UTC 8/23 16:00，实际 %v", stored.UTC())
	}

	// 驱动把它当 UTC 读回来
	readBack := stored.UTC()
	if got := ToCivilDate(readBack); got != original {
		t.Errorf("经数据库 UTC 往返后日期应保持 %v，实际 %v。"+
			"不一致会让豆子的烘焙日在保存后自己变成前一天", original, got)
	}
}

// TestParseCivilDateRejectsGarbage 确认日期解析不悄悄接受奇怪输入。
func TestParseCivilDateRejectsGarbage(t *testing.T) {
	good, err := ParseCivilDate("2026-08-24")
	if err != nil {
		t.Fatalf("合法日期解析失败: %v", err)
	}
	if good != (CivilDate{2026, time.August, 24}) {
		t.Errorf("解析结果错误: %v", good)
	}

	bad := []string{
		"2026/08/24",            // 分隔符错
		"24-08-2026",            // 顺序错
		"2026-13-01",            // 月份越界
		"2026-02-30",            // 该月无此日
		"",                      // 空串
		"today",                 // 自然语言
		"2026-08-24 ",           // 尾随空格
		"2026-08-24T00: 00:00Z", // 完整时刻
	}
	for _, s := range bad {
		if _, err := ParseCivilDate(s); err == nil {
			t.Errorf("%q 应被拒绝，实际解析通过了。悄悄接受会把一个错误日期"+
				"写进豆子记录，之后所有养豆判定都基于它", s)
		}
	}
}

// TestAddDaysCrossesMonthAndYearBoundaries 检查日期加减跨月跨年。
//
// 深烘豆的衰退日是烘后 45 天，横跨月末是常态。
func TestAddDaysCrossesMonthAndYearBoundaries(t *testing.T) {
	cases := []struct {
		from CivilDate
		add  int
		want CivilDate
	}{
		{CivilDate{2026, time.January, 20}, 45, CivilDate{2026, time.March, 6}},
		{CivilDate{2026, time.December, 20}, 45, CivilDate{2027, time.February, 3}},
		// 2028 是闰年，2 月有 29 天
		{CivilDate{2028, time.February, 28}, 1, CivilDate{2028, time.February, 29}},
		{CivilDate{2026, time.February, 28}, 1, CivilDate{2026, time.March, 1}},
		{CivilDate{2026, time.August, 24}, -30, CivilDate{2026, time.July, 25}},
	}

	for _, c := range cases {
		if got := c.from.AddDays(c.add); got != c.want {
			t.Errorf("%v + %d 天应为 %v，实际 %v", c.from, c.add, c.want, got)
		}
	}
}

// TestDaysSinceIsConsistentWithAddDays 检查两个方向互为逆运算。
//
// 生命周期计算里两者都在用：AddDays 算出衰退日期给用户看，
// DaysSince 算出当前日龄做判定。若二者不一致，看板上会出现
// "显示还有 3 天到期，但状态已经是已衰退"这类自相矛盾。
func TestDaysSinceIsConsistentWithAddDays(t *testing.T) {
	base := CivilDate{2026, time.August, 24}

	for n := 0; n <= 400; n++ {
		future := base.AddDays(n)
		if got := base.DaysSince(future.Time()); got != n {
			t.Fatalf("base + %d 天后再求日龄应得 %d，实际 %d（日期 %v）",
				n, n, got, future)
		}
	}
}

// TestFormatDisplayUsesBeijingAndProjectFormat 确认展示格式与时区都符合规范。
func TestFormatDisplayUsesBeijingAndProjectFormat(t *testing.T) {
	// UTC 表示的时刻，展示时必须转回北京
	utc := time.Date(2026, time.August, 23, 16, 30, 45, 0, time.UTC)
	if got := FormatDisplay(utc); got != "2026-08-24 00:30:45" {
		t.Errorf("UTC 8/23 16:30:45 展示为北京时间应是 2026-08-24 00:30:45，实际 %q",
			got)
	}

	if got := FormatDisplay(time.Time{}); got != "" {
		t.Errorf("零值时刻应展示为空串而非 0001-01-01，实际 %q", got)
	}
}

// TestNowIsInBeijing 确认业务时钟落在北京时区。
func TestNowIsInBeijing(t *testing.T) {
	n := Now()
	_, offset := n.Zone()
	if offset != 8*3600 {
		t.Errorf("Now() 的时区偏移应为 +8 小时（28800 秒），实际 %d 秒", offset)
	}
}

// TestZeroDateIsDetectable 确认"未填写"能与合法日期区分开。
//
// 烘焙日期是可空字段。若零值被当成合法日期（公元 1 年 1 月 1 日），
// 生命周期会算出七十多万天的日龄，而不是提示用户补填。
func TestZeroDateIsDetectable(t *testing.T) {
	if !(CivilDate{}).IsZero() {
		t.Error("零值 CivilDate 应报告 IsZero")
	}
	if (CivilDate{2026, time.August, 24}).IsZero() {
		t.Error("合法日期不应报告 IsZero")
	}
	// 1 年 1 月 1 日是 time.Time 的零值对应日期，但作为 CivilDate
	// 它的 Year 非 0，因此不算未填写 —— 这是刻意的：用户真填了个荒谬日期
	// 和没填是两种情况，提示语不同。
	if (CivilDate{1, time.January, 1}).IsZero() {
		t.Error("公元 1 年是荒谬但已填写的日期，不应与未填写混为一谈")
	}
}
