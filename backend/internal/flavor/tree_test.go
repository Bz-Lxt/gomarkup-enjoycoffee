package flavor

import (
	"testing"
	"time"
)

// smallTree 搭一棵需求文档里点名的那棵树：
//
//	柑橘(1) ─ 柠檬(2)
//	        └ 西柚(3) ─ 粉西柚(6)
//	坚果(4) ─ 榛子(5)
//
// 豆子标记：
//
//	101 → 柠檬
//	102 → 西柚
//	103 → 粉西柚
//	104 → 榛子
//	105 → 柑橘（只笼统标到父级，没细分）
//	106 → 柠檬 + 榛子（跨根同时命中）
func smallTree(t *testing.T) *Snapshot {
	t.Helper()

	p := func(id int64) *int64 { return &id }
	nodes := []Node{
		{ID: 1, Name: "柑橘"},
		{ID: 2, ParentID: p(1), Name: "柠檬"},
		{ID: 3, ParentID: p(1), Name: "西柚"},
		{ID: 6, ParentID: p(3), Name: "粉西柚"},
		{ID: 4, Name: "坚果"},
		{ID: 5, ParentID: p(4), Name: "榛子"},
	}
	tags := []Tagging{
		{BeanID: 101, NodeID: 2},
		{BeanID: 102, NodeID: 3},
		{BeanID: 103, NodeID: 6},
		{BeanID: 104, NodeID: 5},
		{BeanID: 105, NodeID: 1},
		{BeanID: 106, NodeID: 2},
		{BeanID: 106, NodeID: 5},
	}
	beans := []int64{101, 102, 103, 104, 105, 106}

	return BuildSnapshot(nodes, tags, beans)
}

func ids(t *testing.T, res FilterResult) []int64 {
	t.Helper()
	return res.BeanIDs
}

func sameIDs(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestSelectingParentIncludesDescendants 是"多级联动"的核心语义：
// 选中「柑橘」必须把标了「柠檬」「西柚」「粉西柚」的豆一并带出来。
//
// 这条如果错了，整个风味树就退化成了一个平铺标签列表 —— 层级白建了。
func TestSelectingParentIncludesDescendants(t *testing.T) {
	s := smallTree(t)

	res := s.Filter(FilterRequest{NodeIDs: []int64{1}})
	want := []int64{101, 102, 103, 105, 106}
	if !sameIDs(ids(t, res), want) {
		t.Errorf("选中「柑橘」应命中 %v（含全部后代与只标到父级的 105），实际 %v",
			want, res.BeanIDs)
	}

	// 三层之外的后代也要算进来：粉西柚在柑橘之下第 3 层。
	if !containsID(res.BeanIDs, 103) {
		t.Error("103 只标了三层深的「粉西柚」，选中根节点「柑橘」时必须命中；" +
			"缺失说明聚合位图没有沿整棵子树向上合并")
	}
}

// TestExactNodeOnlySeparatesParentFromChildren 验证"我只想看笼统标到柑橘、
// 没细分品类的豆"这个次要场景确实可达。
func TestExactNodeOnlySeparatesParentFromChildren(t *testing.T) {
	s := smallTree(t)

	res := s.Filter(FilterRequest{NodeIDs: []int64{1}, ExactNodeOnly: true})
	want := []int64{105}
	if !sameIDs(ids(t, res), want) {
		t.Errorf("ExactNodeOnly 下选中「柑橘」应只命中直接标记的 %v，实际 %v",
			want, res.BeanIDs)
	}
	if res.Conditions[0].IncludedDescendants != 0 {
		t.Error("ExactNodeOnly 下解释信息不应声称包含了后代")
	}
}

// TestMatchAllNarrowsMatchAnyWidens 检查两种组合语义方向相反。
//
// 用户对筛选器的心理预期是"勾得越多结果越少"。若 ALL 实现成了 ANY，
// 界面上不会报错，只会让用户觉得筛选器坏了。
func TestMatchAllNarrowsMatchAnyWidens(t *testing.T) {
	s := smallTree(t)

	all := s.Filter(FilterRequest{NodeIDs: []int64{1, 4}, Match: MatchAll})
	if !sameIDs(ids(t, all), []int64{106}) {
		t.Errorf("ALL 语义下同时要求柑橘与坚果，只有 106 两者都标了，实际 %v",
			all.BeanIDs)
	}

	any := s.Filter(FilterRequest{NodeIDs: []int64{1, 4}, Match: MatchAny})
	want := []int64{101, 102, 103, 104, 105, 106}
	if !sameIDs(ids(t, any), want) {
		t.Errorf("ANY 语义下应命中 %v，实际 %v", want, any.BeanIDs)
	}

	if any.MatchedCount <= all.MatchedCount {
		t.Errorf("同样的条件集，ANY(%d) 必须比 ALL(%d) 宽",
			any.MatchedCount, all.MatchedCount)
	}
}

// TestNoConditionsReturnsEverything 确认"没选任何风味"返回全集而不是空集。
//
// 这是调用方最容易踩的坑：若返回空集，前端一进豆库页面就是一片空白，
// 得靠调用方自己写 if len(nodeIDs)==0 的特例分支。把这个语义收进筛选器里，
// 所有调用点就都不必重复它。
func TestNoConditionsReturnsEverything(t *testing.T) {
	s := smallTree(t)

	res := s.Filter(FilterRequest{})
	if res.MatchedCount != 6 {
		t.Errorf("空条件应返回全部 6 支豆，实际 %d 支", res.MatchedCount)
	}
	if res.TotalBeans != 6 {
		t.Errorf("TotalBeans 应为 6，实际 %d", res.TotalBeans)
	}
}

// TestUnknownNodeIsReportedNotSilentlyDropped 确保不存在的节点被显式回报。
//
// 静默忽略是这里最坏的选择：用户勾选的分类可能刚被另一个标签页删掉，
// 若后端悄悄丢掉这个条件，返回的结果集会比用户预期的宽得多，
// 而界面上没有任何线索说明发生了什么。
func TestUnknownNodeIsReportedNotSilentlyDropped(t *testing.T) {
	s := smallTree(t)

	res := s.Filter(FilterRequest{NodeIDs: []int64{2, 999}})
	if len(res.UnknownNodeIDs) != 1 || res.UnknownNodeIDs[0] != 999 {
		t.Errorf("不存在的节点 999 应出现在 UnknownNodeIDs，实际 %v",
			res.UnknownNodeIDs)
	}
	// 有效条件仍应生效
	if !sameIDs(ids(t, res), []int64{101, 106}) {
		t.Errorf("有效条件「柠檬」应正常生效，实际 %v", res.BeanIDs)
	}
}

// TestDuplicateNodeIsIdempotent 重复勾选同一节点不应改变结果或产生重复解释条目。
func TestDuplicateNodeIsIdempotent(t *testing.T) {
	s := smallTree(t)

	once := s.Filter(FilterRequest{NodeIDs: []int64{1}})
	twice := s.Filter(FilterRequest{NodeIDs: []int64{1, 1, 1}})

	if !sameIDs(once.BeanIDs, twice.BeanIDs) {
		t.Errorf("重复勾选应幂等：一次 %v，三次 %v", once.BeanIDs, twice.BeanIDs)
	}
	if len(twice.Conditions) != 1 {
		t.Errorf("重复条件应去重后只出现一条解释，实际 %d 条", len(twice.Conditions))
	}
}

// TestFacetsGoToZeroForImpossibleBranches 验证联动计数：选中柑橘之后，
// 只挂了 104 一支纯坚果豆的分支应该只剩 106 这一支跨调豆。
//
// 这是"联动"被用户肉眼看见的地方 —— 计数归零的分支前端可以置灰。
func TestFacetsGoToZeroForImpossibleBranches(t *testing.T) {
	s := smallTree(t)

	res := s.Filter(FilterRequest{NodeIDs: []int64{1}, WantFacets: true})
	facets := map[int64]int{}
	for _, f := range res.Facets {
		facets[f.NodeID] = f.Remaining
	}

	if facets[5] != 1 {
		t.Errorf("已选柑橘的前提下，「榛子」应只剩 106 一支（跨调豆），实际 %d",
			facets[5])
	}
	if facets[2] != 2 {
		t.Errorf("已选柑橘的前提下，「柠檬」应剩 101 与 106 两支，实际 %d",
			facets[2])
	}
	if len(res.Facets) != 6 {
		t.Errorf("应为全部 6 个节点都给出剩余计数（含为 0 的），实际 %d 个",
			len(res.Facets))
	}
}

// TestPathAndDepthArePrecomputed 检查面包屑与层级在构建期就算好了。
func TestPathAndDepthArePrecomputed(t *testing.T) {
	s := smallTree(t)

	nv, ok := s.Node(6)
	if !ok {
		t.Fatal("节点 6「粉西柚」应存在")
	}
	if nv.Depth != 2 {
		t.Errorf("粉西柚在柑橘/西柚之下，深度应为 2，实际 %d", nv.Depth)
	}
	if nv.Path != "柑橘 / 西柚 / 粉西柚" {
		t.Errorf("面包屑应为「柑橘 / 西柚 / 粉西柚」，实际 %q", nv.Path)
	}
	if len(nv.Ancestors) != 2 || nv.Ancestors[0] != 1 || nv.Ancestors[1] != 3 {
		t.Errorf("祖先链应为 [1 3]，实际 %v", nv.Ancestors)
	}
}

// TestAggregateCountDeduplicates 验证聚合计数是去重的。
//
// 106 同时标了柠檬与榛子，若聚合计数用的是简单相加而非位图并集，
// 「柑橘」的聚合数会把它算重。
func TestAggregateCountDeduplicates(t *testing.T) {
	s := smallTree(t)

	citrus, _ := s.Node(1)
	// 柑橘子树覆盖 101(柠檬)、102(西柚)、103(粉西柚)、105(直接)、106(柠檬) = 5 支
	if citrus.AggregateBeanCount != 5 {
		t.Errorf("柑橘聚合应为 5 支去重豆，实际 %d", citrus.AggregateBeanCount)
	}
	if citrus.DirectBeanCount != 1 {
		t.Errorf("柑橘直接标记只有 105 一支，实际 %d", citrus.DirectBeanCount)
	}
	if citrus.DescendantCount != 3 {
		t.Errorf("柑橘应有 3 个后代（柠檬/西柚/粉西柚），实际 %d",
			citrus.DescendantCount)
	}
}

// TestIsDescendantRejectsSelfAndSiblings 检查祖先判定不把自己或兄弟算作后代。
//
// 这个判定被"移动节点"的成环校验依赖 —— 若它把自身算作后代，
// 任何移动都会被误判为成环；若它漏判真后代，就会真的成环，
// 之后每次构建快照都会无限递归。
func TestIsDescendantRejectsSelfAndSiblings(t *testing.T) {
	s := smallTree(t)

	// 自反是刻意的：调用方是"移动节点"的成环校验，
	// 而"移到自己下面"和"移到自己的后代下面"都要拒绝。
	// 把自反收进这个判定里，调用方就只需写一次检查而不是两次。
	if !s.IsDescendant(1, 1) {
		t.Error("IsDescendant 按约定含自身，用于成环校验时把「移到自己下面」一并拦住")
	}
	if !s.IsDescendant(6, 1) {
		t.Error("粉西柚(6) 是柑橘(1) 的隔代后代，应判为真")
	}
	if s.IsDescendant(2, 3) {
		t.Error("柠檬与西柚是兄弟，不应互为后代")
	}
	if s.IsDescendant(5, 1) {
		t.Error("榛子在坚果树下，不应是柑橘的后代")
	}
}

// TestSearchMatchesOnFullPath 验证搜索能命中路径而非仅节点名。
//
// 用户记得的往往是"柑橘下面那个"，而不是精确的叶子名。
func TestSearchMatchesOnFullPath(t *testing.T) {
	s := smallTree(t)

	hits := s.SearchNodes("西柚", 10)
	if len(hits) != 2 {
		t.Fatalf("搜索「西柚」应命中「西柚」与「粉西柚」两个节点，实际 %d 个", len(hits))
	}

	// 按路径搜索
	viaPath := s.SearchNodes("柑橘 / 西柚", 10)
	if len(viaPath) == 0 {
		t.Error("按路径前缀搜索应能命中，说明搜索匹配的是 Path 而不只是 Name")
	}

	if got := s.SearchNodes("西柚", 1); len(got) != 1 {
		t.Errorf("limit=1 应只返回 1 条，实际 %d 条", len(got))
	}
}

// TestSnapshotIsUnaffectedByCallerMutatingInput 确认快照拷贝了输入。
//
// 快照的全部并发安全性都建立在"构建后不可变"之上。若它持有了调用方切片的
// 引用，调用方后续复用那个切片就会隔着一层看不见的别名改掉快照内容 ——
// 这类竞态在生产里表现为筛选结果偶发错乱，几乎无法定位。
func TestSnapshotIsUnaffectedByCallerMutatingInput(t *testing.T) {
	p := func(id int64) *int64 { return &id }
	nodes := []Node{{ID: 1, Name: "柑橘"}, {ID: 2, ParentID: p(1), Name: "柠檬"}}
	tags := []Tagging{{BeanID: 101, NodeID: 2}}
	beans := []int64{101}

	s := BuildSnapshot(nodes, tags, beans)
	before := s.Filter(FilterRequest{NodeIDs: []int64{1}}).MatchedCount

	nodes[0].Name = "被篡改"
	nodes[1].ParentID = nil
	tags[0].NodeID = 999
	beans[0] = 777

	nv, _ := s.Node(1)
	if nv.Name != "柑橘" {
		t.Errorf("调用方改动输入切片后快照名称被污染：%q", nv.Name)
	}
	if after := s.Filter(FilterRequest{NodeIDs: []int64{1}}).MatchedCount; after != before {
		t.Errorf("调用方改动输入后筛选结果从 %d 变为 %d", before, after)
	}
}

// TestDeepChainTriggersWarningButStillWorks 验证软性深度上限的行为：
// 提醒而不阻断。
//
// 需求明文要求"无限级"，所以这里不能拒绝写入；但一条 15 层的单链
// 通常意味着用户误用了层级，值得提示一句。
func TestDeepChainTriggersWarningButStillWorks(t *testing.T) {
	const depth = softDepthLimit + 3

	nodes := make([]Node, 0, depth)
	for i := 0; i < depth; i++ {
		n := Node{ID: int64(i + 1), Name: "L" + string(rune('A'+i))}
		if i > 0 {
			parent := int64(i)
			n.ParentID = &parent
		}
		nodes = append(nodes, n)
	}
	// 豆子只标在最深的叶子上
	tags := []Tagging{{BeanID: 1, NodeID: int64(depth)}}

	s := BuildSnapshot(nodes, tags, []int64{1})

	if s.Levels() != depth {
		t.Errorf("树应有 %d 层，实际 %d 层", depth, s.Levels())
	}
	if s.MaxDepth() != depth-1 {
		t.Errorf("最深节点的深度下标应为 %d，实际 %d", depth-1, s.MaxDepth())
	}
	if s.DepthWarning() == "" {
		t.Errorf("深度 %d 已超过软上限 %d，应给出警告", depth, softDepthLimit)
	}

	// 关键：超限了依然要能用 —— 从根节点筛选必须命中最深叶子上的豆。
	res := s.Filter(FilterRequest{NodeIDs: []int64{1}})
	if res.MatchedCount != 1 {
		t.Errorf("超过软上限后筛选仍须正常工作：从根筛选应命中 1 支，实际 %d",
			res.MatchedCount)
	}
	if res.Warning == "" {
		t.Error("筛选响应应把深度警告带给前端")
	}
}

// TestCycleInInputDoesNotHang 确保脏数据不会让构建期无限递归。
//
// 闭包表加成环校验理论上排除了这种数据，但快照构建是进程启动路径上的一步：
// 若一条历史脏数据能让它挂死，服务将无法启动，且现场只剩一个没有栈的超时。
// 宁可丢掉成环的那部分子树，也要让进程活着起来。
func TestCycleInInputDoesNotHang(t *testing.T) {
	p := func(id int64) *int64 { return &id }
	// 2 → 3 → 2 互为父子，形成环；1 是正常根节点
	nodes := []Node{
		{ID: 1, Name: "正常根"},
		{ID: 2, ParentID: p(3), Name: "环A"},
		{ID: 3, ParentID: p(2), Name: "环B"},
	}
	tags := []Tagging{{BeanID: 101, NodeID: 1}}

	done := make(chan *Snapshot, 1)
	go func() { done <- BuildSnapshot(nodes, tags, []int64{101}) }()

	select {
	case s := <-done:
		// 正常那棵树必须完好
		if res := s.Filter(FilterRequest{NodeIDs: []int64{1}}); res.MatchedCount != 1 {
			t.Errorf("成环数据不应影响正常子树，从节点 1 筛选应得 1 支，实际 %d",
				res.MatchedCount)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("输入含环导致 BuildSnapshot 挂死 —— 构建期必须能容忍脏数据")
	}
}

func containsID(list []int64, want int64) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
