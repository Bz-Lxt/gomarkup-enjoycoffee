package store

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alkaid/enjoycoffee/internal/bean"
	"github.com/alkaid/enjoycoffee/internal/brew"
	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/goldcup"
)

// BrewRepo 实现 brew.Repository。
type BrewRepo struct {
	db *DB
}

// NewBrewRepo 构造萃取记录仓储。
func NewBrewRepo(db *DB) *BrewRepo { return &BrewRepo{db: db} }

const brewColumns = `
	id, bean_id, method, title,
	dose_mg, total_water_mg, beverage_mg, tds_ppm, lrr_ppm,
	grinder, grind_setting, grind_micron,
	water_temp_c, dripper, agitation_count,
	pre_infusion_sec, pressure_bar_x10, contact_seconds,
	notes, brewed_at,
	mode, yield_ppm, tds_calc_ppm, ratio_ppm, beverage_calc_mg,
	zone_code, in_gold_cup, confidence_x1000,
	created_at, updated_at`

func scanBrew(row pgx.Row) (*brew.Brew, error) {
	var (
		b      brew.Brew
		method string
		mode   string
	)
	err := row.Scan(
		&b.ID, &b.BeanID, &method, &b.Title,
		&b.DoseMg, &b.TotalWaterMg, &b.BeverageMg, &b.TDS, &b.LRROverride,
		&b.Grinder, &b.GrindSetting, &b.GrindMicron,
		&b.WaterTempC, &b.Dripper, &b.AgitationCount,
		&b.PreInfusionSec, &b.PressureBarX10, &b.ContactSeconds,
		&b.Notes, &b.BrewedAt,
		&mode, &b.YieldPPM, &b.TDSCalcPPM, &b.RatioPPM, &b.BeverageCalcMg,
		&b.ZoneCode, &b.InGoldCup, &b.Confidence,
		&b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	b.Method = domain.BrewMethod(method)
	b.Mode = goldcup.Mode(mode)
	b.PourEvents = []brew.PourEvent{}
	return &b, nil
}

// Create 插入一条萃取记录。
func (r *BrewRepo) Create(ctx context.Context, b *brew.Brew) (int64, error) {
	var id int64
	err := r.db.Pool.QueryRow(ctx, `
		INSERT INTO brew_sessions (
			bean_id, method, title,
			dose_mg, total_water_mg, beverage_mg, tds_ppm, lrr_ppm,
			grinder, grind_setting, grind_micron,
			water_temp_c, dripper, agitation_count,
			pre_infusion_sec, pressure_bar_x10, contact_seconds,
			notes, brewed_at,
			mode, yield_ppm, tds_calc_ppm, ratio_ppm, beverage_calc_mg,
			zone_code, in_gold_cup, confidence_x1000
		) VALUES (
			$1, $2, $3,
			$4, $5, $6, $7, $8,
			$9, $10, $11,
			$12, $13, $14,
			$15, $16, $17,
			$18, $19,
			$20, $21, $22, $23, $24,
			$25, $26, $27
		) RETURNING id`,
		b.BeanID, string(b.Method), b.Title,
		int64(b.DoseMg), int64(b.TotalWaterMg), int64(b.BeverageMg), int64(b.TDS), int64(b.LRROverride),
		b.Grinder, b.GrindSetting, b.GrindMicron,
		b.WaterTempC, b.Dripper, b.AgitationCount,
		b.PreInfusionSec, b.PressureBarX10, b.ContactSeconds,
		b.Notes, b.BrewedAt,
		string(b.Mode), int64(b.YieldPPM), int64(b.TDSCalcPPM), int64(b.RatioPPM), int64(b.BeverageCalcMg),
		b.ZoneCode, b.InGoldCup, b.Confidence,
	).Scan(&id)
	if err != nil {
		return 0, translateError(err, "萃取记录")
	}
	return id, nil
}

// Update 更新一条萃取记录。
func (r *BrewRepo) Update(ctx context.Context, b *brew.Brew) error {
	tag, err := r.db.Pool.Exec(ctx, `
		UPDATE brew_sessions SET
			bean_id = $1, method = $2, title = $3,
			dose_mg = $4, total_water_mg = $5, beverage_mg = $6, tds_ppm = $7, lrr_ppm = $8,
			grinder = $9, grind_setting = $10, grind_micron = $11,
			water_temp_c = $12, dripper = $13, agitation_count = $14,
			pre_infusion_sec = $15, pressure_bar_x10 = $16, contact_seconds = $17,
			notes = $18, brewed_at = $19,
			mode = $20, yield_ppm = $21, tds_calc_ppm = $22, ratio_ppm = $23, beverage_calc_mg = $24,
			zone_code = $25, in_gold_cup = $26, confidence_x1000 = $27
		WHERE id = $28`,
		b.BeanID, string(b.Method), b.Title,
		int64(b.DoseMg), int64(b.TotalWaterMg), int64(b.BeverageMg), int64(b.TDS), int64(b.LRROverride),
		b.Grinder, b.GrindSetting, b.GrindMicron,
		b.WaterTempC, b.Dripper, b.AgitationCount,
		b.PreInfusionSec, b.PressureBarX10, b.ContactSeconds,
		b.Notes, b.BrewedAt,
		string(b.Mode), int64(b.YieldPPM), int64(b.TDSCalcPPM), int64(b.RatioPPM), int64(b.BeverageCalcMg),
		b.ZoneCode, b.InGoldCup, b.Confidence,
		b.ID,
	)
	if err != nil {
		return translateError(err, "萃取记录")
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("萃取记录", b.ID)
	}
	return nil
}

// Get 查单条萃取记录。
func (r *BrewRepo) Get(ctx context.Context, id int64) (*brew.Brew, error) {
	row := r.db.Pool.QueryRow(ctx, `SELECT `+brewColumns+` FROM brew_sessions WHERE id = $1`, id)
	b, err := scanBrew(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.NotFound("萃取记录", id)
		}
		return nil, translateError(err, "萃取记录")
	}
	return b, nil
}

// List 查询萃取记录列表。
//
// 全部条件用「参数为零值时该条件恒真」的形式表达，从而保持单一 SQL 文本。
// 动态拼接 WHERE 会让每种筛选组合产生不同的语句，既废掉预备语句缓存，
// 也让占位符编号变成需要小心维护的手工活 —— 而 $n 编号一旦出现空洞，
// PostgreSQL 会报"无法推断参数类型"，且错误信息完全指不到真正的原因。
func (r *BrewRepo) List(ctx context.Context, f brew.ListFilter) ([]*brew.Brew, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	rows, err := r.db.Pool.Query(ctx, `
		SELECT `+brewColumns+` FROM brew_sessions
		WHERE ($1::bigint = 0 OR bean_id = $1::bigint)
		  AND ($2::text = '' OR method = $2::text)
		  AND ($3::boolean = FALSE OR in_gold_cup = TRUE)
		  AND ($4::boolean = FALSE OR mode = 'MEASURED')
		  AND ($5::timestamptz IS NULL OR brewed_at >= $5::timestamptz)
		ORDER BY brewed_at DESC, id DESC
		LIMIT $6 OFFSET $7`,
		f.BeanID, string(f.Method), f.OnlyGold, f.OnlyMeasured,
		nullableTime(f.Since), limit, maxInt(f.Offset, 0),
	)
	if err != nil {
		return nil, translateError(err, "萃取记录")
	}
	defer rows.Close()

	out := make([]*brew.Brew, 0, 64)
	for rows.Next() {
		b, err := scanBrew(rows)
		if err != nil {
			return nil, translateError(err, "萃取记录")
		}
		out = append(out, b)
	}
	return out, translateError(rows.Err(), "萃取记录")
}

// Delete 删除一条萃取记录。关联的注水节点与评分由外键级联清理。
func (r *BrewRepo) Delete(ctx context.Context, id int64) error {
	tag, err := r.db.Pool.Exec(ctx, `DELETE FROM brew_sessions WHERE id = $1`, id)
	if err != nil {
		return translateError(err, "萃取记录")
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("萃取记录", id)
	}
	return nil
}

// ReplacePourEvents 覆盖式写入注水节点。
//
// 采用「全删全插」而非增量 upsert：注水序列是一个整体，中间插入一个节点
// 会改变相邻段的流速计算。增量维护需要处理插入位置、序号重排、
// 以及"删掉中间一个节点后累计值是否还单调"这些问题，
// 而序列长度通常只有个位数到十几个，整体重写的代价完全可以接受。
//
// 幂等去重发生在服务层（MergePourEvents），到这里已经是最终态。
func (r *BrewRepo) ReplacePourEvents(ctx context.Context, brewID int64, events []brew.PourEvent) error {
	return r.db.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM pour_events WHERE brew_id = $1`, brewID); err != nil {
			return translateError(err, "注水节点")
		}
		if len(events) == 0 {
			return nil
		}

		offsets := make([]int32, 0, len(events))
		cumulatives := make([]int64, 0, len(events))
		techniques := make([]string, 0, len(events))
		sources := make([]string, 0, len(events))
		keys := make([]string, 0, len(events))

		for _, e := range events {
			tech := e.Technique
			if tech == "" {
				tech = domain.PourCircle
			}
			src := e.Source
			if src == "" {
				src = brew.SourceManual
			}
			offsets = append(offsets, int32(e.OffsetMs))
			cumulatives = append(cumulatives, int64(e.CumulativeMg))
			techniques = append(techniques, string(tech))
			sources = append(sources, string(src))
			keys = append(keys, strings.TrimSpace(e.IdempotencyKey))
		}

		// 用多个平行数组 unnest 一次插入全部节点
		if _, err := tx.Exec(ctx, `
			INSERT INTO pour_events (brew_id, offset_ms, cumulative_mg, technique, source, idempotency_key)
			SELECT $1, o, c, t, s, k
			FROM unnest($2::int[], $3::bigint[], $4::text[], $5::text[], $6::text[])
			     AS z(o, c, t, s, k)`,
			brewID, offsets, cumulatives, techniques, sources, keys); err != nil {
			return translateError(err, "注水节点")
		}
		return nil
	})
}

// PourEvents 取某次冲煮的注水节点，按时间偏移升序。
func (r *BrewRepo) PourEvents(ctx context.Context, brewID int64) ([]brew.PourEvent, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, brew_id, offset_ms, cumulative_mg, technique, source, idempotency_key
		FROM pour_events WHERE brew_id = $1
		ORDER BY offset_ms, id`, brewID)
	if err != nil {
		return nil, translateError(err, "注水节点")
	}
	defer rows.Close()

	out := make([]brew.PourEvent, 0, 16)
	for rows.Next() {
		var (
			e    brew.PourEvent
			tech string
			src  string
		)
		if err := rows.Scan(&e.ID, &e.BrewID, &e.OffsetMs, &e.CumulativeMg,
			&tech, &src, &e.IdempotencyKey); err != nil {
			return nil, translateError(err, "注水节点")
		}
		e.Technique = domain.PourTechnique(tech)
		e.Source = brew.PourSource(src)
		out = append(out, e)
	}
	return out, translateError(rows.Err(), "注水节点")
}

// MeasuredSamples 取某支豆某冲煮法下的全部实测样本，作为推算模式的训练集。
//
// mode = 'MEASURED' 这个条件是本查询存在的全部意义。用推算结果去训练推算模型
// 会让误差逐轮放大 —— 第一次推算偏高 1%，它成为训练数据后第二次会更偏，
// 三五次之后模型就完全脱离现实了。把这道约束放在 SQL 里而不是 Go 里，
// 是因为它绝不能被将来某次重构不小心绕过。
func (r *BrewRepo) MeasuredSamples(ctx context.Context, beanID int64, method domain.BrewMethod) ([]goldcup.Sample, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, yield_ppm, tds_ppm, dose_mg, beverage_calc_mg,
		       grind_micron, water_temp_c, contact_seconds, agitation_count
		FROM brew_sessions
		WHERE bean_id = $1 AND method = $2 AND mode = 'MEASURED'
		  AND tds_ppm > 0 AND yield_ppm > 0 AND dose_mg > 0 AND beverage_calc_mg > 0
		ORDER BY brewed_at DESC
		LIMIT 200`, beanID, string(method))
	if err != nil {
		return nil, translateError(err, "萃取样本")
	}
	defer rows.Close()

	out := make([]goldcup.Sample, 0, 32)
	for rows.Next() {
		var s goldcup.Sample
		if err := rows.Scan(&s.BrewID, &s.Yield, &s.TDS, &s.Dose, &s.Beverage,
			&s.GrindMicron, &s.WaterTempC, &s.ContactSeconds, &s.AgitationCount); err != nil {
			return nil, translateError(err, "萃取样本")
		}
		out = append(out, s)
	}
	return out, translateError(rows.Err(), "萃取样本")
}

// ChartSamples 取控制图与偏好曲线所需的样本，含风味评分。
//
// 与 MeasuredSamples 不同，这里同时返回推算样本：控制图要把它们也画出来
// （用空心点区分），否则用户会以为自己那些没测 TDS 的冲煮凭空消失了。
// 偏好曲线那一侧会在内存里再过滤掉推算样本。
func (r *BrewRepo) ChartSamples(ctx context.Context, beanID int64, method domain.BrewMethod) ([]goldcup.ScoredSample, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT b.id, b.yield_ppm, b.tds_calc_ppm, b.ratio_ppm,
		       b.dose_mg, b.beverage_calc_mg, b.mode, b.title, b.brewed_at,
		       COALESCE(s.acidity_x10 + s.sweet_x10 + s.aroma_x10 +
		                s.aftertone_x10 + s.body_x10 + s.bitter_x10, 0) AS total_x10,
		       COALESCE(s.sweet_x10, 0) AS sweet_x10
		FROM brew_sessions b
		LEFT JOIN flavor_scores s ON s.brew_id = b.id
		WHERE b.bean_id = $1 AND b.method = $2
		  AND b.yield_ppm > 0 AND b.tds_calc_ppm > 0
		ORDER BY b.brewed_at
		LIMIT 500`, beanID, string(method))
	if err != nil {
		return nil, translateError(err, "图表样本")
	}
	defer rows.Close()

	out := make([]goldcup.ScoredSample, 0, 32)
	for rows.Next() {
		var (
			s          goldcup.ScoredSample
			mode       string
			title      string
			brewedTime time.Time
		)
		if err := rows.Scan(&s.BrewID, &s.Yield, &s.TDS, &s.Ratio,
			&s.Dose, &s.Beverage, &mode, &title, &brewedTime,
			&s.TotalScoreX100, &s.SweetScoreX100); err != nil {
			return nil, translateError(err, "图表样本")
		}
		s.Mode = goldcup.Mode(mode)
		if strings.TrimSpace(title) != "" {
			s.Label = title
		} else {
			s.Label = domain.FormatDisplay(brewedTime)
		}
		// 评分以 ×10 存储（0–600 总分），而 ScoredSample 约定 ×100，需放大十倍
		s.TotalScoreX100 *= 10
		s.SweetScoreX100 *= 10
		out = append(out, s)
	}
	return out, translateError(rows.Err(), "图表样本")
}

// StatsByBean 汇总每支豆的冲煮次数与最近冲煮时间。
func (r *BrewRepo) StatsByBean(ctx context.Context) (map[int64]bean.BrewStat, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT bean_id, count(*), max(brewed_at)
		FROM brew_sessions GROUP BY bean_id`)
	if err != nil {
		return nil, translateError(err, "萃取统计")
	}
	defer rows.Close()

	out := make(map[int64]bean.BrewStat, 32)
	for rows.Next() {
		var (
			id   int64
			st   bean.BrewStat
			last time.Time
		)
		if err := rows.Scan(&id, &st.Count, &last); err != nil {
			return nil, translateError(err, "萃取统计")
		}
		st.LastAt = last
		out[id] = st
	}
	return out, translateError(rows.Err(), "萃取统计")
}
