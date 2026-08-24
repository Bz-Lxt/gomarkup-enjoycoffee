package flavor

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/alkaid/enjoycoffee/internal/domain"
)

// Service 承载风味树的写操作与校验。
type Service struct {
	repo  Repository
	cache *Cache
}

// NewService 构造服务。
func NewService(repo Repository, cache *Cache) *Service {
	return &Service{repo: repo, cache: cache}
}

// Snapshot 暴露当前快照，供读路径直接使用。
func (s *Service) Snapshot() *Snapshot { return s.cache.Snapshot() }

// maxNameLen 是节点名长度上限（按字符数，非字节数）。
//
// 按字符计数而非字节：中文风味名在 UTF-8 下每字 3 字节，若按 32 字节限长，
// 用户只能输入 10 个汉字，会被误以为是 bug。
const maxNameLen = 32

// CreateNodeInput 是创建风味节点的输入。
type CreateNodeInput struct {
	ParentID  *int64
	Name      string
	Color     string
	Icon      string
	SortOrder int
}

// CreateNode 创建风味节点。
func (s *Service) CreateNode(ctx context.Context, in CreateNodeInput) (*NodeView, string, error) {
	name, err := normalizeName(in.Name)
	if err != nil {
		return nil, "", err
	}

	snap := s.cache.Snapshot()

	if in.ParentID != nil {
		if _, ok := snap.Node(*in.ParentID); !ok {
			return nil, "", domain.NotFound("父风味节点", *in.ParentID)
		}
	}

	// 同一父节点下不允许重名。允许重名会让"多级联动筛选"的结果无法向用户解释
	// —— 界面上出现两个完全一样的「柠檬」，点哪个都不知道会筛出什么。
	if dup := s.findSiblingByName(snap, in.ParentID, name, 0); dup != 0 {
		return nil, "", domain.Conflict("DUPLICATE_FLAVOR_NAME",
			"同一层级下已存在名为「"+name+"」的风味节点")
	}

	id, err := s.repo.CreateNode(ctx, Node{
		ParentID:  in.ParentID,
		Name:      name,
		Color:     normalizeColor(in.Color),
		Icon:      strings.TrimSpace(in.Icon),
		SortOrder: in.SortOrder,
	})
	if err != nil {
		return nil, "", err
	}

	if err := s.cache.Rebuild(ctx); err != nil {
		return nil, "", err
	}

	newSnap := s.cache.Snapshot()
	nv, ok := newSnap.Node(id)
	if !ok {
		return nil, "", domain.Internal("节点已创建但快照中查不到，缓存可能未同步")
	}
	return nv, newSnap.DepthWarning(), nil
}

// UpdateNodeInput 是更新风味节点的输入。nil 字段表示不修改。
type UpdateNodeInput struct {
	ID        int64
	Name      *string
	Color     *string
	Icon      *string
	SortOrder *int
}

// UpdateNode 更新节点属性。不改变树结构 —— 移动节点请用 MoveNode。
func (s *Service) UpdateNode(ctx context.Context, in UpdateNodeInput) (*NodeView, error) {
	snap := s.cache.Snapshot()
	nv, ok := snap.Node(in.ID)
	if !ok {
		return nil, domain.NotFound("风味节点", in.ID)
	}

	next := nv.Node

	if in.Name != nil {
		name, err := normalizeName(*in.Name)
		if err != nil {
			return nil, err
		}
		if name != next.Name {
			if dup := s.findSiblingByName(snap, next.ParentID, name, in.ID); dup != 0 {
				return nil, domain.Conflict("DUPLICATE_FLAVOR_NAME",
					"同一层级下已存在名为「"+name+"」的风味节点")
			}
		}
		next.Name = name
	}
	if in.Color != nil {
		next.Color = normalizeColor(*in.Color)
	}
	if in.Icon != nil {
		next.Icon = strings.TrimSpace(*in.Icon)
	}
	if in.SortOrder != nil {
		next.SortOrder = *in.SortOrder
	}

	if err := s.repo.UpdateNode(ctx, next); err != nil {
		return nil, err
	}
	if err := s.cache.Rebuild(ctx); err != nil {
		return nil, err
	}
	updated, _ := s.cache.Snapshot().Node(in.ID)
	return updated, nil
}

// MoveNode 把节点（连同其整棵子树）移动到新的父节点之下。
//
// 这个操作是风味树里最危险的一个：把节点移到自己的后代之下会形成环，
// 而一旦环进入数据库，所有递归查询都会死循环。因此环检测必须在写入前完成，
// 且不能依赖数据库约束（外键无法表达"不得成环"）。
func (s *Service) MoveNode(ctx context.Context, id int64, newParentID *int64) (*NodeView, string, error) {
	snap := s.cache.Snapshot()

	nv, ok := snap.Node(id)
	if !ok {
		return nil, "", domain.NotFound("风味节点", id)
	}

	if newParentID != nil {
		if *newParentID == id {
			return nil, "", domain.Conflict("SELF_PARENT", "不能把节点移动到自己之下")
		}
		target, ok := snap.Node(*newParentID)
		if !ok {
			return nil, "", domain.NotFound("目标父节点", *newParentID)
		}
		// 核心环检测：目标父节点若是本节点的后代，移动后就会形成环。
		if snap.IsDescendant(*newParentID, id) {
			return nil, "", domain.Conflict("CYCLIC_MOVE",
				"不能把「"+nv.Name+"」移动到它自己的后代「"+target.Name+"」之下，这会让分类树形成环")
		}
		// 目标层级下的重名检查
		if dup := s.findSiblingByName(snap, newParentID, nv.Name, id); dup != 0 {
			return nil, "", domain.Conflict("DUPLICATE_FLAVOR_NAME",
				"目标层级下已存在名为「"+nv.Name+"」的风味节点")
		}
	} else if nv.ParentID == nil {
		return nil, "", domain.Conflict("ALREADY_ROOT", "该节点已经是根节点")
	} else if dup := s.findSiblingByName(snap, nil, nv.Name, id); dup != 0 {
		return nil, "", domain.Conflict("DUPLICATE_FLAVOR_NAME",
			"根层级下已存在名为「"+nv.Name+"」的风味节点")
	}

	if err := s.repo.MoveNode(ctx, id, newParentID); err != nil {
		return nil, "", err
	}
	if err := s.cache.Rebuild(ctx); err != nil {
		return nil, "", err
	}

	newSnap := s.cache.Snapshot()
	moved, _ := newSnap.Node(id)
	return moved, newSnap.DepthWarning(), nil
}

// DeleteMode 决定删除节点时如何处理其子树。
type DeleteMode string

const (
	// DeleteCascade 连带删除整棵子树。
	DeleteCascade DeleteMode = "CASCADE"
	// DeletePromote 只删除该节点，把它的子节点上提到它原来的父节点之下。
	//
	// 这个选项存在的理由：用户整理分类时常想删掉一个多余的中间层
	// （比如「果调 → 柑橘 → 柠檬」想变成「果调 → 柠檬」），
	// 若只提供级联删除，他就得先手工搬走所有子节点。
	DeletePromote DeleteMode = "PROMOTE"
)

// DeleteResult 描述删除操作的实际影响。
type DeleteResult struct {
	DeletedCount  int    `json:"deleted_count"`
	PromotedCount int    `json:"promoted_count"`
	Mode          string `json:"mode"`
	Message       string `json:"message"`
}

// DeleteNode 删除风味节点。
func (s *Service) DeleteNode(ctx context.Context, id int64, mode DeleteMode) (*DeleteResult, error) {
	snap := s.cache.Snapshot()
	nv, ok := snap.Node(id)
	if !ok {
		return nil, domain.NotFound("风味节点", id)
	}

	// 内置节点受保护：它们是 SCA 风味轮衍生的种子数据，是新用户理解
	// 分类体系的参照。允许改名和移动，但不允许删除 —— 删掉之后用户
	// 就再也拿不回来了（种子只在首次启动时写入）。
	if nv.Builtin {
		return nil, domain.Conflict("BUILTIN_PROTECTED",
			"「"+nv.Name+"」是内置风味节点，不可删除。你可以重命名它，或把不需要的分支移到别处。")
	}

	res := &DeleteResult{Mode: string(mode)}

	switch mode {
	case DeletePromote:
		if err := s.repo.ReparentChildren(ctx, id, nv.ParentID); err != nil {
			return nil, err
		}
		if err := s.repo.DeleteNode(ctx, id); err != nil {
			return nil, err
		}
		res.DeletedCount = 1
		res.PromotedCount = len(nv.Children)
		res.Message = "已删除「" + nv.Name + "」，其 " + itoa(len(nv.Children)) + " 个子节点已上提一级"

	default:
		n, err := s.repo.DeleteSubtree(ctx, id)
		if err != nil {
			return nil, err
		}
		res.DeletedCount = n
		res.Message = "已删除「" + nv.Name + "」及其 " + itoa(n-1) + " 个后代节点"
	}

	if err := s.cache.Rebuild(ctx); err != nil {
		return nil, err
	}
	return res, nil
}

// SetBeanFlavors 覆盖式设置某支豆的风味标签。
func (s *Service) SetBeanFlavors(ctx context.Context, beanID int64, nodeIDs []int64) error {
	snap := s.cache.Snapshot()

	seen := make(map[int64]bool, len(nodeIDs))
	clean := make([]int64, 0, len(nodeIDs))
	e := domain.Validation("INVALID_FLAVOR_NODES", "存在无效的风味节点")
	bad := false

	for _, id := range nodeIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, ok := snap.Node(id); !ok {
			e.WithField("flavor_node_ids", "节点 "+itoa64(id)+" 不存在")
			bad = true
			continue
		}
		clean = append(clean, id)
	}
	if bad {
		return e
	}

	if err := s.repo.SetBeanFlavors(ctx, beanID, clean); err != nil {
		return err
	}
	return s.cache.Rebuild(ctx)
}

// Refresh 强制重建快照。豆子被创建或删除后必须调用 —— 否则位图的
// 序号映射会与实际豆子集合脱节，导致筛选结果漏掉新豆。
func (s *Service) Refresh(ctx context.Context) error {
	return s.cache.Rebuild(ctx)
}

// findSiblingByName 在指定父节点下查找同名兄弟，返回其 ID；未找到返回 0。
// excludeID 用于更新场景排除自身。
func (s *Service) findSiblingByName(snap *Snapshot, parentID *int64, name string, excludeID int64) int64 {
	var siblings []int64
	if parentID == nil {
		siblings = snap.Roots()
	} else if pv, ok := snap.Node(*parentID); ok {
		siblings = pv.Children
	}
	for _, sid := range siblings {
		if sid == excludeID {
			continue
		}
		if sv, ok := snap.Node(sid); ok && sv.Name == name {
			return sid
		}
	}
	return 0
}

func normalizeName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", domain.Validation("EMPTY_FLAVOR_NAME", "风味名称不能为空").
			WithField("name", "必填")
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		return "", domain.Validation("FLAVOR_NAME_TOO_LONG",
			"风味名称不能超过 "+itoa(maxNameLen)+" 个字符").
			WithField("name", "当前 "+itoa(utf8.RuneCountInString(name))+" 个字符")
	}
	// 路径分隔符会破坏 Path 字段的可读性与前端的面包屑拆分
	if strings.Contains(name, "/") {
		return "", domain.Validation("INVALID_FLAVOR_NAME",
			"风味名称不能包含斜杠，它被用作层级路径的分隔符").
			WithField("name", "不能包含 /")
	}
	return name, nil
}

// normalizeColor 校验并归一化十六进制颜色，非法值回落到中性灰。
//
// 回落而非报错：颜色是纯装饰属性，为一个拼错的色值拒绝整次创建
// 会让用户很恼火。给个默认色并让他自己看到不对劲更合理。
func normalizeColor(raw string) string {
	c := strings.TrimSpace(raw)
	if c == "" {
		return "#9CA3AF"
	}
	if !strings.HasPrefix(c, "#") {
		c = "#" + c
	}
	if len(c) != 7 {
		return "#9CA3AF"
	}
	for _, r := range c[1:] {
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !isHex {
			return "#9CA3AF"
		}
	}
	return strings.ToUpper(c)
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
