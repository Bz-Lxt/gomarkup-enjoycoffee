package goldcup

import (
	"sync/atomic"

	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
	"github.com/alkaid/enjoycoffee/internal/logger"
)

// Mode 是引擎的工作模式。
//
// 存在动因（Requirements 裁定 C-01）：原始需求把 TDS 标为"可选"，但萃取率的
// 定义式里 TDS 是唯一的实测浓度输入 —— 没有折射仪就没有真实萃取率，这是物理
// 事实而非工程缺陷。因此引擎必须显式区分"测量"与"推算"，并且两者的输出在
// 数据结构层面就不能混淆，否则用户会把一个统计推断当成实测结果去调整配方。
type Mode string

const (
	// ModeMeasured 用户提供了实测 TDS，萃取率由定义式精确算出。
	// 此模式出具正式的金杯判定。
	ModeMeasured Mode = "MEASURED"
	// ModeEstimated 未提供 TDS，萃取率由历史样本回归或动力学模型推算。
	// 此模式只给倾向提示，禁止输出"合格"结论。
	ModeEstimated Mode = "ESTIMATED"
)

// Input 是一次萃取评估的完整输入。
type Input struct {
	Method domain.BrewMethod

	// Dose 干粉重量。任何模式下都是必需的。
	Dose fixed.Mass
	// TotalWater 总注水量。手冲用它配合 LRR 推导液重。
	TotalWater fixed.Mass
	// MeasuredBeverage 实测液重/出液重量。意式必需；手冲若提供则优先于 LRR 推导。
	MeasuredBeverage fixed.Mass
	// TDS 实测浓度。为 0 表示未提供，引擎将切换到推算模式。
	TDS fixed.Ratio
	// LRROverride 覆盖 Profile 默认持水系数，为 0 表示不覆盖。
	LRROverride fixed.Ratio

	// 以下为推算模式的动力学特征。测量模式下它们不参与计算，
	// 但仍会被记录，因为它们是回归模型未来的训练数据。
	GrindMicron    int // 研磨粒径中值（微米）。0 表示未知。
	WaterTempC     int // 水温（摄氏度）。0 表示未知。
	ContactSeconds int // 水与粉的总接触时间（秒）。0 表示未知。
	AgitationCount int // 搅拌/断水次数。
}

// Result 是一次萃取评估的完整输出。
type Result struct {
	Mode   Mode              `json:"mode"`
	Method domain.BrewMethod `json:"method"`
	// Advisory 为 true 时表示本结果是统计推断而非测量，前端必须以虚线与
	// "推算值"角标渲染，且不得展示"合格"字样。
	Advisory bool        `json:"advisory"`
	Profile  ProfileView `json:"profile"`

	DoseGrams       float64 `json:"dose_g"`
	DoseText        string  `json:"dose_text"`
	BeverageGrams   float64 `json:"beverage_g"`
	BeverageText    string  `json:"beverage_text"`
	TotalWaterGrams float64 `json:"total_water_g"`
	AbsorbedGrams   float64 `json:"absorbed_g"`
	AbsorbedText    string  `json:"absorbed_text"`
	BrewRatioValue  float64 `json:"brew_ratio"`
	BrewRatioText   string  `json:"brew_ratio_text"`
	TDSPercent      float64 `json:"tds_percent"`
	TDSText         string  `json:"tds_text"`
	YieldPercent    float64 `json:"yield_percent"`
	YieldText       string  `json:"yield_text"`
	SolidsGrams     float64 `json:"dissolved_solids_g"`
	SolidsText      string  `json:"dissolved_solids_text"`

	Zone   Zone     `json:"zone"`
	Advice []Advice `json:"advice"`

	// Estimation 仅在推算模式下非空，携带置信度与区间边界。
	Estimation *Estimation `json:"estimation"`

	// Warnings 是不阻断计算但值得用户注意的提示。
	Warnings []string `json:"warnings"`

	// 定点数原值，供服务层落库。不进入 JSON，避免前端误用 PPM 标度做计算。
	RawYield    fixed.Ratio `json:"-"`
	RawTDS      fixed.Ratio `json:"-"`
	RawBeverage fixed.Mass  `json:"-"`
	RawRatio    fixed.Ratio `json:"-"`
}

// Engine 是金杯计算引擎。
//
// 它是纯计算组件：不持有数据库连接，不发起 IO。历史样本由调用方查出后传入。
// 这使得引擎的全部行为都可在单元测试中确定性地复现，也是精度回归测试
// 能断言"完全相等"而非"误差小于 epsilon"的前提。
type Engine struct {
	// profiles 允许运行期覆盖出厂标准（Roadmap V-07 配置面板）。
	//
	// 用 atomic.Pointer 而非裸 map：设置面板保存配置时要热更新这张表，
	// 而此刻可能有若干个请求正在 Evaluate 里读它。Go 的 map 不允许
	// 并发读写，一次这样的竞争会直接让整个进程崩掉（fatal error，
	// 连 recover 都拦不住）。原子替换整张表使读侧完全无锁，
	// 且任一次读到的必然是某个完整一致的版本，不会读到半新半旧的组合。
	profiles atomic.Pointer[map[domain.BrewMethod]Profile]
}

// NewEngine 构造引擎。传入 nil 表示全部使用出厂标准。
func NewEngine(overrides map[domain.BrewMethod]Profile) *Engine {
	e := &Engine{}
	e.SetProfiles(overrides)
	return e
}

// SetProfiles 原子替换全部覆盖配置。
//
// 语义是"全量替换"而非"增量合并"：调用方（配置仓储）每次都从数据库
// 读取完整的覆盖集合，增量合并会让一条被删除的覆盖永远留在内存里，
// 于是"恢复出厂标准"这个操作就失效了。
func (e *Engine) SetProfiles(overrides map[domain.BrewMethod]Profile) {
	next := make(map[domain.BrewMethod]Profile, 2)
	for _, p := range DefaultProfiles() {
		next[p.Method] = p
	}
	for m, p := range overrides {
		if err := p.Validate(); err != nil {
			// 非法覆盖被丢弃而非导致启动失败：一个写坏的配置不应让整个服务不可用，
			// 但必须留下痕迹以便排查。
			logger.Warn("金杯标准覆盖配置非法，已回落到出厂标准", "method", string(m), "error", err.Error())
			continue
		}
		next[m] = p
	}
	e.profiles.Store(&next)
}

func (e *Engine) profileMap() map[domain.BrewMethod]Profile {
	if p := e.profiles.Load(); p != nil {
		return *p
	}
	return nil
}

// ProfileFor 返回给定冲煮法当前生效的金杯标准。
func (e *Engine) ProfileFor(m domain.BrewMethod) (Profile, error) {
	if p, ok := e.profileMap()[m]; ok {
		return p, nil
	}
	return DefaultProfile(m)
}

// Profiles 返回当前全部生效标准，供设置页展示。
func (e *Engine) Profiles() []ProfileView {
	current := e.profileMap()
	out := make([]ProfileView, 0, len(current))
	for _, m := range []domain.BrewMethod{domain.MethodFilter, domain.MethodEspresso} {
		if p, ok := current[m]; ok {
			out = append(out, p.View())
		}
	}
	return out
}

// Evaluate 执行一次完整的萃取评估。
//
// samples 是同一支豆、同一冲煮法下的历史测量样本，仅在推算模式下被使用。
// 传入 nil 是合法的：此时推算模式会回落到动力学经验模型并给出低置信度。
func (e *Engine) Evaluate(in Input, samples []Sample) (*Result, error) {
	if !in.Method.Valid() {
		return nil, domain.Validation("UNKNOWN_BREW_METHOD", "冲煮法必须为 FILTER 或 ESPRESSO").
			WithField("method", "非法值")
	}

	p, err := e.ProfileFor(in.Method)
	if err != nil {
		return nil, err
	}

	if err := validatePhysicalInput(in.Dose, in.TotalWater, in.MeasuredBeverage, in.TDS); err != nil {
		return nil, err
	}

	effectiveLRR := in.LRROverride
	if effectiveLRR <= 0 {
		effectiveLRR = p.LRR
	}

	bev, err := BeverageMass(p, in.Dose, in.TotalWater, in.MeasuredBeverage, effectiveLRR)
	if err != nil {
		return nil, err
	}

	ratio, err := BrewRatio(bev, in.Dose)
	if err != nil {
		return nil, err
	}

	res := &Result{
		Method:          in.Method,
		Profile:         p.View(),
		DoseGrams:       in.Dose.ApproxGramsFloat(),
		DoseText:        in.Dose.GramsPrecise(),
		BeverageGrams:   bev.ApproxGramsFloat(),
		BeverageText:    bev.GramsPrecise(),
		TotalWaterGrams: in.TotalWater.ApproxGramsFloat(),
		BrewRatioValue:  ratio.ApproxMultipleFloat(),
		BrewRatioText:   ratio.BrewRatioLabel(),
		RawBeverage:     bev,
		RawRatio:        ratio,
		Warnings:        []string{},
		Advice:          []Advice{},
	}

	if p.UsesLRR && in.MeasuredBeverage <= 0 {
		if absorbed, aerr := fixed.MulMassRatio(in.Dose, effectiveLRR); aerr == nil {
			res.AbsorbedGrams = absorbed.ApproxGramsFloat()
			res.AbsorbedText = absorbed.GramsPrecise()
			res.Warnings = append(res.Warnings,
				"液重由「总注水量 − 粉量 × 持水系数 "+effectiveLRR.Multiple()+
					"」推导得出（约 "+absorbed.GramsPrecise()+"g 水留在粉层与滤纸中）。"+
					"若你能直接称量咖啡液重量，填入实测值可消除持水系数带来的不确定性。")
		}
	}

	var yield, tds fixed.Ratio

	if in.TDS > 0 {
		// ---- 测量模式：萃取率由定义式精确算出 ----
		res.Mode = ModeMeasured
		res.Advisory = false
		tds = in.TDS
		yield, err = ExtractionYield(bev, tds, in.Dose)
		if err != nil {
			return nil, err
		}
	} else {
		// ---- 推算模式：先推断缺失的 TDS，再套用同一条定义式 ----
		res.Mode = ModeEstimated
		res.Advisory = true

		est, eerr := estimate(p, in, bev, ratio, samples)
		if eerr != nil {
			return nil, eerr
		}
		res.Estimation = est
		yield = est.RawYield
		tds = est.RawTDS
		res.Warnings = append(res.Warnings, est.Disclaimer)
	}

	if yield > maxYield {
		// 推算模型或极端输入可能给出物理上不可能的萃取率。截断并明确告知，
		// 而不是输出一个 40% 的数字让用户困惑。
		res.Warnings = append(res.Warnings,
			"计算得到的萃取率 "+yield.Percent()+"% 超过咖啡豆可溶物总量上限 30%，"+
				"已截断至 30%。请检查 TDS 与液重的单位是否填错。")
		yield = maxYield
	}

	res.RawYield = yield
	res.RawTDS = tds
	res.TDSPercent = tds.ApproxPercentFloat()
	res.TDSText = tds.Percent()
	res.YieldPercent = yield.ApproxPercentFloat()
	res.YieldText = yield.Percent()

	// 溶解物绝对质量 = 液重 × TDS。这是个直观量：一杯 250g、1.3% 的手冲
	// 里只有 3.25g 咖啡固体，其余全是水。展示它能帮用户建立浓度的物理直觉。
	if solids, serr := fixed.MulMassRatio(bev, tds); serr == nil {
		res.SolidsGrams = solids.ApproxGramsFloat()
		res.SolidsText = solids.GramsPrecise()
	}

	zone := classify(p, yield, tds)

	if res.Mode == ModeEstimated {
		// 裁定 C-01 的硬性落实：推算模式不得输出"合格"结论。
		// 即使推算值恰好落在金杯区间，InGoldCup 也强制为 false，
		// 并把标签改写为倾向性表述，从数据结构层面杜绝前端误渲染。
		zone.InGoldCup = false
		zone.Label = "推测倾向 · " + zone.Label
		zone.Diagnosis = "【推算结果，非测量】" + zone.Diagnosis +
			" 该结论基于统计推断，不能替代折射仪测量。若要确认是否真正落在金杯区间，请测量 TDS。"
	}
	res.Zone = zone
	res.Advice = buildAdvice(p, zone, in, bev, yield, tds)

	// 粉液比落在参考区间外时给出提示。这不是错误 —— 很多优秀配方刻意偏离
	// 常规比例 —— 但值得让用户知道自己偏离了基准。
	if ratio < p.RatioMin || ratio > p.RatioMax {
		res.Warnings = append(res.Warnings,
			"粉液比 "+ratio.BrewRatioLabel()+" 偏离该冲煮法的参考区间 "+
				p.RatioMin.BrewRatioLabel()+" ~ "+p.RatioMax.BrewRatioLabel()+
				"。这不一定是问题，但会同时影响浓度与萃取率，调参时需一并考虑。")
	}

	return res, nil
}

// Solve 执行三向反解中的一路，由 target 决定解哪个未知量。
func (e *Engine) Solve(req SolveRequest) (*SolveResult, error) {
	p, err := e.ProfileFor(req.Method)
	if err != nil {
		return nil, err
	}

	out := &SolveResult{Target: req.Target, Method: req.Method}

	switch req.Target {
	case SolveTargetTDS:
		v, serr := SolveTDS(req.TargetYield, req.Dose, req.Beverage)
		if serr != nil {
			return nil, serr
		}
		out.ValuePercent = v.ApproxPercentFloat()
		out.ValueText = v.Percent() + "%"
		out.ValueRaw = v.Percent()
		out.Explanation = "要让 " + req.Dose.GramsPrecise() + "g 粉在 " +
			req.Beverage.GramsPrecise() + "g 液体中达到 " + req.TargetYield.Percent() +
			"% 萃取率，折射仪应读到 " + v.Percent() + "%。冲煮过程中测到低于此值说明还需继续萃取。"

	case SolveTargetBeverage:
		v, serr := SolveBeverage(req.TargetYield, req.TDS, req.Dose)
		if serr != nil {
			return nil, serr
		}
		out.ValueGrams = v.ApproxGramsFloat()
		out.ValueText = v.GramsPrecise() + "g"
		out.ValueRaw = v.GramsPrecise()
		out.Explanation = "当前 TDS " + req.TDS.Percent() + "%、粉量 " + req.Dose.GramsPrecise() +
			"g，接到 " + v.GramsPrecise() + "g 液体时萃取率正好达到 " + req.TargetYield.Percent() + "%。"
		if p.UsesLRR {
			if tw, terr := SolveTotalWater(p, v, req.Dose, req.LRROverride); terr == nil {
				out.Explanation += " 换算成总注水量约 " + tw.GramsPrecise() +
					"g（已计入粉层持水 " + p.LRR.Multiple() + " 倍）。"
			}
		}

	case SolveTargetDose:
		v, serr := SolveDose(req.TargetYield, req.TDS, req.Beverage)
		if serr != nil {
			return nil, serr
		}
		out.ValueGrams = v.ApproxGramsFloat()
		out.ValueText = v.GramsPrecise() + "g"
		out.ValueRaw = v.GramsPrecise()
		out.Explanation = "要做一杯 " + req.Beverage.GramsPrecise() + "g、浓度 " +
			req.TDS.Percent() + "% 且萃取率 " + req.TargetYield.Percent() +
			"% 的咖啡，需称取 " + v.GramsPrecise() + "g 粉。"

	case SolveTargetTotalWater:
		v, serr := SolveTotalWater(p, req.Beverage, req.Dose, req.LRROverride)
		if serr != nil {
			return nil, serr
		}
		out.ValueGrams = v.ApproxGramsFloat()
		out.ValueText = v.GramsPrecise() + "g"
		out.ValueRaw = v.GramsPrecise()
		out.Explanation = "要接出 " + req.Beverage.GramsPrecise() + "g 咖啡液，需注入 " +
			v.GramsPrecise() + "g 水，差额是粉层与滤纸的持水量。"

	default:
		return nil, domain.Validation("UNKNOWN_SOLVE_TARGET",
			"反解目标必须为 tds | beverage | dose | total_water")
	}

	return out, nil
}

// SolveTarget 指明反解的未知量。
type SolveTarget string

const (
	SolveTargetTDS        SolveTarget = "tds"
	SolveTargetBeverage   SolveTarget = "beverage"
	SolveTargetDose       SolveTarget = "dose"
	SolveTargetTotalWater SolveTarget = "total_water"
)

// SolveTargets 供前端渲染反解目标选择器，并说明每个目标需要哪些输入。
//
// 把「哪个目标需要填哪几个字段」放在后端，是因为这个依赖关系直接来自
// 公式的代数结构。前端若自己维护一份，公式一旦扩展就会出现
// "表单让你填了但后端不看"或"后端要但表单没有"的错位。
func SolveTargets() []map[string]any {
	return []map[string]any{
		{
			"value":    string(SolveTargetTDS),
			"label":    "反推浓度 TDS",
			"requires": []string{"target_yield_percent", "dose_g", "beverage_g"},
			"hint":     "已知粉量与液重，问要达到目标萃取率需要多少浓度。",
		},
		{
			"value":    string(SolveTargetBeverage),
			"label":    "反推液重",
			"requires": []string{"target_yield_percent", "tds_percent", "dose_g"},
			"hint":     "已知粉量与浓度，问该接出多少咖啡液。",
		},
		{
			"value":    string(SolveTargetDose),
			"label":    "反推粉量",
			"requires": []string{"target_yield_percent", "tds_percent", "beverage_g"},
			"hint":     "已知目标液重与浓度，问该称多少粉。",
		},
		{
			"value":    string(SolveTargetTotalWater),
			"label":    "反推总注水量",
			"requires": []string{"beverage_g", "dose_g"},
			"hint":     "已知目标液重与粉量，问该注入多少水（含粉层持水）。仅手冲适用。",
		},
	}
}

// SolveRequest 是反解请求。
type SolveRequest struct {
	Method      domain.BrewMethod
	Target      SolveTarget
	TargetYield fixed.Ratio
	TDS         fixed.Ratio
	Dose        fixed.Mass
	Beverage    fixed.Mass
	LRROverride fixed.Ratio
}

// SolveResult 是反解结果。
type SolveResult struct {
	Target       SolveTarget       `json:"target"`
	Method       domain.BrewMethod `json:"method"`
	ValueGrams   float64           `json:"value_g"`
	ValuePercent float64           `json:"value_percent"`
	// ValueText 带单位，供直接展示；ValueRaw 是不带单位的纯十进制串，
	// 供前端一键填回表单。两者分开是因为表单字段要的是可再次提交的值，
	// 让前端自己去剥 "g" 后缀，只会在某个单位变了之后悄悄剥错。
	ValueText   string `json:"value_text"`
	ValueRaw    string `json:"value_raw"`
	Explanation string `json:"explanation"`
}
