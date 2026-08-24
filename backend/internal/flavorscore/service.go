// Package flavorscore 实现六维风味评分及其聚合。
//
// 关键设计决策：评分挂在「萃取记录」上，而不是挂在「咖啡豆」上。
//
// 理由是同一支豆在不同参数下的风味可以差得像两支豆 —— 一支埃塞豆欠萃时
// 是尖锐的柠檬酸，萃到位是蜜桃与红茶，过萃则是涩口的中药味。把评分绑在豆子上
// 就抹掉了这个差异，也就抹掉了整个项目存在的意义（参数与风味的对应关系）。
// 豆子层面的雷达值是它全部冲煮评分的加权聚合，是派生量而非存储量。
package flavorscore

import (
	"context"
	"sort"
	"time"

	"github.com/alkaid/enjoycoffee/internal/domain"
)

// Score 是一次冲煮的六维风味评分。
//
// 各维度以 ×10 的定点整数存储（0–100 表示 0–10.0 分，步进 5 表示 0.5 分）。
// 用整数而非浮点：评分会被反复求和、求均值、按萃取率分箱聚合，
// 浮点累加在上百条记录上会产生可观测的漂移，而分数本身只需要 0.5 的分辨率。
type Score struct {
	ID     int64
	BrewID int64
	BeanID int64

	AcidityX10   int
	SweetX10     int
	AromaX10     int
	AftertoneX10 int
	BodyX10      int
	BitterX10    int

	Note      string
	ScoredAt  time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// View 是 Score 的出网形态。
//
// Score 本身不带 JSON 标签且不直接序列化：它的 *X10 字段是存储细节，
// 直接出网会让前端拿到 AcidityX10 这样的 PascalCase 字段，
// 与全站 snake_case 契约不符，还得自己做 ÷10 的换算。
// 这里一次性给出整数（比较、画图用）与文本（展示用）两种形态。
type View struct {
	ID     int64 `json:"id"`
	BrewID int64 `json:"brew_id"`
	BeanID int64 `json:"bean_id"`

	AcidityX10   int `json:"acidity_x10"`
	SweetX10     int `json:"sweet_x10"`
	AromaX10     int `json:"aroma_x10"`
	AftertoneX10 int `json:"aftertone_x10"`
	BodyX10      int `json:"body_x10"`
	BitterX10    int `json:"bitter_x10"`

	AcidityText   string `json:"acidity_text"`
	SweetText     string `json:"sweet_text"`
	AromaText     string `json:"aroma_text"`
	AftertoneText string `json:"aftertone_text"`
	BodyText      string `json:"body_text"`
	BitterText    string `json:"bitter_text"`

	TotalX10  int    `json:"total_x10"`
	TotalText string `json:"total_text"`

	Note     string `json:"note"`
	ScoredAt string `json:"scored_at"`
}

// View 把评分转成出网形态。s 为 nil 时返回 nil，
// 让「尚未评分」在 JSON 里表达为 null 而不是一份全零的假评分。
func (s *Score) View() *View {
	if s == nil {
		return nil
	}
	return &View{
		ID:            s.ID,
		BrewID:        s.BrewID,
		BeanID:        s.BeanID,
		AcidityX10:    s.AcidityX10,
		SweetX10:      s.SweetX10,
		AromaX10:      s.AromaX10,
		AftertoneX10:  s.AftertoneX10,
		BodyX10:       s.BodyX10,
		BitterX10:     s.BitterX10,
		AcidityText:   domain.FormatScoreX10(s.AcidityX10),
		SweetText:     domain.FormatScoreX10(s.SweetX10),
		AromaText:     domain.FormatScoreX10(s.AromaX10),
		AftertoneText: domain.FormatScoreX10(s.AftertoneX10),
		BodyText:      domain.FormatScoreX10(s.BodyX10),
		BitterText:    domain.FormatScoreX10(s.BitterX10),
		TotalX10:      s.TotalX10(),
		TotalText:     domain.FormatScoreX10(s.TotalX10()),
		Note:          s.Note,
		ScoredAt:      domain.FormatDisplay(s.ScoredAt),
	}
}

// axisValueX10 按维度取值，供聚合逻辑统一处理六个维度。
func (s *Score) axisValueX10(a domain.FlavorAxis) int {
	switch a {
	case domain.AxisAcidity:
		return s.AcidityX10
	case domain.AxisSweet:
		return s.SweetX10
	case domain.AxisAroma:
		return s.AromaX10
	case domain.AxisAftertone:
		return s.AftertoneX10
	case domain.AxisBody:
		return s.BodyX10
	case domain.AxisBitter:
		return s.BitterX10
	default:
		return 0
	}
}

// TotalX10 返回六维总分（×10），值域 0–600。
func (s *Score) TotalX10() int {
	total := 0
	for _, a := range domain.FlavorAxes() {
		total += s.axisValueX10(a)
	}
	return total
}

// Validate 校验评分。
func (s *Score) Validate() error {
	e := domain.Validation("INVALID_FLAVOR_SCORE", "风味评分不合法")
	bad := false

	if s.BrewID <= 0 {
		e.WithField("brew_id", "必须指定所属的萃取记录")
		bad = true
	}

	for _, a := range domain.FlavorAxes() {
		v := s.axisValueX10(a)
		if v < 0 || v > 100 {
			e.WithField(string(a), a.Label()+" 评分应在 0–10 之间")
			bad = true
		}
		// 步进 0.5 分：×10 后必须是 5 的倍数。
		// 拦住这个是为了保证前端滑块与后端存储的粒度一致 ——
		// 若允许 7.3 分入库，滑块回显时会跳到最近的 0.5 刻度，
		// 用户会以为自己的输入被篡改了。
		if v%5 != 0 {
			e.WithField(string(a), a.Label()+" 评分的步进为 0.5 分")
			bad = true
		}
	}

	if len([]rune(s.Note)) > 1000 {
		e.WithField("note", "风味笔记不能超过 1000 个字符")
		bad = true
	}

	if bad {
		return e
	}
	return nil
}

// Repository 是评分的持久化出口。
type Repository interface {
	Upsert(ctx context.Context, s *Score) (int64, error)
	GetByBrew(ctx context.Context, brewID int64) (*Score, error)
	ListByBrews(ctx context.Context, brewIDs []int64) (map[int64]*Score, error)
	// ListByBeanWithTime 返回某支豆的全部评分，按评分时间升序，
	// 供时间加权聚合使用。
	ListByBeanWithTime(ctx context.Context, beanID int64) ([]*Score, error)
	ListByBeans(ctx context.Context, beanIDs []int64) (map[int64][]*Score, error)
	Delete(ctx context.Context, brewID int64) error
}

// Service 承载评分的读写与聚合。
type Service struct {
	repo Repository
}

// NewService 构造评分服务。
func NewService(repo Repository) *Service { return &Service{repo: repo} }

// Save 新增或覆盖某次冲煮的评分。
//
// 语义是 upsert 而非 insert：一次冲煮只有一份评分。用户重新品尝后改分
// 是正常操作，不该产生两条冲突的记录，也不该迫使前端先判断该调 POST 还是 PUT。
func (s *Service) Save(ctx context.Context, sc *Score) (*domain.RadarSummary, error) {
	if sc.ScoredAt.IsZero() {
		sc.ScoredAt = domain.Now()
	}
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	if _, err := s.repo.Upsert(ctx, sc); err != nil {
		return nil, err
	}
	return radarFromScore(sc), nil
}

// Delete 删除某次冲煮的评分。
func (s *Service) Delete(ctx context.Context, brewID int64) error {
	return s.repo.Delete(ctx, brewID)
}

// RadarForBrews 实现 brew.RadarProvider：返回每次冲煮自身的评分雷达。
func (s *Service) RadarForBrews(ctx context.Context, brewIDs []int64) (map[int64]*domain.RadarSummary, error) {
	scores, err := s.repo.ListByBrews(ctx, brewIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*domain.RadarSummary, len(scores))
	for id, sc := range scores {
		out[id] = radarFromScore(sc)
	}
	return out, nil
}

// halfLifeDays 是时间加权的半衰期：90 天。
//
// 为何要给近期评分更高权重：用户的品鉴能力和口味都在变化。半年前给一支豆
// 打的 7 分，与上周打的 7 分，含义并不相同 —— 中间他可能已经喝过几十支豆，
// 参照系整体上移了。用等权平均会让早期不成熟的评分永久拖住聚合值。
//
// 90 天的依据：一包豆的完整生命周期约 1–1.5 个月，90 天覆盖两到三包豆的
// 品鉴跨度，既能让新评分及时反映，又不至于让单条新记录主导整个均值。
const halfLifeDays = 90

// RadarForBeans 实现 bean.RadarProvider：返回每支豆的时间加权聚合雷达。
func (s *Service) RadarForBeans(ctx context.Context, beanIDs []int64) (map[int64]*domain.RadarSummary, error) {
	grouped, err := s.repo.ListByBeans(ctx, beanIDs)
	if err != nil {
		return nil, err
	}

	now := domain.Now()
	out := make(map[int64]*domain.RadarSummary, len(beanIDs))
	for _, id := range beanIDs {
		scores := grouped[id]
		if len(scores) == 0 {
			out[id] = domain.NewEmptyRadar()
			continue
		}
		out[id] = aggregateRadar(scores, now)
	}
	return out, nil
}

// GetByBrew 取某次冲煮的评分。未评分时返回 nil, nil。
func (s *Service) GetByBrew(ctx context.Context, brewID int64) (*Score, *domain.RadarSummary, error) {
	sc, err := s.repo.GetByBrew(ctx, brewID)
	if err != nil {
		return nil, nil, err
	}
	if sc == nil {
		return nil, domain.NewEmptyRadar(), nil
	}
	return sc, radarFromScore(sc), nil
}

// ---------------------------------------------------------------------------
// 聚合
// ---------------------------------------------------------------------------

func radarFromScore(sc *Score) *domain.RadarSummary {
	axes := make([]domain.RadarAxisValue, 0, 6)
	for _, a := range domain.FlavorAxes() {
		v := sc.axisValueX10(a)
		axes = append(axes, domain.RadarAxisValue{
			Axis:      a,
			Label:     a.Label(),
			Value:     float64(v) / 10,
			ValueText: fmtX10(v),
		})
	}
	return &domain.RadarSummary{
		Axes:        axes,
		TotalScore:  float64(sc.TotalX10()) / 10,
		MaxScore:    domain.MaxAxisScore,
		SampleCount: 1,
		Weighting:   "单次冲煮评分",
		Balance:     diagnoseBalance(sc.AcidityX10, sc.SweetX10, sc.BitterX10, sc.BodyX10),
	}
}

// aggregateRadar 对多条评分做时间加权聚合。
//
// 权重函数采用半衰期衰减：距今 d 天的评分权重为 2^(-d/90)。
// 用整数近似实现，避免引入 math.Pow —— 权重只需要相对大小正确，
// 分段近似完全够用，而且结果可复现（不受平台浮点实现差异影响）。
func aggregateRadar(scores []*Score, now time.Time) *domain.RadarSummary {
	sorted := make([]*Score, len(scores))
	copy(sorted, scores)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ScoredAt.Before(sorted[j].ScoredAt) })

	sums := make(map[domain.FlavorAxis]int64, 6)
	var weightTotal int64

	for _, sc := range sorted {
		ageDays := int(now.Sub(sc.ScoredAt).Hours() / 24)
		if ageDays < 0 {
			ageDays = 0
		}
		w := recencyWeight(ageDays)
		weightTotal += w
		for _, a := range domain.FlavorAxes() {
			sums[a] += int64(sc.axisValueX10(a)) * w
		}
	}

	if weightTotal == 0 {
		return domain.NewEmptyRadar()
	}

	axes := make([]domain.RadarAxisValue, 0, 6)
	var totalX10 int64
	vals := make(map[domain.FlavorAxis]int, 6)
	for _, a := range domain.FlavorAxes() {
		// 四舍五入到 ×10 精度
		v := int((sums[a]*2/weightTotal + 1) / 2)
		vals[a] = v
		totalX10 += int64(v)
		axes = append(axes, domain.RadarAxisValue{
			Axis:      a,
			Label:     a.Label(),
			Value:     float64(v) / 10,
			ValueText: fmtX10(v),
		})
	}

	return &domain.RadarSummary{
		Axes:        axes,
		TotalScore:  float64(totalX10) / 10,
		MaxScore:    domain.MaxAxisScore,
		SampleCount: len(sorted),
		Weighting: "按 " + itoa(len(sorted)) + " 次冲煮评分做时间加权（半衰期 " +
			itoa(halfLifeDays) + " 天，近期评分权重更高）",
		Balance: diagnoseBalance(
			vals[domain.AxisAcidity], vals[domain.AxisSweet],
			vals[domain.AxisBitter], vals[domain.AxisBody]),
	}
}

// recencyWeight 返回距今 ageDays 天的评分权重（基数 1024）。
//
// 实现为 1024 >> (ageDays / halfLifeDays)，即每过一个半衰期权重折半。
// 这是 2^(-d/90) 的阶梯近似：在半衰期边界上有跳变，但对"近期评分更重要"
// 这个粗粒度意图来说完全够用，且全程整数运算、结果可精确复现。
func recencyWeight(ageDays int) int64 {
	halfLives := ageDays / halfLifeDays
	// 超过 10 个半衰期（约 2.5 年）后权重降到 1，不再继续衰减到 0 ——
	// 保留最低权重是为了让极老的评分仍然可见，而不是被静默丢弃。
	if halfLives > 10 {
		return 1
	}
	w := int64(1024) >> uint(halfLives)
	if w < 1 {
		return 1
	}
	return w
}

// diagnoseBalance 把六维数字翻译成一句风味平衡度评价。
//
// 判据来自杯测经验：甜感是萃取平衡的核心指标。酸压过甜通常意味着欠萃，
// 苦压过甜通常意味着过萃，而甜感能立住时酸苦的高低就只是风格差异。
func diagnoseBalance(acidityX10, sweetX10, bitterX10, bodyX10 int) string {
	if sweetX10 == 0 && acidityX10 == 0 && bitterX10 == 0 {
		return "还没有风味评分记录"
	}

	switch {
	case sweetX10 >= 70 && acidityX10 >= 60 && bitterX10 <= 40:
		return "甜感与酸质都立得住而苦味克制，是萃取平衡的典型样貌。这组参数值得固定下来。"
	case acidityX10-sweetX10 >= 25:
		return "酸明显压过甜（酸 " + fmtX10(acidityX10) + " vs 甜 " + fmtX10(sweetX10) +
			"），是欠萃的经典特征。甜味物质溶出比酸性物质慢，磨细一档或提高水温通常能把甜感带上来。"
	case bitterX10-sweetX10 >= 20:
		return "苦明显压过甜（苦 " + fmtX10(bitterX10) + " vs 甜 " + fmtX10(sweetX10) +
			"），指向过萃。磨粗一档或缩短接触时间能减少苦涩物的溶出。"
	case sweetX10 <= 30 && acidityX10 <= 40 && bitterX10 <= 40:
		return "三个主轴都偏低，整杯偏平淡。这更像是浓度不足而非萃取问题 —— 先试着收紧粉液比。"
	case bodyX10 <= 30 && sweetX10 >= 50:
		return "甜感不错但醇厚度偏薄。可以试着减少滤纸预冲或改用金属滤网，保留更多油脂与细粉。"
	default:
		return "各维度分布在可接受范围内，没有明显的单轴失衡。"
	}
}

func fmtX10(v int) string {
	neg := v < 0
	if neg {
		v = -v
	}
	s := itoa(v/10) + "." + itoa(v%10)
	if neg {
		return "-" + s
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
