package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alkaid/enjoycoffee/internal/bean"
	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

// BeanRepo 实现 bean.Repository。
type BeanRepo struct {
	db *DB
}

// NewBeanRepo 构造豆库仓储。
func NewBeanRepo(db *DB) *BeanRepo { return &BeanRepo{db: db} }

const beanColumns = `
	id, name, roaster, is_blend,
	country, region, farm, altitude_m, process, variety,
	roast_level, roast_note, roasted_on, opened_on,
	initial_weight_mg, remaining_mg,
	notes, archived, created_at, updated_at`

func scanBean(row pgx.Row) (*bean.Bean, error) {
	var (
		b         bean.Bean
		roastedOn time.Time
		openedOn  *time.Time
		process   string
		roast     string
	)
	err := row.Scan(
		&b.ID, &b.Name, &b.Roaster, &b.IsBlend,
		&b.Country, &b.Region, &b.Farm, &b.Altitude, &process, &b.Variety,
		&roast, &b.RoastNote, &roastedOn, &openedOn,
		&b.InitialWeightMg, &b.RemainingMg,
		&b.Notes, &b.Archived, &b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	b.Process = domain.ProcessMethod(process)
	b.RoastLevel = domain.RoastLevel(roast)
	b.RoastedOn = domain.ToCivilDate(roastedOn)
	b.OpenedOn = nullableDate(openedOn)
	return &b, nil
}

// Create 插入一支豆。
func (r *BeanRepo) Create(ctx context.Context, b *bean.Bean) (int64, error) {
	var id int64
	err := r.db.Pool.QueryRow(ctx, `
		INSERT INTO beans (
			name, roaster, is_blend,
			country, region, farm, altitude_m, process, variety,
			roast_level, roast_note, roasted_on, opened_on,
			initial_weight_mg, remaining_mg, notes, archived
		) VALUES (
			$1, $2, $3,
			$4, $5, $6, $7, $8, $9,
			$10, $11, $12, $13,
			$14, $15, $16, $17
		) RETURNING id`,
		b.Name, b.Roaster, b.IsBlend,
		b.Country, b.Region, b.Farm, b.Altitude, string(b.Process), b.Variety,
		string(b.RoastLevel), b.RoastNote, b.RoastedOn.Time(), datePtr(b.OpenedOn),
		int64(b.InitialWeightMg), int64(b.RemainingMg), b.Notes, b.Archived,
	).Scan(&id)
	if err != nil {
		return 0, translateError(err, "咖啡豆")
	}
	return id, nil
}

// Update 更新一支豆。
func (r *BeanRepo) Update(ctx context.Context, b *bean.Bean) error {
	tag, err := r.db.Pool.Exec(ctx, `
		UPDATE beans SET
			name = $1, roaster = $2, is_blend = $3,
			country = $4, region = $5, farm = $6, altitude_m = $7, process = $8, variety = $9,
			roast_level = $10, roast_note = $11, roasted_on = $12, opened_on = $13,
			initial_weight_mg = $14, remaining_mg = $15, notes = $16, archived = $17
		WHERE id = $18`,
		b.Name, b.Roaster, b.IsBlend,
		b.Country, b.Region, b.Farm, b.Altitude, string(b.Process), b.Variety,
		string(b.RoastLevel), b.RoastNote, b.RoastedOn.Time(), datePtr(b.OpenedOn),
		int64(b.InitialWeightMg), int64(b.RemainingMg), b.Notes, b.Archived,
		b.ID,
	)
	if err != nil {
		return translateError(err, "咖啡豆")
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("咖啡豆", b.ID)
	}
	return nil
}

// Get 查单支豆，附带其风味标签 ID。
func (r *BeanRepo) Get(ctx context.Context, id int64) (*bean.Bean, error) {
	row := r.db.Pool.QueryRow(ctx, `SELECT `+beanColumns+` FROM beans WHERE id = $1`, id)
	b, err := scanBean(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, domain.NotFound("咖啡豆", id)
		}
		return nil, translateError(err, "咖啡豆")
	}

	ids, err := r.flavorIDs(ctx, id)
	if err != nil {
		return nil, err
	}
	b.FlavorNodeIDs = ids
	return b, nil
}

func (r *BeanRepo) flavorIDs(ctx context.Context, beanID int64) ([]int64, error) {
	rows, err := r.db.Pool.Query(ctx,
		`SELECT node_id FROM bean_flavors WHERE bean_id = $1 ORDER BY node_id`, beanID)
	if err != nil {
		return nil, translateError(err, "风味关联")
	}
	defer rows.Close()

	// 初始化为空切片而非 nil：JSON 序列化时 nil 会变成 null，
	// 前端的 flavor_node_ids.map 会直接崩。
	out := make([]int64, 0, 8)
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, translateError(err, "风味关联")
		}
		out = append(out, v)
	}
	return out, translateError(rows.Err(), "风味关联")
}

// List 查询豆子列表。
//
// ids 非空时作为主键白名单（风味索引筛出的结果），为空时不限制。
// 用 unnest + ANY 而非拼接 IN 子句：拼接会随白名单长度产生无数种
// 不同的 SQL 文本，彻底废掉 PostgreSQL 的预备语句缓存，
// 而这条路径在每次筛选时都要走。
func (r *BeanRepo) List(ctx context.Context, ids []int64, includeArchived bool) ([]*bean.Bean, error) {
	q := `SELECT ` + beanColumns + ` FROM beans WHERE ($1::boolean OR archived = FALSE)`
	args := []any{includeArchived}

	if len(ids) > 0 {
		q += ` AND id = ANY($2::bigint[])`
		args = append(args, ids)
	}
	q += ` ORDER BY roasted_on DESC, id DESC`

	rows, err := r.db.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, translateError(err, "咖啡豆")
	}
	defer rows.Close()

	out := make([]*bean.Bean, 0, 64)
	for rows.Next() {
		b, err := scanBean(rows)
		if err != nil {
			return nil, translateError(err, "咖啡豆")
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, translateError(err, "咖啡豆")
	}

	// 一次查出全部关联再按豆分组，避免每支豆各查一次（N+1）。
	if len(out) > 0 {
		beanIDs := make([]int64, 0, len(out))
		for _, b := range out {
			beanIDs = append(beanIDs, b.ID)
		}
		grouped, err := r.flavorIDsBatch(ctx, beanIDs)
		if err != nil {
			return nil, err
		}
		for _, b := range out {
			if v, ok := grouped[b.ID]; ok {
				b.FlavorNodeIDs = v
			} else {
				b.FlavorNodeIDs = []int64{}
			}
		}
	}

	return out, nil
}

func (r *BeanRepo) flavorIDsBatch(ctx context.Context, beanIDs []int64) (map[int64][]int64, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT bean_id, node_id FROM bean_flavors
		WHERE bean_id = ANY($1::bigint[])
		ORDER BY bean_id, node_id`, beanIDs)
	if err != nil {
		return nil, translateError(err, "风味关联")
	}
	defer rows.Close()

	out := make(map[int64][]int64, len(beanIDs))
	for rows.Next() {
		var bid, nid int64
		if err := rows.Scan(&bid, &nid); err != nil {
			return nil, translateError(err, "风味关联")
		}
		out[bid] = append(out[bid], nid)
	}
	return out, translateError(rows.Err(), "风味关联")
}

// Delete 删除一支豆。
func (r *BeanRepo) Delete(ctx context.Context, id int64) error {
	tag, err := r.db.Pool.Exec(ctx, `DELETE FROM beans WHERE id = $1`, id)
	if err != nil {
		return translateError(err, "咖啡豆")
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("咖啡豆", id)
	}
	return nil
}

// SetRemaining 更新剩余粉量。
func (r *BeanRepo) SetRemaining(ctx context.Context, id int64, remaining fixed.Mass) error {
	tag, err := r.db.Pool.Exec(ctx,
		`UPDATE beans SET remaining_mg = $1 WHERE id = $2`, int64(remaining), id)
	if err != nil {
		return translateError(err, "咖啡豆")
	}
	if tag.RowsAffected() == 0 {
		return domain.NotFound("咖啡豆", id)
	}
	return nil
}

// CountBrews 统计某支豆的萃取记录数。
func (r *BeanRepo) CountBrews(ctx context.Context, id int64) (int, error) {
	var n int
	err := r.db.Pool.QueryRow(ctx,
		`SELECT count(*) FROM brew_sessions WHERE bean_id = $1`, id).Scan(&n)
	if err != nil {
		return 0, translateError(err, "萃取记录")
	}
	return n, nil
}
