package api

import (
	"net/http"

	"github.com/alkaid/enjoycoffee/internal/bean"
	"github.com/alkaid/enjoycoffee/internal/flavor"
	"github.com/alkaid/enjoycoffee/internal/httpx"
	"github.com/alkaid/enjoycoffee/internal/validate"
)

// beanPayload 是豆子的创建/更新请求体。
//
// 重量字段是字符串而非 number：见 internal/validate 包注释。
// 用户输入的 "227.5" 克必须原样进精度包，中途落进 float64 就会丢精度。
type beanPayload struct {
	Name    string `json:"name"`
	Roaster string `json:"roaster"`
	IsBlend bool   `json:"is_blend"`

	Country  string `json:"country"`
	Region   string `json:"region"`
	Farm     string `json:"farm"`
	Altitude int    `json:"altitude_m"`
	Process  string `json:"process"`
	Variety  string `json:"variety"`

	RoastLevel string `json:"roast_level"`
	RoastNote  string `json:"roast_note"`
	RoastedOn  string `json:"roasted_on"`
	OpenedOn   string `json:"opened_on"`

	InitialWeightG string `json:"initial_weight_g"`
	RemainingG     string `json:"remaining_g"`

	Notes    string `json:"notes"`
	Archived bool   `json:"archived"`

	FlavorNodeIDs []int64 `json:"flavor_node_ids"`
}

// toDomain 把请求体转为领域对象，收集全部字段级校验失败。
//
// 领域层的 Validate 仍会再跑一遍 —— 这不是冗余：这里负责的是
// "字符串能否变成定点数"，领域层负责的是"这些数值在业务上讲不讲得通"。
// 两者的职责不同，且领域层必须能独立守住不变量（种子数据、未来的导入
// 功能都不经过 HTTP 层）。
func (p beanPayload) toDomain(id int64) (*bean.Bean, error) {
	c := validate.New("INVALID_BEAN_PAYLOAD", "咖啡豆信息校验未通过")

	b := &bean.Bean{
		ID:              id,
		Name:            c.NonEmpty("name", p.Name),
		Roaster:         c.MaxLen("roaster", p.Roaster, 60),
		IsBlend:         p.IsBlend,
		Country:         c.MaxLen("country", p.Country, 40),
		Region:          c.MaxLen("region", p.Region, 60),
		Farm:            c.MaxLen("farm", p.Farm, 80),
		Altitude:        c.IntRange("altitude_m", p.Altitude, 0, 4000, true),
		Variety:         c.MaxLen("variety", p.Variety, 120),
		RoastLevel:      c.RoastLevel("roast_level", p.RoastLevel),
		RoastNote:       c.MaxLen("roast_note", p.RoastNote, 200),
		RoastedOn:       c.RequiredCivilDate("roasted_on", p.RoastedOn),
		OpenedOn:        c.CivilDate("opened_on", p.OpenedOn),
		InitialWeightMg: c.Grams("initial_weight_g", p.InitialWeightG),
		RemainingMg:     c.Grams("remaining_g", p.RemainingG),
		Notes:           c.MaxLen("notes", p.Notes, 2000),
		Archived:        p.Archived,
		FlavorNodeIDs:   p.FlavorNodeIDs,
	}
	b.Process = normalizeProcess(c.MaxLen("process", p.Process, 40))
	b.Name = c.MaxLen("name", b.Name, 80)

	if err := c.Err(); err != nil {
		return nil, err
	}
	return b, nil
}

func (h *Handlers) listBeans(w http.ResponseWriter, r *http.Request) {
	page, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	c := validate.New("INVALID_BEAN_FILTER", "豆库筛选条件不合法")

	nodeIDs, err := httpx.QueryInt64List(r, "flavor_ids")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	f := bean.ListFilter{
		Keyword:         httpx.QueryString(r, "keyword"),
		RoastLevels:     c.RoastLevels("roast_levels", httpx.QueryStringList(r, "roast_levels")),
		Stages:          c.FreshnessStages("stages", httpx.QueryStringList(r, "stages")),
		FlavorNodeIDs:   nodeIDs,
		FlavorMatch:     flavor.ParseMatchMode(httpx.QueryString(r, "flavor_match")),
		ExactFlavorOnly: httpx.QueryBool(r, "exact_flavor", false),
		IncludeArchived: httpx.QueryBool(r, "include_archived", false),
		OnlyOpened:      httpx.QueryBool(r, "only_opened", false),
		OnlyUnopened:    httpx.QueryBool(r, "only_unopened", false),
		Sort:            bean.SortKey(httpx.QueryString(r, "sort")),
		Limit:           page.Limit,
		Offset:          page.Offset,
	}
	if err := c.Err(); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	res, err := h.Beans.List(r.Context(), f)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	httpx.OKWithMeta(w, res, &httpx.Meta{
		Total:    res.Total,
		Page:     page.Page,
		PageSize: page.PageSize,
	})
}

func (h *Handlers) getBean(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	v, err := h.Beans.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.OK(w, v)
}

func (h *Handlers) createBean(w http.ResponseWriter, r *http.Request) {
	var p beanPayload
	if err := httpx.DecodeJSON(w, r, &p); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	b, err := p.toDomain(0)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	v, err := h.Beans.Create(r.Context(), b)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.Created(w, v, nil)
}

func (h *Handlers) updateBean(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var p beanPayload
	if err := httpx.DecodeJSON(w, r, &p); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	b, err := p.toDomain(id)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	v, err := h.Beans.Update(r.Context(), b)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.OK(w, v)
}

func (h *Handlers) deleteBean(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err := h.Beans.Delete(r.Context(), id); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.NoContent(w)
}

type consumePayload struct {
	AmountG string `json:"amount_g"`
}

// consumeBean 手动扣减豆量，用于"不记录参数只是喝掉了一些"的场景。
func (h *Handlers) consumeBean(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var p consumePayload
	if err := httpx.DecodeJSON(w, r, &p); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	c := validate.New("INVALID_CONSUME_PAYLOAD", "扣减量不合法")
	amount := c.RequiredGrams("amount_g", p.AmountG)
	if err := c.Err(); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	warning, err := h.Beans.Consume(r.Context(), id, amount)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	v, err := h.Beans.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var warnings []string
	if warning != "" {
		warnings = append(warnings, warning)
	}
	httpx.OKWithWarnings(w, v, warnings)
}

// beanBoard 返回豆库看板：按紧迫度排序的全部在库豆子及其生命周期分段。
func (h *Handlers) beanBoard(w http.ResponseWriter, r *http.Request) {
	board, err := h.Beans.BuildBoard(r.Context())
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.OK(w, board)
}
