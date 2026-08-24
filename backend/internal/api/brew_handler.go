package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/alkaid/enjoycoffee/internal/brew"
	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/httpx"
	"github.com/alkaid/enjoycoffee/internal/validate"
)

// brewPayload 是萃取记录的创建/更新/预览请求体。
type brewPayload struct {
	BeanID int64  `json:"bean_id"`
	Method string `json:"method"`
	Title  string `json:"title"`

	DoseG       string `json:"dose_g"`
	TotalWaterG string `json:"total_water_g"`
	BeverageG   string `json:"beverage_g"`
	TDSPercent  string `json:"tds_percent"`
	LRROverride string `json:"lrr_override"`

	Grinder      string `json:"grinder"`
	GrindSetting string `json:"grind_setting"`
	GrindMicron  int    `json:"grind_micron"`

	WaterTempC     int    `json:"water_temp_c"`
	Dripper        string `json:"dripper"`
	AgitationCount int    `json:"agitation_count"`

	PreInfusionSec int `json:"pre_infusion_sec"`
	PressureBarX10 int `json:"pressure_bar_x10"`

	ContactSeconds int `json:"contact_seconds"`

	Notes    string `json:"notes"`
	BrewedAt string `json:"brewed_at"`

	PourEvents []pourEventPayload `json:"pour_events"`
}

type pourEventPayload struct {
	OffsetMs    int    `json:"offset_ms"`
	CumulativeG string `json:"cumulative_g"`
	Technique   string `json:"technique"`
	Key         string `json:"idempotency_key"`
}

func (p brewPayload) toDomain(id int64) (*brew.Brew, error) {
	c := validate.New("INVALID_BREW_PAYLOAD", "萃取参数校验未通过")

	if p.BeanID <= 0 {
		c.Add("bean_id", "必须指定一支咖啡豆")
	}

	b := &brew.Brew{
		ID:           id,
		BeanID:       p.BeanID,
		Method:       c.BrewMethod("method", p.Method, domain.MethodFilter),
		Title:        c.MaxLen("title", p.Title, 80),
		DoseMg:       c.RequiredGrams("dose_g", p.DoseG),
		TotalWaterMg: c.Grams("total_water_g", p.TotalWaterG),
		BeverageMg:   c.Grams("beverage_g", p.BeverageG),
		TDS:          c.Percent("tds_percent", p.TDSPercent),
		LRROverride:  c.Multiple("lrr_override", p.LRROverride),

		Grinder:      c.MaxLen("grinder", p.Grinder, 60),
		GrindSetting: c.MaxLen("grind_setting", p.GrindSetting, 40),
		GrindMicron:  c.IntRange("grind_micron", p.GrindMicron, 100, 2000, true),

		WaterTempC:     c.IntRange("water_temp_c", p.WaterTempC, 60, 100, true),
		Dripper:        c.MaxLen("dripper", p.Dripper, 60),
		AgitationCount: c.IntRange("agitation_count", p.AgitationCount, 0, 30, true),

		PreInfusionSec: c.IntRange("pre_infusion_sec", p.PreInfusionSec, 0, 60, true),
		PressureBarX10: c.IntRange("pressure_bar_x10", p.PressureBarX10, 10, 200, true),

		ContactSeconds: c.IntRange("contact_seconds", p.ContactSeconds, 1, 1800, true),

		Notes: c.MaxLen("notes", p.Notes, 4000),
	}

	if p.BrewedAt != "" {
		t, err := time.Parse(time.RFC3339, p.BrewedAt)
		if err != nil {
			c.Add("brewed_at", "必须是 RFC3339 格式的时刻，如 2026-08-24T09:30:00+08:00")
		} else {
			b.BrewedAt = t.In(domain.Beijing)
		}
	}

	b.PourEvents = decodePourEvents(c, "pour_events", p.PourEvents)

	if err := c.Err(); err != nil {
		return nil, err
	}
	return b, nil
}

func decodePourEvents(c *validate.Collector, field string, in []pourEventPayload) []brew.PourEvent {
	if len(in) == 0 {
		return nil
	}
	out := make([]brew.PourEvent, 0, len(in))
	for i, ev := range in {
		idx := field + "[" + strconv.Itoa(i) + "]"
		e := brew.PourEvent{
			OffsetMs:       c.IntRange(idx+".offset_ms", ev.OffsetMs, 0, 1800000, true),
			CumulativeMg:   c.Grams(idx+".cumulative_g", ev.CumulativeG),
			Technique:      c.PourTechnique(idx+".technique", ev.Technique),
			Source:         brew.SourceManual,
			IdempotencyKey: ev.Key,
		}
		out = append(out, e)
	}
	return out
}

func (h *Handlers) listBrews(w http.ResponseWriter, r *http.Request) {
	page, err := httpx.ParsePagination(r)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	beanID, err := httpx.QueryInt64(r, "bean_id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	c := validate.New("INVALID_BREW_FILTER", "萃取记录筛选条件不合法")
	f := brew.ListFilter{
		BeanID:       beanID,
		Method:       c.BrewMethod("method", httpx.QueryString(r, "method"), ""),
		OnlyGold:     httpx.QueryBool(r, "only_gold", false),
		OnlyMeasured: httpx.QueryBool(r, "only_measured", false),
		Limit:        page.Limit,
		Offset:       page.Offset,
	}
	if since := httpx.QueryString(r, "since"); since != "" {
		d := c.CivilDate("since", since)
		if !d.IsZero() {
			f.Since = d.Time()
		}
	}
	if err := c.Err(); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	res, err := h.Brews.List(r.Context(), f)
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

func (h *Handlers) getBrew(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	v, err := h.Brews.Get(r.Context(), id)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.OK(w, v)
}

// previewBrew 在不落库的前提下评估一组参数，供萃取沙盘实时反馈。
func (h *Handlers) previewBrew(w http.ResponseWriter, r *http.Request) {
	var p brewPayload
	if err := httpx.DecodeJSON(w, r, &p); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	// 预览不需要真实豆子（用户可能还没决定用哪支），
	// 给一个占位 ID 让领域校验通过。引擎的计算与豆子无关。
	if p.BeanID == 0 {
		p.BeanID = -1
	}
	b, err := p.toDomain(0)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if b.BeanID == -1 {
		b.BeanID = 0
	}

	res, err := h.Brews.Preview(r.Context(), b)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.OK(w, res)
}

func (h *Handlers) createBrew(w http.ResponseWriter, r *http.Request) {
	var p brewPayload
	if err := httpx.DecodeJSON(w, r, &p); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	b, err := p.toDomain(0)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	v, warnings, err := h.Brews.Create(r.Context(), b)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.Created(w, v, warnings)
}

func (h *Handlers) updateBrew(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var p brewPayload
	if err := httpx.DecodeJSON(w, r, &p); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	b, err := p.toDomain(id)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	v, err := h.Brews.Update(r.Context(), b)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.OK(w, v)
}

func (h *Handlers) deleteBrew(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err := h.Brews.Delete(r.Context(), id); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.NoContent(w)
}

type appendPourPayload struct {
	Events []pourEventPayload `json:"events"`
}

// appendPourEvents 是 WebSocket 之外的注水打点入口。
//
// 保留这条 HTTP 路径而非只提供 WebSocket：用户可能在冲煮结束后
// 才想起来补录注水节点，此时开一条实时通道毫无意义。两条路径共用
// 同一个服务方法，因此幂等合并与曲线推导的行为完全一致。
func (h *Handlers) appendPourEvents(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var p appendPourPayload
	if err := httpx.DecodeJSON(w, r, &p); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	c := validate.New("INVALID_POUR_EVENTS", "注水节点校验未通过")
	events := decodePourEvents(c, "events", p.Events)
	if err := c.Err(); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	curve, accepted, err := h.Brews.AppendPourEvents(r.Context(), id, events)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	// 通过 HTTP 打的点也要广播给 WebSocket 端，否则同时开着实时曲线的
	// 那个设备会看不到这些点，两端显示的曲线就分叉了。
	h.broadcastCurve(id, curve, accepted)

	httpx.OK(w, map[string]any{"curve": curve, "accepted": accepted})
}
