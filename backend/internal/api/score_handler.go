package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/flavorscore"
	"github.com/alkaid/enjoycoffee/internal/httpx"
	"github.com/alkaid/enjoycoffee/internal/validate"
)

// scorePayload 是六维风味评分的请求体。
//
// 分值走 ×10 整数而非浮点小数：前端滑块的粒度是 0.5 分，
// 用整数表达就把"用户能选到的值"和"能存进数据库的值"变成了同一个集合，
// 不会出现 7.3 分这种滑块选不到、却能通过 API 塞进来的值。
type scorePayload struct {
	AcidityX10   int    `json:"acidity_x10"`
	SweetX10     int    `json:"sweet_x10"`
	AromaX10     int    `json:"aroma_x10"`
	AftertoneX10 int    `json:"aftertone_x10"`
	BodyX10      int    `json:"body_x10"`
	BitterX10    int    `json:"bitter_x10"`
	Note         string `json:"note"`
	ScoredAt     string `json:"scored_at"`
}

func (h *Handlers) saveScore(w http.ResponseWriter, r *http.Request) {
	brewID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	var p scorePayload
	if err := httpx.DecodeJSON(w, r, &p); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	c := validate.New("INVALID_SCORE_PAYLOAD", "风味评分校验未通过")
	sc := &flavorscore.Score{
		BrewID:       brewID,
		AcidityX10:   scoreAxis(c, "acidity_x10", p.AcidityX10),
		SweetX10:     scoreAxis(c, "sweet_x10", p.SweetX10),
		AromaX10:     scoreAxis(c, "aroma_x10", p.AromaX10),
		AftertoneX10: scoreAxis(c, "aftertone_x10", p.AftertoneX10),
		BodyX10:      scoreAxis(c, "body_x10", p.BodyX10),
		BitterX10:    scoreAxis(c, "bitter_x10", p.BitterX10),
		Note:         c.MaxLen("note", p.Note, 2000),
	}

	if p.ScoredAt != "" {
		t, perr := time.Parse(time.RFC3339, p.ScoredAt)
		if perr != nil {
			c.Add("scored_at", "必须是 RFC3339 格式的时刻")
		} else {
			sc.ScoredAt = t.In(domain.Beijing)
		}
	}

	if err := c.Err(); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	radar, err := h.Scores.Save(detachContext(r), sc)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"score": sc.View(), "radar": radar})
}

func (h *Handlers) getScore(w http.ResponseWriter, r *http.Request) {
	brewID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	sc, radar, err := h.Scores.GetByBrew(r.Context(), brewID)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	// score 为 nil 表示尚未评分。这是合法状态，返回 200 加空值，
	// 让前端渲染"去评分"的空态；返回 404 会被前端的错误处理拦成红条提示。
	httpx.OK(w, map[string]any{"score": sc.View(), "radar": radar})
}

func (h *Handlers) deleteScore(w http.ResponseWriter, r *http.Request) {
	brewID, err := httpx.PathID(r, "id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err := h.Scores.Delete(r.Context(), brewID); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.NoContent(w)
}

// radarWall 返回多支豆的雷达聚合，供风味雷达墙做叠加对比。
//
// 上限 6 条是设计约束而非技术限制：七条以上的半透明多边形叠在一起，
// 人眼已经无法分辨哪条属于哪支豆，图表就从"对比工具"退化成"装饰"。
// 这个上限在 DesignSpec 里也有对应的图例配色方案。
func (h *Handlers) radarWall(w http.ResponseWriter, r *http.Request) {
	beanIDs, err := httpx.QueryInt64List(r, "bean_ids")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if len(beanIDs) == 0 {
		httpx.Fail(w, r, domain.Validation("MISSING_BEAN_IDS",
			"请用 bean_ids=1,2,3 指定要对比的豆子"))
		return
	}
	if len(beanIDs) > 6 {
		httpx.Fail(w, r, domain.Validation("TOO_MANY_BEANS",
			"最多同时对比 6 支豆。更多的半透明图层叠加后已无法分辨，请分批对比。").
			WithField("bean_ids", "收到 "+strconv.Itoa(len(beanIDs))+" 支"))
		return
	}

	radars, err := h.Scores.RadarForBeans(r.Context(), beanIDs)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	// 按请求顺序组装并附上豆子名称：前端画图例时需要名字，
	// 让它为每支豆再发一个请求去取名字是不必要的往返。
	type layer struct {
		BeanID int64                `json:"bean_id"`
		Name   string               `json:"name"`
		Origin string               `json:"origin"`
		Roast  string               `json:"roast_level_label"`
		Radar  *domain.RadarSummary `json:"radar"`
	}
	layers := make([]layer, 0, len(beanIDs))
	for _, id := range beanIDs {
		bv, err := h.Beans.Get(r.Context(), id)
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}
		l := layer{BeanID: id, Name: bv.Name, Origin: bv.Origin, Roast: bv.RoastLevelLabel}
		if rad, ok := radars[id]; ok && rad != nil {
			l.Radar = rad
		} else {
			// 没评过分的豆也要出现在图例里，画成一个塌到中心的空多边形。
			// 直接省略它会让用户以为自己少选了一支豆。
			l.Radar = domain.NewEmptyRadar()
		}
		layers = append(layers, l)
	}

	httpx.OK(w, map[string]any{
		"layers": layers,
		"axes":   domain.FlavorAxes(),
		"max":    domain.MaxAxisScore,
	})
}

// scoreAxis 校验单个维度分值：0–100 且为 5 的倍数（对应 0–10 分，步进 0.5）。
func scoreAxis(c *validate.Collector, field string, v int) int {
	if v < 0 || v > 100 {
		c.Add(field, "必须在 0–100 之间（对应 0–10.0 分）")
		return 0
	}
	if v%5 != 0 {
		c.Add(field, "步进为 5（对应 0.5 分），收到 "+strconv.Itoa(v))
		return 0
	}
	return v
}
