package store

import (
	"context"
	"fmt"
	"time"

	"github.com/alkaid/enjoycoffee/internal/logger"
	"github.com/alkaid/enjoycoffee/migrations"
)

// migrationLockID 是迁移用的 advisory lock 键。任意固定值即可，
// 只需保证同一部署内所有副本用同一个数。
const migrationLockID int64 = 8_1024_2026

// Migrate 顺序执行全部迁移脚本。
//
// 并发安全的实现要点（这里踩过坑，值得写清楚）：
//
// 多副本同时启动时，即使每条 DDL 都写了 IF NOT EXISTS，PostgreSQL 仍可能在
// pg_type 上抛 23505 —— 因为 CREATE TABLE IF NOT EXISTS 的存在性检查与
// 实际建表之间不是原子的。必须用 advisory lock 把迁移串行化。
//
// 而 advisory lock 是会话级的：若通过连接池执行 pg_advisory_lock 与
// pg_advisory_unlock，两条语句极可能落在池里不同的连接上，导致锁根本
// 没被释放，后续启动的副本会永久卡在这里，表现为"容器起来了但端口不监听"。
// 因此必须从池里显式取一条连接，在同一条连接上完成加锁、迁移、解锁。
func Migrate(ctx context.Context, db *DB) error {
	conn, err := db.Pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("获取迁移专用连接失败: %w", err)
	}
	defer conn.Release()

	lockCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	if _, err := conn.Exec(lockCtx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return fmt.Errorf("获取迁移锁失败: %w", err)
	}
	defer func() {
		// 用独立的 context：即使外层 ctx 已取消，也必须尝试释放锁，
		// 否则下一次启动会一直等在这里。
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer unlockCancel()
		if _, err := conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", migrationLockID); err != nil {
			logger.Warn("释放迁移锁失败", "error", err.Error())
		}
	}()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("创建迁移记录表失败: %w", err)
	}

	applied := make(map[string]bool)
	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("读取已应用迁移失败: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("扫描迁移记录失败: %w", err)
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历迁移记录失败: %w", err)
	}

	scripts, err := migrations.All()
	if err != nil {
		return fmt.Errorf("读取迁移脚本失败: %w", err)
	}

	for _, script := range scripts {
		name := script.Version
		if applied[name] {
			continue
		}

		start := time.Now()
		// 整个脚本连同版本登记在一个事务里：迁移要么完整生效并被记录，
		// 要么完全没发生。避免出现"表建了一半但版本没登记"的中间状态。
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("开启迁移事务失败: %w", err)
		}
		if _, err := tx.Exec(ctx, script.Body); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("执行迁移 %s 失败: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			"INSERT INTO schema_migrations (version) VALUES ($1)", name); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("登记迁移 %s 失败: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("提交迁移 %s 失败: %w", name, err)
		}

		logger.Info("迁移已应用", "version", name, "elapsed_ms", time.Since(start).Milliseconds())
	}

	return nil
}
