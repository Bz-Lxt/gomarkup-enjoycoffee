package domain

import "time"

// Beijing 是全项目唯一的民用时区基准（GMT+8）。
//
// 为何必须显式指定：养豆天数是"民用自然日"之差，不是"经过了多少个 24 小时"。
// 若用 UTC 的 Year/Month/Day 取日期，北京时间 00:00–07:59 之间发生的事件会被
// 归到前一个 UTC 日，导致"开封第 7 天"在早晨查询时显示为第 6 天。
// 对一个以"排气期第几天"为核心判定的应用，这是实质性的正确性缺陷而非显示瑕疵。
var Beijing = loadBeijing()

func loadBeijing() *time.Location {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return loc
	}
	// 容器镜像未安装 tzdata 时兜底。中国大陆自 1991 年起无夏令时，
	// 固定 +8 偏移与 Asia/Shanghai 在可预见范围内语义等价。
	return time.FixedZone("CST", 8*3600)
}

// Now 返回当前北京时间。业务代码一律用它，不直接调 time.Now()。
func Now() time.Time { return time.Now().In(Beijing) }

// CivilDate 是不含时刻的民用日期，用于豆子的烘焙日与开封日。
type CivilDate struct {
	Year  int
	Month time.Month
	Day   int
}

// ToCivilDate 把时刻折算为北京时区下的民用日期。
func ToCivilDate(t time.Time) CivilDate {
	b := t.In(Beijing)
	return CivilDate{Year: b.Year(), Month: b.Month(), Day: b.Day()}
}

// Time 把民用日期还原为该日北京时间零点。
func (d CivilDate) Time() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, Beijing)
}

// String 按前端统一日期格式渲染（knowledge-base/client.md 要求 yyyy-MM-dd）。
func (d CivilDate) String() string { return d.Time().Format("2006-01-02") }

// IsZero 报告是否为未设置的零值日期。
func (d CivilDate) IsZero() bool { return d.Year == 0 && d.Day == 0 }

// DaysSince 返回从 d 到 to 经过的民用自然日数。
//
// 语义定义：同一天返回 0，次日返回 1。这与咖啡圈"烘焙后第 N 天"的口语一致
// （烘焙当天称第 0 天或"当天"，而非第 1 天）。
func (d CivilDate) DaysSince(to time.Time) int {
	from := d.Time()
	target := ToCivilDate(to).Time()
	// 两个时刻都已归一化到北京零点，相减必为整日数，不受夏令时或闰秒影响
	return int(target.Sub(from).Hours() / 24)
}

// AddDays 返回偏移若干天后的民用日期。
func (d CivilDate) AddDays(n int) CivilDate {
	return ToCivilDate(d.Time().AddDate(0, 0, n))
}

// ParseCivilDate 解析 "2006-01-02" 格式的日期字符串。
func ParseCivilDate(s string) (CivilDate, error) {
	t, err := time.ParseInLocation("2006-01-02", s, Beijing)
	if err != nil {
		return CivilDate{}, Validation("INVALID_DATE", "日期格式必须为 yyyy-MM-dd").WithCause(err)
	}
	return ToCivilDate(t), nil
}

// DisplayTimeFormat 是全项目对用户可见的时刻格式。
// 内部存储与 API 传输仍用 RFC3339，仅展示层统一为此格式
// （knowledge-base/client.md: 用户可见的展示层和输入提示必须统一为 yyyy-MM-dd HH:mm:ss）。
const DisplayTimeFormat = "2006-01-02 15:04:05"

// FormatDisplay 按展示格式渲染时刻，自动转换到北京时区。
func FormatDisplay(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(Beijing).Format(DisplayTimeFormat)
}
