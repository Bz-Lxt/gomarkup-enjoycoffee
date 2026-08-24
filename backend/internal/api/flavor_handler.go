package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/flavor"
	"github.com/alkaid/enjoycoffee/internal/httpx"
	"github.com/alkaid/enjoycoffee/internal/validate"
)

// flavorTree 返回完整的风味树。
//
// 一次返回整棵树而非按需懒加载子节点：树的规模上限是几百个节点
// （NFR-01 的基准是 500 节点），整棵树的 JSON 也就几十 KB，
// 一次拿全让前端的多级联动筛选可以纯前端展开，没有任何请求延迟。
// 懒加载在这个数据量下只会带来每次展开一个 loading 转圈的糟糕体验。
func (h *Handlers) flavorTree(w http.ResponseWriter, r *http.Request) {
	snap := h.Flavors.Snapshot()
	stats := snap.Stats()

	payload := map[string]any{
		"tree":          snap.Tree(),
		"stats":         stats,
		"depth_warning": snap.DepthWarning(),
		"built_at":      snap.BuiltAt().Format(time.RFC3339),
	}
	httpx.OK(w, payload)
}

// flavorFilter 执行多级联动筛选，返回命中的豆子 ID 与分面计数。
//
// 这个端点是 NFR-01（P99 ≤ 10ms）的直接度量对象。响应里回传 elapsed_micros
// 而非只写日志，是为了让 QA 与前端都能看到真实耗时 —— 一个只有服务端
// 日志里才有的性能指标，在验收时等于不存在。
func (h *Handlers) flavorFilter(w http.ResponseWriter, r *http.Request) {
	nodeIDs, err := httpx.QueryInt64List(r, "flavor_ids")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	req := flavor.FilterRequest{
		NodeIDs:       nodeIDs,
		Match:         flavor.ParseMatchMode(httpx.QueryString(r, "match")),
		ExactNodeOnly: httpx.QueryBool(r, "exact", false),
		WantFacets:    httpx.QueryBool(r, "facets", true),
	}

	res := h.Flavors.Snapshot().Filter(req)
	httpx.OK(w, res)
}

// flavorSearch 按关键词搜索节点，供前端搜索框做跳转定位。
func (h *Handlers) flavorSearch(w http.ResponseWriter, r *http.Request) {
	keyword := httpx.QueryString(r, "q")
	if keyword == "" {
		httpx.OK(w, []any{})
		return
	}
	limit, err := httpx.QueryInt(r, "limit", 20)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}

	snap := h.Flavors.Snapshot()
	nodes := snap.SearchNodes(keyword, limit)

	// 一并回传每个命中节点的祖先链，让前端能直接显示
	// 「果调 › 柑橘 › 柠檬」这样的面包屑而不必二次请求
	type hit struct {
		Node      *flavor.NodeView   `json:"node"`
		Ancestors []*flavor.NodeView `json:"ancestors"`
	}
	hits := make([]hit, 0, len(nodes))
	for _, n := range nodes {
		hits = append(hits, hit{Node: n, Ancestors: snap.Ancestors(n.ID)})
	}
	httpx.OK(w, hits)
}

type createNodePayload struct {
	ParentID  *int64 `json:"parent_id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	Icon      string `json:"icon"`
	SortOrder int    `json:"sort_order"`
}

func (h *Handlers) createFlavorNode(w http.ResponseWriter, r *http.Request) {
	var p createNodePayload
	if err := httpx.DecodeJSON(w, r, &p); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	c := validate.New("INVALID_FLAVOR_NODE", "风味节点信息不合法")
	name := c.MaxLen("name", c.NonEmpty("name", p.Name), 30)
	color := normalizeColor(c, "color", p.Color)
	if err := c.Err(); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	nv, warning, err := h.Flavors.CreateNode(r.Context(), flavor.CreateNodeInput{
		ParentID:  p.ParentID,
		Name:      name,
		Color:     color,
		Icon:      strings.TrimSpace(p.Icon),
		SortOrder: p.SortOrder,
	})
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var warnings []string
	if warning != "" {
		warnings = append(warnings, warning)
	}
	httpx.Created(w, nv, warnings)
}

type updateNodePayload struct {
	Name      *string `json:"name"`
	Color     *string `json:"color"`
	Icon      *string `json:"icon"`
	SortOrder *int    `json:"sort_order"`
}

func (h *Handlers) updateFlavorNode(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var p updateNodePayload
	if err := httpx.DecodeJSON(w, r, &p); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	c := validate.New("INVALID_FLAVOR_NODE", "风味节点信息不合法")
	if p.Name != nil {
		v := c.MaxLen("name", c.NonEmpty("name", *p.Name), 30)
		p.Name = &v
	}
	if p.Color != nil {
		v := normalizeColor(c, "color", *p.Color)
		p.Color = &v
	}
	if err := c.Err(); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	nv, err := h.Flavors.UpdateNode(r.Context(), flavor.UpdateNodeInput{
		ID:        id,
		Name:      p.Name,
		Color:     p.Color,
		Icon:      p.Icon,
		SortOrder: p.SortOrder,
	})
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.OK(w, nv)
}

// movePayload 需要区分三种意图，而单个 *int64 只能表达两种：
// 「没传」和「传了 null」在 JSON 解码后都是 nil 指针，但前者是
// 不完整的请求（应当报错），后者是"移到根层级"（一个合法操作）。
// 因此用一个显式的 to_root 布尔把根层级这个意图独立出来。
type movePayload struct {
	ParentID *int64 `json:"parent_id"`
	ToRoot   bool   `json:"to_root"`
}

func (h *Handlers) moveFlavorNode(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var p movePayload
	if err := httpx.DecodeJSON(w, r, &p); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	if p.ParentID == nil && !p.ToRoot {
		httpx.Fail(w, r, domain.Validation("MISSING_MOVE_TARGET",
			"必须指定 parent_id（移动到某节点下），或把 to_root 设为 true（移到根层级）"))
		return
	}

	target := p.ParentID
	if p.ToRoot {
		target = nil
	}

	nv, warning, err := h.Flavors.MoveNode(r.Context(), id, target)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var warnings []string
	if warning != "" {
		warnings = append(warnings, warning)
	}
	httpx.OKWithWarnings(w, nv, warnings)
}

func (h *Handlers) deleteFlavorNode(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	mode := flavor.DeleteMode(strings.ToUpper(httpx.QueryString(r, "mode")))
	switch mode {
	case flavor.DeleteCascade, flavor.DeletePromote:
	case "":
		// 默认上提子节点而非级联删除：误删一个中间层节点时，
		// 上提最多让分类变扁，级联则会连带删掉用户辛苦整理的整棵子树。
		// 破坏性更小的那个应该是默认值。
		mode = flavor.DeletePromote
	default:
		httpx.Fail(w, r, domain.Validation("INVALID_DELETE_MODE",
			"mode 只支持 CASCADE（连带删除子树）或 PROMOTE（上提子节点）"))
		return
	}

	res, err := h.Flavors.DeleteNode(r.Context(), id, mode)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.OK(w, res)
}

type setBeanFlavorsPayload struct {
	NodeIDs []int64 `json:"node_ids"`
}

func (h *Handlers) setBeanFlavors(w http.ResponseWriter, r *http.Request) {
	beanID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var p setBeanFlavorsPayload
	if err := httpx.DecodeJSON(w, r, &p); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	if err := h.Flavors.SetBeanFlavors(r.Context(), beanID, p.NodeIDs); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	v, err := h.Beans.Get(r.Context(), beanID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.OK(w, v)
}

// normalizeColor 校验并规范化十六进制色值。
func normalizeColor(c *validate.Collector, field, raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "#") {
		v = "#" + v
	}
	if len(v) != 7 {
		c.Add(field, "必须是 #RRGGBB 形式的十六进制色值")
		return ""
	}
	for _, ch := range v[1:] {
		isHex := (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
		if !isHex {
			c.Add(field, "含非十六进制字符")
			return ""
		}
	}
	return strings.ToUpper(v)
}

func normalizeProcess(raw string) domain.ProcessMethod {
	return domain.ProcessMethod(strings.TrimSpace(raw))
}
