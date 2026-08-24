package flavor

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alkaid/enjoycoffee/internal/logger"
)

// Repository 是风味树的持久化出口。
//
// 定义为接口而非直接依赖 pgx，是为了让 Cache 与 Service 的全部行为都能在
// 内存假实现上做确定性测试 —— 包括那个 10ms 的性能基准。若性能测试必须
// 连真实数据库，它就会变成一个又慢又不稳定的测试，最终被人跳过。
type Repository interface {
	ListNodes(ctx context.Context) ([]Node, error)
	ListTaggings(ctx context.Context) ([]Tagging, error)
	ListBeanIDs(ctx context.Context) ([]int64, error)

	CreateNode(ctx context.Context, n Node) (int64, error)
	UpdateNode(ctx context.Context, n Node) error
	MoveNode(ctx context.Context, id int64, newParentID *int64) error
	DeleteSubtree(ctx context.Context, id int64) (int, error)
	ReparentChildren(ctx context.Context, id int64, newParentID *int64) error
	DeleteNode(ctx context.Context, id int64) error

	SetBeanFlavors(ctx context.Context, beanID int64, nodeIDs []int64) error
}

// Cache 持有风味树的内存物化快照，并负责其生命周期。
//
// 并发模型：读路径通过 atomic.Pointer 无锁取快照；写路径在互斥锁保护下
// 重建整棵快照后原子替换指针。正在进行的读操作继续持有旧快照直到完成，
// 不会看到半更新状态，也不会被写操作阻塞。
type Cache struct {
	repo Repository
	snap atomic.Pointer[Snapshot]

	// rebuildMu 只保护"重建"这个动作本身，防止并发写操作各自基于陈旧数据
	// 重建快照后互相覆盖。它不参与读路径。
	rebuildMu sync.Mutex

	refreshEvery time.Duration
	stopOnce     sync.Once
	stop         chan struct{}
}

// NewCache 构造缓存并立即完成首次加载。
//
// 首次加载失败即返回错误而非降级为空树：一个空的风味树会让前端筛选界面
// 看起来"正常但没有任何分类"，比明确的启动失败更难排查。
func NewCache(ctx context.Context, repo Repository, refreshEvery time.Duration) (*Cache, error) {
	c := &Cache{
		repo:         repo,
		refreshEvery: refreshEvery,
		stop:         make(chan struct{}),
	}
	if err := c.Rebuild(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// Snapshot 返回当前快照。永不返回 nil：构造函数保证首次加载成功，
// 且重建失败时保留旧快照而非置空。
func (c *Cache) Snapshot() *Snapshot {
	if s := c.snap.Load(); s != nil {
		return s
	}
	// 理论不可达的兜底。返回空快照而非 nil，避免调用方每处都判空。
	return BuildSnapshot(nil, nil, nil)
}

// Rebuild 从仓储重新加载并替换快照。
func (c *Cache) Rebuild(ctx context.Context) error {
	c.rebuildMu.Lock()
	defer c.rebuildMu.Unlock()

	start := time.Now()

	nodes, err := c.repo.ListNodes(ctx)
	if err != nil {
		return err
	}
	taggings, err := c.repo.ListTaggings(ctx)
	if err != nil {
		return err
	}
	beanIDs, err := c.repo.ListBeanIDs(ctx)
	if err != nil {
		return err
	}

	snap := BuildSnapshot(nodes, taggings, beanIDs)
	c.snap.Store(snap)

	logger.Debug("风味树快照已重建",
		"nodes", snap.NodeCount(),
		"beans", snap.BeanCount(),
		"depth_levels", snap.Levels(),
		"elapsed_ms", time.Since(start).Milliseconds())
	return nil
}

// StartRefreshLoop 启动兜底重建循环。
//
// 正常路径是每次写操作后主动 Rebuild。这个循环只是防御性兜底：
// 若将来新增了某条忘记触发失效的写路径，缓存最多陈旧一个周期，
// 而不会永久停留在旧状态。它不是主要的一致性机制。
func (c *Cache) StartRefreshLoop(ctx context.Context) {
	if c.refreshEvery <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(c.refreshEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-c.stop:
				return
			case <-ticker.C:
				if err := c.Rebuild(ctx); err != nil {
					// 重建失败保留旧快照继续服务。风味树是辅助检索功能，
					// 让它降级为"数据略旧"远好于让整个豆库页面报错。
					logger.Warn("风味树快照定期重建失败，继续使用旧快照",
						"error", err.Error())
				}
			}
		}
	}()
}

// Close 停止兜底循环。多次调用是安全的。
func (c *Cache) Close() {
	c.stopOnce.Do(func() { close(c.stop) })
}
