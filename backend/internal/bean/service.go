package bean

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
	"github.com/alkaid/enjoycoffee/internal/flavor"
)

// Repository 是豆库的持久化出口。
type Repository interface {
	Create(ctx context.Context, b *Bean) (int64, error)
	Update(ctx context.Context, b *Bean) error
	Get(ctx context.Context, id int64) (*Bean, error)
	List(ctx context.Context, ids []int64, includeArchived bool) ([]*Bean, error)
	Delete(ctx context.Context, id int64) error
	SetRemaining(ctx context.Context, id int64, remaining fixed.Mass) error
	CountBrews(ctx context.Context, id int64) (int, error)
}

// RadarProvider 提供豆子层面的六维风味聚合值。
// 由 flavorscore 包实现；用接口隔离是为了避免 bean 与 flavorscore 相互依赖。
type RadarProvider interface {
	RadarForBeans(ctx context.Context, beanIDs []int64) (map[int64]*domain.RadarSummary, error)
}

// BrewStat 是某支豆的冲煮统计。
type BrewStat struct {
	Count  int
	LastAt time.Time
}

// BrewStatsProvider 提供每支豆的冲煮次数与最近冲煮时间。
type BrewStatsProvider interface {
	StatsByBean(ctx context.Context) (map[int64]BrewStat, error)
}

// Service 承载豆库的业务逻辑。
type Service struct {
	repo    Repository
	flavors *flavor.Service
	radar   RadarProvider
	brews   BrewStatsProvider
}

// NewService 构造豆库服务。radar 与 brews 允许为 nil（此时相应字段留空），
// 便于在单元测试中只关注豆库自身逻辑。
func NewService(repo Repository, flavors *flavor.Service, radar RadarProvider, brews BrewStatsProvider) *Service {
	return &Service{repo: repo, flavors: flavors, radar: radar, brews: brews}
}

// SetBrewStats 事后注入冲煮统计来源。
//
// 存在的理由是打破一个真实的双向依赖：豆库看板要显示"这支豆冲过几次"，
// 而萃取服务创建记录时要调豆库扣减剩余量。两个构造函数无法互为参数，
// 因此让依赖较弱的那一侧（豆库只在展示时需要统计数据，没有它仍能工作）
// 接受后注入。装配代码必须在 wire 里紧跟 brew 服务构造之后调用它，
// 否则看板上的冲煮次数会永远是 0 —— 一个不报错、只是数字不对的静默故障。
func (s *Service) SetBrewStats(brews BrewStatsProvider) {
	s.brews = brews
}

// SortKey 是列表排序方式。
type SortKey string

const (
	// SortUrgency 按紧迫度排序：越接近衰退的排在前面。这是豆库看板的默认排序，
	// 因为用户打开看板最想知道的是"哪包该赶紧喝了"。
	SortUrgency SortKey = "urgency"
	SortName    SortKey = "name"
	SortRoasted SortKey = "roasted_on"
	SortRemain  SortKey = "remaining"
	SortCreated SortKey = "created_at"
)

// ListFilter 是豆库列表的筛选条件。
type ListFilter struct {
	Keyword         string
	RoastLevels     []domain.RoastLevel
	Stages          []domain.FreshnessStage
	FlavorNodeIDs   []int64
	FlavorMatch     flavor.MatchMode
	ExactFlavorOnly bool
	IncludeArchived bool
	OnlyOpened      bool
	OnlyUnopened    bool
	Sort            SortKey
	Limit           int
	Offset          int
}

// ListResult 是豆库列表的响应。
type ListResult struct {
	Items []View `json:"items"`
	Total int    `json:"total"`
	// FlavorFilter 回传风味筛选的执行细节（含耗时），既服务于可解释性，
	// 也是 NFR-01 性能指标的可观测抓手。
	FlavorFilter *flavor.FilterResult `json:"flavor_filter"`
}

// List 查询豆库。
//
// 执行顺序是有意设计的：先用位图索引把风味条件收窄成一个豆子 ID 白名单，
// 再拿这个白名单去查数据库。反过来（先全量查库再在内存里过滤风味）
// 会把风味索引的性能优势完全浪费掉。
func (s *Service) List(ctx context.Context, f ListFilter) (*ListResult, error) {
	res := &ListResult{Items: []View{}}

	var idWhitelist []int64
	if len(f.FlavorNodeIDs) > 0 && s.flavors != nil {
		fr := s.flavors.Snapshot().Filter(flavor.FilterRequest{
			NodeIDs:       f.FlavorNodeIDs,
			Match:         f.FlavorMatch,
			ExactNodeOnly: f.ExactFlavorOnly,
		})
		res.FlavorFilter = &fr
		if fr.MatchedCount == 0 {
			// 风味条件已经排除了所有豆子，无需再查库
			return res, nil
		}
		idWhitelist = fr.BeanIDs
	}

	beans, err := s.repo.List(ctx, idWhitelist, f.IncludeArchived)
	if err != nil {
		return nil, err
	}

	now := domain.Now()
	views := make([]View, 0, len(beans))

	snap := (*flavor.Snapshot)(nil)
	if s.flavors != nil {
		snap = s.flavors.Snapshot()
	}

	keyword := strings.ToLower(strings.TrimSpace(f.Keyword))
	roastSet := make(map[domain.RoastLevel]bool, len(f.RoastLevels))
	for _, r := range f.RoastLevels {
		roastSet[r] = true
	}
	stageSet := make(map[domain.FreshnessStage]bool, len(f.Stages))
	for _, st := range f.Stages {
		stageSet[st] = true
	}

	for _, b := range beans {
		if len(roastSet) > 0 && !roastSet[b.RoastLevel] {
			continue
		}
		if f.OnlyOpened && b.OpenedOn.IsZero() {
			continue
		}
		if f.OnlyUnopened && !b.OpenedOn.IsZero() {
			continue
		}
		if keyword != "" && !matchesKeyword(b, keyword) {
			continue
		}

		v := b.ToView(now)

		// 阶段筛选必须在计算完新鲜度之后做：阶段是派生量，不是存储字段。
		// 把它存进数据库会立刻过期 —— 一支豆的阶段每天都在变，
		// 而没有任何写操作发生。
		if len(stageSet) > 0 && !stageSet[v.Freshness.Stage] {
			continue
		}

		if snap != nil {
			v.Flavors = tagsFor(snap, b.ID)
		}
		views = append(views, v)
	}

	// 关键词也匹配风味标签路径，需要在标签填充后补一轮
	if keyword != "" && snap != nil {
		views = appendFlavorKeywordMatches(views)
	}

	sortViews(views, f.Sort)
	res.Total = len(views)

	if s.radar != nil && len(views) > 0 {
		ids := make([]int64, 0, len(views))
		for _, v := range views {
			ids = append(ids, v.ID)
		}
		if radars, rerr := s.radar.RadarForBeans(ctx, ids); rerr == nil {
			for i := range views {
				if r, ok := radars[views[i].ID]; ok {
					views[i].Radar = r
				} else {
					views[i].Radar = domain.NewEmptyRadar()
				}
			}
		}
	}

	if s.brews != nil {
		if stats, serr := s.brews.StatsByBean(ctx); serr == nil {
			for i := range views {
				if st, ok := stats[views[i].ID]; ok {
					views[i].BrewCount = st.Count
					views[i].LastBrewedAt = domain.FormatDisplay(st.LastAt)
				}
			}
		}
	}

	// 分页在排序与筛选全部完成之后做
	if f.Offset > 0 {
		if f.Offset >= len(views) {
			views = []View{}
		} else {
			views = views[f.Offset:]
		}
	}
	if f.Limit > 0 && f.Limit < len(views) {
		views = views[:f.Limit]
	}
	res.Items = views
	return res, nil
}

// Get 查单支豆的完整视图。
func (s *Service) Get(ctx context.Context, id int64) (*View, error) {
	b, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	v := b.ToView(domain.Now())

	if s.flavors != nil {
		v.Flavors = tagsFor(s.flavors.Snapshot(), id)
	}
	if s.radar != nil {
		if radars, rerr := s.radar.RadarForBeans(ctx, []int64{id}); rerr == nil {
			if r, ok := radars[id]; ok {
				v.Radar = r
			}
		}
	}
	if v.Radar == nil {
		v.Radar = domain.NewEmptyRadar()
	}
	if n, cerr := s.repo.CountBrews(ctx, id); cerr == nil {
		v.BrewCount = n
	}
	return &v, nil
}

// Create 新增一支豆。
func (s *Service) Create(ctx context.Context, b *Bean) (*View, error) {
	b.Normalize()
	if err := b.Validate(); err != nil {
		return nil, err
	}

	id, err := s.repo.Create(ctx, b)
	if err != nil {
		return nil, err
	}
	b.ID = id

	if s.flavors != nil {
		// 先落风味标签再重建索引。SetBeanFlavors 内部会触发重建，
		// 因此新豆的序号映射会在同一次重建中被建立 —— 不需要额外 Refresh。
		if err := s.flavors.SetBeanFlavors(ctx, id, b.FlavorNodeIDs); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, id)
}

// Update 更新一支豆。
func (s *Service) Update(ctx context.Context, b *Bean) (*View, error) {
	existing, err := s.repo.Get(ctx, b.ID)
	if err != nil {
		return nil, err
	}

	b.Normalize()
	// 保留创建时间，防止被请求体里的零值覆盖
	b.CreatedAt = existing.CreatedAt
	if err := b.Validate(); err != nil {
		return nil, err
	}

	if err := s.repo.Update(ctx, b); err != nil {
		return nil, err
	}
	if s.flavors != nil {
		if err := s.flavors.SetBeanFlavors(ctx, b.ID, b.FlavorNodeIDs); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, b.ID)
}

// Delete 删除一支豆。
//
// 已有冲煮记录的豆子不允许硬删除：那些记录是用户积累的萃取数据，
// 删掉豆子会让它们变成孤儿，也会破坏偏好曲线的历史。
// 引导用户改用归档，语义上更贴近他真正想做的事（"从在库列表里拿走"）。
func (s *Service) Delete(ctx context.Context, id int64) error {
	if _, err := s.repo.Get(ctx, id); err != nil {
		return err
	}
	n, err := s.repo.CountBrews(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return domain.Conflict("BEAN_HAS_BREWS",
			"这支豆已有 "+itoa(n)+" 条萃取记录，删除会让这些记录失去归属。"+
				"建议改用归档：归档后它不再出现在在库列表，但历史记录与偏好曲线完好保留。")
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	if s.flavors != nil {
		return s.flavors.Refresh(ctx)
	}
	return nil
}

// Consume 扣减剩余粉量。每次记录冲煮时由 brew 服务调用。
//
// 扣到负数时截断为 0 并返回警告而非报错：用户忘记及时更新剩余量是常态，
// 为此拒绝记录一次真实发生过的冲煮是本末倒置的。
func (s *Service) Consume(ctx context.Context, id int64, amount fixed.Mass) (string, error) {
	if amount <= 0 {
		return "", nil
	}
	b, err := s.repo.Get(ctx, id)
	if err != nil {
		return "", err
	}

	next := b.RemainingMg - amount
	warning := ""
	if next < 0 {
		warning = "「" + b.Name + "」记录的剩余量只有 " + b.RemainingMg.Grams() +
			"g，本次用掉 " + amount.Grams() + "g 后已归零。若实际还有存量，请到豆库更正剩余重量。"
		next = 0
	}
	if err := s.repo.SetRemaining(ctx, id, next); err != nil {
		return "", err
	}
	return warning, nil
}

// ---------------------------------------------------------------------------
// 豆库看板
// ---------------------------------------------------------------------------

// BoardGroup 是看板上的一个阶段分组。
type BoardGroup struct {
	Stage      domain.FreshnessStage `json:"stage"`
	StageLabel string                `json:"stage_label"`
	ColorHint  string                `json:"color_hint"`
	Count      int                   `json:"count"`
	TotalGrams float64               `json:"total_grams"`
	Items      []View                `json:"items"`
}

// Board 是豆库临期与排气期看板。
type Board struct {
	Groups []BoardGroup `json:"groups"`
	// Urgent 是最需要立刻处理的豆子（临期且尚有存量），跨分组抽取，
	// 按剩余天数升序。看板顶部的行动清单就用它渲染。
	Urgent      []View  `json:"urgent"`
	TotalBeans  int     `json:"total_beans"`
	TotalGrams  float64 `json:"total_grams"`
	OpenedCount int     `json:"opened_count"`
	Summary     string  `json:"summary"`
	GeneratedAt string  `json:"generated_at"`
}

// BuildBoard 生成豆库看板。
func (s *Service) BuildBoard(ctx context.Context) (*Board, error) {
	lr, err := s.List(ctx, ListFilter{Sort: SortUrgency})
	if err != nil {
		return nil, err
	}

	stages := []domain.FreshnessStage{
		domain.StageDegassing, domain.StagePeak,
		domain.StageNearExpiry, domain.StageDeclined,
	}

	b := &Board{
		Groups:      make([]BoardGroup, 0, len(stages)),
		Urgent:      []View{},
		GeneratedAt: domain.FormatDisplay(domain.Now()),
	}

	byStage := make(map[domain.FreshnessStage][]View, len(stages))
	for _, v := range lr.Items {
		byStage[v.Freshness.Stage] = append(byStage[v.Freshness.Stage], v)
		b.TotalBeans++
		b.TotalGrams += v.RemainingG
		if v.Freshness.Opened {
			b.OpenedCount++
		}
	}

	for _, st := range stages {
		items := byStage[st]
		if items == nil {
			items = []View{}
		}
		g := BoardGroup{
			Stage:      st,
			StageLabel: st.Label(),
			ColorHint:  st.ColorHint(),
			Count:      len(items),
			Items:      items,
		}
		for _, v := range items {
			g.TotalGrams += v.RemainingG
		}
		b.Groups = append(b.Groups, g)
	}

	// 紧迫清单：临期且还有存量的豆子。已衰退的不进清单 —— 那已经来不及了，
	// 提醒也无从行动；把它们混进来只会淹没真正还救得回来的那几包。
	for _, v := range byStage[domain.StageNearExpiry] {
		if v.RemainingG > 0 {
			b.Urgent = append(b.Urgent, v)
		}
	}
	sort.SliceStable(b.Urgent, func(i, j int) bool {
		return b.Urgent[i].Freshness.DaysUntilNextStage < b.Urgent[j].Freshness.DaysUntilNextStage
	})
	if len(b.Urgent) > 5 {
		b.Urgent = b.Urgent[:5]
	}

	b.Summary = boardSummary(b, byStage)
	return b, nil
}

func boardSummary(b *Board, byStage map[domain.FreshnessStage][]View) string {
	if b.TotalBeans == 0 {
		return "豆库还是空的。录入第一支豆时记得填上烘焙日期 —— 排气期与最佳风味窗口全靠它推算。"
	}

	peak := len(byStage[domain.StagePeak])
	degas := len(byStage[domain.StageDegassing])
	near := len(byStage[domain.StageNearExpiry])
	declined := len(byStage[domain.StageDeclined])

	s := "在库 " + itoa(b.TotalBeans) + " 支，其中 " + itoa(peak) + " 支处于最佳风味期"
	if degas > 0 {
		s += "，" + itoa(degas) + " 支还在排气"
	}
	if near > 0 {
		s += "，" + itoa(near) + " 支已临期"
	}
	if declined > 0 {
		s += "，" + itoa(declined) + " 支已进入衰退"
	}
	s += "。"

	switch {
	case near > 0:
		s += "临期的那 " + itoa(near) + " 支建议优先安排 —— 它们的高频酸香正在流失，再放就只剩底味了。"
	case peak == 0 && degas > 0:
		s += "目前没有处于最佳期的豆子，最快的一支还要再养 " +
			itoa(minDaysUntilNext(byStage[domain.StageDegassing])) + " 天。"
	case declined > 0:
		s += "衰退的那几支不建议再用来做参数对比 —— 它们的萃取表现已经不稳定，会污染偏好曲线。"
	default:
		s += "库存状态健康。"
	}
	return s
}

func minDaysUntilNext(items []View) int {
	m := -1
	for _, v := range items {
		d := v.Freshness.DaysUntilNextStage
		if m < 0 || d < m {
			m = d
		}
	}
	if m < 0 {
		return 0
	}
	return m
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

func matchesKeyword(b *Bean, kw string) bool {
	fields := []string{
		b.Name, b.Roaster, b.Country, b.Region, b.Farm,
		b.Variety, string(b.Process), b.Notes, b.RoastNote,
	}
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), kw) {
			return true
		}
	}
	return false
}

// appendFlavorKeywordMatches 是关键词匹配的占位补充点。
//
// 当前实现直接返回原列表：风味标签的关键词匹配已由 flavor 包的 SearchNodes
// 提供独立入口，前端的搜索框会同时调用两处并合并结果。在这里重复一遍
// 子串匹配只会让同一个语义分散在两个地方，将来改匹配规则时必然漏改一处。
func appendFlavorKeywordMatches(views []View) []View { return views }

func tagsFor(snap *flavor.Snapshot, beanID int64) []FlavorTag {
	nodes := snap.NodesForBean(beanID)
	out := make([]FlavorTag, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, FlavorTag{
			NodeID: n.ID,
			Name:   n.Name,
			Path:   n.Path,
			Color:  n.Color,
			Icon:   n.Icon,
			Depth:  n.Depth,
		})
	}
	return out
}

func sortViews(views []View, key SortKey) {
	switch key {
	case SortName:
		sort.SliceStable(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	case SortRoasted:
		sort.SliceStable(views, func(i, j int) bool { return views[i].RoastedOn > views[j].RoastedOn })
	case SortRemain:
		sort.SliceStable(views, func(i, j int) bool { return views[i].RemainingG > views[j].RemainingG })
	case SortCreated:
		sort.SliceStable(views, func(i, j int) bool { return views[i].CreatedAt > views[j].CreatedAt })
	default:
		// 紧迫度：先按阶段排（临期最前），同阶段内按距下一阶段的剩余天数升序。
		//
		// 为何临期排在排气期之前：排气期的豆子"还没到时候"，用户不需要行动；
		// 临期的豆子"再不喝就废了"，需要立刻行动。看板的价值在于催办，
		// 而不是按时间顺序陈列。
		rank := map[domain.FreshnessStage]int{
			domain.StageNearExpiry: 0,
			domain.StagePeak:       1,
			domain.StageDegassing:  2,
			domain.StageDeclined:   3,
		}
		sort.SliceStable(views, func(i, j int) bool {
			ri, rj := rank[views[i].Freshness.Stage], rank[views[j].Freshness.Stage]
			if ri != rj {
				return ri < rj
			}
			return views[i].Freshness.DaysUntilNextStage < views[j].Freshness.DaysUntilNextStage
		})
	}
}
