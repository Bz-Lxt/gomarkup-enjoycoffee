package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/alkaid/enjoycoffee/internal/flavorscore"
)

// ScoreRepo 实现 flavorscore.Repository。
type ScoreRepo struct {
	db *DB
}

// NewScoreRepo 构造评分仓储。
func NewScoreRepo(db *DB) *ScoreRepo { return &ScoreRepo{db: db} }

const scoreColumns = `
	id, brew_id, bean_id,
	acidity_x10, sweet_x10, aroma_x10, aftertone_x10, body_x10, bitter_x10,
	note, scored_at, created_at, updated_at`

func scanScore(row pgx.Row) (*flavorscore.Score, error) {
	var s flavorscore.Score
	err := row.Scan(&s.ID, &s.BrewID, &s.BeanID,
		&s.AcidityX10, &s.SweetX10, &s.AromaX10, &s.AftertoneX10, &s.BodyX10, &s.BitterX10,
		&s.Note, &s.ScoredAt, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Upsert 新增或覆盖某次冲煮的评分。
//
// bean_id 由 brew_sessions 反查而非由调用方传入：让客户端提供它就意味着
// 客户端可以提供一个与 brew_id 不匹配的 bean_id，从而把评分挂到错误的豆子上，
// 污染该豆的雷达聚合与偏好曲线。从数据库反查是唯一不可能出错的来源。
func (r *ScoreRepo) Upsert(ctx context.Context, s *flavorscore.Score) (int64, error) {
	var id int64
	err := r.db.Pool.QueryRow(ctx, `
		INSERT INTO flavor_scores (
			brew_id, bean_id,
			acidity_x10, sweet_x10, aroma_x10, aftertone_x10, body_x10, bitter_x10,
			note, scored_at
		)
		SELECT $1, b.bean_id, $2, $3, $4, $5, $6, $7, $8, $9
		FROM brew_sessions b WHERE b.id = $1
		ON CONFLICT (brew_id) DO UPDATE SET
			acidity_x10 = EXCLUDED.acidity_x10,
			sweet_x10 = EXCLUDED.sweet_x10,
			aroma_x10 = EXCLUDED.aroma_x10,
			aftertone_x10 = EXCLUDED.aftertone_x10,
			body_x10 = EXCLUDED.body_x10,
			bitter_x10 = EXCLUDED.bitter_x10,
			note = EXCLUDED.note,
			scored_at = EXCLUDED.scored_at
		RETURNING id`,
		s.BrewID,
		s.AcidityX10, s.SweetX10, s.AromaX10, s.AftertoneX10, s.BodyX10, s.BitterX10,
		s.Note, s.ScoredAt,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// SELECT ... FROM brew_sessions WHERE id = $1 没命中，
			// 说明 brew_id 不存在。这比让外键报 23503 更能说清问题。
			return 0, translateError(pgx.ErrNoRows, "萃取记录")
		}
		return 0, translateError(err, "风味评分")
	}
	s.ID = id
	return id, nil
}

// GetByBrew 取某次冲煮的评分。未评分返回 nil, nil。
func (r *ScoreRepo) GetByBrew(ctx context.Context, brewID int64) (*flavorscore.Score, error) {
	row := r.db.Pool.QueryRow(ctx,
		`SELECT `+scoreColumns+` FROM flavor_scores WHERE brew_id = $1`, brewID)
	s, err := scanScore(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// "还没评分"是完全正常的状态，不是错误。返回 nil, nil 让调用方
			// 用空态渲染，而不是把一个 404 冒泡到用户面前。
			return nil, nil
		}
		return nil, translateError(err, "风味评分")
	}
	return s, nil
}

// ListByBrews 批量取多次冲煮的评分。
func (r *ScoreRepo) ListByBrews(ctx context.Context, brewIDs []int64) (map[int64]*flavorscore.Score, error) {
	out := make(map[int64]*flavorscore.Score, len(brewIDs))
	if len(brewIDs) == 0 {
		return out, nil
	}

	rows, err := r.db.Pool.Query(ctx,
		`SELECT `+scoreColumns+` FROM flavor_scores WHERE brew_id = ANY($1::bigint[])`, brewIDs)
	if err != nil {
		return nil, translateError(err, "风味评分")
	}
	defer rows.Close()

	for rows.Next() {
		s, err := scanScore(rows)
		if err != nil {
			return nil, translateError(err, "风味评分")
		}
		out[s.BrewID] = s
	}
	return out, translateError(rows.Err(), "风味评分")
}

// ListByBeanWithTime 取某支豆的全部评分，按评分时间升序。
func (r *ScoreRepo) ListByBeanWithTime(ctx context.Context, beanID int64) ([]*flavorscore.Score, error) {
	rows, err := r.db.Pool.Query(ctx,
		`SELECT `+scoreColumns+` FROM flavor_scores WHERE bean_id = $1 ORDER BY scored_at`, beanID)
	if err != nil {
		return nil, translateError(err, "风味评分")
	}
	defer rows.Close()

	out := make([]*flavorscore.Score, 0, 16)
	for rows.Next() {
		s, err := scanScore(rows)
		if err != nil {
			return nil, translateError(err, "风味评分")
		}
		out = append(out, s)
	}
	return out, translateError(rows.Err(), "风味评分")
}

// ListByBeans 批量取多支豆的评分，按豆分组。
func (r *ScoreRepo) ListByBeans(ctx context.Context, beanIDs []int64) (map[int64][]*flavorscore.Score, error) {
	out := make(map[int64][]*flavorscore.Score, len(beanIDs))
	if len(beanIDs) == 0 {
		return out, nil
	}

	rows, err := r.db.Pool.Query(ctx,
		`SELECT `+scoreColumns+` FROM flavor_scores
		 WHERE bean_id = ANY($1::bigint[]) ORDER BY bean_id, scored_at`, beanIDs)
	if err != nil {
		return nil, translateError(err, "风味评分")
	}
	defer rows.Close()

	for rows.Next() {
		s, err := scanScore(rows)
		if err != nil {
			return nil, translateError(err, "风味评分")
		}
		out[s.BeanID] = append(out[s.BeanID], s)
	}
	return out, translateError(rows.Err(), "风味评分")
}

// Delete 删除某次冲煮的评分。
func (r *ScoreRepo) Delete(ctx context.Context, brewID int64) error {
	_, err := r.db.Pool.Exec(ctx, `DELETE FROM flavor_scores WHERE brew_id = $1`, brewID)
	return translateError(err, "风味评分")
}
