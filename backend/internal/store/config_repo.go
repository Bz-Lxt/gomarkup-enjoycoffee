package store

import (
	"context"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
	"github.com/alkaid/enjoycoffee/internal/goldcup"
	"github.com/alkaid/enjoycoffee/internal/logger"
)

// ConfigRepo 持久化可配置的金杯标准（Roadmap V-07）。
type ConfigRepo struct {
	db *DB
}

// NewConfigRepo 构造配置仓储。
func NewConfigRepo(db *DB) *ConfigRepo { return &ConfigRepo{db: db} }

// Load 读取全部自定义金杯标准。
//
// 表为空时返回空 map，引擎会使用出厂默认值。这是刻意的：
// 不在首次启动时把默认值写进表里，从而"用户没改过"与"用户改成了和默认值一样"
// 这两种状态可以区分开 —— 前者会随出厂标准的更新自动跟进，后者被固定住。
func (r *ConfigRepo) Load(ctx context.Context) (map[domain.BrewMethod]goldcup.Profile, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT method, yield_min_ppm, yield_max_ppm,
		       strength_min_ppm, strength_max_ppm,
		       ratio_min_ppm, ratio_max_ppm, lrr_ppm
		FROM brew_method_configs`)
	if err != nil {
		return nil, translateError(err, "金杯标准配置")
	}
	defer rows.Close()

	out := make(map[domain.BrewMethod]goldcup.Profile, 2)
	for rows.Next() {
		var (
			method string
			p      goldcup.Profile
		)
		if err := rows.Scan(&method, &p.YieldMin, &p.YieldMax,
			&p.StrengthMin, &p.StrengthMax, &p.RatioMin, &p.RatioMax, &p.LRR); err != nil {
			return nil, translateError(err, "金杯标准配置")
		}

		m := domain.BrewMethod(method)
		// 从出厂标准继承那些不可配置的元数据（标签、图表类型、是否用 LRR），
		// 只覆盖用户真正调整过的数值区间。
		base, berr := goldcup.DefaultProfile(m)
		if berr != nil {
			logger.Warn("配置表中存在未知冲煮法，已跳过", "method", method)
			continue
		}
		base.YieldMin, base.YieldMax = p.YieldMin, p.YieldMax
		base.StrengthMin, base.StrengthMax = p.StrengthMin, p.StrengthMax
		base.RatioMin, base.RatioMax = p.RatioMin, p.RatioMax
		if base.UsesLRR && p.LRR > 0 {
			base.LRR = p.LRR
		}

		if verr := base.Validate(); verr != nil {
			// 数据库里的配置非法时丢弃并回落到默认值，而不是让服务启动失败。
			// CHECK 约束已经拦住了大部分情况，能走到这里的通常是约束添加之前的旧数据。
			logger.Warn("持久化的金杯标准非法，已回落到出厂标准",
				"method", method, "error", verr.Error())
			continue
		}
		out[m] = base
	}
	return out, translateError(rows.Err(), "金杯标准配置")
}

// Save 写入或更新某冲煮法的金杯标准。
func (r *ConfigRepo) Save(ctx context.Context, p goldcup.Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	_, err := r.db.Pool.Exec(ctx, `
		INSERT INTO brew_method_configs (
			method, yield_min_ppm, yield_max_ppm,
			strength_min_ppm, strength_max_ppm,
			ratio_min_ppm, ratio_max_ppm, lrr_ppm, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (method) DO UPDATE SET
			yield_min_ppm = EXCLUDED.yield_min_ppm,
			yield_max_ppm = EXCLUDED.yield_max_ppm,
			strength_min_ppm = EXCLUDED.strength_min_ppm,
			strength_max_ppm = EXCLUDED.strength_max_ppm,
			ratio_min_ppm = EXCLUDED.ratio_min_ppm,
			ratio_max_ppm = EXCLUDED.ratio_max_ppm,
			lrr_ppm = EXCLUDED.lrr_ppm,
			updated_at = now()`,
		string(p.Method), int64(p.YieldMin), int64(p.YieldMax),
		int64(p.StrengthMin), int64(p.StrengthMax),
		int64(p.RatioMin), int64(p.RatioMax), int64(p.LRR))
	return translateError(err, "金杯标准配置")
}

// Reset 删除某冲煮法的自定义配置，恢复出厂标准。
func (r *ConfigRepo) Reset(ctx context.Context, m domain.BrewMethod) error {
	_, err := r.db.Pool.Exec(ctx,
		`DELETE FROM brew_method_configs WHERE method = $1`, string(m))
	return translateError(err, "金杯标准配置")
}

// ProfileFromPercents 由用户输入的百分数/倍数字符串构造 Profile。
//
// 走字符串而非 float64 是精度包契约的延伸：设置面板提交的 "1.35" 若先落进
// float64 再乘以 10000，得到的可能是 13499 而不是 13500，
// 而这 1 个 PPM 的偏差会让恰好落在边界上的历史记录判定翻转。
func ProfileFromPercents(m domain.BrewMethod, yieldMin, yieldMax, strengthMin, strengthMax, ratioMin, ratioMax, lrr string) (goldcup.Profile, error) {
	base, err := goldcup.DefaultProfile(m)
	if err != nil {
		return goldcup.Profile{}, err
	}

	e := domain.Validation("INVALID_PROFILE_INPUT", "金杯标准输入无法解析")
	bad := false

	parsePercent := func(field, raw string, target *fixed.Ratio) {
		if raw == "" {
			return
		}
		v, perr := fixed.ParsePercent(raw)
		if perr != nil {
			e.WithField(field, "必须是形如 1.35 的十进制数")
			bad = true
			return
		}
		*target = v
	}
	parseMultiple := func(field, raw string, target *fixed.Ratio) {
		if raw == "" {
			return
		}
		v, perr := fixed.ParseMultiple(raw)
		if perr != nil {
			e.WithField(field, "必须是形如 16 或 2.0 的十进制数")
			bad = true
			return
		}
		*target = v
	}

	parsePercent("yield_min_percent", yieldMin, &base.YieldMin)
	parsePercent("yield_max_percent", yieldMax, &base.YieldMax)
	parsePercent("strength_min_percent", strengthMin, &base.StrengthMin)
	parsePercent("strength_max_percent", strengthMax, &base.StrengthMax)
	parseMultiple("ratio_min", ratioMin, &base.RatioMin)
	parseMultiple("ratio_max", ratioMax, &base.RatioMax)
	if base.UsesLRR {
		parseMultiple("lrr", lrr, &base.LRR)
	}

	if bad {
		return goldcup.Profile{}, e
	}
	if verr := base.Validate(); verr != nil {
		return goldcup.Profile{}, verr
	}
	return base, nil
}
