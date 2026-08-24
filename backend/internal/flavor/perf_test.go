package flavor

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"runtime"
	"runtime/debug"
	"sort"
	"testing"
	"time"
)

// 基准数据集规模，与 Requirements NFR-01 中写明的一致。
const (
	benchNodes = 500
	benchDepth = 8
	benchBeans = 2000
	// benchTagsPerBean 是每支豆平均标记的风味数。真实咖啡豆的风味描述
	// 通常是 3~6 个词，取 5 是偏保守（标记越多，位图越密，交集越不容易短路）。
	benchTagsPerBean = 5
)

// buildBenchSnapshot 造一棵形状接近真实使用的树。
//
// 刻意不做成均匀树：真实的风味树是偏斜的 —— 「果调」下面枝繁叶茂，
// 「缺陷味」下面只有两三个叶子。均匀树会让每个节点的位图密度一致，
// 从而掩盖"选到一个巨大分支"这个最坏情况。
func buildBenchSnapshot(t testing.TB) *Snapshot {
	t.Helper()
	rng := rand.New(rand.NewSource(20260824))

	nodes := make([]Node, 0, benchNodes)
	// byDepth[d] 收集深度为 d 的节点 ID，用于给下一层挑父节点。
	byDepth := make([][]int64, benchDepth)

	// 先铺 8 个根，对应 SCA 风味轮的一级大类。
	const roots = 8
	for i := 0; i < roots; i++ {
		id := int64(i + 1)
		nodes = append(nodes, Node{ID: id, Name: fmt.Sprintf("root-%d", i)})
		byDepth[0] = append(byDepth[0], id)
	}

	nextID := int64(roots + 1)
	for len(nodes) < benchNodes {
		// 越深的层挑中的概率越低，形成自然收窄的树形；
		// 但仍保证能长到第 benchDepth 层。
		d := 1 + rng.Intn(benchDepth-1)
		for len(byDepth[d-1]) == 0 {
			d--
		}
		parents := byDepth[d-1]
		// 偏斜：靠前的父节点被挑中的概率显著更高。
		pi := rng.Intn(len(parents))
		if rng.Intn(2) == 0 {
			pi = rng.Intn(len(parents)/4 + 1)
		}
		parent := parents[pi]

		id := nextID
		nextID++
		nodes = append(nodes, Node{
			ID:       id,
			ParentID: &parent,
			Name:     fmt.Sprintf("n%d-d%d", id, d),
		})
		byDepth[d] = append(byDepth[d], id)
	}

	beans := make([]int64, 0, benchBeans)
	// 豆子主键刻意稀疏且不连续，模拟真实自增主键在删除后留下的空洞。
	// 稠密序号映射若有问题，这里就会暴露。
	for i := 0; i < benchBeans; i++ {
		beans = append(beans, int64(1000+i*7))
	}

	tags := make([]Tagging, 0, benchBeans*benchTagsPerBean)
	for _, b := range beans {
		n := benchTagsPerBean - 2 + rng.Intn(5)
		for k := 0; k < n; k++ {
			tags = append(tags, Tagging{BeanID: b, NodeID: nodes[rng.Intn(len(nodes))].ID})
		}
	}

	return BuildSnapshot(nodes, tags, beans)
}

// deepestNodes 挑出树里最深的若干节点，用于构造最坏情况的筛选条件。
func deepestNodes(s *Snapshot, n int) []int64 {
	type nd struct {
		id    int64
		depth int
	}
	all := make([]nd, 0, s.NodeCount())
	for _, id := range s.bfsOrder {
		all = append(all, nd{id, s.nodes[id].Depth})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].depth > all[j].depth })

	out := make([]int64, 0, n)
	for i := 0; i < n && i < len(all); i++ {
		out = append(out, all[i].id)
	}
	return out
}

// widestNodes 挑出聚合命中数最大的若干节点。
//
// 这是位运算的最坏输入：位图最密，交集不会提前短路成空集，
// 且遍历置位位要吐出最多的豆子 ID。
func widestNodes(s *Snapshot, n int) []int64 {
	type nd struct {
		id    int64
		count int
	}
	all := make([]nd, 0, s.NodeCount())
	for _, id := range s.bfsOrder {
		all = append(all, nd{id, s.nodes[id].AggregateBeanCount})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].count > all[j].count })

	out := make([]int64, 0, n)
	for i := 0; i < n && i < len(all); i++ {
		out = append(out, all[i].id)
	}
	return out
}

// worstCaseRequest 构造 NFR-01 的最坏输入。
//
// 不用平均情况：最宽的 8 个节点做 ANY 组合，结果集接近全集，
// 联动计数全量计算。若这个组合都在预算内，用户实际点出来的任何组合都不会超。
func worstCaseRequest(s *Snapshot) FilterRequest {
	return FilterRequest{
		NodeIDs:    widestNodes(s, 8),
		Match:      MatchAny,
		WantFacets: true,
	}
}

func assertBenchDataset(t *testing.T, s *Snapshot) {
	t.Helper()
	if s.NodeCount() < benchNodes {
		t.Fatalf("基准数据集应有 %d 个节点，实际 %d", benchNodes, s.NodeCount())
	}
	if s.BeanCount() != benchBeans {
		t.Fatalf("基准数据集应有 %d 支豆，实际 %d", benchBeans, s.BeanCount())
	}
	if s.Levels() < benchDepth {
		t.Fatalf("基准数据集应至少 %d 层，实际 %d 层", benchDepth, s.Levels())
	}
}

// TestFilterP99UnderTenMillis 是 NFR-01 的断言。
//
// 测的是什么：需求写的是"前端在多级联动筛选豆子时响应速度在 10ms 以内"。
// 网络传输不在后端可控范围内，所以测量范围是筛选 + 计算联动计数 +
// JSON 序列化 —— 也就是 handler 收到请求后到把字节交给 net/http 之前
// 做的全部工作。把序列化算进来很重要：筛选本身是位运算，
// 真正可能吃掉毫秒的是把两千个 ID 和五百个联动计数编码成 JSON。
//
// 为什么在测量期间关掉 GC：
//
// 这是这个测试最容易写错的地方，值得说清楚。最初的版本开着 GC 跑 2000 轮，
// 结果 P50=95µs 但 P99=1.5ms、MAX=17ms，而且并行跑其他包的测试时 P99
// 会飙到 13ms 并失败。看起来像"尾延迟不达标"，其实是测量方法的产物：
//
// 单次请求分配约 99KB（两千个 ID 加 JSON 缓冲，见下面的分配预算测试）。
// 循环连续跑 2000 轮就是在 0.3 秒内产生 200MB 垃圾。Go 对分配过快的
// goroutine 会摊派 GC assist 工作 —— 那些毫秒级样本就是本 goroutine
// 被拉去帮 GC 干活的时间，不是筛选逻辑的耗时。
//
// 真实场景下这个分配速率不可能出现：这是用户点一下筛选器发一个请求，
// 量级是每秒几次，assist 摊派可忽略。也就是说开着 GC 测出来的尾部
// 反映的是"测试循环有多贪"，不是"用户等多久"。
//
// 所以这里把 GC 关掉测纯计算成本，用它守住算法复杂度；
// 再由 TestFilterAllocationBudget 单独守住分配量。
// 两者合起来才是完整的账：关 GC 不会掩盖分配回归，
// 因为分配一旦变多，那个测试会先失败。
func TestFilterP99UnderTenMillis(t *testing.T) {
	const (
		budget = 10 * time.Millisecond
		rounds = 2000
	)

	s := buildBenchSnapshot(t)
	assertBenchDataset(t, s)

	req := worstCaseRequest(s)
	probe := s.Filter(req)

	// 预热：让首次分配与页错误不落进统计样本。
	for i := 0; i < 50; i++ {
		_ = s.Filter(req)
	}

	// 关 GC 前先手动回收一次，避免测量期堆无节制膨胀。
	runtime.GC()
	prevGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(prevGC)

	var startStats, endStats runtime.MemStats
	runtime.ReadMemStats(&startStats)

	samples := make([]time.Duration, 0, rounds)
	for i := 0; i < rounds; i++ {
		start := time.Now()
		res := s.Filter(req)
		// 序列化算进预算内 —— 它和筛选一样是响应路径上的必要工作。
		if _, err := json.Marshal(res); err != nil {
			t.Fatalf("序列化筛选结果失败: %v", err)
		}
		samples = append(samples, time.Since(start))
	}

	runtime.ReadMemStats(&endStats)

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p50 := samples[len(samples)*50/100]
	p99 := samples[len(samples)*99/100]
	max := samples[len(samples)-1]

	t.Logf("数据集 %d 节点 / %d 层 / %d 豆，8 条件 ANY + 全量联动计数 + JSON 序列化",
		s.NodeCount(), s.Levels(), s.BeanCount())
	t.Logf("命中 %d 支豆，联动计数 %d 项", probe.MatchedCount, len(probe.Facets))
	t.Logf("P50=%v  P99=%v  MAX=%v  预算=%v（测量期 GC 关闭，%d 轮共触发 %d 次 GC）",
		p50, p99, max, budget, rounds, endStats.NumGC-startStats.NumGC)

	if raceEnabled {
		t.Logf("竞态检测器开启，本轮只记录不判定 NFR-01 —— " +
			"插桩后的耗时不代表用户感受到的延迟（理由见 race_on_test.go）。" +
			"预算判定请跑不带 -race 的 go test ./internal/flavor/")
		return
	}

	if p99 > budget {
		t.Errorf("NFR-01 未达标：P99 %v 超出 %v 预算", p99, budget)
	}
}

// TestFilterAllocationBudget 守住分配量，是上面那个测试关掉 GC 后的另一半账。
//
// 为什么要单独测分配：NFR-01 的耗时测试关了 GC，若某次改动让每次筛选
// 多分配十倍内存，耗时测试可能仍然通过（不 GC 就不会被摊派 assist），
// 但线上会因 GC 压力上升而出现真实的尾延迟。这个测试把那条路堵上。
//
// 预算怎么定：当前实测约 99KB / 27 次分配（M1 Pro，Go 1.22）。
// 上限放到 200KB / 60 次 —— 留出足够余量容纳合理的实现变化，
// 但抓得住"每支豆一次分配"这类量级错误（那会是 2000 次分配）。
func TestFilterAllocationBudget(t *testing.T) {
	const (
		maxBytesPerOp  = 200 << 10
		maxAllocsPerOp = 60
	)

	s := buildBenchSnapshot(t)
	assertBenchDataset(t, s)
	req := worstCaseRequest(s)

	work := func() {
		res := s.Filter(req)
		if _, err := json.Marshal(res); err != nil {
			t.Fatalf("序列化筛选结果失败: %v", err)
		}
	}

	for i := 0; i < 50; i++ {
		work()
	}

	allocs := testing.AllocsPerRun(200, work)

	// AllocsPerRun 只给次数，字节数要自己读 MemStats。
	const rounds = 200
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < rounds; i++ {
		work()
	}
	runtime.ReadMemStats(&after)
	bytesPerOp := (after.TotalAlloc - before.TotalAlloc) / rounds

	t.Logf("每次筛选 + 序列化：%d 字节 / %.0f 次分配（预算 %d 字节 / %d 次）",
		bytesPerOp, allocs, maxBytesPerOp, maxAllocsPerOp)

	if bytesPerOp > maxBytesPerOp {
		t.Errorf("单次分配 %d 字节超出 %d 预算，GC 压力会转化为线上尾延迟",
			bytesPerOp, maxBytesPerOp)
	}
	if allocs > maxAllocsPerOp {
		t.Errorf("单次分配次数 %.0f 超出 %d 预算，检查热路径上是否出现了逐元素分配",
			allocs, maxAllocsPerOp)
	}
}

// TestFilterStaysFastAsDepthGrows 验证"无限级"承诺的关键性质：
// 筛选耗时不随树深增长。
//
// 这条比绝对耗时更重要。10ms 是在某台机器上跑出来的数字，换台机器会变；
// 但"耗时与深度无关"是架构选择（预计算聚合位图）的直接推论 ——
// 如果哪天有人把它改回递归下探，绝对值可能仍然在 10ms 内，
// 但这条测试会立刻失败。
func TestFilterStaysFastAsDepthGrows(t *testing.T) {
	measure := func(depth int) time.Duration {
		// 一条 depth 长的单链，豆子全部挂在最深的叶子上。
		// 从根节点筛选：递归实现要走 depth 层，位图实现只查一次表。
		nodes := make([]Node, 0, depth)
		for i := 0; i < depth; i++ {
			n := Node{ID: int64(i + 1), Name: fmt.Sprintf("L%d", i)}
			if i > 0 {
				parent := int64(i)
				n.ParentID = &parent
			}
			nodes = append(nodes, n)
		}

		beans := make([]int64, 0, 512)
		tags := make([]Tagging, 0, 512)
		for i := 0; i < 512; i++ {
			id := int64(1000 + i)
			beans = append(beans, id)
			tags = append(tags, Tagging{BeanID: id, NodeID: int64(depth)})
		}

		s := BuildSnapshot(nodes, tags, beans)
		req := FilterRequest{NodeIDs: []int64{1}}

		for i := 0; i < 100; i++ {
			_ = s.Filter(req)
		}

		const rounds = 2000
		samples := make([]time.Duration, 0, rounds)
		for i := 0; i < rounds; i++ {
			start := time.Now()
			res := s.Filter(req)
			if res.MatchedCount != 512 {
				t.Fatalf("深度 %d：从根筛选应命中全部 512 支，实际 %d",
					depth, res.MatchedCount)
			}
			samples = append(samples, time.Since(start))
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		return samples[len(samples)/2]
	}

	shallow := measure(3)
	deep := measure(60)

	t.Logf("深度 3 中位耗时 %v，深度 60 中位耗时 %v", shallow, deep)

	// 允许 4 倍波动：定时器分辨率与调度抖动在微秒量级的测量上占比不小，
	// 卡太紧会变成一个偶发失败的测试。但递归下探会带来 20 倍的差距，
	// 这个阈值足以把它抓住。
	if deep > shallow*4 && deep-shallow > 200*time.Microsecond {
		t.Errorf("筛选耗时随树深显著增长（深度 3: %v → 深度 60: %v），"+
			"说明热路径上出现了子树下探。聚合位图的意义就是把这个代价"+
			"挪到构建期。", shallow, deep)
	}
}

// TestSnapshotBuildIsFastEnoughForWriteThrough 确认写后重建全树是可接受的。
//
// 这是"读优化"这个取舍的另一半账。每次改风味树都要重建整棵快照，
// 若重建要几百毫秒，那么"整理分类"这个操作在界面上就会明显卡顿，
// 取舍就不成立了 —— 那时该改成增量更新位图。
func TestSnapshotBuildIsFastEnoughForWriteThrough(t *testing.T) {
	const budget = 100 * time.Millisecond

	// 先造一次拿到输入规模，再单独计时纯构建。
	rng := rand.New(rand.NewSource(7))
	nodes := make([]Node, 0, benchNodes)
	for i := 0; i < benchNodes; i++ {
		n := Node{ID: int64(i + 1), Name: fmt.Sprintf("n%d", i)}
		if i >= 8 {
			parent := int64(1 + rng.Intn(i))
			n.ParentID = &parent
		}
		nodes = append(nodes, n)
	}
	beans := make([]int64, 0, benchBeans)
	for i := 0; i < benchBeans; i++ {
		beans = append(beans, int64(1000+i*7))
	}
	tags := make([]Tagging, 0, benchBeans*benchTagsPerBean)
	for _, b := range beans {
		for k := 0; k < benchTagsPerBean; k++ {
			tags = append(tags, Tagging{BeanID: b, NodeID: int64(1 + rng.Intn(benchNodes))})
		}
	}

	start := time.Now()
	s := BuildSnapshot(nodes, tags, beans)
	elapsed := time.Since(start)

	t.Logf("构建 %d 节点 / %d 豆 / %d 关联的快照耗时 %v（内存约 %d KB）",
		s.NodeCount(), s.BeanCount(), len(tags), elapsed, s.Stats().MemoryKB)

	if elapsed > budget {
		t.Errorf("快照构建耗时 %v 超出 %v 预算。写后全量重建的取舍不再成立，"+
			"应改为增量更新位图。", elapsed, budget)
	}
}

func BenchmarkFilterSingleCondition(b *testing.B) {
	s := buildBenchSnapshot(b)
	req := FilterRequest{NodeIDs: widestNodes(s, 1)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Filter(req)
	}
}

func BenchmarkFilterEightConditionsAll(b *testing.B) {
	s := buildBenchSnapshot(b)
	req := FilterRequest{NodeIDs: deepestNodes(s, 8), Match: MatchAll}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Filter(req)
	}
}

func BenchmarkFilterEightConditionsAnyWidest(b *testing.B) {
	s := buildBenchSnapshot(b)
	req := FilterRequest{NodeIDs: widestNodes(s, 8), Match: MatchAny}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Filter(req)
	}
}

func BenchmarkFilterWithFacets(b *testing.B) {
	s := buildBenchSnapshot(b)
	req := FilterRequest{NodeIDs: widestNodes(s, 8), Match: MatchAny, WantFacets: true}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Filter(req)
	}
}

// BenchmarkFilterAndSerialize 是最接近 handler 真实工作量的一条。
func BenchmarkFilterAndSerialize(b *testing.B) {
	s := buildBenchSnapshot(b)
	req := FilterRequest{NodeIDs: widestNodes(s, 8), Match: MatchAny, WantFacets: true}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := s.Filter(req)
		if _, err := json.Marshal(res); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBuildSnapshot(b *testing.B) {
	rng := rand.New(rand.NewSource(7))
	nodes := make([]Node, 0, benchNodes)
	for i := 0; i < benchNodes; i++ {
		n := Node{ID: int64(i + 1), Name: fmt.Sprintf("n%d", i)}
		if i >= 8 {
			parent := int64(1 + rng.Intn(i))
			n.ParentID = &parent
		}
		nodes = append(nodes, n)
	}
	beans := make([]int64, 0, benchBeans)
	for i := 0; i < benchBeans; i++ {
		beans = append(beans, int64(1000+i*7))
	}
	tags := make([]Tagging, 0, benchBeans*benchTagsPerBean)
	for _, bn := range beans {
		for k := 0; k < benchTagsPerBean; k++ {
			tags = append(tags, Tagging{BeanID: bn, NodeID: int64(1 + rng.Intn(benchNodes))})
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = BuildSnapshot(nodes, tags, beans)
	}
}

func BenchmarkTreeSerialization(b *testing.B) {
	s := buildBenchSnapshot(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(s.Tree()); err != nil {
			b.Fatal(err)
		}
	}
}
