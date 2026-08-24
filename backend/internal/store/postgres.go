// Package store 是 PostgreSQL 持久化层，实现各领域包定义的 Repository 接口。
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/logger"
)

// DB 包装连接池，为各仓储提供共享入口。
type DB struct {
	Pool *pgxpool.Pool
}

// Open 建立连接池并等待数据库就绪。
//
// 为何要主动重试而不是直接失败：compose 的 depends_on + healthcheck 已经
// 保证了 postgres 进程可接受连接，但 initdb 之后到真正能建表之间仍有
// 短暂窗口。在这个窗口里失败退出会让 backend 容器反复重启，
// 表现为一个看起来像"数据库连不上"的启动失败，实际上只需再等两秒。
func Open(ctx context.Context, dsn string, maxConns int32, waitFor time.Duration) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析 DATABASE_URL 失败: %w", err)
	}
	cfg.MaxConns = maxConns
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 10 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	// 会话级时区。即使镜像的 TZ 环境变量被遗漏，SQL 侧的 now() 与
	// DATE 转换也仍然落在北京时区。
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["timezone"] = "Asia/Shanghai"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("创建连接池失败: %w", err)
	}

	deadline := time.Now().Add(waitFor)
	attempt := 0
	for {
		attempt++
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = pool.Ping(pingCtx)
		cancel()
		if err == nil {
			logger.Info("数据库连接就绪", "attempts", attempt)
			return &DB{Pool: pool}, nil
		}
		if time.Now().After(deadline) {
			pool.Close()
			return nil, fmt.Errorf("等待数据库就绪超时（已重试 %d 次）: %w", attempt, err)
		}
		logger.Debug("数据库尚未就绪，重试中", "attempt", attempt, "error", err.Error())
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// Close 关闭连接池。
func (db *DB) Close() {
	if db != nil && db.Pool != nil {
		db.Pool.Close()
	}
}

// InTx 在事务中执行 fn，出错自动回滚。
func (db *DB) InTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	defer func() {
		// Rollback 在已提交的事务上返回 ErrTxClosed，属预期情况，无需处理
		_ = tx.Rollback(ctx)
	}()

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

// translateError 把 pgx 错误翻译为领域错误。
//
// 存在动因：handler 层不该也不能理解 SQLSTATE。更重要的是，
// 未翻译的 pgx 错误消息里含有表名、列名甚至 SQL 片段，
// 直接冒泡到 API 响应等于向外泄露 schema。
func translateError(err error, resource string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NotFound(resource, "指定条件")
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return domain.Conflict("DUPLICATE", "数据重复，违反唯一约束").WithCause(err)
		case "23503": // foreign_key_violation
			return domain.Conflict("FOREIGN_KEY", "关联的数据不存在或仍被引用").WithCause(err)
		case "23514": // check_violation
			// CHECK 约束是数据库侧的最后一道防线。走到这里说明应用层校验有漏洞，
			// 值得在日志里留下痕迹以便补齐，但对用户仍给出可读的提示。
			logger.Warn("数据库 CHECK 约束被触发，应用层校验存在缺口",
				"constraint", pgErr.ConstraintName, "detail", pgErr.Detail)
			return domain.Validation("CHECK_VIOLATION",
				"数据不满足业务约束: "+pgErr.ConstraintName).WithCause(err)
		case "23502": // not_null_violation
			return domain.Validation("MISSING_REQUIRED_FIELD",
				"缺少必填字段: "+pgErr.ColumnName).WithCause(err)
		}
	}

	return domain.Internal("数据库操作失败").WithCause(err)
}

// nullableDate 把可空的 DATE 列转为民用日期。
func nullableDate(t *time.Time) domain.CivilDate {
	if t == nil {
		return domain.CivilDate{}
	}
	return domain.ToCivilDate(*t)
}

// datePtr 把民用日期转为可空 DATE 参数。
func datePtr(d domain.CivilDate) *time.Time {
	if d.IsZero() {
		return nil
	}
	t := d.Time()
	return &t
}

// nullableTime 把零值时刻转为 SQL NULL，用于"未指定则不过滤"的查询条件。
func nullableTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
