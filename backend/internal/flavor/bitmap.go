package flavor

import "math/bits"

// Bitset 是定长位图集合，用于表示"哪些豆子带有某个风味"。
//
// 为何自己写而不用 map[int64]bool 或 roaring bitmap：
//
// 多级联动筛选的热路径是「取若干个集合的交集」。map 的交集需要遍历其中一个 map
// 并逐个查另一个，涉及大量哈希计算与随机内存访问；在 2000 款豆子的规模下，
// 单次交集就要几十微秒，而筛选条件叠加三四层后就逼近毫秒级 —— 距 10ms 的
// 承诺只剩一个数量级的余量，不够安全。
//
// 位图把交集变成按字与运算：2000 个豆子只需 32 个 uint64，一次交集是 32 条
// AND 指令，纳秒级完成，且全部数据落在同一个 CPU 缓存行组内。
//
// 为何不用 roaring bitmap：roaring 的优势在于稀疏超大值域（百万级 ID）。
// 本场景豆子数量是千级且 ID 稠密（映射为连续序号后无空洞），
// 定长位图的常数因子更小，且没有外部依赖。
type Bitset struct {
	words []uint64
	size  int // 值域大小（可容纳的序号数），非置位个数
}

// NewBitset 创建可容纳 size 个元素（序号 0..size-1）的位图。
func NewBitset(size int) *Bitset {
	if size < 0 {
		size = 0
	}
	return &Bitset{
		words: make([]uint64, (size+63)/64),
		size:  size,
	}
}

// Set 置位序号 i。越界的序号被静默忽略 —— 调用方是快照构建器，
// 越界只可能源于构建逻辑错误，而在热路径上做 panic 检查得不偿失。
func (b *Bitset) Set(i int) {
	if i < 0 || i >= b.size {
		return
	}
	b.words[i>>6] |= 1 << (uint(i) & 63)
}

// Has 报告序号 i 是否置位。
func (b *Bitset) Has(i int) bool {
	if i < 0 || i >= b.size {
		return false
	}
	return b.words[i>>6]&(1<<(uint(i)&63)) != 0
}

// Count 返回置位个数。
func (b *Bitset) Count() int {
	n := 0
	for _, w := range b.words {
		n += bits.OnesCount64(w)
	}
	return n
}

// IsEmpty 报告位图是否无任何置位。
//
// 提前短路的价值：多条件交集中一旦出现空集，后续所有交集都是空集，
// 可以立刻返回。在"用户选了互斥的风味组合"这个常见场景下省掉全部剩余运算。
func (b *Bitset) IsEmpty() bool {
	for _, w := range b.words {
		if w != 0 {
			return false
		}
	}
	return true
}

// Clone 返回位图的独立副本。
func (b *Bitset) Clone() *Bitset {
	cp := &Bitset{words: make([]uint64, len(b.words)), size: b.size}
	copy(cp.words, b.words)
	return cp
}

// UnionInto 把 other 并入 b（原地修改）。
func (b *Bitset) UnionInto(other *Bitset) {
	if other == nil {
		return
	}
	n := len(b.words)
	if len(other.words) < n {
		n = len(other.words)
	}
	for i := 0; i < n; i++ {
		b.words[i] |= other.words[i]
	}
}

// IntersectInto 把 b 与 other 求交（原地修改）。
func (b *Bitset) IntersectInto(other *Bitset) {
	if other == nil {
		// 与空集求交得空集
		for i := range b.words {
			b.words[i] = 0
		}
		return
	}
	for i := range b.words {
		if i < len(other.words) {
			b.words[i] &= other.words[i]
		} else {
			b.words[i] = 0
		}
	}
}

// Each 按序号升序遍历所有置位元素。
//
// 用 TrailingZeros64 逐位跳跃而非 0..size 全扫：当置位稀疏时
// （用户选了个罕见风味，只命中 3 款豆），遍历代价与命中数成正比而非与值域成正比。
func (b *Bitset) Each(fn func(i int)) {
	for wi, w := range b.words {
		for w != 0 {
			t := bits.TrailingZeros64(w)
			fn(wi<<6 + t)
			w &= w - 1 // 清除最低置位
		}
	}
}

// Slice 返回全部置位序号的升序切片。
func (b *Bitset) Slice() []int {
	out := make([]int, 0, b.Count())
	b.Each(func(i int) { out = append(out, i) })
	return out
}
