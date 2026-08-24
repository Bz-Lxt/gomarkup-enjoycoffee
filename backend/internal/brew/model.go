// Package brew 实现萃取记录（冲煮会话）的领域模型与业务逻辑。
package brew

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
	"github.com/alkaid/enjoycoffee/internal/goldcup"
)

// Brew 是一次萃取记录。
type Brew struct {
	ID     int64
	BeanID int64
	Method domain.BrewMethod
	Title  string

	// 通用参数
	DoseMg       fixed.Mass
	TotalWaterMg fixed.Mass
	BeverageMg   fixed.Mass
	TDS          fixed.Ratio
	LRROverride  fixed.Ratio

	// 研磨
	Grinder      string
	GrindSetting string // 用户的磨豆机刻度原文，如 "C40 格数 22"
	GrindMicron  int    // 折算粒径（微米），用于跨设备比较与动力学修正

	// 手冲专属
	WaterTempC     int
	Dripper        string
	AgitationCount int

	// 意式专属
	PreInfusionSec int
	PressureBarX10 int // 压力（bar×10），定点整数避免浮点

	ContactSeconds int

	Notes    string
	BrewedAt time.Time

	// 引擎计算结果快照。存下来而非每次读取时重算，理由有两点：
	// 一是历史记录的判定应该稳定 —— 用户改了金杯标准配置后，
	// 不该让三个月前那杯咖啡的判定悄悄变了；
	// 二是列表页与图表要一次取回几百条记录，实时重算是不必要的开销。
	Mode           goldcup.Mode
	YieldPPM       fixed.Ratio
	TDSCalcPPM     fixed.Ratio
	RatioPPM       fixed.Ratio
	BeverageCalcMg fixed.Mass
	ZoneCode       string
	InGoldCup      bool
	Confidence     int // 置信度 ×1000，定点整数；测量模式为 1000

	PourEvents []PourEvent

	CreatedAt time.Time
	UpdatedAt time.Time
}

// View 是萃取记录的 API 输出形态。
type View struct {
	ID          int64  `json:"id"`
	BeanID      int64  `json:"bean_id"`
	BeanName    string `json:"bean_name"`
	Method      string `json:"method"`
	MethodLabel string `json:"method_label"`
	Title       string `json:"title"`

	DoseG       float64 `json:"dose_g"`
	DoseText    string  `json:"dose_text"`
	TotalWaterG float64 `json:"total_water_g"`
	BeverageG   float64 `json:"beverage_g"`
	TDSPercent  float64 `json:"tds_percent"`
	HasTDS      bool    `json:"has_tds"`

	Grinder      string `json:"grinder"`
	GrindSetting string `json:"grind_setting"`
	GrindMicron  int    `json:"grind_micron"`

	WaterTempC     int    `json:"water_temp_c"`
	Dripper        string `json:"dripper"`
	AgitationCount int    `json:"agitation_count"`

	PreInfusionSec int     `json:"pre_infusion_sec"`
	PressureBar    float64 `json:"pressure_bar"`
	ContactSeconds int     `json:"contact_seconds"`
	ContactLabel   string  `json:"contact_label"`

	Notes    string `json:"notes"`
	BrewedAt string `json:"brewed_at"`

	// Result 是引擎的完整评估结果。列表场景下为 nil 以减小载荷，
	// 详情场景下填充。
	Result *goldcup.Result `json:"result"`

	// 以下为从存储快照直接读出的摘要字段，供列表页渲染而无需完整 Result。
	Mode           string  `json:"mode"`
	Advisory       bool    `json:"advisory"`
	YieldPercent   float64 `json:"yield_percent"`
	YieldText      string  `json:"yield_text"`
	CalcTDSPercent float64 `json:"calc_tds_percent"`
	BrewRatio      float64 `json:"brew_ratio"`
	BrewRatioText  string  `json:"brew_ratio_text"`
	ZoneCode       string  `json:"zone_code"`
	ZoneLabel      string  `json:"zone_label"`
	InGoldCup      bool    `json:"in_gold_cup"`
	Confidence     float64 `json:"confidence"`

	PourCurve *PourCurve           `json:"pour_curve"`
	Radar     *domain.RadarSummary `json:"radar"`

	CreatedAt string `json:"created_at"`
}

// ToInput 把记录转为引擎输入。
func (b *Brew) ToInput() goldcup.Input {
	return goldcup.Input{
		Method:           b.Method,
		Dose:             b.DoseMg,
		TotalWater:       b.TotalWaterMg,
		MeasuredBeverage: b.BeverageMg,
		TDS:              b.TDS,
		LRROverride:      b.LRROverride,
		GrindMicron:      b.GrindMicron,
		WaterTempC:       b.WaterTempC,
		ContactSeconds:   b.EffectiveContactSeconds(),
		AgitationCount:   b.AgitationCount,
	}
}

// EffectiveContactSeconds 返回实际接触时间。
//
// 优先用显式填写的值；未填时从注水节点序列的末端偏移推导 ——
// 用户既然打了点，就没必要让他再手填一遍总时长。
func (b *Brew) EffectiveContactSeconds() int {
	if b.ContactSeconds > 0 {
		return b.ContactSeconds
	}
	maxMs := 0
	for _, e := range b.PourEvents {
		if e.OffsetMs > maxMs {
			maxMs = e.OffsetMs
		}
	}
	return maxMs / 1000
}

// EffectiveTotalWater 返回实际总注水量。
//
// 同上：若用户打了注水节点，末端的累计值就是总注水量，
// 不需要也不应该让他再填一个可能与曲线矛盾的数字。
func (b *Brew) EffectiveTotalWater() fixed.Mass {
	if b.TotalWaterMg > 0 {
		return b.TotalWaterMg
	}
	var maxMg fixed.Mass
	for _, e := range b.PourEvents {
		if e.CumulativeMg > maxMg {
			maxMg = e.CumulativeMg
		}
	}
	return maxMg
}

// ToView 生成基础视图（不含 Result / PourCurve / Radar，由服务层按需填充）。
func (b *Brew) ToView() View {
	v := View{
		ID:             b.ID,
		BeanID:         b.BeanID,
		Method:         string(b.Method),
		MethodLabel:    b.Method.Label(),
		Title:          b.Title,
		DoseG:          b.DoseMg.ApproxGramsFloat(),
		DoseText:       b.DoseMg.GramsPrecise(),
		TotalWaterG:    b.TotalWaterMg.ApproxGramsFloat(),
		BeverageG:      b.BeverageCalcMg.ApproxGramsFloat(),
		TDSPercent:     b.TDS.ApproxPercentFloat(),
		HasTDS:         b.TDS > 0,
		Grinder:        b.Grinder,
		GrindSetting:   b.GrindSetting,
		GrindMicron:    b.GrindMicron,
		WaterTempC:     b.WaterTempC,
		Dripper:        b.Dripper,
		AgitationCount: b.AgitationCount,
		PreInfusionSec: b.PreInfusionSec,
		PressureBar:    float64(b.PressureBarX10) / 10,
		ContactSeconds: b.ContactSeconds,
		ContactLabel:   formatStopwatch(b.ContactSeconds * 1000),
		Notes:          b.Notes,
		BrewedAt:       domain.FormatDisplay(b.BrewedAt),
		Mode:           string(b.Mode),
		Advisory:       b.Mode == goldcup.ModeEstimated,
		YieldPercent:   b.YieldPPM.ApproxPercentFloat(),
		YieldText:      b.YieldPPM.Percent(),
		CalcTDSPercent: b.TDSCalcPPM.ApproxPercentFloat(),
		BrewRatio:      b.RatioPPM.ApproxMultipleFloat(),
		BrewRatioText:  b.RatioPPM.BrewRatioLabel(),
		ZoneCode:       b.ZoneCode,
		InGoldCup:      b.InGoldCup,
		Confidence:     float64(b.Confidence) / 1000,
		CreatedAt:      domain.FormatDisplay(b.CreatedAt),
	}
	if b.Title == "" {
		v.Title = b.Method.Label() + " · " + domain.FormatDisplay(b.BrewedAt)
	}
	return v
}

// Normalize 修剪字符串并补齐可推导字段。
func (b *Brew) Normalize() {
	b.Title = strings.TrimSpace(b.Title)
	b.Grinder = strings.TrimSpace(b.Grinder)
	b.GrindSetting = strings.TrimSpace(b.GrindSetting)
	b.Dripper = strings.TrimSpace(b.Dripper)
	b.Notes = strings.TrimSpace(b.Notes)

	if b.BrewedAt.IsZero() {
		b.BrewedAt = domain.Now()
	}
	if b.TotalWaterMg == 0 {
		b.TotalWaterMg = b.EffectiveTotalWater()
	}
	if b.ContactSeconds == 0 {
		b.ContactSeconds = b.EffectiveContactSeconds()
	}
	b.PourEvents = MergePourEvents(nil, b.PourEvents)
}

// Validate 校验萃取记录的业务约束。
//
// 注意分工：物理量的合理范围（粉量是否为正、TDS 是否超过 30%）由 goldcup
// 引擎统一把守，此处不重复 —— 重复校验会在两处规则不一致时产生
// "校验通过但计算报错"这种最难排查的情况。此处只管引擎不关心的字段。
func (b *Brew) Validate() error {
	e := domain.Validation("INVALID_BREW", "萃取记录不完整或不合法")
	bad := false

	if b.BeanID <= 0 {
		e.WithField("bean_id", "必须指定所用的咖啡豆")
		bad = true
	}
	if !b.Method.Valid() {
		e.WithField("method", "冲煮法必须为 FILTER 或 ESPRESSO")
		bad = true
	}

	if b.GrindMicron < 0 || b.GrindMicron > 2000 {
		// 2000µm 已是粗如砂糖的法压壶研磨；超出即单位错误
		e.WithField("grind_micron", "研磨粒径应在 0–2000 微米之间")
		bad = true
	}

	if b.WaterTempC != 0 && (b.WaterTempC < 60 || b.WaterTempC > 100) {
		// 低于 60℃ 无法有效萃取，高于 100℃ 在常压下不存在液态水
		e.WithField("water_temp_c", "水温应在 60–100℃ 之间")
		bad = true
	}

	if b.AgitationCount < 0 || b.AgitationCount > 50 {
		e.WithField("agitation_count", "搅拌次数应在 0–50 之间")
		bad = true
	}

	if b.ContactSeconds < 0 || b.ContactSeconds > 30*60 {
		e.WithField("contact_seconds", "接触时间应在 0–1800 秒之间")
		bad = true
	}

	if b.Method == domain.MethodEspresso {
		if b.PreInfusionSec < 0 || b.PreInfusionSec > 60 {
			e.WithField("pre_infusion_sec", "预浸泡时长应在 0–60 秒之间")
			bad = true
		}
		if b.PressureBarX10 < 0 || b.PressureBarX10 > 200 {
			// 20 bar 已超出所有商用意式机的上限
			e.WithField("pressure_bar", "萃取压力应在 0–20 bar 之间")
			bad = true
		}
		if b.PreInfusionSec > 0 && b.ContactSeconds > 0 && b.PreInfusionSec >= b.ContactSeconds {
			// 时序矛盾：预浸泡是总萃取时间的一部分，不可能等于或超过它
			e.WithField("pre_infusion_sec", "预浸泡时长必须短于总萃取时间")
			bad = true
		}
	}

	if utf8.RuneCountInString(b.Notes) > 2000 {
		e.WithField("notes", "备注不能超过 2000 个字符")
		bad = true
	}
	if utf8.RuneCountInString(b.Title) > 80 {
		e.WithField("title", "标题不能超过 80 个字符")
		bad = true
	}

	if b.BrewedAt.After(domain.Now().Add(24 * time.Hour)) {
		e.WithField("brewed_at", "冲煮时间不能是未来")
		bad = true
	}

	if err := ValidatePourEvents(b.PourEvents); err != nil {
		return err
	}

	if bad {
		return e
	}
	return nil
}

// ApplyResult 把引擎结果写回记录的快照字段。
func (b *Brew) ApplyResult(r *goldcup.Result) {
	b.Mode = r.Mode
	b.YieldPPM = r.RawYield
	b.TDSCalcPPM = r.RawTDS
	b.RatioPPM = r.RawRatio
	b.BeverageCalcMg = r.RawBeverage
	b.ZoneCode = r.Zone.Code
	b.InGoldCup = r.Zone.InGoldCup

	if r.Mode == goldcup.ModeMeasured {
		b.Confidence = 1000
	} else if r.Estimation != nil {
		b.Confidence = int(r.Estimation.Confidence*1000 + 0.5)
	}
}
