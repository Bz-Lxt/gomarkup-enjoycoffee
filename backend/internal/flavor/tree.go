// Package flavor 实现风味特征树：无限级用户自定义分类，以及在其上的
// 多级联动筛选。
//
// 性能承诺（Requirements NFR-01）：多级联动筛选 P99 ≤ 10ms，测量范围为
// handler 入口到响应序列化完成，不含网络传输。基准数据集为 500 个风味节点、
// 树深 8 级、2000 款豆子。
//
// 承诺是怎么兑现的（Requirements 裁定 C-03）：
//
// 朴素做法是每次筛选都发 WITH RECURSIVE 下探子树，再 JOIN 关联表。这条路
// 无法对"无限级"承诺固定延迟 —— 耗时随树深线性增长，且每次都要跨进程往返数据库。
//
// 本包的做法是把整棵树连同"节点→豆子"的倒排索引物化在进程内存里：
//   - 每个节点预先算好「自身及全部后代所标记的豆子」的位图（aggregated bitset）
//   - 于是"选中柑橘"这个动作不需要下探子树，直接取该节点的聚合位图
//   - 多条件叠加退化为若干次按字与运算，纳秒级
//
// 代价是写操作后需要重建快照。这个取舍成立的前提是读写比极度悬殊：
// 风味树是用户偶尔整理一次的分类体系，而筛选是每次浏览豆库都要做的操作。
package flavor

import (
	"sort"
	"strings"
	"time"

	"github.com/alkaid/enjoycoffee/internal/domain"
)

// Node 是一个风味节点，如「柑橘」或其子节点「柠檬」。
//
// 带 JSON 标签是因为 NodeView 内嵌了它并经 /flavors/search 直接出网。
// 缺标签时这些字段会以 Go 的 PascalCase 序列化，与全站 snake_case 契约不符。
type Node struct {
	ID        int64     `json:"id"`
	ParentID  *int64    `json:"parent_id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	Icon      string    `json:"icon"`
	SortOrder int       `json:"sort_order"`
	Builtin   bool      `json:"builtin"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

// Tagging 是一条「豆子 ↔ 风味节点」关联。
type Tagging struct {
	BeanID int64
	NodeID int64
}

// softDepthLimit 是软性深度上限。
//
// 超过它不阻断写入（"无限级"是需求的明文要求），但会在响应里附带警告。
// 取 12 的理由：SCA 风味轮本身只有 3 层，加上用户自定义的细分，
// 8 层已经能表达极其精细的分类；到了 12 层，分类树通常已经失去检索价值，
// 变成了单链条的备忘录。此时提醒用户比沉默更有帮助。
const softDepthLimit = 12

// NodeView 是快照中的节点，携带预计算的层级信息。
type NodeView struct {
	Node
	Depth int `json:"depth"`
	// Path 是从根到本节点的名称链，如 "柑橘 / 柠檬"。用于面包屑与搜索匹配。
	Path string `json:"path"`
	// Children 是直接子节点 ID，按 SortOrder 再按 Name 排序。
	Children []int64 `json:"children"`
	// Ancestors 是从根到父节点的 ID 链（不含自身）。
	Ancestors []int64 `json:"ancestors"`
	// DescendantCount 是全部后代节点数（不含自身）。
	DescendantCount int `json:"descendant_count"`
	// DirectBeanCount 是直接标记在本节点上的豆子数。
	DirectBeanCount int `json:"direct_bean_count"`
	// AggregateBeanCount 是本节点及全部后代标记的豆子去重总数。
	AggregateBeanCount int `json:"aggregate_bean_count"`
}

// Snapshot 是风味树在某一时刻的不可变物化视图。
//
// 不可变性是并发安全的基础：读路径完全不加锁，因为快照一旦构建就永不修改；
// 写操作构建全新快照后原子替换指针。这避免了读写锁在高频读场景下的
// 缓存行争用，也彻底排除了"遍历中被修改"这类竞态。
type Snapshot struct {
	nodes map[int64]*NodeView
	roots []int64
	// bfsOrder 是自根向下的层序遍历顺序，保证父节点总在子节点之前。
	bfsOrder []int64

	// beanOrdinal 把稀疏的豆子主键映射为稠密序号，使位图不留空洞。
	beanOrdinal map[int64]int
	beanIDs     []int64

	// directBeans[nodeID] 是直接标记在该节点上的豆子位图。
	directBeans map[int64]*Bitset
	// aggregateBeans[nodeID] 是该节点及全部后代所标记豆子的并集位图。
	// 这是 10ms 承诺的核心：把子树下探的代价从查询期挪到了构建期。
	aggregateBeans map[int64]*Bitset

	maxDepth int
	builtAt  time.Time
}

// BuildSnapshot 由节点集、关联集与豆子 ID 集构建物化快照。
//
// 复杂度 O(N + E + N·B/64)，其中 N 为节点数、E 为关联数、B 为豆子数。
// 基准数据集（500 节点 / 2000 豆）下末项为 500 × 32 = 16000 次字运算，
// 在现代 CPU 上是几十微秒的量级，因此写后重建全树是可接受的。
func BuildSnapshot(nodes []Node, taggings []Tagging, beanIDs []int64) *Snapshot {
	s := &Snapshot{
		nodes:          make(map[int64]*NodeView, len(nodes)),
		roots:          []int64{},
		bfsOrder:       make([]int64, 0, len(nodes)),
		beanOrdinal:    make(map[int64]int, len(beanIDs)),
		beanIDs:        make([]int64, 0, len(beanIDs)),
		directBeans:    make(map[int64]*Bitset, len(nodes)),
		aggregateBeans: make(map[int64]*Bitset, len(nodes)),
		builtAt:        domain.Now(),
	}

	sortedBeans := make([]int64, len(beanIDs))
	copy(sortedBeans, beanIDs)
	sort.Slice(sortedBeans, func(i, j int) bool { return sortedBeans[i] < sortedBeans[j] })
	for _, id := range sortedBeans {
		if _, dup := s.beanOrdinal[id]; dup {
			continue
		}
		s.beanOrdinal[id] = len(s.beanIDs)
		s.beanIDs = append(s.beanIDs, id)
	}
	beanCount := len(s.beanIDs)

	for i := range nodes {
		n := nodes[i]
		s.nodes[n.ID] = &NodeView{Node: n, Children: []int64{}, Ancestors: []int64{}}
	}

	// 建立父子关系。父节点不存在时（数据不一致或父节点已被删除），
	// 该节点降级为根，而不是从树中消失 —— 让用户看到一个错位的节点
	// 远比让它凭空不见更容易发现问题。
	for id, nv := range s.nodes {
		if nv.ParentID == nil {
			s.roots = append(s.roots, id)
			continue
		}
		parent, ok := s.nodes[*nv.ParentID]
		if !ok {
			s.roots = append(s.roots, id)
			continue
		}
		parent.Children = append(parent.Children, id)
	}

	sortIDs := func(ids []int64) {
		sort.SliceStable(ids, func(i, j int) bool {
			a, b := s.nodes[ids[i]], s.nodes[ids[j]]
			if a.SortOrder != b.SortOrder {
				return a.SortOrder < b.SortOrder
			}
			return a.Name < b.Name
		})
	}
	sortIDs(s.roots)
	for _, nv := range s.nodes {
		sortIDs(nv.Children)
	}

	// 层序遍历计算深度、路径与祖先链。
	// 这里也是环的天然防线：BFS 只访问每个节点一次，即使数据里存在环
	// （理论上被写路径的环检测拦住，但仓储层可能被绕过），遍历也不会死循环。
	queue := make([]int64, 0, len(s.nodes))
	queue = append(queue, s.roots...)
	visited := make(map[int64]bool, len(s.nodes))
	for _, id := range s.roots {
		visited[id] = true
		nv := s.nodes[id]
		nv.Depth = 0
		nv.Path = nv.Name
	}

	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		s.bfsOrder = append(s.bfsOrder, id)
		nv := s.nodes[id]
		if nv.Depth > s.maxDepth {
			s.maxDepth = nv.Depth
		}
		for _, cid := range nv.Children {
			if visited[cid] {
				continue
			}
			visited[cid] = true
			cv := s.nodes[cid]
			cv.Depth = nv.Depth + 1
			cv.Path = nv.Path + " / " + cv.Name
			cv.Ancestors = append(append([]int64{}, nv.Ancestors...), id)
			queue = append(queue, cid)
		}
	}

	// 直接关联位图
	for _, nv := range s.nodes {
		s.directBeans[nv.ID] = NewBitset(beanCount)
	}
	for _, t := range taggings {
		bs, ok := s.directBeans[t.NodeID]
		if !ok {
			continue
		}
		ord, ok := s.beanOrdinal[t.BeanID]
		if !ok {
			continue
		}
		bs.Set(ord)
	}

	// 聚合位图：按 BFS 逆序自底向上合并，保证处理某节点时其所有子节点已完成聚合。
	// 这是整个包最关键的一步 —— 它把"下探子树"这个 O(subtree) 的操作
	// 预先摊平成了查询期的 O(1) 位图取用。
	for i := len(s.bfsOrder) - 1; i >= 0; i-- {
		id := s.bfsOrder[i]
		nv := s.nodes[id]
		agg := s.directBeans[id].Clone()
		descCount := 0
		for _, cid := range nv.Children {
			agg.UnionInto(s.aggregateBeans[cid])
			descCount += 1 + s.nodes[cid].DescendantCount
		}
		s.aggregateBeans[id] = agg
		nv.DescendantCount = descCount
		nv.DirectBeanCount = s.directBeans[id].Count()
		nv.AggregateBeanCount = agg.Count()
	}

	return s
}

// BuiltAt 返回快照构建时刻，用于缓存新鲜度诊断。
func (s *Snapshot) BuiltAt() time.Time { return s.builtAt }

// MaxDepth 返回最深节点的深度下标（根为 0）。
//
// 想要"这棵树有几层"请用 Levels()。两者差 1，且这个 off-by-one 曾经真的
// 造成过分歧：Stats 里报的是层数，日志里打的是下标，同一棵树两个数字。
func (s *Snapshot) MaxDepth() int { return s.maxDepth }

// Levels 返回树的层数（只有根节点时为 1）。这是给人看的那个数字。
func (s *Snapshot) Levels() int {
	if len(s.nodes) == 0 {
		return 0
	}
	return s.maxDepth + 1
}

// NodeCount 返回节点总数。
func (s *Snapshot) NodeCount() int { return len(s.nodes) }

// BeanCount 返回索引覆盖的豆子总数。
func (s *Snapshot) BeanCount() int { return len(s.beanIDs) }

// DepthWarning 在树深超过软上限时返回提示，否则返回空串。
func (s *Snapshot) DepthWarning() string {
	if s.Levels() <= softDepthLimit {
		return ""
	}
	return "风味树当前深度已达 " + itoa(s.Levels()) + " 级，超过建议上限 " +
		itoa(softDepthLimit) + " 级。层数过深的分类树通常已失去检索价值，" +
		"建议把过细的分支合并。此提示不影响功能，筛选依然可用。"
}

// Node 按 ID 查节点。
func (s *Snapshot) Node(id int64) (*NodeView, bool) {
	nv, ok := s.nodes[id]
	return nv, ok
}

// Roots 返回根节点 ID 列表（已排序）。
func (s *Snapshot) Roots() []int64 {
	out := make([]int64, len(s.roots))
	copy(out, s.roots)
	return out
}

// TreeNode 是嵌套形态的树节点，供前端一次性拿到整棵树。
type TreeNode struct {
	ID                 int64       `json:"id"`
	ParentID           *int64      `json:"parent_id"`
	Name               string      `json:"name"`
	Path               string      `json:"path"`
	Color              string      `json:"color"`
	Icon               string      `json:"icon"`
	Depth              int         `json:"depth"`
	SortOrder          int         `json:"sort_order"`
	Builtin            bool        `json:"builtin"`
	DirectBeanCount    int         `json:"direct_bean_count"`
	AggregateBeanCount int         `json:"aggregate_bean_count"`
	DescendantCount    int         `json:"descendant_count"`
	Children           []*TreeNode `json:"children"`
}

// Tree 返回嵌套形态的完整风味树。
func (s *Snapshot) Tree() []*TreeNode {
	out := make([]*TreeNode, 0, len(s.roots))
	for _, id := range s.roots {
		out = append(out, s.buildTreeNode(id))
	}
	return out
}

// Subtree 返回以 rootID 为根的子树，用于局部刷新。
func (s *Snapshot) Subtree(rootID int64) (*TreeNode, bool) {
	if _, ok := s.nodes[rootID]; !ok {
		return nil, false
	}
	return s.buildTreeNode(rootID), true
}

func (s *Snapshot) buildTreeNode(id int64) *TreeNode {
	nv := s.nodes[id]
	tn := &TreeNode{
		ID:                 nv.ID,
		ParentID:           nv.ParentID,
		Name:               nv.Name,
		Path:               nv.Path,
		Color:              nv.Color,
		Icon:               nv.Icon,
		Depth:              nv.Depth,
		SortOrder:          nv.SortOrder,
		Builtin:            nv.Builtin,
		DirectBeanCount:    nv.DirectBeanCount,
		AggregateBeanCount: nv.AggregateBeanCount,
		DescendantCount:    nv.DescendantCount,
		// 必须初始化为空切片而非 nil：nil 切片序列化为 null，
		// 前端 children.map 会直接崩。
		Children: []*TreeNode{},
	}
	for _, cid := range nv.Children {
		tn.Children = append(tn.Children, s.buildTreeNode(cid))
	}
	return tn
}

// Ancestors 返回从根到指定节点父节点的完整链，用于面包屑。
func (s *Snapshot) Ancestors(id int64) []*NodeView {
	nv, ok := s.nodes[id]
	if !ok {
		return nil
	}
	out := make([]*NodeView, 0, len(nv.Ancestors))
	for _, aid := range nv.Ancestors {
		if av, ok := s.nodes[aid]; ok {
			out = append(out, av)
		}
	}
	return out
}

// IsDescendant 报告 candidate 是否为 ancestor 的后代（含自身）。
//
// 这是子树移动时的环检测依据：把一个节点移到自己的后代之下会形成环，
// 必须在写入前拒绝。用祖先链判断而非向下搜索子树，代价是 O(depth) 而非 O(subtree)。
func (s *Snapshot) IsDescendant(candidate, ancestor int64) bool {
	if candidate == ancestor {
		return true
	}
	nv, ok := s.nodes[candidate]
	if !ok {
		return false
	}
	for _, aid := range nv.Ancestors {
		if aid == ancestor {
			return true
		}
	}
	return false
}

// SearchNodes 按名称或路径做子串匹配，返回命中的节点。
func (s *Snapshot) SearchNodes(keyword string, limit int) []*NodeView {
	kw := strings.ToLower(strings.TrimSpace(keyword))
	if kw == "" {
		return nil
	}
	if limit <= 0 {
		limit = 50
	}
	out := make([]*NodeView, 0, limit)
	// 按 BFS 顺序遍历，使浅层节点优先命中 —— 用户搜"柑橘"时
	// 想要的通常是那个大类，而不是某个深层的孙节点。
	for _, id := range s.bfsOrder {
		nv := s.nodes[id]
		if strings.Contains(strings.ToLower(nv.Name), kw) ||
			strings.Contains(strings.ToLower(nv.Path), kw) {
			out = append(out, nv)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
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
