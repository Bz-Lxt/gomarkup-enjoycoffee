package brew

import (
	"context"
	"sort"
	"time"

	"github.com/alkaid/enjoycoffee/internal/bean"
	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
	"github.com/alkaid/enjoycoffee/internal/goldcup"
	"github.com/alkaid/enjoycoffee/internal/logger"
)

// Repository 是萃取记录的持久化出口。
type Repository interface {
	Create(ctx context.Context, b *Brew) (int64, error)
	Update(ctx context.Context, b *Brew) error
	Get(ctx context.Context, id int64) (*Brew, error)
	List(ctx context.Context, f ListFilter) ([]*Brew, error)
	Delete(ctx context.Context, id int64) error

	ReplacePourEvents(ctx context.Context, brewID int64, events []PourEvent) error
	PourEvents(ctx context.Context, brewID int64) ([]PourEvent, error)

	// MeasuredSamples 只返回实测（MEASURED）记录。
	// 这个约束由 SQL 层把守：用推算结果训练推算模型会导致误差自我放大。
	MeasuredSamples(ctx context.Context, beanID int64, method domain.BrewMethod) ([]goldcup.Sample, error)
	ChartSamples(ctx context.Context, beanID int64, method domain.BrewMethod) ([]goldcup.ScoredSample, error)

	StatsByBean(ctx context.Context) (map[int64]bean.BrewStat, error)
}

// BeanGateway 是 brew 对豆库的依赖出口。
type BeanGateway interface {
	Get(ctx context.Context, id int64) (*bean.View, error)
	Consume(ctx context.Context, id int64, amount fixed.Mass) (string, error)
}

// RadarProvider 提供单次冲煮的六维评分。
type RadarProvider interface {
	RadarForBrews(ctx context.Context, brewIDs []int64) (map[int64]*domain.RadarSummary, error)
}

// Service 承载萃取记录的业务逻辑。
type Service struct {
	repo   Repository
	engine *goldcup.Engine
	beans  BeanGateway
	radar  RadarProvider
}

// NewService 构造萃取服务。
func NewService(repo Repository, engine *goldcup.Engine, beans BeanGateway, radar RadarProvider) *Service {
	return &Service{repo: repo, engine: engine, beans: beans, radar: radar}
}

// ListFilter 是萃取记录的查询条件。
type ListFilter struct {
	BeanID       int64
	Method       domain.BrewMethod
	OnlyGold     bool
	OnlyMeasured bool
	Since        time.Time
	Limit        int
	Offset       int
}

// Preview 在不落库的前提下评估一组萃取参数。
//
// 这是萃取沙盘的核心能力：用户在冲煮过程中反复调整参数看结果如何变化，
// 每次调整都落库会产生大量垃圾记录。Preview 与 Create 走完全相同的
// 引擎路径，因此预览值与最终落库值必然一致 —— 不存在"预览说合格、
// 保存后变成欠萃"这种情况。
func (s *Service) Preview(ctx context.Context, b *Brew) (*goldcup.Result, error) {
	b.Normalize()
	if err := b.Validate(); err != nil {
		return nil, err
	}

	samples, err := s.samplesFor(ctx, b)
	if err != nil {
		return nil, err
	}
	return s.engine.Evaluate(b.ToInput(), samples)
}

// Create 记录一次萃取。
func (s *Service) Create(ctx context.Context, b *Brew) (*View, []string, error) {
	b.Normalize()
	if err := b.Validate(); err != nil {
		return nil, nil, err
	}

	if s.beans != nil {
		if _, err := s.beans.Get(ctx, b.BeanID); err != nil {
			return nil, nil, err
		}
	}

	samples, err := s.samplesFor(ctx, b)
	if err != nil {
		return nil, nil, err
	}

	result, err := s.engine.Evaluate(b.ToInput(), samples)
	if err != nil {
		return nil, nil, err
	}
	b.ApplyResult(result)

	id, err := s.repo.Create(ctx, b)
	if err != nil {
		return nil, nil, err
	}
	b.ID = id

	if len(b.PourEvents) > 0 {
		if err := s.repo.ReplacePourEvents(ctx, id, b.PourEvents); err != nil {
			return nil, nil, err
		}
	}

	notices := []string{}
	if s.beans != nil && b.DoseMg > 0 {
		w, cerr := s.beans.Consume(ctx, b.BeanID, b.DoseMg)
		if cerr != nil {
			// 扣减库存失败不应让已经发生的冲煮记录丢失。
			// 记录告警并继续 —— 数据的真实性优先于库存账面的一致性。
			logger.Warn("扣减豆子剩余量失败，萃取记录已保存",
				"brew_id", id, "bean_id", b.BeanID, "error", cerr.Error())
			notices = append(notices, "萃取记录已保存，但豆子剩余量未能自动扣减，请手动更正。")
		} else if w != "" {
			notices = append(notices, w)
		}
	}

	v, err := s.Get(ctx, id)
	if err != nil {
		return nil, notices, err
	}
	return v, notices, nil
}

// Update 更新一次萃取记录并重新评估。
func (s *Service) Update(ctx context.Context, b *Brew) (*View, error) {
	existing, err := s.repo.Get(ctx, b.ID)
	if err != nil {
		return nil, err
	}

	b.Normalize()
	b.CreatedAt = existing.CreatedAt
	if err := b.Validate(); err != nil {
		return nil, err
	}

	samples, err := s.samplesFor(ctx, b)
	if err != nil {
		return nil, err
	}
	result, err := s.engine.Evaluate(b.ToInput(), samples)
	if err != nil {
		return nil, err
	}
	b.ApplyResult(result)

	if err := s.repo.Update(ctx, b); err != nil {
		return nil, err
	}
	if err := s.repo.ReplacePourEvents(ctx, b.ID, b.PourEvents); err != nil {
		return nil, err
	}
	return s.Get(ctx, b.ID)
}

// Get 查单条记录的完整视图，含引擎结果、流速曲线与风味雷达。
func (s *Service) Get(ctx context.Context, id int64) (*View, error) {
	b, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	events, err := s.repo.PourEvents(ctx, id)
	if err != nil {
		return nil, err
	}
	b.PourEvents = events

	v := b.ToView()

	samples, err := s.samplesFor(ctx, b)
	if err != nil {
		return nil, err
	}
	if result, rerr := s.engine.Evaluate(b.ToInput(), samples); rerr == nil {
		v.Result = result
		v.ZoneLabel = result.Zone.Label
	} else {
		// 参数在存储后变得不可评估（例如金杯标准配置被改成了非法值）。
		// 仍然返回记录本身，只是缺少实时评估 —— 让用户看到数据，
		// 比因为一个派生量算不出来就整条记录 500 要好得多。
		logger.Warn("历史记录实时重算失败，仅返回存储快照",
			"brew_id", id, "error", rerr.Error())
	}

	if len(events) > 0 {
		curve := AnalyzePourCurve(events, b.DoseMg)
		v.PourCurve = &curve
	}

	if s.beans != nil {
		if bv, berr := s.beans.Get(ctx, b.BeanID); berr == nil {
			v.BeanName = bv.Name
		}
	}
	if s.radar != nil {
		if radars, rerr := s.radar.RadarForBrews(ctx, []int64{id}); rerr == nil {
			if r, ok := radars[id]; ok {
				v.Radar = r
			}
		}
	}
	if v.Radar == nil {
		v.Radar = domain.NewEmptyRadar()
	}

	return &v, nil
}

// ListResult 是萃取记录列表的响应。
type ListResult struct {
	Items []View `json:"items"`
	Total int    `json:"total"`
}

// List 查询萃取记录列表。
//
// 刻意不为每条记录调用引擎重算：列表只需要存储快照里的摘要字段。
// 几百条记录各跑一次引擎（每次还要查一遍历史样本）会把一个列表请求
// 变成 N+1 查询灾难。详情页才做完整评估。
func (s *Service) List(ctx context.Context, f ListFilter) (*ListResult, error) {
	brews, err := s.repo.List(ctx, f)
	if err != nil {
		return nil, err
	}

	res := &ListResult{Items: make([]View, 0, len(brews))}
	ids := make([]int64, 0, len(brews))
	for _, b := range brews {
		v := b.ToView()
		if _, diag := zoneLabelFromCode(b.ZoneCode); diag != "" {
			v.ZoneLabel = diag
		}
		res.Items = append(res.Items, v)
		ids = append(ids, b.ID)
	}
	res.Total = len(res.Items)

	if s.radar != nil && len(ids) > 0 {
		if radars, rerr := s.radar.RadarForBrews(ctx, ids); rerr == nil {
			for i := range res.Items {
				if r, ok := radars[res.Items[i].ID]; ok {
					res.Items[i].Radar = r
				}
			}
		}
	}

	if s.beans != nil {
		// 批量补豆名。用 map 缓存避免同一支豆重复查询 ——
		// 同一支豆通常有几十条记录，逐条查会把一次列表请求放大成几十次查询。
		nameCache := make(map[int64]string, 8)
		for i := range res.Items {
			bid := res.Items[i].BeanID
			if n, ok := nameCache[bid]; ok {
				res.Items[i].BeanName = n
				continue
			}
			if bv, berr := s.beans.Get(ctx, bid); berr == nil {
				nameCache[bid] = bv.Name
				res.Items[i].BeanName = bv.Name
			}
		}
	}

	return res, nil
}

// Delete 删除一条萃取记录。
func (s *Service) Delete(ctx context.Context, id int64) error {
	if _, err := s.repo.Get(ctx, id); err != nil {
		return err
	}
	return s.repo.Delete(ctx, id)
}

// AppendPourEvents 幂等地追加注水节点，用于 WebSocket 断线重连后的续传。
//
// 幂等性靠客户端提供的 IdempotencyKey 保证：重连后客户端把本地缓存
// 全部重发，服务端按键去重，只有真正新增的节点会落库。
func (s *Service) AppendPourEvents(ctx context.Context, brewID int64, incoming []PourEvent) (*PourCurve, int, error) {
	b, err := s.repo.Get(ctx, brewID)
	if err != nil {
		return nil, 0, err
	}

	existing, err := s.repo.PourEvents(ctx, brewID)
	if err != nil {
		return nil, 0, err
	}

	merged := MergePourEvents(existing, incoming)
	if err := ValidatePourEvents(merged); err != nil {
		return nil, 0, err
	}

	added := len(merged) - len(existing)
	if added > 0 {
		if err := s.repo.ReplacePourEvents(ctx, brewID, merged); err != nil {
			return nil, 0, err
		}
	}

	curve := AnalyzePourCurve(merged, b.DoseMg)
	return &curve, added, nil
}

// Chart 生成某支豆的冲煮控制图与个人偏好曲线。
func (s *Service) Chart(ctx context.Context, beanID int64, method domain.BrewMethod) (*goldcup.Chart, error) {
	if !method.Valid() {
		method = domain.MethodFilter
	}
	p, err := s.engine.ProfileFor(method)
	if err != nil {
		return nil, err
	}

	samples, err := s.repo.ChartSamples(ctx, beanID, method)
	if err != nil {
		return nil, err
	}

	chart := goldcup.BuildChart(p, samples)
	return &chart, nil
}

// StatsByBean 实现 bean.BrewStatsProvider。
func (s *Service) StatsByBean(ctx context.Context) (map[int64]bean.BrewStat, error) {
	return s.repo.StatsByBean(ctx)
}

// Solve 转发三向反解请求到引擎。
func (s *Service) Solve(req goldcup.SolveRequest) (*goldcup.SolveResult, error) {
	return s.engine.Solve(req)
}

// samplesFor 取出用于推算模式的历史测量样本。
//
// 仅在缺少 TDS 时才去查库：测量模式下引擎完全不使用样本，
// 白查一次数据库是纯粹的浪费，而记录列表页会反复走这条路径。
func (s *Service) samplesFor(ctx context.Context, b *Brew) ([]goldcup.Sample, error) {
	if b.TDS > 0 {
		return nil, nil
	}
	if b.BeanID <= 0 {
		return nil, nil
	}
	samples, err := s.repo.MeasuredSamples(ctx, b.BeanID, b.Method)
	if err != nil {
		return nil, err
	}
	// 排除自身：更新一条已有记录时，用它自己的旧值来推算它的新值
	// 会造成自我强化的循环引用。
	if b.ID > 0 {
		filtered := make([]goldcup.Sample, 0, len(samples))
		for _, sm := range samples {
			if sm.BrewID != b.ID {
				filtered = append(filtered, sm)
			}
		}
		samples = filtered
	}
	return samples, nil
}

// zoneLabelFromCode 由存储的落区码反查中文标签。
func zoneLabelFromCode(code string) (bool, string) {
	if code == "" {
		return false, ""
	}
	for _, c := range goldcup.ZoneMatrix() {
		if c.Code == code {
			return true, c.Label
		}
	}
	return false, ""
}

// SortBrewsByTime 按冲煮时间倒序排列，最近的在前。
func SortBrewsByTime(items []View) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].BrewedAt > items[j].BrewedAt })
}
