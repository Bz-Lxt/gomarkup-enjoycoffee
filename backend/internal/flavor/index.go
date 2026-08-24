package flavor

import (
	"sort"
	"time"
)

// MatchMode 决定多个筛选条件之间的组合方式。
type MatchMode string

const (
	// MatchAll 豆子必须同时具备所有选中的风味。这是默认值，
	// 也是"多级联动筛选"的自然语义 —— 用户逐层收窄条件时期待结果集变小。
	MatchAll MatchMode = "ALL"
	// MatchAny 豆子具备任一选中风味即命中。用于"给我看看所有带果调的豆"这类宽查询。
	MatchAny MatchMode = "ANY"
)

// ParseMatchMode 宽松解析组合方式，默认 ALL。
func ParseMatchMode(s string) MatchMode {
	switch s {
	case "ANY", "any", "or", "OR":
		return MatchAny
	default:
		return MatchAll
	}
}

// FilterRequest 是一次多级联动筛选请求。
type FilterRequest struct {
	// NodeIDs 是选中的风味节点。空表示不做风味过滤。
	NodeIDs []int64
	// Match 决定条件间是与还是或。
	Match MatchMode
	// ExactNodeOnly 为真时只匹配直接标记在该节点上的豆子，不含后代。
	//
	// 默认（false）行为是包含后代：选中「柑橘」会命中标记了「柠檬」「西柚」的豆子。
	// 这正是需求所要求的联动语义。ExactNodeOnly 是给"我只想看笼统标了柑橘、
	// 没细分到具体品类的豆"这个次要场景留的出口。
	ExactNodeOnly bool
	// WantFacets 为真时额外计算每个节点在当前结果集下的剩余命中数，
	// 供前端把不可能有结果的分支置灰。
	WantFacets bool
}

// AppliedCondition 记录单个筛选条件的命中情况，用于向用户解释结果是怎么来的。
type AppliedCondition struct {
	NodeID       int64  `json:"node_id"`
	Name         string `json:"name"`
	Path         string `json:"path"`
	Depth        int    `json:"depth"`
	MatchedCount int    `json:"matched_count"`
	// IncludedDescendants 说明这个条件是否把后代节点算进来了，
	// 以及算进来了几个后代 —— 这解释了"我只选了柑橘，怎么出来一支标着柠檬的豆"。
	IncludedDescendants int `json:"included_descendants"`
}

// Facet 是某个风味节点在当前结果集下的剩余命中数。
type Facet struct {
	NodeID    int64 `json:"node_id"`
	Remaining int   `json:"remaining"`
}

// FilterResult 是筛选结果。
type FilterResult struct {
	BeanIDs      []int64            `json:"bean_ids"`
	MatchedCount int                `json:"matched_count"`
	TotalBeans   int                `json:"total_beans"`
	Match        MatchMode          `json:"match"`
	Conditions   []AppliedCondition `json:"conditions"`
	Facets       []Facet            `json:"facets"`
	// ElapsedMicros 是筛选本身的耗时（微秒）。它被下发到前端不是为了炫技，
	// 而是 NFR-01 的可观测性抓手：性能回退时用户和 QA 都能立刻看到。
	ElapsedMicros int64  `json:"elapsed_micros"`
	Warning       string `json:"warning"`
	// UnknownNodeIDs 是请求里那些在当前快照中不存在的节点。
	// 静默忽略会让用户以为筛选生效了，实际上条件被丢掉了。
	UnknownNodeIDs []int64 `json:"unknown_node_ids"`
}

// Filter 执行多级联动筛选。
//
// 热路径的全部工作量：
//  1. 按节点 ID 取出预计算的聚合位图（哈希查表，O(k)）
//  2. 位图按字与/或（O(k · B/64)，B 为豆子数）
//  3. 遍历置位位得到豆子 ID（O(命中数)）
//
// 没有任何递归下探，没有任何数据库往返。在基准数据集（500 节点 / 深度 8 /
// 2000 豆）下，即使叠加 8 个筛选条件，第 2 步也只有 8 × 32 = 256 条字运算。
func (s *Snapshot) Filter(req FilterRequest) FilterResult {
	start := time.Now()

	res := FilterResult{
		Match:          req.Match,
		TotalBeans:     len(s.beanIDs),
		BeanIDs:        []int64{},
		Conditions:     []AppliedCondition{},
		Facets:         []Facet{},
		UnknownNodeIDs: []int64{},
		Warning:        s.DepthWarning(),
	}
	if res.Match == "" {
		res.Match = MatchAll
	}

	pick := func(id int64) *Bitset {
		if req.ExactNodeOnly {
			return s.directBeans[id]
		}
		return s.aggregateBeans[id]
	}

	// 去重后的有效节点。重复选中同一节点在 ALL 语义下是幂等的，
	// 但会白做一次位运算并在解释信息里出现重复条目。
	seen := make(map[int64]bool, len(req.NodeIDs))
	valid := make([]int64, 0, len(req.NodeIDs))
	for _, id := range req.NodeIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, ok := s.nodes[id]; !ok {
			res.UnknownNodeIDs = append(res.UnknownNodeIDs, id)
			continue
		}
		valid = append(valid, id)
	}

	var acc *Bitset

	if len(valid) == 0 {
		// 无风味条件：结果是全部豆子。这让调用方无需为"没选风味"写特例分支。
		acc = NewBitset(len(s.beanIDs))
		for i := range s.beanIDs {
			acc.Set(i)
		}
	} else {
		// 按命中数升序处理条件。对 ALL 语义而言，先与最小的集合能让
		// 后续每一步都在更小的活跃集上工作，也更快触发空集短路。
		sort.SliceStable(valid, func(i, j int) bool {
			return pick(valid[i]).Count() < pick(valid[j]).Count()
		})

		for _, id := range valid {
			bs := pick(id)
			if acc == nil {
				acc = bs.Clone()
			} else if res.Match == MatchAny {
				acc.UnionInto(bs)
			} else {
				acc.IntersectInto(bs)
				if acc.IsEmpty() {
					// ALL 语义下一旦为空，后续所有交集都是空集
					break
				}
			}
		}
	}

	for _, id := range valid {
		nv := s.nodes[id]
		res.Conditions = append(res.Conditions, AppliedCondition{
			NodeID:              id,
			Name:                nv.Name,
			Path:                nv.Path,
			Depth:               nv.Depth,
			MatchedCount:        pick(id).Count(),
			IncludedDescendants: descendantsIncluded(req, nv),
		})
	}

	if acc != nil {
		acc.Each(func(i int) {
			if i < len(s.beanIDs) {
				res.BeanIDs = append(res.BeanIDs, s.beanIDs[i])
			}
		})
	}
	res.MatchedCount = len(res.BeanIDs)

	if req.WantFacets && acc != nil {
		res.Facets = s.computeFacets(acc)
	}

	res.ElapsedMicros = time.Since(start).Microseconds()
	return res
}

func descendantsIncluded(req FilterRequest, nv *NodeView) int {
	if req.ExactNodeOnly {
		return 0
	}
	return nv.DescendantCount
}

// computeFacets 计算每个节点在当前结果集下的剩余命中数。
//
// 这是"联动"真正被用户感知到的部分：选中「柑橘」之后，「坚果」上的计数
// 会从 40 变成 3，用户立刻知道柑橘调与坚果调很少共存。计数为 0 的分支
// 前端可以直接置灰，避免用户点进一个必然空结果的筛选。
//
// 代价是 N 次位图交集与 popcount：500 节点 × 32 字 = 16000 次字运算，
// 微秒级，完全在 10ms 预算之内。
func (s *Snapshot) computeFacets(current *Bitset) []Facet {
	out := make([]Facet, 0, len(s.nodes))
	scratch := NewBitset(len(s.beanIDs))

	for _, id := range s.bfsOrder {
		agg := s.aggregateBeans[id]
		if agg == nil {
			continue
		}
		copy(scratch.words, current.words)
		scratch.IntersectInto(agg)
		out = append(out, Facet{NodeID: id, Remaining: scratch.Count()})
	}
	return out
}

// BeansForNode 返回某节点（含后代）标记的全部豆子 ID。
func (s *Snapshot) BeansForNode(id int64, includeDescendants bool) ([]int64, bool) {
	var bs *Bitset
	if includeDescendants {
		bs = s.aggregateBeans[id]
	} else {
		bs = s.directBeans[id]
	}
	if bs == nil {
		return nil, false
	}
	out := make([]int64, 0, bs.Count())
	bs.Each(func(i int) {
		if i < len(s.beanIDs) {
			out = append(out, s.beanIDs[i])
		}
	})
	return out, true
}

// NodesForBean 反查某支豆被标记了哪些风味节点（仅直接标记，不含推导出的祖先）。
func (s *Snapshot) NodesForBean(beanID int64) []*NodeView {
	ord, ok := s.beanOrdinal[beanID]
	if !ok {
		return nil
	}
	out := make([]*NodeView, 0, 8)
	for _, id := range s.bfsOrder {
		if bs := s.directBeans[id]; bs != nil && bs.Has(ord) {
			out = append(out, s.nodes[id])
		}
	}
	return out
}

// Stats 是索引的运行期统计，供健康检查与性能诊断端点使用。
type Stats struct {
	NodeCount    int    `json:"node_count"`
	BeanCount    int    `json:"bean_count"`
	Levels       int    `json:"depth_levels"`
	RootCount    int    `json:"root_count"`
	BitsetWords  int    `json:"bitset_words_per_node"`
	MemoryKB     int    `json:"approx_memory_kb"`
	BuiltAt      string `json:"built_at"`
	DepthWarning string `json:"depth_warning"`
	SoftDepthMax int    `json:"soft_depth_limit"`
}

// Stats 返回索引统计。
func (s *Snapshot) Stats() Stats {
	words := (len(s.beanIDs) + 63) / 64
	// 两套位图（direct + aggregate），每套每节点 words 个 uint64
	bytes := len(s.nodes) * words * 8 * 2
	return Stats{
		NodeCount:    len(s.nodes),
		BeanCount:    len(s.beanIDs),
		Levels:       s.Levels(),
		RootCount:    len(s.roots),
		BitsetWords:  words,
		MemoryKB:     bytes / 1024,
		BuiltAt:      s.builtAt.Format("2006-01-02 15:04:05"),
		DepthWarning: s.DepthWarning(),
		SoftDepthMax: softDepthLimit,
	}
}
