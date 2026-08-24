// Package bean 实现咖啡豆库的领域模型与业务逻辑。
package bean

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
)

// Bean 是一支入库的咖啡豆。
type Bean struct {
	ID      int64
	Name    string
	Roaster string
	IsBlend bool

	// 产地溯源
	Country  string
	Region   string
	Farm     string
	Altitude int // 海拔（米）。0 表示未填。
	Process  domain.ProcessMethod
	Variety  string

	// 烘焙
	RoastLevel domain.RoastLevel
	RoastNote  string
	RoastedOn  domain.CivilDate

	// 生命周期
	OpenedOn        domain.CivilDate
	InitialWeightMg fixed.Mass
	RemainingMg     fixed.Mass

	Notes    string
	Archived bool

	FlavorNodeIDs []int64

	CreatedAt time.Time
	UpdatedAt time.Time
}

// View 是豆子的 API 输出形态，携带派生出的新鲜度与风味信息。
type View struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Roaster string `json:"roaster"`
	IsBlend bool   `json:"is_blend"`

	Country  string `json:"country"`
	Region   string `json:"region"`
	Farm     string `json:"farm"`
	Altitude int    `json:"altitude_m"`
	Process  string `json:"process"`
	Variety  string `json:"variety"`
	// Origin 是拼好的产地展示串，如"埃塞俄比亚 · 耶加雪菲 · 孔加合作社"。
	// 由后端拼装，避免前端各处用不同的分隔符拼出不一致的展示。
	Origin string `json:"origin"`

	RoastLevel      string `json:"roast_level"`
	RoastLevelLabel string `json:"roast_level_label"`
	RoastNote       string `json:"roast_note"`
	RoastedOn       string `json:"roasted_on"`
	OpenedOn        string `json:"opened_on"`

	InitialWeightG   float64 `json:"initial_weight_g"`
	RemainingG       float64 `json:"remaining_g"`
	RemainingText    string  `json:"remaining_text"`
	RemainingPercent float64 `json:"remaining_percent"`
	// EstimatedBrewsLeft 按典型单次用粉量估算还能冲几次。
	// 这比"剩余 137g"更贴近用户的实际决策："还够喝一周吗"。
	EstimatedBrewsLeft int `json:"estimated_brews_left"`

	Notes    string `json:"notes"`
	Archived bool   `json:"archived"`

	Freshness Freshness            `json:"freshness"`
	Flavors   []FlavorTag          `json:"flavors"`
	Radar     *domain.RadarSummary `json:"radar"`

	BrewCount    int    `json:"brew_count"`
	LastBrewedAt string `json:"last_brewed_at"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// FlavorTag 是豆子上的一个风味标签，携带路径便于前端展示层级。
type FlavorTag struct {
	NodeID int64  `json:"node_id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Color  string `json:"color"`
	Icon   string `json:"icon"`
	Depth  int    `json:"depth"`
}

// typicalDoseMg 是估算剩余可冲次数用的典型单次粉量（15g）。
//
// 取 15g 而非区分手冲/意式：豆子本身不绑定冲煮法，同一包豆既可能手冲也可能
// 做意式。15g 介于手冲常用的 15–20g 与意式的 18–20g 之间，作为粗估足够。
// 这个数字只用于"还够喝几次"的直觉展示，不参与任何计算。
const typicalDoseMg = fixed.Mass(15_000)

// ToView 把领域对象转为 API 输出形态。
func (b *Bean) ToView(now time.Time) View {
	v := View{
		ID:              b.ID,
		Name:            b.Name,
		Roaster:         b.Roaster,
		IsBlend:         b.IsBlend,
		Country:         b.Country,
		Region:          b.Region,
		Farm:            b.Farm,
		Altitude:        b.Altitude,
		Process:         string(b.Process),
		Variety:         b.Variety,
		Origin:          b.OriginLabel(),
		RoastLevel:      string(b.RoastLevel),
		RoastLevelLabel: b.RoastLevel.Label(),
		RoastNote:       b.RoastNote,
		InitialWeightG:  b.InitialWeightMg.ApproxGramsFloat(),
		RemainingG:      b.RemainingMg.ApproxGramsFloat(),
		RemainingText:   b.RemainingMg.Grams(),
		Notes:           b.Notes,
		Archived:        b.Archived,
		Freshness:       EvaluateFreshness(b.RoastLevel, b.RoastedOn, b.OpenedOn, now),
		Flavors:         []FlavorTag{},
		CreatedAt:       domain.FormatDisplay(b.CreatedAt),
		UpdatedAt:       domain.FormatDisplay(b.UpdatedAt),
	}

	if !b.RoastedOn.IsZero() {
		v.RoastedOn = b.RoastedOn.String()
	}
	if !b.OpenedOn.IsZero() {
		v.OpenedOn = b.OpenedOn.String()
	}

	if b.InitialWeightMg > 0 {
		v.RemainingPercent = float64(int64(b.RemainingMg)*1000/int64(b.InitialWeightMg)) / 10
	}
	if b.RemainingMg > 0 {
		v.EstimatedBrewsLeft = int(b.RemainingMg / typicalDoseMg)
	}

	return v
}

// OriginLabel 拼装产地展示串，自动跳过未填字段。
func (b *Bean) OriginLabel() string {
	parts := make([]string, 0, 3)
	for _, p := range []string{b.Country, b.Region, b.Farm} {
		if t := strings.TrimSpace(p); t != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return "产地未填"
	}
	return strings.Join(parts, " · ")
}

// Validate 校验豆子的业务约束。
//
// 校验放在领域层而非 handler：同样的约束在创建、更新、种子数据导入
// 三条路径上都必须生效，写在 handler 里会漏掉后两条
// （global.md: 外部数据导入必须校验数据结构完整性，不能仅依赖调用处的简单格式检查）。
func (b *Bean) Validate() error {
	e := domain.Validation("INVALID_BEAN", "咖啡豆信息不完整或不合法")
	bad := false

	name := strings.TrimSpace(b.Name)
	switch {
	case name == "":
		e.WithField("name", "豆名必填")
		bad = true
	case utf8.RuneCountInString(name) > 60:
		e.WithField("name", "豆名不能超过 60 个字符")
		bad = true
	}

	if !b.RoastLevel.Valid() {
		e.WithField("roast_level", "烘焙度必须为 LIGHT / LIGHT_MEDIUM / MEDIUM / MEDIUM_DARK / DARK / VERY_DARK 之一")
		bad = true
	}

	if b.RoastedOn.IsZero() {
		// 烘焙日期是生命周期看板的唯一输入，缺了它整个"排气期/最佳期"功能就是空的。
		// 因此这里是硬性必填，而不是可选字段。
		e.WithField("roasted_on", "烘焙日期必填，它是养豆期与衰退期计算的唯一依据")
		bad = true
	}

	if b.Altitude < 0 || b.Altitude > 4000 {
		// 4000m 是咖啡种植的绝对上限（实际最高的秘鲁与埃塞产区约 2300m）。
		// 超出即单位错误，通常是把英尺当成米填了。
		e.WithField("altitude_m", "海拔应在 0–4000 米之间（若你填的是英尺，请先换算为米）")
		bad = true
	}

	if b.InitialWeightMg < 0 {
		e.WithField("initial_weight_g", "初始重量不能为负")
		bad = true
	}
	if b.RemainingMg < 0 {
		e.WithField("remaining_g", "剩余重量不能为负")
		bad = true
	}
	if b.InitialWeightMg > 0 && b.RemainingMg > b.InitialWeightMg {
		e.WithField("remaining_g", "剩余重量不能大于初始重量")
		bad = true
	}
	if b.InitialWeightMg > fixed.Mass(50_000_000) {
		e.WithField("initial_weight_g", "初始重量超过 50kg，请确认单位是克")
		bad = true
	}

	if !b.RoastedOn.IsZero() && !b.OpenedOn.IsZero() {
		if b.OpenedOn.Time().Before(b.RoastedOn.Time()) {
			// 时序矛盾：开封早于烘焙在物理上不可能
			e.WithField("opened_on", "开封日期不能早于烘焙日期")
			bad = true
		}
	}

	if utf8.RuneCountInString(b.Notes) > 2000 {
		e.WithField("notes", "备注不能超过 2000 个字符")
		bad = true
	}

	if bad {
		return e
	}
	return nil
}

// Normalize 修剪字符串字段并补齐可推导的默认值。
func (b *Bean) Normalize() {
	b.Name = strings.TrimSpace(b.Name)
	b.Roaster = strings.TrimSpace(b.Roaster)
	b.Country = strings.TrimSpace(b.Country)
	b.Region = strings.TrimSpace(b.Region)
	b.Farm = strings.TrimSpace(b.Farm)
	b.Variety = strings.TrimSpace(b.Variety)
	b.RoastNote = strings.TrimSpace(b.RoastNote)
	b.Notes = strings.TrimSpace(b.Notes)
	b.Process = domain.ProcessMethod(strings.TrimSpace(string(b.Process)))

	// 未填剩余量时按初始量补齐：新入库的豆子剩余量就等于总量，
	// 强迫用户填两遍同一个数字是多余的。
	if b.RemainingMg == 0 && b.InitialWeightMg > 0 {
		b.RemainingMg = b.InitialWeightMg
	}
}
