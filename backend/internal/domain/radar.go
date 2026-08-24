package domain

// 雷达图类型定义在 domain 而非某个业务包内，是因为它同时被 bean（豆子层面的
// 风味均值）与 flavorscore（单次冲煮的评分）需要。若定义在其中一方，
// 另一方引用就会形成包循环。

// RadarAxisValue 是雷达图上一个顶点的取值。
type RadarAxisValue struct {
	Axis  FlavorAxis `json:"axis"`
	Label string     `json:"label"`
	Value float64    `json:"value"`
	// ValueText 是格式化后的展示串，保留一位小数。
	ValueText string `json:"value_text"`
}

// RadarSummary 是一组六维风味值，可以来自单次冲煮的评分，
// 也可以来自某支豆全部冲煮的加权聚合。
type RadarSummary struct {
	Axes       []RadarAxisValue `json:"axes"`
	TotalScore float64          `json:"total_score"`
	// MaxScore 是单维满分，固定为 10。下发它是为了让前端不必硬编码坐标轴上限，
	// 将来若改成百分制也不需要改前端。
	MaxScore    float64 `json:"max_score"`
	SampleCount int     `json:"sample_count"`
	// Weighting 说明聚合口径，让用户知道这个雷达图是怎么算出来的。
	Weighting string `json:"weighting"`
	// Balance 是风味平衡度诊断，把六个数字翻译成一句可理解的评价。
	Balance string `json:"balance"`
}

// MaxAxisScore 是单个风味维度的满分。
const MaxAxisScore = 10.0

// FormatScoreX10 把 ×10 的定点评分渲染成一位小数的展示串。
//
// 走整数除法而非 float64(x)/10 再 Sprintf："%.1f" 对 x=85 这类值依赖
// 浮点最近舍入，虽然当前值域内结果正确，但这条路径没有必要引入浮点。
// 整数拆分是精确的，且与项目「计算层不用 float64」的一贯口径一致。
func FormatScoreX10(x10 int) string {
	sign := ""
	if x10 < 0 {
		sign = "-"
		x10 = -x10
	}
	return sign + itoa(x10/10) + "." + itoa(x10%10)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// NewEmptyRadar 构造一个全零雷达，用于"还没有评分"的展示态。
// 返回零值而非 nil，让前端不必为空态写特例分支。
func NewEmptyRadar() *RadarSummary {
	axes := make([]RadarAxisValue, 0, len(FlavorAxes()))
	for _, a := range FlavorAxes() {
		axes = append(axes, RadarAxisValue{
			Axis:      a,
			Label:     a.Label(),
			Value:     0,
			ValueText: "0.0",
		})
	}
	return &RadarSummary{
		Axes:        axes,
		TotalScore:  0,
		MaxScore:    MaxAxisScore,
		SampleCount: 0,
		Weighting:   "暂无评分",
		Balance:     "还没有风味评分记录",
	}
}
