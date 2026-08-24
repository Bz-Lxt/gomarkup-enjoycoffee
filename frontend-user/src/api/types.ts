/**
 * 后端契约的 TypeScript 镜像。字段名与 docs/API.md 逐一对应。
 *
 * 数值字段的三种形态（API.md §0.2）在类型上刻意区分：
 *   - 请求体里的数值一律 string
 *   - 响应里的 number 用于画图与比较
 *   - 响应里的 *_text 用于展示
 * 类型不能强制"展示时用 text"，但把两者都摆在这里，
 * 写代码时选错的概率会低一些。
 */

// ---------------------------------------------------------------- 信封

export interface FieldError {
  field: string;
  reason: string;
}

export interface ErrorBody {
  kind: 'VALIDATION' | 'NOT_FOUND' | 'CONFLICT' | 'INTERNAL';
  code: string;
  message: string;
  fields?: FieldError[];
}

export interface Meta {
  total?: number;
  page?: number;
  page_size?: number;
  took_ms?: number;
}

export interface Envelope<T> {
  ok: boolean;
  data?: T;
  error?: ErrorBody;
  warnings?: string[];
  meta?: Meta;
}

// ---------------------------------------------------------------- 枚举与元数据

export type ColorHint = 'green' | 'amber' | 'red' | 'blue' | 'neutral';
export type BrewMethod = 'FILTER' | 'ESPRESSO';
export type FreshnessStage = 'DEGASSING' | 'PEAK' | 'NEAR_EXPIRY' | 'DECLINED';
export type FlavorAxis = 'ACIDITY' | 'SWEET' | 'AROMA' | 'AFTERTONE' | 'BODY' | 'BITTER';
export type MatchMode = 'ALL' | 'ANY';
export type PourSourceMode = 'manual' | 'simulator' | 'device';

export interface EnumOption {
  value: string;
  label: string;
  band?: string;
  color_hint?: ColorHint;
}

export interface LifecycleWindow {
  band: string;
  label: string;
  degassing_days: number;
  peak_end_day: number;
  decline_day: number;
  opened_shelf_life_days: number;
}

export interface SolveTargetMeta {
  value: string;
  label: string;
  /** 该反解目标需要填的字段名，前端据此禁用无关输入 */
  requires: string[];
  hint: string;
}

export interface MetaResponse {
  brew_methods: EnumOption[];
  roast_levels: EnumOption[];
  freshness_stages: EnumOption[];
  pour_techniques: EnumOption[];
  process_methods: string[];
  flavor_axes: FlavorAxis[];
  max_axis_score: number;
  pour_source_mode: PourSourceMode;
  lifecycle_windows: LifecycleWindow[];
  roast_level_bands: { roast_level: string; band: string }[];
  solve_targets: SolveTargetMeta[];
  flavor_match_modes: MatchMode[];
}

// ---------------------------------------------------------------- 雷达

export interface RadarAxisValue {
  axis: FlavorAxis;
  label: string;
  value: number;
  value_text: string;
}

export interface RadarSummary {
  axes: RadarAxisValue[];
  total_score: number;
  max_score: number;
  sample_count: number;
  weighting: string;
  balance: string;
}

// ---------------------------------------------------------------- 豆库

export interface LifecycleSegment {
  stage: FreshnessStage;
  label: string;
  color_hint: ColorHint;
  start_day: number;
  end_day: number;
  width_percent: number;
}

export interface Freshness {
  stage: FreshnessStage;
  stage_label: string;
  color_hint: ColorHint;
  roast_age_days: number;
  /** -1 表示未开封 */
  opened_age_days: number;
  opened: boolean;
  window: LifecycleWindow;
  progress_percent: number;
  peak_progress_percent: number;
  effective_decline_day: number;
  effective_decline_on: string;
  /** ROAST = 烘焙窗口决定；OPENING = 开封氧化提前了衰退 */
  limited_by: 'ROAST' | 'OPENING' | '';
  days_until_next_stage: number;
  next_stage_label: string;
  segments: LifecycleSegment[];
  advice: string;
}

export interface FlavorTag {
  node_id: number;
  name: string;
  path: string;
  color: string;
  icon: string;
  depth: number;
}

export interface BeanView {
  id: number;
  name: string;
  roaster: string;
  is_blend: boolean;
  country: string;
  region: string;
  farm: string;
  altitude_m: number;
  process: string;
  variety: string;
  /** 后端拼好的产地串，前端不要自己拼 */
  origin: string;
  roast_level: string;
  roast_level_label: string;
  roast_note: string;
  roasted_on: string;
  opened_on: string;
  initial_weight_g: number;
  remaining_g: number;
  remaining_text: string;
  remaining_percent: number;
  estimated_brews_left: number;
  notes: string;
  archived: boolean;
  freshness: Freshness;
  flavors: FlavorTag[];
  radar: RadarSummary | null;
  brew_count: number;
  last_brewed_at: string;
  created_at: string;
  updated_at: string;
}

export interface BeanListResponse {
  items: BeanView[];
  total: number;
  flavor_filter: FilterResult | null;
}

export interface BoardGroup {
  stage: FreshnessStage;
  stage_label: string;
  color_hint: ColorHint;
  count: number;
  total_grams: number;
  items: BeanView[];
}

export interface BeanBoard {
  groups: BoardGroup[];
  urgent: BeanView[];
  total_beans: number;
  total_grams: number;
  opened_count: number;
  summary: string;
  generated_at: string;
}

/** 创建/更新豆子的请求体。数值一律字符串（API.md §0.2）。 */
export interface BeanPayload {
  name: string;
  roaster: string;
  is_blend: boolean;
  country: string;
  region: string;
  farm: string;
  altitude_m: number;
  process: string;
  variety: string;
  roast_level: string;
  roast_note: string;
  roasted_on: string;
  opened_on: string;
  initial_weight_g: string;
  remaining_g: string;
  notes: string;
  archived: boolean;
  flavor_node_ids: number[];
}

// ---------------------------------------------------------------- 风味树

export interface FlavorTreeNode {
  id: number;
  parent_id: number | null;
  name: string;
  path: string;
  color: string;
  icon: string;
  depth: number;
  sort_order: number;
  builtin: boolean;
  direct_bean_count: number;
  aggregate_bean_count: number;
  descendant_count: number;
  children: FlavorTreeNode[];
}

export interface FlavorStats {
  node_count: number;
  bean_count: number;
  /** 层数（只有根节点时为 1），不是深度下标 */
  depth_levels: number;
  root_count: number;
  bitset_words_per_node: number;
  approx_memory_kb: number;
  built_at: string;
  depth_warning: string;
  soft_depth_limit: number;
}

export interface FlavorTreeResponse {
  tree: FlavorTreeNode[];
  stats: FlavorStats;
  depth_warning: string;
  built_at: string;
}

export interface AppliedCondition {
  node_id: number;
  name: string;
  path: string;
  depth: number;
  matched_count: number;
  included_descendants: number;
}

export interface Facet {
  node_id: number;
  remaining: number;
}

export interface FilterResult {
  bean_ids: number[];
  matched_count: number;
  total_beans: number;
  match: MatchMode;
  conditions: AppliedCondition[];
  facets: Facet[];
  /** NFR-01 的可观测抓手 */
  elapsed_micros: number;
  warning: string;
  /** 非空说明有筛选条件被丢弃了 —— 必须提示用户 */
  unknown_node_ids: number[];
}

export interface FlavorNodeView {
  id: number;
  parent_id: number | null;
  name: string;
  path: string;
  color: string;
  icon: string;
  depth: number;
  sort_order: number;
  builtin: boolean;
  children: number[];
  ancestors: number[];
  descendant_count: number;
  direct_bean_count: number;
  aggregate_bean_count: number;
}

export interface FlavorSearchHit {
  node: FlavorNodeView;
  ancestors: FlavorNodeView[];
}

export interface DeleteNodeResult {
  deleted_count: number;
  promoted_count: number;
  mode: 'CASCADE' | 'PROMOTE';
  message: string;
}

// ---------------------------------------------------------------- 金杯引擎

export interface ProfileView {
  method: BrewMethod;
  label: string;
  chart_kind: 'SCA_BREWING_CONTROL' | 'ESPRESSO_COMPASS';
  uses_lrr: boolean;
  yield_min_percent: number;
  yield_max_percent: number;
  strength_min_percent: number;
  strength_max_percent: number;
  ratio_min: number;
  ratio_max: number;
  lrr: number;
  yield_min_text: string;
  yield_max_text: string;
  strength_min_text: string;
  strength_max_text: string;
  ratio_min_text: string;
  ratio_max_text: string;
  lrr_text: string;
}

export interface ZoneInfo {
  code: string;
  label: string;
  yield_zone: 'UNDER' | 'IDEAL' | 'OVER';
  strength_zone: 'WEAK' | 'IDEAL' | 'STRONG';
  in_gold_cup: boolean;
  diagnosis: string;
  /** 相对金杯区边界的偏移，正负号有意义 */
  yield_offset_percent: number;
  strength_offset_percent: number;
  yield_offset_text: string;
  strength_offset_text: string;
}

export interface Advice {
  kind: string;
  direction: string;
  headline: string;
  rationale: string;
  target_text: string;
  priority: number;
}

export interface Estimation {
  estimator: 'HISTORY_REGRESSION' | 'KINETIC_PRIOR';
  estimator_label: string;
  sample_size: number;
  confidence: number;
  confidence_tier: 'HIGH' | 'MEDIUM' | 'LOW';
  yield_percent: number;
  yield_lower_percent: number;
  yield_upper_percent: number;
  yield_range_text: string;
  tds_percent: number;
  basis: string[];
  /** 必须展示（DesignSpec §5.6） */
  disclaimer: string;
}

export interface GoldCupResult {
  mode: 'MEASURED' | 'ESTIMATED';
  method: BrewMethod;
  /** true 时必须虚线 + "推算"角标渲染 */
  advisory: boolean;
  profile: ProfileView;
  dose_g: number;
  dose_text: string;
  beverage_g: number;
  beverage_text: string;
  total_water_g: number;
  absorbed_g: number;
  absorbed_text: string;
  brew_ratio: number;
  brew_ratio_text: string;
  tds_percent: number;
  tds_text: string;
  yield_percent: number;
  yield_text: string;
  dissolved_solids_g: number;
  dissolved_solids_text: string;
  zone: ZoneInfo;
  advice: Advice[];
  estimation: Estimation | null;
  warnings: string[];
}

export interface ZoneMeta {
  code: string;
  yield_zone: 'UNDER' | 'IDEAL' | 'OVER';
  strength_zone: 'WEAK' | 'IDEAL' | 'STRONG';
  label: string;
  diagnosis: string;
  in_gold_cup: boolean;
  severity_hue: ColorHint;
}

export interface GoldCupProfilesResponse {
  profiles: ProfileView[];
  zones: ZoneMeta[];
}

export interface ProfilePayload {
  yield_min_percent: string;
  yield_max_percent: string;
  strength_min_percent: string;
  strength_max_percent: string;
  ratio_min: string;
  ratio_max: string;
  lrr: string;
}

export interface Axis {
  min: number;
  max: number;
  ticks: number[];
  label: string;
  unit: string;
}

export interface ZoneRect {
  code: string;
  label: string;
  diagnosis: string;
  severity_hue: ColorHint;
  in_gold_cup: boolean;
  x_min: number;
  x_max: number;
  y_min: number;
  y_max: number;
}

export interface IsoRatioLine {
  ratio: number;
  label: string;
  x1: number;
  y1: number;
  x2: number;
  y2: number;
  emphasize: boolean;
}

export interface ChartPoint {
  brew_id: number;
  label: string;
  yield_percent: number;
  tds_percent: number;
  brew_ratio: number;
  brew_ratio_text: string;
  zone_code: string;
  zone_label: string;
  in_gold_cup: boolean;
  advisory: boolean;
  total_score: number;
  has_score: boolean;
}

export interface PreferencePoint {
  yield_percent: number;
  avg_score: number;
  avg_sweet: number;
  sample_count: number;
  in_gold_cup: boolean;
}

export interface PreferenceCurve {
  /** false 时必须展示 reason，不画编造的曲线 */
  available: boolean;
  reason: string;
  points: PreferencePoint[];
  peak_yield_percent: number;
  peak_score: number;
  peak_label: string;
  delta_from_sca_center: number;
  insight: string;
  basis: string[];
  scored_sample_count: number;
}

export interface GoldCupChart {
  method: BrewMethod;
  chart_kind: 'SCA_BREWING_CONTROL' | 'ESPRESSO_COMPASS';
  title: string;
  axis_x: Axis;
  axis_y: Axis;
  zones: ZoneRect[];
  iso_ratios: IsoRatioLine[];
  points: ChartPoint[];
  preference_curve: PreferenceCurve | null;
}

export interface SolvePayload {
  method: BrewMethod;
  target: string;
  target_yield_percent: string;
  tds_percent: string;
  dose_g: string;
  beverage_g: string;
  lrr_override: string;
}

export interface SolveResult {
  target: string;
  method: BrewMethod;
  value_g: number;
  value_percent: number;
  value_text: string;
  /** 一键填回表单用这个（不带单位） */
  value_raw: string;
  explanation: string;
}

// ---------------------------------------------------------------- 萃取记录

export interface PourPoint {
  offset_ms: number;
  offset_sec: number;
  time_label: string;
  cumulative_g: number;
  flow_rate: number;
  technique: string;
  tech_label: string;
  source: string;
  is_pause: boolean;
}

export interface PourSegment {
  /** 从 1 起，给用户看的段序号。不能当数组下标 */
  ordinal: number;
  from_ms: number;
  to_ms: number;
  duration_sec: number;
  poured_g: number;
  flow_rate: number;
  share_percent: number;
  technique: string;
  tech_label: string;
  is_pause: boolean;
}

export interface PourCurve {
  points: PourPoint[];
  segments: PourSegment[];
  total_water_g: number;
  total_duration_sec: number;
  avg_flow_rate: number;
  peak_flow_rate: number;
  peak_at_sec: number;
  bloom_water_g: number;
  bloom_ratio: number;
  bloom_seconds: number;
  has_bloom: boolean;
  pause_count: number;
  source_summary: string;
  insights: string[];
}

export interface BrewView {
  id: number;
  bean_id: number;
  bean_name: string;
  method: BrewMethod;
  method_label: string;
  title: string;
  dose_g: number;
  dose_text: string;
  total_water_g: number;
  beverage_g: number;
  tds_percent: number;
  has_tds: boolean;
  grinder: string;
  grind_setting: string;
  grind_micron: number;
  water_temp_c: number;
  dripper: string;
  agitation_count: number;
  pre_infusion_sec: number;
  pressure_bar: number;
  contact_seconds: number;
  contact_label: string;
  notes: string;
  brewed_at: string;

  /** 列表接口为省载荷返回 null，详情接口才填充 */
  result: GoldCupResult | null;

  /* 以下摘要字段直接平铺在 View 上（不是嵌套对象），
     让列表页无需 result 就能渲染。 */
  mode: 'MEASURED' | 'ESTIMATED';
  advisory: boolean;
  yield_percent: number;
  yield_text: string;
  calc_tds_percent: number;
  brew_ratio: number;
  brew_ratio_text: string;
  zone_code: string;
  zone_label: string;
  in_gold_cup: boolean;
  confidence: number;

  pour_curve: PourCurve | null;
  radar: RadarSummary | null;
  created_at: string;
}

export interface BrewListResponse {
  items: BrewView[];
  total: number;
}

export interface PourEventPayload {
  offset_ms: number;
  /** 累计示数，不是本次注入量。字符串 */
  cumulative_g: string;
  technique: string;
  idempotency_key: string;
}

export interface BrewPayload {
  bean_id: number;
  method: BrewMethod;
  title: string;
  dose_g: string;
  total_water_g: string;
  beverage_g: string;
  tds_percent: string;
  lrr_override: string;
  grinder: string;
  grind_setting: string;
  grind_micron: number;
  water_temp_c: number;
  dripper: string;
  agitation_count: number;
  pre_infusion_sec: number;
  /** ×10 整数：95 表示 9.5 bar */
  pressure_bar_x10: number;
  contact_seconds: number;
  notes: string;
  brewed_at: string;
  pour_events: PourEventPayload[];
}

export interface AppendPourResponse {
  curve: PourCurve;
  accepted: number;
}

// ---------------------------------------------------------------- 评分

export interface ScorePayload {
  acidity_x10: number;
  sweet_x10: number;
  aroma_x10: number;
  aftertone_x10: number;
  body_x10: number;
  bitter_x10: number;
  note: string;
  scored_at: string;
}

export interface ScoreView {
  id: number;
  brew_id: number;
  bean_id: number;

  /** ×10 整数，滑块与比较用 */
  acidity_x10: number;
  sweet_x10: number;
  aroma_x10: number;
  aftertone_x10: number;
  body_x10: number;
  bitter_x10: number;

  /** 一位小数的展示串，直接渲染 */
  acidity_text: string;
  sweet_text: string;
  aroma_text: string;
  aftertone_text: string;
  body_text: string;
  bitter_text: string;

  total_x10: number;
  total_text: string;

  note: string;
  scored_at: string;
}

export interface ScoreResponse {
  /** null 表示尚未评分（此时 HTTP 仍是 200，渲染"去评分"空态） */
  score: ScoreView | null;
  radar: RadarSummary;
}

export interface RadarLayer {
  bean_id: number;
  name: string;
  origin: string;
  roast_level_label: string;
  radar: RadarSummary;
}

export interface RadarWall {
  layers: RadarLayer[];
  axes: FlavorAxis[];
  max: number;
}

// ---------------------------------------------------------------- WebSocket

export interface WsInbound {
  type: 'hello' | 'curve' | 'pong' | 'sim_state' | 'error';
  mode?: PourSourceMode;
  server_time_ms?: number;
  sim_running?: boolean;
  curve?: PourCurve;
  accepted?: number;
  code?: string;
  message?: string;
}

export interface WsOutbound {
  type: 'mark' | 'ping' | 'sync' | 'sim_start' | 'sim_stop';
  offset_ms?: number;
  cumulative_g?: string;
  technique?: string;
  /** 幂等键。注意 WS 上的字段名是 key，HTTP 上是 idempotency_key */
  key?: string;
}
