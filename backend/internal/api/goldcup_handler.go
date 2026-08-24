package api

import (
	"net/http"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/goldcup"
	"github.com/alkaid/enjoycoffee/internal/httpx"
	"github.com/alkaid/enjoycoffee/internal/store"
	"github.com/alkaid/enjoycoffee/internal/validate"
)

// goldcupProfiles 返回当前生效的金杯标准。
//
// 前端画控制图需要知道理想区间的边界在哪。这些边界必须来自后端而非
// 前端硬编码 18%–22%：一旦店主在设置面板改了标准，硬编码的那条线
// 就会和后端的判定结果对不上，图上一个点会显示在绿区里却被标成欠萃。
func (h *Handlers) goldcupProfiles(w http.ResponseWriter, r *http.Request) {
	views := make([]goldcup.ProfileView, 0, 2)
	for _, m := range []domain.BrewMethod{domain.MethodFilter, domain.MethodEspresso} {
		p, err := h.Engine.ProfileFor(m)
		if err != nil {
			httpx.Fail(w, r, err)
			return
		}
		views = append(views, p.View())
	}
	httpx.OK(w, map[string]any{
		"profiles": views,
		"zones":    goldcup.ZoneMatrix(),
	})
}

type profilePayload struct {
	YieldMinPercent    string `json:"yield_min_percent"`
	YieldMaxPercent    string `json:"yield_max_percent"`
	StrengthMinPercent string `json:"strength_min_percent"`
	StrengthMaxPercent string `json:"strength_max_percent"`
	RatioMin           string `json:"ratio_min"`
	RatioMax           string `json:"ratio_max"`
	LRR                string `json:"lrr"`
}

// saveGoldcupProfile 保存店主自定义的出品标准（Roadmap V-07 / V-08）。
func (h *Handlers) saveGoldcupProfile(w http.ResponseWriter, r *http.Request) {
	c := validate.New("INVALID_METHOD", "冲煮法不合法")
	method := c.BrewMethod("method", chiParam(r, "method"), "")
	if method == "" {
		c.Add("method", "只支持 FILTER 或 ESPRESSO")
	}
	if err := c.Err(); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	var p profilePayload
	if err := httpx.DecodeJSON(w, r, &p); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	profile, err := store.ProfileFromPercents(method,
		p.YieldMinPercent, p.YieldMaxPercent,
		p.StrengthMinPercent, p.StrengthMaxPercent,
		p.RatioMin, p.RatioMax, p.LRR)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	if err := h.Configs.Save(r.Context(), profile); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err := h.reloadEngineProfiles(r.Context()); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	warnings := []string{
		"新标准只影响此后新增或重新计算的记录。已有记录保留当时的判定快照，" +
			"以免历史结论在你改配置后悄悄变样。",
	}
	httpx.OKWithWarnings(w, profile.View(), warnings)
}

// resetGoldcupProfile 恢复出厂标准。
func (h *Handlers) resetGoldcupProfile(w http.ResponseWriter, r *http.Request) {
	c := validate.New("INVALID_METHOD", "冲煮法不合法")
	method := c.BrewMethod("method", chiParam(r, "method"), "")
	if method == "" {
		c.Add("method", "只支持 FILTER 或 ESPRESSO")
	}
	if err := c.Err(); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	if err := h.Configs.Reset(r.Context(), method); err != nil {
		httpx.Fail(w, r, err)
		return
	}
	if err := h.reloadEngineProfiles(r.Context()); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	p, err := h.Engine.ProfileFor(method)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.OK(w, p.View())
}

// goldcupChart 返回某支豆某冲煮法的控制图几何数据。
func (h *Handlers) goldcupChart(w http.ResponseWriter, r *http.Request) {
	beanID, err := httpx.QueryInt64(r, "bean_id")
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}

	c := validate.New("INVALID_CHART_QUERY", "控制图查询参数不合法")
	method := c.BrewMethod("method", httpx.QueryString(r, "method"), domain.MethodFilter)
	if err := c.Err(); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	chart, err := h.Brews.Chart(r.Context(), beanID, method)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.OK(w, chart)
}

type solvePayload struct {
	Method      string `json:"method"`
	Target      string `json:"target"`
	TargetYield string `json:"target_yield_percent"`
	TDSPercent  string `json:"tds_percent"`
	DoseG       string `json:"dose_g"`
	BeverageG   string `json:"beverage_g"`
	LRROverride string `json:"lrr_override"`
}

// solve 三向反解：给定目标萃取率，回答"粉量该多少 / 水量该多少 / TDS 会是多少"。
func (h *Handlers) solve(w http.ResponseWriter, r *http.Request) {
	var p solvePayload
	if err := httpx.DecodeJSON(w, r, &p); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	c := validate.New("INVALID_SOLVE_PAYLOAD", "反解参数校验未通过")
	target := goldcup.SolveTarget(p.Target)
	switch target {
	case goldcup.SolveTargetTDS, goldcup.SolveTargetBeverage,
		goldcup.SolveTargetDose, goldcup.SolveTargetTotalWater:
	default:
		c.Add("target", "只支持 tds / beverage / dose / total_water")
	}

	req := goldcup.SolveRequest{
		Method:      c.BrewMethod("method", p.Method, domain.MethodFilter),
		Target:      target,
		TargetYield: c.Percent("target_yield_percent", p.TargetYield),
		TDS:         c.Percent("tds_percent", p.TDSPercent),
		Dose:        c.Grams("dose_g", p.DoseG),
		Beverage:    c.Grams("beverage_g", p.BeverageG),
		LRROverride: c.Multiple("lrr_override", p.LRROverride),
	}
	if err := c.Err(); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	res, err := h.Brews.Solve(req)
	if err != nil {
		httpx.Fail(w, r, err)
		return
	}
	httpx.OK(w, res)
}

// meta 返回全部枚举字典，供前端渲染下拉框与图例。
//
// 集中在一个端点而非散落在各处硬编码：枚举一旦在前后端各写一份，
// 后端加了一个烘焙度档位而前端下拉框里没有，用户就永远选不到它，
// 而且这种不一致不会报错，只会静默地少一个选项。
func (h *Handlers) meta(w http.ResponseWriter, r *http.Request) {
	roastLevels := make([]map[string]any, 0, 6)
	for _, lv := range domain.AllRoastLevels() {
		roastLevels = append(roastLevels, map[string]any{
			"value": string(lv),
			"label": lv.Label(),
			"band":  string(lv.Band()),
		})
	}

	stages := make([]map[string]any, 0, 4)
	for _, st := range domain.AllFreshnessStages() {
		stages = append(stages, map[string]any{
			"value":      string(st),
			"label":      st.Label(),
			"color_hint": st.ColorHint(),
		})
	}

	techniques := make([]map[string]any, 0, 8)
	for _, t := range domain.AllPourTechniques() {
		techniques = append(techniques, map[string]any{
			"value": string(t),
			"label": t.Label(),
		})
	}

	methods := make([]map[string]any, 0, 2)
	for _, m := range []domain.BrewMethod{domain.MethodFilter, domain.MethodEspresso} {
		methods = append(methods, map[string]any{
			"value": string(m),
			"label": m.Label(),
		})
	}

	httpx.OK(w, map[string]any{
		"brew_methods":       methods,
		"roast_levels":       roastLevels,
		"freshness_stages":   stages,
		"pour_techniques":    techniques,
		"process_methods":    domain.CommonProcessMethods(),
		"flavor_axes":        domain.FlavorAxes(),
		"max_axis_score":     domain.MaxAxisScore,
		"pour_source_mode":   string(h.Hub.Mode()),
		"lifecycle_windows":  h.lifecycleWindows(),
		"roast_level_bands":  roastLevelBands(),
		"solve_targets":      goldcup.SolveTargets(),
		"flavor_match_modes": []string{"ALL", "ANY"},
	})
}
