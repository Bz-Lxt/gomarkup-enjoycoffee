# Mini 咖啡大师

面向咖啡发烧友与独立咖啡店主的咖啡豆库管理与萃取参数沙盘。
后端手写 SCA 金杯萃取率引擎（定点数运算，规避浮点误差），
前端用 Canvas 绘制注水流速曲线与六维风味雷达图。

## 1. 如何启动

```bash
docker compose up --build -d
```

首次构建约 30 秒达到全部 healthy。启动后打开 <http://localhost:31411>。

不需要任何手动准备步骤：数据库迁移与演示数据（5 支咖啡豆、8 条冲煮记录、
86 个风味分类节点）在后端启动时自动导入。

停止与清理：

```bash
docker compose down      # 停止，保留数据
docker compose down -v   # 停止并清空数据库卷（下次启动重新导入演示数据）
```

## 2. 使用说明

五个页面对应五件事：

**豆库看板**（`/`）
按新鲜度分组展示所有咖啡豆。每支豆下方的彩色进度条是生命周期轴 ——
排气期、最佳风味期、风味衰退期各占一段，当前位置由烘焙日与开封日
按 GMT+8 民用日历天数算出。卡片上还有余量、还能冲几次、已冲几次。
左侧可按风味标签多级联动筛选，也可搜索豆名/烘焙商/产地。

**萃取沙盘**（`/brew`）
先选一支豆（金杯落区与偏好曲线是按豆聚合的），再填粉量、总注水量、
液重、浓度 TDS 等参数，右侧读数即时更新：萃取率、浓度、落在九宫格
哪一格、是否进入金杯区，以及一句改进建议。

- **没有折射仪**：把「浓度 TDS」留空，系统转入**推算模式**。
  推算出的每个读数都会带「推算」角标并附免责声明 ——
  推算值不能当实测值用来做判断。
- **反解**：想反过来问「要达到 20% 萃取率该放多少粉」，
  用反解功能，四个方向（粉量/液重/浓度/总注水量）都支持。
  物理上不可达的目标（例如 95% 萃取率）会被明确拒绝而不是给个荒谬答案。
- **注水打点**：点「开始计时」后每次注水按一下「打点」，
  或填入电子秤累计读数。曲线、平均流速、闷蒸判定由后端算出。
  也可以启动内置模拟器代替智能秤（见 §7）。
- 填完点「记录这次冲煮」存档，此后可继续补打注水节点。

**风味雷达墙**（`/radar`）
最多同时叠加 6 支豆的六维风味（酸、甜、香气、余韵、醇厚、苦），
用于横向对比。每支豆的分数是它历次评分的加权平均，近期评分权重更高。

**风味树**（`/flavors`）
自定义风味分类，层级不限（柑橘 → 柠檬/西柚）。可增删改、移动节点。
删除有子节点的分类时会问你是「连子类一起删」还是「把子类提升一级」，
并说明影响到几个节点、几支豆。右侧「索引状态」显示节点数、层数与
位图内存占用。

**设置**（`/settings`）
修改手冲与意式各自的出品标准（萃取率区间、浓度区间、粉液比、持水系数）。
改完立刻影响全站的落区判定与控制图刻度，不只是存起来看。
可一键恢复出厂默认。

## 3. 服务列表及 API 说明

| 服务 | 地址 | 说明 |
|---|---|---|
| 前端 | <http://localhost:31411> | Nginx 托管静态资源，并同源反代 `/api` 与 `/api/v1/ws` |
| 后端 | <http://localhost:31410> | Go HTTP API（也可直连调试） |
| 数据库 | `localhost:31412` | PostgreSQL 16，库 `enjoycoffee`，用户 `coffee` |

前端与后端**同源**：浏览器只访问 31411，API 与 WebSocket 由 Nginx 转发到
后端。所以镜像里没有烘焙任何主机地址，换机器换端口都不用重新构建。

主要接口（完整契约见 `docs/API.md`，含全部字段与错误码）：

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/api/v1/health` `/api/v1/ready` | 存活 / 就绪探针 |
| GET | `/api/v1/meta` | 全部枚举、标签、九宫格图例、反解方向声明 |
| GET/POST | `/api/v1/beans` | 豆库列表（支持风味筛选、分页）/ 新建 |
| GET/PUT/DELETE | `/api/v1/beans/{id}` | 单支豆读写 |
| GET | `/api/v1/beans/board` | 豆库看板（含生命周期几何） |
| POST | `/api/v1/beans/{id}/consume` | 扣减余量 |
| PUT | `/api/v1/beans/{id}/flavors` | 设置风味标签 |
| GET/POST | `/api/v1/brews` | 冲煮记录列表 / 新建 |
| POST | `/api/v1/brews/{id}/pour` | 追加注水节点（幂等键去重） |
| GET/PUT/DELETE | `/api/v1/brews/{id}/score` | 六维风味评分 |
| POST | `/api/v1/goldcup/preview` | 金杯试算（不落库） |
| POST | `/api/v1/goldcup/solve` | 四向反解 |
| GET | `/api/v1/goldcup/chart` | 金杯控制图（九宫格 + 等比线 + 历史落点） |
| GET/PUT/DELETE | `/api/v1/goldcup/profiles[/{method}]` | 出品标准读写 / 恢复默认 |
| GET | `/api/v1/flavors/tree` | 完整风味树 |
| GET | `/api/v1/flavors/filter` | 多级联动筛选（自报耗时 `elapsed_micros`） |
| POST/PATCH/DELETE | `/api/v1/flavors/nodes[/{id}]` | 节点增删改 |
| POST | `/api/v1/flavors/nodes/{id}/move` | 移动节点（含环检测） |
| GET | `/api/v1/radar/wall` | 多豆雷达聚合 |
| WS | `/api/v1/ws/brews/{id}/pour` | 实时注水通道 |

两条设计约定值得先知道，能少踩坑：

- **未知字段一律拒绝**。查询参数和请求体都是白名单，写错名字会返回
  400 并提示正确的那个（把 `node_ids` 写成筛选参数会提示 `flavor_ids`）。
  这是刻意的：静默忽略会让调用方以为筛选生效了，实际条件被丢掉。
- **数值走字符串**。粉量、注水量、浓度等在 JSON 里是字符串而非 number，
  避免 IEEE754 破坏定点数精度。展示串（`*_text`）由后端算好下发，
  前端不做单位换算或除法。

## 4. 测试账号

**本项目没有登录，也不需要账号。** 打开 <http://localhost:31411> 直接可用。

这是刻意的：需求描述的是单人使用的本地工具（发烧友管理自己的豆库），
引入账号体系属于需求外的功能。数据库凭据仅用于容器间连接，
在 `docker-compose.yml` 中明文可见（`coffee` / `coffee_dev_pw`），
不作为对外登录入口。

## 5. 题目内容

原始需求逐字留档在 `docs/.meta/original_prompt.md`。摘要：

> 使用 Go 语言实现咖啡豆库管理、手冲/意式萃取参数记录与金杯曲线推导。
> 前端 React + Canvas 动态折线图 + 六维风味雷达图，含萃取参数动态记录盘
> （秒表与水流曲线，WebSocket 实时记录或手动录入）、风味雷达墙（六维、
> 多豆重叠对比）、豆库临期与排气期看板（彩色进度条）。
> 后端手写 SCA 金杯萃取率计算引擎（规避浮点数精度问题，判定 18%–22%
> 理想区间）与风味特征树无限级分类（多级联动筛选响应 10ms 以内）。

## 6. 项目结构

```
.
├── backend/                    Go 后端（50 个非测试源文件 + 21 个测试文件）
│   ├── cmd/server/             入口：装配依赖、优雅停机
│   ├── migrations/             SQL 迁移与演示数据
│   └── internal/
│       ├── fixed/              定点数：big.Rat + PPM/毫克定标、银行家舍入
│       ├── goldcup/            金杯引擎：SCA 公式、双模式、落区、反解、控制图
│       ├── flavor/             风味树：闭包表 + 内存物化快照 + 位图倒排索引
│       ├── bean/               豆库领域：生命周期（GMT+8 民用日）、余量
│       ├── brew/               冲煮领域：注水曲线、流速、闷蒸判定
│       ├── flavorscore/        六维评分与雷达聚合
│       ├── api/                HTTP 处理器与路由（含查询参数白名单）
│       ├── ws/                 WebSocket Hub 与注水模拟器
│       ├── store/              PostgreSQL 仓储
│       ├── domain/             领域错误、时间与格式化
│       ├── httpx/              响应信封、严格解码、超时中间件
│       ├── validate/           收集式字段校验
│       ├── config/             环境变量配置与自检
│       └── logger/             结构化日志
├── frontend-user/              React + TypeScript + Tailwind（32 个源文件）
│   ├── nginx.conf              静态托管 + /api 与 WS 同源反代
│   └── src/
│       ├── api/                契约类型与 fetch 封装
│       ├── components/         UI 基础组件与三个手写 Canvas 图表
│       ├── pages/              五个业务页面
│       └── lib/                WebSocket、秒表等 hooks
├── tests/                      Playwright（容器内执行）
│   ├── api-smoke/              接口契约、金杯精度、写入路径全生命周期
│   ├── e2e/                    五页交互、实时注水通道
│   └── merge_coverage.py       合并单测与端到端插桩两份覆盖率剖面
├── docs/                       需求、路线图、契约、设计规范、QA 与审核记录
├── docker-compose.yml          主编排
└── docker-compose.cover.yml    覆盖率插桩用的覆盖层
```

跑测试（会自动拉起依赖，全程不产生任何外部费用）：

```bash
docker compose --profile qa run --rm qa npx playwright test
```

## 7. API 模拟与切换指南

本项目**不调用任何外部或计费 API**，全部数值由本地手写公式算出。
唯一的模拟对象是**智能电子秤**：真实场景下由蓝牙秤按秒推送累计注水量，
开发与演示环境没有这台硬件，因此内置了一个流速模拟器。

由环境变量 `POUR_SOURCE_MODE` 切换，三个取值：

| 取值 | 行为 | 用途 |
|---|---|---|
| `manual` | 只接受用户手动打点 | 没有智能秤，手动记录注水节点 |
| `simulator`（**当前默认**） | 启用内置模拟器，按四六冲法推送 195 秒完整流程 | 演示与测试 |
| `device` | 外部设备按公开协议直接推流，不启动模拟器 | **接入真实智能秤** |

切换方式 —— 改 `docker-compose.yml` 的 `backend.environment`：

```yaml
    environment:
      POUR_SOURCE_MODE: "device"   # 由 simulator 改为 device
```

然后 `docker compose up -d backend` 重启后端即可。

**真实实现通路是通的，不是留了个空壳。** `device` 模式下设备连上
`ws://<host>/api/v1/ws/brews/{brewID}/pour` 后，按以下格式推送即可：

```json
{ "type": "mark", "offset_ms": 30000, "cumulative_g": "120.5",
  "technique": "CIRCLE", "key": "设备侧唯一幂等键" }
```

- `cumulative_g` 是**累计**注水量（不是本次增量），走字符串保精度。
- `key` 是幂等键：弱网重连后设备可以放心重推它认为可能没送达的点，
  服务端按此去重，曲线上不会出现重复台阶。
- 服务端回 `{"type":"curve","curve":{...},"accepted":N}`，
  `accepted` 为本批实际新增的节点数。

模拟器与真实设备走的是**同一条服务方法**，因此幂等合并、流速推导、
曲线重算的行为完全一致 —— 换成真秤不会碰到「模拟能跑真机不行」的落差。
两种来源在数据库里被分别标记为 `SIMULATOR` 与 `DEVICE`，
手动打点标记为 `MANUAL`，数据溯源不会混。

模拟器只在 `simulator` 模式下可用：其它模式下发起 `sim_start`
会收到 `SIMULATOR_DISABLED` 错误并附带切换说明，不会静默失败。
