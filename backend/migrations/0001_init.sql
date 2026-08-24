-- EnjoyCoffee 初始 schema
--
-- 精度约束（Requirements §4.2 与 §5）：所有涉及数值精度的列一律使用 BIGINT
-- 定点整数，全库禁用 DOUBLE PRECISION / REAL。
--   · 质量类：毫克（mg）。18.5g → 18500
--   · 比例类：百万分率（PPM）。1.35% → 13500；粉液比 1:16 → 16000000
--   · 评分类：×10（0–100 表示 0–10.0 分）
--   · 压力类：×10（bar）
--   · 置信度：×1000
--
-- 时区约束：全部时刻列使用 TIMESTAMPTZ，容器统一 TZ=Asia/Shanghai。
-- 日期列（烘焙日、开封日）使用 DATE —— 它们是民用自然日而非时刻，
-- 用 TIMESTAMPTZ 存会引入一个无意义的时刻部分，并让"烘后第几天"的
-- 计算在跨时区读取时产生 ±1 天误差。

-- ---------------------------------------------------------------------------
-- 风味特征树
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS flavor_nodes (
    id          BIGSERIAL PRIMARY KEY,
    parent_id   BIGINT REFERENCES flavor_nodes (id) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    color       TEXT        NOT NULL DEFAULT '#9CA3AF',
    icon        TEXT        NOT NULL DEFAULT '',
    sort_order  INTEGER     NOT NULL DEFAULT 0,
    builtin     BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT flavor_nodes_name_not_blank CHECK (btrim(name) <> ''),
    -- 名称中的斜杠会破坏物化路径的可读性与前端面包屑的拆分
    CONSTRAINT flavor_nodes_name_no_slash CHECK (position('/' in name) = 0)
);

CREATE INDEX IF NOT EXISTS idx_flavor_nodes_parent ON flavor_nodes (parent_id);

-- 同层级下不允许重名。用两个部分索引而非单个唯一约束：
-- PostgreSQL 的唯一约束把 NULL 视为互不相等，因此 (NULL, '柑橘') 可以插入多次，
-- 根层级的重名就漏过去了。必须为根层级单独建一个索引。
CREATE UNIQUE INDEX IF NOT EXISTS uq_flavor_nodes_sibling_name
    ON flavor_nodes (parent_id, name) WHERE parent_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_flavor_nodes_root_name
    ON flavor_nodes (name) WHERE parent_id IS NULL;

-- 闭包表：为每一对「祖先 → 后代」存一行，含自反行（depth = 0）。
--
-- 它的价值在于把"取某节点的全部后代"从递归查询变成单次索引扫描，
-- 耗时与树深无关 —— 这是对"无限级分类"承诺固定延迟的存储层基础
-- （Requirements 裁定 C-03）。内存物化树是它的读侧加速层，
-- 而闭包表保证即使绕过缓存直接查库，语义也完全一致。
CREATE TABLE IF NOT EXISTS flavor_closure (
    ancestor_id   BIGINT  NOT NULL REFERENCES flavor_nodes (id) ON DELETE CASCADE,
    descendant_id BIGINT  NOT NULL REFERENCES flavor_nodes (id) ON DELETE CASCADE,
    depth         INTEGER NOT NULL,
    PRIMARY KEY (ancestor_id, descendant_id),
    CONSTRAINT flavor_closure_depth_nonneg CHECK (depth >= 0)
);

CREATE INDEX IF NOT EXISTS idx_flavor_closure_descendant ON flavor_closure (descendant_id);

-- ---------------------------------------------------------------------------
-- 豆库
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS beans (
    id                BIGSERIAL PRIMARY KEY,
    name              TEXT        NOT NULL,
    roaster           TEXT        NOT NULL DEFAULT '',
    is_blend          BOOLEAN     NOT NULL DEFAULT FALSE,

    country           TEXT        NOT NULL DEFAULT '',
    region            TEXT        NOT NULL DEFAULT '',
    farm              TEXT        NOT NULL DEFAULT '',
    altitude_m        INTEGER     NOT NULL DEFAULT 0,
    process           TEXT        NOT NULL DEFAULT '',
    variety           TEXT        NOT NULL DEFAULT '',

    roast_level       TEXT        NOT NULL,
    roast_note        TEXT        NOT NULL DEFAULT '',
    roasted_on        DATE        NOT NULL,
    opened_on         DATE,

    initial_weight_mg BIGINT      NOT NULL DEFAULT 0,
    remaining_mg      BIGINT      NOT NULL DEFAULT 0,

    notes             TEXT        NOT NULL DEFAULT '',
    archived          BOOLEAN     NOT NULL DEFAULT FALSE,

    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT beans_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT beans_roast_level_valid CHECK (
        roast_level IN ('LIGHT', 'LIGHT_MEDIUM', 'MEDIUM', 'MEDIUM_DARK', 'DARK', 'VERY_DARK')
    ),
    CONSTRAINT beans_altitude_range CHECK (altitude_m >= 0 AND altitude_m <= 4000),
    CONSTRAINT beans_weight_nonneg CHECK (initial_weight_mg >= 0 AND remaining_mg >= 0),
    -- 开封不可能早于烘焙
    CONSTRAINT beans_opened_after_roasted CHECK (opened_on IS NULL OR opened_on >= roasted_on)
);

CREATE INDEX IF NOT EXISTS idx_beans_archived ON beans (archived);
CREATE INDEX IF NOT EXISTS idx_beans_roasted_on ON beans (roasted_on DESC);
CREATE INDEX IF NOT EXISTS idx_beans_roast_level ON beans (roast_level);

CREATE TABLE IF NOT EXISTS bean_flavors (
    bean_id BIGINT NOT NULL REFERENCES beans (id) ON DELETE CASCADE,
    node_id BIGINT NOT NULL REFERENCES flavor_nodes (id) ON DELETE CASCADE,
    PRIMARY KEY (bean_id, node_id)
);

CREATE INDEX IF NOT EXISTS idx_bean_flavors_node ON bean_flavors (node_id);

-- ---------------------------------------------------------------------------
-- 萃取记录
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS brew_sessions (
    id                BIGSERIAL PRIMARY KEY,
    bean_id           BIGINT      NOT NULL REFERENCES beans (id) ON DELETE CASCADE,
    method            TEXT        NOT NULL,
    title             TEXT        NOT NULL DEFAULT '',

    dose_mg           BIGINT      NOT NULL,
    total_water_mg    BIGINT      NOT NULL DEFAULT 0,
    beverage_mg       BIGINT      NOT NULL DEFAULT 0,
    tds_ppm           BIGINT      NOT NULL DEFAULT 0,
    lrr_ppm           BIGINT      NOT NULL DEFAULT 0,

    grinder           TEXT        NOT NULL DEFAULT '',
    grind_setting     TEXT        NOT NULL DEFAULT '',
    grind_micron      INTEGER     NOT NULL DEFAULT 0,

    water_temp_c      INTEGER     NOT NULL DEFAULT 0,
    dripper           TEXT        NOT NULL DEFAULT '',
    agitation_count   INTEGER     NOT NULL DEFAULT 0,

    pre_infusion_sec  INTEGER     NOT NULL DEFAULT 0,
    pressure_bar_x10  INTEGER     NOT NULL DEFAULT 0,
    contact_seconds   INTEGER     NOT NULL DEFAULT 0,

    notes             TEXT        NOT NULL DEFAULT '',
    brewed_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- 引擎结果快照。存下来是为了让历史判定稳定：用户改了金杯标准配置后，
    -- 三个月前那杯咖啡的判定不该悄悄变掉。
    mode              TEXT        NOT NULL DEFAULT 'ESTIMATED',
    yield_ppm         BIGINT      NOT NULL DEFAULT 0,
    tds_calc_ppm      BIGINT      NOT NULL DEFAULT 0,
    ratio_ppm         BIGINT      NOT NULL DEFAULT 0,
    beverage_calc_mg  BIGINT      NOT NULL DEFAULT 0,
    zone_code         TEXT        NOT NULL DEFAULT '',
    in_gold_cup       BOOLEAN     NOT NULL DEFAULT FALSE,
    confidence_x1000  INTEGER     NOT NULL DEFAULT 0,

    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT brew_method_valid CHECK (method IN ('FILTER', 'ESPRESSO')),
    CONSTRAINT brew_mode_valid CHECK (mode IN ('MEASURED', 'ESTIMATED')),
    CONSTRAINT brew_dose_positive CHECK (dose_mg > 0),
    CONSTRAINT brew_masses_nonneg CHECK (
        total_water_mg >= 0 AND beverage_mg >= 0 AND tds_ppm >= 0 AND lrr_ppm >= 0
    ),
    -- TDS 上限 30%（=300000 PPM）：咖啡豆可溶物总量的物理天花板
    CONSTRAINT brew_tds_max CHECK (tds_ppm <= 300000),
    CONSTRAINT brew_temp_range CHECK (water_temp_c = 0 OR (water_temp_c >= 60 AND water_temp_c <= 100)),
    CONSTRAINT brew_grind_range CHECK (grind_micron >= 0 AND grind_micron <= 2000),
    CONSTRAINT brew_confidence_range CHECK (confidence_x1000 >= 0 AND confidence_x1000 <= 1000)
);

CREATE INDEX IF NOT EXISTS idx_brew_bean ON brew_sessions (bean_id);
CREATE INDEX IF NOT EXISTS idx_brew_brewed_at ON brew_sessions (brewed_at DESC);
-- 推算模式的训练集查询走这条索引：按豆 + 冲煮法 + 仅实测
CREATE INDEX IF NOT EXISTS idx_brew_samples ON brew_sessions (bean_id, method, mode);

CREATE TABLE IF NOT EXISTS pour_events (
    id              BIGSERIAL PRIMARY KEY,
    brew_id         BIGINT  NOT NULL REFERENCES brew_sessions (id) ON DELETE CASCADE,
    offset_ms       INTEGER NOT NULL,
    -- 存累计注水量而非单次注入量：智能秤读到的本来就是累计示数，
    -- 且丢失单条事件时累计值不会让后续全部点位错位。
    cumulative_mg   BIGINT  NOT NULL,
    technique       TEXT    NOT NULL DEFAULT 'CIRCLE',
    source          TEXT    NOT NULL DEFAULT 'MANUAL',
    idempotency_key TEXT    NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT pour_offset_range CHECK (offset_ms >= 0 AND offset_ms <= 1800000),
    CONSTRAINT pour_cumulative_nonneg CHECK (cumulative_mg >= 0),
    CONSTRAINT pour_source_valid CHECK (source IN ('MANUAL', 'SIMULATOR', 'DEVICE'))
);

CREATE INDEX IF NOT EXISTS idx_pour_brew_offset ON pour_events (brew_id, offset_ms);

-- 幂等键在同一次冲煮内唯一，支撑 WebSocket 断线重连后的重复推送去重。
-- 空键不参与约束（手动打点不强制携带幂等键）。
CREATE UNIQUE INDEX IF NOT EXISTS uq_pour_idempotency
    ON pour_events (brew_id, idempotency_key) WHERE idempotency_key <> '';

-- ---------------------------------------------------------------------------
-- 六维风味评分
-- ---------------------------------------------------------------------------

-- 评分挂在萃取记录上而非豆子上：同一支豆在不同参数下的风味可以差得像两支豆，
-- 把评分绑在豆子上就抹掉了"参数 → 风味"这个本项目赖以存在的对应关系。
CREATE TABLE IF NOT EXISTS flavor_scores (
    id            BIGSERIAL PRIMARY KEY,
    brew_id       BIGINT      NOT NULL UNIQUE REFERENCES brew_sessions (id) ON DELETE CASCADE,
    bean_id       BIGINT      NOT NULL REFERENCES beans (id) ON DELETE CASCADE,

    acidity_x10   INTEGER     NOT NULL DEFAULT 0,
    sweet_x10     INTEGER     NOT NULL DEFAULT 0,
    aroma_x10     INTEGER     NOT NULL DEFAULT 0,
    aftertone_x10 INTEGER     NOT NULL DEFAULT 0,
    body_x10      INTEGER     NOT NULL DEFAULT 0,
    bitter_x10    INTEGER     NOT NULL DEFAULT 0,

    note          TEXT        NOT NULL DEFAULT '',
    scored_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT score_range CHECK (
        acidity_x10 BETWEEN 0 AND 100 AND sweet_x10 BETWEEN 0 AND 100 AND
        aroma_x10 BETWEEN 0 AND 100 AND aftertone_x10 BETWEEN 0 AND 100 AND
        body_x10 BETWEEN 0 AND 100 AND bitter_x10 BETWEEN 0 AND 100
    ),
    -- 步进 0.5 分：×10 后必须是 5 的倍数，与前端滑块粒度一致
    CONSTRAINT score_step CHECK (
        acidity_x10 % 5 = 0 AND sweet_x10 % 5 = 0 AND aroma_x10 % 5 = 0 AND
        aftertone_x10 % 5 = 0 AND body_x10 % 5 = 0 AND bitter_x10 % 5 = 0
    )
);

CREATE INDEX IF NOT EXISTS idx_flavor_scores_bean ON flavor_scores (bean_id, scored_at DESC);

-- ---------------------------------------------------------------------------
-- 可配置金杯标准
-- ---------------------------------------------------------------------------

-- 店主可按自家出品标准调整区间（Roadmap V-07）。表为空时引擎使用出厂默认值。
CREATE TABLE IF NOT EXISTS brew_method_configs (
    method           TEXT PRIMARY KEY,
    yield_min_ppm    BIGINT      NOT NULL,
    yield_max_ppm    BIGINT      NOT NULL,
    strength_min_ppm BIGINT      NOT NULL,
    strength_max_ppm BIGINT      NOT NULL,
    ratio_min_ppm    BIGINT      NOT NULL,
    ratio_max_ppm    BIGINT      NOT NULL,
    lrr_ppm          BIGINT      NOT NULL DEFAULT 0,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT config_method_valid CHECK (method IN ('FILTER', 'ESPRESSO')),
    CONSTRAINT config_yield_order CHECK (yield_min_ppm > 0 AND yield_min_ppm < yield_max_ppm),
    CONSTRAINT config_strength_order CHECK (strength_min_ppm > 0 AND strength_min_ppm < strength_max_ppm),
    CONSTRAINT config_ratio_order CHECK (ratio_min_ppm > 0 AND ratio_min_ppm < ratio_max_ppm),
    CONSTRAINT config_yield_ceiling CHECK (yield_max_ppm <= 300000)
);

-- ---------------------------------------------------------------------------
-- updated_at 自动维护
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION touch_updated_at() RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['flavor_nodes', 'beans', 'brew_sessions', 'flavor_scores']
    LOOP
        EXECUTE format(
            'DROP TRIGGER IF EXISTS trg_%1$s_touch ON %1$s;
             CREATE TRIGGER trg_%1$s_touch BEFORE UPDATE ON %1$s
             FOR EACH ROW EXECUTE FUNCTION touch_updated_at();', t);
    END LOOP;
END $$;
