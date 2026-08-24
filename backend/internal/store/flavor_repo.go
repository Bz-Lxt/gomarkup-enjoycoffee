package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/alkaid/enjoycoffee/internal/flavor"
)

// FlavorRepo 实现 flavor.Repository。
//
// 闭包表（flavor_closure）的维护全部集中在本文件，且每个改结构的操作
// 都在事务里完成。闭包表与 parent_id 是同一份信息的两种表示，
// 一旦不一致，树就会出现"父子关系正常但祖先查询查不到"这类幽灵故障。
// 唯一的防线就是让它们永远在同一个事务里一起变。
type FlavorRepo struct {
	db *DB
}

// NewFlavorRepo 构造风味树仓储。
func NewFlavorRepo(db *DB) *FlavorRepo { return &FlavorRepo{db: db} }

// ListNodes 读取全部节点。
func (r *FlavorRepo) ListNodes(ctx context.Context) ([]flavor.Node, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT id, parent_id, name, color, icon, sort_order, builtin, created_at, updated_at
		FROM flavor_nodes
		ORDER BY sort_order, name`)
	if err != nil {
		return nil, translateError(err, "风味节点")
	}
	defer rows.Close()

	out := make([]flavor.Node, 0, 128)
	for rows.Next() {
		var n flavor.Node
		if err := rows.Scan(&n.ID, &n.ParentID, &n.Name, &n.Color, &n.Icon,
			&n.SortOrder, &n.Builtin, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, translateError(err, "风味节点")
		}
		out = append(out, n)
	}
	return out, translateError(rows.Err(), "风味节点")
}

// ListTaggings 读取全部「豆子 ↔ 风味」关联。
func (r *FlavorRepo) ListTaggings(ctx context.Context) ([]flavor.Tagging, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT bean_id, node_id FROM bean_flavors`)
	if err != nil {
		return nil, translateError(err, "风味关联")
	}
	defer rows.Close()

	out := make([]flavor.Tagging, 0, 512)
	for rows.Next() {
		var t flavor.Tagging
		if err := rows.Scan(&t.BeanID, &t.NodeID); err != nil {
			return nil, translateError(err, "风味关联")
		}
		out = append(out, t)
	}
	return out, translateError(rows.Err(), "风味关联")
}

// ListBeanIDs 读取全部豆子主键。
//
// 包含已归档的豆子：归档只影响列表展示，不该让它从风味索引里消失 ——
// 否则用户按风味筛选时会看不到归档豆，而归档豆的历史冲煮记录仍然存在，
// 两处口径就不一致了。
func (r *FlavorRepo) ListBeanIDs(ctx context.Context) ([]int64, error) {
	rows, err := r.db.Pool.Query(ctx, `SELECT id FROM beans ORDER BY id`)
	if err != nil {
		return nil, translateError(err, "咖啡豆")
	}
	defer rows.Close()

	out := make([]int64, 0, 256)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, translateError(err, "咖啡豆")
		}
		out = append(out, id)
	}
	return out, translateError(rows.Err(), "咖啡豆")
}

// CreateNode 插入节点并建立其闭包关系。
func (r *FlavorRepo) CreateNode(ctx context.Context, n flavor.Node) (int64, error) {
	var id int64
	err := r.db.InTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			INSERT INTO flavor_nodes (parent_id, name, color, icon, sort_order, builtin)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id`,
			n.ParentID, n.Name, n.Color, n.Icon, n.SortOrder, n.Builtin,
		).Scan(&id); err != nil {
			return translateError(err, "风味节点")
		}
		return insertClosureFor(ctx, tx, id, n.ParentID)
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// insertClosureFor 为新节点建立完整的祖先链。
//
// 做法是把父节点的全部祖先（含父节点自身，depth=0 的自反行）各复制一行，
// 深度加一，再补上本节点的自反行。这样一条 SQL 就建立了从根到本节点的
// 所有祖先关系，无需递归。
func insertClosureFor(ctx context.Context, tx pgx.Tx, id int64, parentID *int64) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO flavor_closure (ancestor_id, descendant_id, depth)
		VALUES ($1, $1, 0)
		ON CONFLICT DO NOTHING`, id); err != nil {
		return translateError(err, "风味闭包")
	}

	if parentID == nil {
		return nil
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO flavor_closure (ancestor_id, descendant_id, depth)
		SELECT c.ancestor_id, $1, c.depth + 1
		FROM flavor_closure c
		WHERE c.descendant_id = $2
		ON CONFLICT DO NOTHING`, id, *parentID); err != nil {
		return translateError(err, "风味闭包")
	}
	return nil
}

// UpdateNode 更新节点属性，不改变树结构。
func (r *FlavorRepo) UpdateNode(ctx context.Context, n flavor.Node) error {
	tag, err := r.db.Pool.Exec(ctx, `
		UPDATE flavor_nodes
		SET name = $1, color = $2, icon = $3, sort_order = $4
		WHERE id = $5`,
		n.Name, n.Color, n.Icon, n.SortOrder, n.ID)
	if err != nil {
		return translateError(err, "风味节点")
	}
	if tag.RowsAffected() == 0 {
		return translateError(pgx.ErrNoRows, "风味节点")
	}
	return nil
}

// MoveNode 把节点连同其子树移动到新的父节点之下。
//
// 闭包表的子树移动是一个经典的三步操作：
//  1. 断开：删掉「子树内所有节点」与「子树外所有祖先」之间的连线，
//     同时保留子树内部的连线（子树自身的结构不变）。
//  2. 重连：把新父节点的全部祖先链，与子树内每个节点做笛卡尔积，
//     深度取两侧之和再加一。
//  3. parent_id 同步更新。
//
// 环检测不在这里做 —— 它由 flavor.Service 基于内存快照在事务之前完成。
// 放在服务层是因为环检测需要遍历祖先链，用已物化的内存树判断比
// 在 SQL 里再查一遍更直接，且能给出"不能把 X 移到它的后代 Y 之下"
// 这种含具体名称的错误信息。
func (r *FlavorRepo) MoveNode(ctx context.Context, id int64, newParentID *int64) error {
	return r.db.InTx(ctx, func(tx pgx.Tx) error {
		// 第一步：断开子树与外部祖先的连线
		if _, err := tx.Exec(ctx, `
			DELETE FROM flavor_closure
			WHERE descendant_id IN (
				SELECT descendant_id FROM flavor_closure WHERE ancestor_id = $1
			)
			AND ancestor_id NOT IN (
				SELECT descendant_id FROM flavor_closure WHERE ancestor_id = $1
			)`, id); err != nil {
			return translateError(err, "风味闭包")
		}

		// 第二步：与新父节点的祖先链重连
		if newParentID != nil {
			if _, err := tx.Exec(ctx, `
				INSERT INTO flavor_closure (ancestor_id, descendant_id, depth)
				SELECT super.ancestor_id, sub.descendant_id, super.depth + sub.depth + 1
				FROM flavor_closure super
				CROSS JOIN flavor_closure sub
				WHERE super.descendant_id = $1
				  AND sub.ancestor_id = $2
				ON CONFLICT DO NOTHING`, *newParentID, id); err != nil {
				return translateError(err, "风味闭包")
			}
		}

		// 第三步：同步 parent_id
		tag, err := tx.Exec(ctx, `UPDATE flavor_nodes SET parent_id = $1 WHERE id = $2`,
			newParentID, id)
		if err != nil {
			return translateError(err, "风味节点")
		}
		if tag.RowsAffected() == 0 {
			return translateError(pgx.ErrNoRows, "风味节点")
		}
		return nil
	})
}

// DeleteSubtree 删除节点及其全部后代，返回删除的节点数。
func (r *FlavorRepo) DeleteSubtree(ctx context.Context, id int64) (int, error) {
	var count int
	err := r.db.InTx(ctx, func(tx pgx.Tx) error {
		// 用闭包表一次取出整棵子树，无需递归。
		// flavor_closure 与 bean_flavors 的外键都是 ON DELETE CASCADE，
		// 因此删节点即可自动清理关联，不需要手工按顺序删。
		tag, err := tx.Exec(ctx, `
			DELETE FROM flavor_nodes
			WHERE id IN (SELECT descendant_id FROM flavor_closure WHERE ancestor_id = $1)`, id)
		if err != nil {
			return translateError(err, "风味节点")
		}
		count = int(tag.RowsAffected())
		return nil
	})
	return count, err
}

// ReparentChildren 把某节点的直接子节点全部上提到指定父节点之下。
//
// 逐个调用移动逻辑而不是批量 UPDATE parent_id：闭包表必须跟着变，
// 而每个子节点的子树都需要独立重连。批量改 parent_id 会留下一张
// 与 parent_id 矛盾的闭包表，那是最难发现的一类数据损坏。
func (r *FlavorRepo) ReparentChildren(ctx context.Context, id int64, newParentID *int64) error {
	rows, err := r.db.Pool.Query(ctx,
		`SELECT id FROM flavor_nodes WHERE parent_id = $1 ORDER BY sort_order, name`, id)
	if err != nil {
		return translateError(err, "风味节点")
	}
	children := make([]int64, 0, 8)
	for rows.Next() {
		var cid int64
		if err := rows.Scan(&cid); err != nil {
			rows.Close()
			return translateError(err, "风味节点")
		}
		children = append(children, cid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return translateError(err, "风味节点")
	}

	for _, cid := range children {
		if err := r.MoveNode(ctx, cid, newParentID); err != nil {
			return err
		}
	}
	return nil
}

// DeleteNode 删除单个节点。调用前应确保其子节点已被处理。
func (r *FlavorRepo) DeleteNode(ctx context.Context, id int64) error {
	tag, err := r.db.Pool.Exec(ctx, `DELETE FROM flavor_nodes WHERE id = $1`, id)
	if err != nil {
		return translateError(err, "风味节点")
	}
	if tag.RowsAffected() == 0 {
		return translateError(pgx.ErrNoRows, "风味节点")
	}
	return nil
}

// SetBeanFlavors 覆盖式设置某支豆的风味标签。
func (r *FlavorRepo) SetBeanFlavors(ctx context.Context, beanID int64, nodeIDs []int64) error {
	return r.db.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM bean_flavors WHERE bean_id = $1`, beanID); err != nil {
			return translateError(err, "风味关联")
		}
		if len(nodeIDs) == 0 {
			return nil
		}
		// 用 unnest 一次插入全部关联。逐条 INSERT 在标签较多时会产生
		// 几十次往返，而这条路径在每次保存豆子时都要走。
		if _, err := tx.Exec(ctx, `
			INSERT INTO bean_flavors (bean_id, node_id)
			SELECT $1, n FROM unnest($2::bigint[]) AS n
			ON CONFLICT DO NOTHING`, beanID, nodeIDs); err != nil {
			return translateError(err, "风味关联")
		}
		return nil
	})
}

// DescendantIDs 直接从闭包表取某节点的全部后代。
//
// 内存索引已经覆盖了正常的查询路径，本方法的用途是诊断与验证：
// QA 可以用它对比"内存树给出的后代集合"与"数据库给出的后代集合"是否一致，
// 从而验证缓存没有偏离真相。
func (r *FlavorRepo) DescendantIDs(ctx context.Context, id int64) ([]int64, error) {
	rows, err := r.db.Pool.Query(ctx, `
		SELECT descendant_id FROM flavor_closure
		WHERE ancestor_id = $1
		ORDER BY depth, descendant_id`, id)
	if err != nil {
		return nil, translateError(err, "风味闭包")
	}
	defer rows.Close()

	out := make([]int64, 0, 16)
	for rows.Next() {
		var d int64
		if err := rows.Scan(&d); err != nil {
			return nil, translateError(err, "风味闭包")
		}
		out = append(out, d)
	}
	return out, translateError(rows.Err(), "风味闭包")
}
