package store

import (
	"context"
	"strconv"
	"time"

	"github.com/alkaid/enjoycoffee/internal/bean"
	"github.com/alkaid/enjoycoffee/internal/brew"
	"github.com/alkaid/enjoycoffee/internal/domain"
	"github.com/alkaid/enjoycoffee/internal/fixed"
	"github.com/alkaid/enjoycoffee/internal/flavor"
	"github.com/alkaid/enjoycoffee/internal/flavorscore"
	"github.com/alkaid/enjoycoffee/internal/logger"
)

// Seeder 装配种子数据。
//
// 它刻意经由各领域 Service 而非直接 INSERT：这样种子数据与用户手工录入的
// 数据在结构上完全等价 —— 引擎算过、校验过、闭包表与索引都维护过。
// 直接写 SQL 造种子的常见后果是"演示数据看起来对，但换成用户自己录的就出问题"，
// 因为两条路径的不变量维护得不一样。
type Seeder struct {
	flavorRepo *FlavorRepo
	flavorSvc  *flavor.Service
	beanSvc    *bean.Service
	brewSvc    *brew.Service
	scoreSvc   *flavorscore.Service
}

// NewSeeder 构造种子装配器。
func NewSeeder(
	flavorRepo *FlavorRepo,
	flavorSvc *flavor.Service,
	beanSvc *bean.Service,
	brewSvc *brew.Service,
	scoreSvc *flavorscore.Service,
) *Seeder {
	return &Seeder{
		flavorRepo: flavorRepo,
		flavorSvc:  flavorSvc,
		beanSvc:    beanSvc,
		brewSvc:    brewSvc,
		scoreSvc:   scoreSvc,
	}
}

// seedNode 是种子风味树的声明式节点。
type seedNode struct {
	name     string
	color    string
	icon     string
	children []seedNode
}

// builtinFlavorTree 是内置风味树，从 SCA 咖啡风味轮（Coffee Taster's Flavor Wheel）
// 的一级与二级分类衍生，并按中文咖啡圈的惯用说法本地化。
//
// 为何要预置：一棵空树对新用户毫无帮助 —— 他既不知道该怎么分类，
// 也不知道这个功能能干什么。预置一棵可用的树让"多级联动筛选"从第一分钟
// 就有东西可筛，而用户随时可以改名、移动、增删来把它变成自己的体系。
//
// 深度为 3 级（大类 → 中类 → 具体风味），既足够展示多级联动，
// 又不至于让新用户面对一棵展不开的树。
var builtinFlavorTree = []seedNode{
	{name: "果调", color: "#EF4444", icon: "cherry", children: []seedNode{
		{name: "柑橘", color: "#F97316", children: []seedNode{
			{name: "柠檬"}, {name: "西柚"}, {name: "甜橙"}, {name: "青柠"}, {name: "橘皮"},
		}},
		{name: "浆果", color: "#DC2626", children: []seedNode{
			{name: "蓝莓"}, {name: "草莓"}, {name: "树莓"}, {name: "黑加仑"},
		}},
		{name: "核果", color: "#F472B6", children: []seedNode{
			{name: "蜜桃"}, {name: "杏"}, {name: "樱桃"}, {name: "李子"},
		}},
		{name: "热带水果", color: "#FBBF24", children: []seedNode{
			{name: "芒果"}, {name: "菠萝"}, {name: "百香果"}, {name: "荔枝"}, {name: "番石榴"},
		}},
		{name: "干果", color: "#B45309", children: []seedNode{
			{name: "葡萄干"}, {name: "红枣"}, {name: "无花果"}, {name: "蔓越莓干"},
		}},
	}},
	{name: "花香", color: "#EC4899", icon: "flower", children: []seedNode{
		{name: "茉莉"}, {name: "玫瑰"}, {name: "洋甘菊"}, {name: "橙花"}, {name: "桂花"}, {name: "薰衣草"},
	}},
	{name: "甜感", color: "#F59E0B", icon: "honey", children: []seedNode{
		{name: "糖类", color: "#D97706", children: []seedNode{
			{name: "红糖"}, {name: "焦糖"}, {name: "枫糖"}, {name: "麦芽糖"},
		}},
		{name: "蜂蜜"}, {name: "香草"}, {name: "太妃糖"},
	}},
	{name: "坚果可可", color: "#A16207", icon: "nut", children: []seedNode{
		{name: "坚果", color: "#92400E", children: []seedNode{
			{name: "榛子"}, {name: "杏仁"}, {name: "花生"}, {name: "核桃"}, {name: "腰果"},
		}},
		{name: "可可", color: "#78350F", children: []seedNode{
			{name: "黑巧克力"}, {name: "牛奶巧克力"}, {name: "可可粉"},
		}},
	}},
	{name: "香辛料", color: "#7C3AED", icon: "spice", children: []seedNode{
		{name: "肉桂"}, {name: "丁香"}, {name: "黑胡椒"}, {name: "豆蔻"}, {name: "八角"},
	}},
	{name: "焙烤", color: "#78716C", icon: "roast", children: []seedNode{
		{name: "烤麦"}, {name: "烟熏"}, {name: "焦糊"}, {name: "麦芽"}, {name: "烤面包"},
	}},
	{name: "发酵", color: "#10B981", icon: "ferment", children: []seedNode{
		{name: "酒酿"}, {name: "红酒"}, {name: "威士忌桶"}, {name: "酸奶"}, {name: "熟成水果"},
	}},
	{name: "草本", color: "#22C55E", icon: "herb", children: []seedNode{
		{name: "红茶"}, {name: "绿茶"}, {name: "薄荷"}, {name: "青草"}, {name: "番茄"}, {name: "青椒"},
	}},
	{name: "瑕疵", color: "#6B7280", icon: "warning", children: []seedNode{
		{name: "纸味"}, {name: "土味"}, {name: "药味"}, {name: "霉味"}, {name: "橡胶味"},
	}},
}

// Run 执行种子装配。已有数据时跳过。
//
// 幂等判据是"风味树是否为空"而非某个标记位：若用户删光了豆子但保留了
// 自己整理的风味树，重启时不该把内置树再灌一遍造成重名冲突。
func (s *Seeder) Run(ctx context.Context, withDemoData bool) error {
	nodes, err := s.flavorRepo.ListNodes(ctx)
	if err != nil {
		return err
	}
	if len(nodes) > 0 {
		logger.Debug("风味树已存在，跳过种子装配", "existing_nodes", len(nodes))
		return nil
	}

	logger.Info("正在装配内置风味树…")
	nameToID, err := s.seedFlavorTree(ctx)
	if err != nil {
		return err
	}
	logger.Info("内置风味树装配完成", "nodes", len(nameToID))

	if !withDemoData {
		return nil
	}

	logger.Info("正在装配演示数据…")
	if err := s.seedDemoData(ctx, nameToID); err != nil {
		return err
	}
	logger.Info("演示数据装配完成")
	return nil
}

// seedFlavorTree 逐层插入内置风味树，返回「名称 → 节点 ID」映射。
func (s *Seeder) seedFlavorTree(ctx context.Context) (map[string]int64, error) {
	nameToID := make(map[string]int64, 128)

	var insert func(nodes []seedNode, parentID *int64, inheritColor string) error
	insert = func(nodes []seedNode, parentID *int64, inheritColor string) error {
		for i, n := range nodes {
			color := n.color
			if color == "" {
				// 子节点继承父节点色系，使同一大类在界面上视觉成组
				color = inheritColor
			}
			id, err := s.flavorRepo.CreateNode(ctx, flavor.Node{
				ParentID:  parentID,
				Name:      n.name,
				Color:     color,
				Icon:      n.icon,
				SortOrder: i,
				Builtin:   true,
			})
			if err != nil {
				return err
			}
			nameToID[n.name] = id

			if len(n.children) > 0 {
				childParent := id
				if err := insert(n.children, &childParent, color); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if err := insert(builtinFlavorTree, nil, "#9CA3AF"); err != nil {
		return nil, err
	}

	// 装配完成后重建一次索引，让后续的豆子标记能正确落进位图
	if err := s.flavorSvc.Refresh(ctx); err != nil {
		return nil, err
	}
	return nameToID, nil
}

// seedDemoData 装配演示豆与演示萃取记录。
//
// 演示数据的设计目标不是"填满页面"，而是让每一个核心功能都有可观察的样本：
//   - 四种新鲜度阶段各有一支豆，看板的四种配色都能看到
//   - 一支豆有 5 条带 TDS 与评分的手冲记录，且评分峰值刻意落在 20.75% 而非
//     SCA 中心 20% —— 这样个人偏好曲线一进去就能展示"你的靶心和标准不一样"
//   - 一条记录带完整注水节点序列，流速曲线与断水检测有东西可画
//   - 一条记录刻意不填 TDS，用来展示推算模式与它的低置信度声明
//   - 一支意式拼配豆带浓缩记录，Espresso Compass 图表不是空的
func (s *Seeder) seedDemoData(ctx context.Context, flavors map[string]int64) error {
	today := domain.Now()

	pick := func(names ...string) []int64 {
		out := make([]int64, 0, len(names))
		for _, n := range names {
			if id, ok := flavors[n]; ok {
				out = append(out, id)
			}
		}
		return out
	}
	daysAgo := func(n int) domain.CivilDate {
		return domain.ToCivilDate(today.AddDate(0, 0, -n))
	}
	grams := func(s string) fixed.Mass {
		v, err := fixed.ParseGrams(s)
		if err != nil {
			// 种子里的字面量写错属于源码错误，等同编译期问题，应当立刻暴露
			panic("seed: 非法克数字面量 " + s + ": " + err.Error())
		}
		return v
	}
	percent := func(s string) fixed.Ratio {
		v, err := fixed.ParsePercent(s)
		if err != nil {
			panic("seed: 非法百分数字面量 " + s + ": " + err.Error())
		}
		return v
	}

	// ---- 演示豆 ----

	yega, err := s.beanSvc.Create(ctx, &bean.Bean{
		Name:            "耶加雪菲 孔加 G1 水洗",
		Roaster:         "晨光烘焙工坊",
		Country:         "埃塞俄比亚",
		Region:          "耶加雪菲",
		Farm:            "孔加合作社",
		Altitude:        1950,
		Process:         "水洗",
		Variety:         "原生种 Heirloom",
		RoastLevel:      domain.RoastLight,
		RoastNote:       "一爆结束后 45 秒下豆，发展率 18%",
		RoastedOn:       daysAgo(12),
		OpenedOn:        daysAgo(9),
		InitialWeightMg: grams("250"),
		RemainingMg:     grams("152"),
		Notes:           "这支是目前的主力豆。前两周还有明显的柠檬尖酸，第 10 天开始茉莉香才立起来。",
		FlavorNodeIDs:   pick("柠檬", "茉莉", "蜜桃", "红茶", "蜂蜜"),
	})
	if err != nil {
		return err
	}

	huila, err := s.beanSvc.Create(ctx, &bean.Bean{
		Name:            "哥伦比亚 慧兰 粉红波旁 蜜处理",
		Roaster:         "南山小炉",
		Country:         "哥伦比亚",
		Region:          "慧兰",
		Farm:            "圣塔玛丽亚庄园",
		Altitude:        1750,
		Process:         "蜜处理",
		Variety:         "Pink Bourbon",
		RoastLevel:      domain.RoastMediumDark,
		RoastedOn:       daysAgo(34),
		OpenedOn:        daysAgo(20),
		InitialWeightMg: grams("200"),
		RemainingMg:     grams("48"),
		Notes:           "开封快三周了，红糖甜感还在但花香基本掉完，要赶紧喝掉。",
		FlavorNodeIDs:   pick("红糖", "焦糖", "杏仁", "熟成水果"),
	})
	if err != nil {
		return err
	}

	if _, err := s.beanSvc.Create(ctx, &bean.Bean{
		Name:            "巴西 喜拉多 日晒 深烘",
		Roaster:         "老街咖啡",
		Country:         "巴西",
		Region:          "喜拉多",
		Altitude:        1100,
		Process:         "日晒",
		Variety:         "黄波旁",
		RoastLevel:      domain.RoastDark,
		RoastedOn:       daysAgo(41),
		OpenedOn:        daysAgo(30),
		InitialWeightMg: grams("500"),
		RemainingMg:     grams("85"),
		Notes:           "已经过了衰退期，苦味开始发木。留着做冷萃或者奶咖打底还行，不适合再拿来做参数实验。",
		FlavorNodeIDs:   pick("黑巧克力", "烤麦", "核桃", "烟熏"),
	}); err != nil {
		return err
	}

	if _, err := s.beanSvc.Create(ctx, &bean.Bean{
		Name:            "巴拿马 翡翠庄园 瑰夏 水洗",
		Roaster:         "晨光烘焙工坊",
		Country:         "巴拿马",
		Region:          "波奎特",
		Farm:            "翡翠庄园 Jaramillo",
		Altitude:        1650,
		Process:         "水洗",
		Variety:         "Geisha",
		RoastLevel:      domain.RoastLight,
		RoastNote:       "极浅烘，一爆密集期下豆",
		RoastedOn:       daysAgo(3),
		InitialWeightMg: grams("100"),
		RemainingMg:     grams("100"),
		Notes:           "刚到手三天，还在排气期。这支豆很贵，等养到第 8 天再开封，不想浪费在通道效应上。",
		FlavorNodeIDs:   pick("茉莉", "橙花", "百香果", "蜂蜜", "绿茶"),
	}); err != nil {
		return err
	}

	blend, err := s.beanSvc.Create(ctx, &bean.Bean{
		Name:            "晨间意式拼配 No.3",
		Roaster:         "晨光烘焙工坊",
		IsBlend:         true,
		Country:         "巴西 / 埃塞俄比亚 / 印尼",
		Process:         "日晒 + 水洗 + 湿刨",
		Variety:         "拼配（70% 巴西 / 20% 埃塞 / 10% 曼特宁）",
		RoastLevel:      domain.RoastMediumDark,
		RoastNote:       "意式配方，二爆初下豆",
		RoastedOn:       daysAgo(15),
		OpenedOn:        daysAgo(13),
		InitialWeightMg: grams("454"),
		RemainingMg:     grams("266"),
		Notes:           "做拿铁的基础豆。牛奶巧克力和榛子打底，加奶之后甜感很扎实。",
		FlavorNodeIDs:   pick("牛奶巧克力", "榛子", "红糖", "太妃糖"),
	})
	if err != nil {
		return err
	}

	// ---- 演示手冲记录 ----
	//
	// 五条记录共用同一组基准参数（18g 粉、324g 水、1:16 粉液比），
	// 只让研磨度与水温变化，从而让 TDS 与萃取率呈现一条清晰的单调序列。
	// 这样偏好曲线的横轴分布是均匀的，峰值位置有意义。
	//
	// 参数与结果的对应关系（LRR 2.0，液重 = 324 − 18×2 = 288g）：
	//   萃取率 = 288 × TDS ÷ 18 = 16 × TDS
	type filterSeed struct {
		daysAgo   int
		title     string
		tds       string
		micron    int
		temp      int
		contact   int
		agitation int
		notes     string
		scores    [6]int // 酸 甜 香 余韵 醇厚 苦，均为 ×10
	}

	filterSeeds := []filterSeed{
		{
			daysAgo: 9, title: "第一次尝试 · 磨太粗了", tds: "1.11",
			micron: 850, temp: 90, contact: 155, agitation: 1,
			notes:  "尖酸很明显，甜感几乎没出来，余韵短。喝完舌头两侧发涩。",
			scores: [6]int{85, 40, 60, 45, 45, 30},
		},
		{
			daysAgo: 8, title: "磨细两档", tds: "1.20",
			micron: 780, temp: 92, contact: 175, agitation: 2,
			notes:  "酸质柔和了一些，开始能喝到蜜桃。还差一点甜。",
			scores: [6]int{75, 65, 70, 60, 60, 35},
		},
		{
			daysAgo: 6, title: "再细一档 + 提温", tds: "1.25",
			micron: 740, temp: 93, contact: 190, agitation: 2,
			notes:  "平衡不错，茉莉香开始明显。这一杯已经很能喝了。",
			scores: [6]int{70, 75, 75, 70, 65, 35},
		},
		{
			daysAgo: 3, title: "目前最好的一杯", tds: "1.30",
			micron: 700, temp: 94, contact: 205, agitation: 3,
			notes:  "蜂蜜甜感很足，茉莉和红茶的余韵拖得很长。这组参数记下来。",
			scores: [6]int{65, 85, 80, 80, 70, 40},
		},
		{
			daysAgo: 1, title: "试试再萃透一点", tds: "1.35",
			micron: 660, temp: 94, contact: 225, agitation: 3,
			notes:  "醇厚度上来了但酸质变钝，尾段开始有一点干苦。萃过头了一点点。",
			scores: [6]int{55, 70, 70, 65, 75, 60},
		},
	}

	for i, fs := range filterSeeds {
		b := &brew.Brew{
			BeanID:         yega.ID,
			Method:         domain.MethodFilter,
			Title:          fs.title,
			DoseMg:         grams("18"),
			TotalWaterMg:   grams("324"),
			TDS:            percent(fs.tds),
			Grinder:        "Comandante C40 MK4",
			GrindSetting:   "外圈 " + strconv.Itoa(fs.micron/30) + " 格",
			GrindMicron:    fs.micron,
			WaterTempC:     fs.temp,
			Dripper:        "Hario V60 02 陶瓷",
			AgitationCount: fs.agitation,
			ContactSeconds: fs.contact,
			Notes:          fs.notes,
			BrewedAt:       today.AddDate(0, 0, -fs.daysAgo).Add(time.Duration(-i) * time.Hour),
		}

		// 最好的那一杯附带完整注水节点，让流速曲线有真实数据可画
		if fs.daysAgo == 3 {
			b.PourEvents = demoPourEvents()
		}

		created, _, err := s.brewSvc.Create(ctx, b)
		if err != nil {
			return err
		}

		if _, err := s.scoreSvc.Save(ctx, &flavorscore.Score{
			BrewID:       created.ID,
			AcidityX10:   fs.scores[0],
			SweetX10:     fs.scores[1],
			AromaX10:     fs.scores[2],
			AftertoneX10: fs.scores[3],
			BodyX10:      fs.scores[4],
			BitterX10:    fs.scores[5],
			Note:         fs.notes,
			ScoredAt:     b.BrewedAt.Add(20 * time.Minute),
		}); err != nil {
			return err
		}
	}

	// ---- 推算模式演示：刻意不填 TDS ----
	//
	// 这条记录挂在慧兰豆上而非耶加豆上，是为了让它走「无历史样本 → 动力学先验」
	// 那条分支，从而展示最低置信度的输出形态。若挂在已有 5 条实测记录的耶加豆上，
	// 它会走回归分支，反而看不到"数据不足"的诚实声明。
	if _, _, err := s.brewSvc.Create(ctx, &brew.Brew{
		BeanID:         huila.ID,
		Method:         domain.MethodFilter,
		Title:          "没带折射仪 · 只能推算",
		DoseMg:         grams("20"),
		TotalWaterMg:   grams("340"),
		Grinder:        "1Zpresso JX-Pro",
		GrindSetting:   "2 圈 4 格",
		GrindMicron:    760,
		WaterTempC:     92,
		Dripper:        "Kalita Wave 185",
		AgitationCount: 2,
		ContactSeconds: 195,
		Notes:          "出门带的手冲套装没装折射仪。红糖甜感很足，酸偏低，主观感觉萃得挺透。",
		BrewedAt:       today.AddDate(0, 0, -5),
	}); err != nil {
		return err
	}

	// ---- 意式记录 ----
	espressoSeeds := []struct {
		daysAgo  int
		title    string
		dose     string
		beverage string
		tds      string
		micron   int
		preInf   int
		pressure int
		contact  int
		notes    string
		scores   [6]int
	}{
		{
			daysAgo: 4, title: "早晨第一杯 · 1:2 标准", dose: "18", beverage: "36",
			tds: "9.5", micron: 250, preInf: 6, pressure: 90, contact: 28,
			notes:  "牛奶巧克力很扎实，Crema 厚。加奶做拿铁刚好。",
			scores: [6]int{50, 75, 65, 70, 85, 45},
		},
		{
			daysAgo: 2, title: "磨细试 ristretto", dose: "18", beverage: "27",
			tds: "11.5", micron: 235, preInf: 8, pressure: 90, contact: 32,
			notes:  "浓得多，甜感更集中，但尾段有一点点涩。单喝偏重。",
			scores: [6]int{45, 70, 60, 60, 90, 60},
		},
	}

	for _, es := range espressoSeeds {
		created, _, err := s.brewSvc.Create(ctx, &brew.Brew{
			BeanID:         blend.ID,
			Method:         domain.MethodEspresso,
			Title:          es.title,
			DoseMg:         grams(es.dose),
			BeverageMg:     grams(es.beverage),
			TDS:            percent(es.tds),
			Grinder:        "Eureka Mignon Specialita",
			GrindSetting:   "刻度 " + strconv.Itoa(es.micron/50),
			GrindMicron:    es.micron,
			WaterTempC:     93,
			PreInfusionSec: es.preInf,
			PressureBarX10: es.pressure,
			ContactSeconds: es.contact,
			Notes:          es.notes,
			BrewedAt:       today.AddDate(0, 0, -es.daysAgo),
		})
		if err != nil {
			return err
		}
		if _, err := s.scoreSvc.Save(ctx, &flavorscore.Score{
			BrewID:       created.ID,
			AcidityX10:   es.scores[0],
			SweetX10:     es.scores[1],
			AromaX10:     es.scores[2],
			AftertoneX10: es.scores[3],
			BodyX10:      es.scores[4],
			BitterX10:    es.scores[5],
			Note:         es.notes,
			ScoredAt:     today.AddDate(0, 0, -es.daysAgo).Add(15 * time.Minute),
		}); err != nil {
			return err
		}
	}

	return nil
}

// demoPourEvents 构造一条真实的三段式断水注水序列。
//
// 序列描述的是一次典型的四六冲法变体：闷蒸 45g（粉量 2.5 倍）静置 37 秒，
// 之后分三段注水，每段之间断水等下水。累计值单调递增，末端 324g 与
// 记录的总注水量一致 —— 这个一致性很重要，否则流速曲线与萃取率会各说各话。
func demoPourEvents() []brew.PourEvent {
	mg := func(g int) fixed.Mass { return fixed.Mass(g * 1000) }

	return []brew.PourEvent{
		{OffsetMs: 8000, CumulativeMg: mg(45), Technique: domain.PourBloom, Source: brew.SourceManual, IdempotencyKey: "demo-1"},
		{OffsetMs: 45000, CumulativeMg: mg(46), Technique: domain.PourBloom, Source: brew.SourceManual, IdempotencyKey: "demo-2"},
		{OffsetMs: 70000, CumulativeMg: mg(160), Technique: domain.PourCircle, Source: brew.SourceManual, IdempotencyKey: "demo-3"},
		{OffsetMs: 95000, CumulativeMg: mg(162), Technique: domain.PourPulse, Source: brew.SourceManual, IdempotencyKey: "demo-4"},
		{OffsetMs: 120000, CumulativeMg: mg(250), Technique: domain.PourSpiral, Source: brew.SourceManual, IdempotencyKey: "demo-5"},
		{OffsetMs: 145000, CumulativeMg: mg(252), Technique: domain.PourPulse, Source: brew.SourceManual, IdempotencyKey: "demo-6"},
		{OffsetMs: 165000, CumulativeMg: mg(324), Technique: domain.PourCircle, Source: brew.SourceManual, IdempotencyKey: "demo-7"},
		{OffsetMs: 205000, CumulativeMg: mg(324), Technique: domain.PourDrawoff, Source: brew.SourceManual, IdempotencyKey: "demo-8"},
	}
}
