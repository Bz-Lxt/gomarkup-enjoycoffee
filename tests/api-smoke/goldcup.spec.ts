import { expect, test } from '@playwright/test';

/**
 * 金杯引擎的精度与语义。
 *
 * 断言优先用 *_text 字段：项目的精度契约是「后端定点数算完给字符串，
 * 前端只负责显示」。对着 yield_percent 这类 number 断言等于在测试里
 * 重新引入浮点误差，也测不到用户实际看到的那个值。
 *
 * 注意 *_text 的约定是**不带单位**（yield_text = "17.55"），
 * 单位由前端按字段语义补。唯一的例外是反解结果的 value_text（"3.42g"），
 * 它同时提供不带单位的 value_raw 供回代计算。
 */

// 预览必须挂在一支豆上（偏好曲线与历史落点都按豆聚合），
// 所以先取一个真实 bean_id，而不是写死 1。
let beanID = 0;

test.beforeAll(async ({ request }) => {
  const res = await request.get('/api/v1/beans?page_size=200');
  const { data } = await res.json();
  expect(data.items.length, '种子数据里应至少有一支豆').toBeGreaterThan(0);
  beanID = data.items[0].id;
});

/** 实测模式需要 TDS；缺 TDS 会转入推算模式。 */
function measured(over: Record<string, unknown> = {}) {
  return {
    bean_id: beanID,
    method: 'FILTER',
    dose_g: '20',
    total_water_g: '300',
    beverage_g: '260',
    tds_percent: '1.35',
    ...over,
  };
}

test.describe('实测模式', () => {
  test('教科书例子零误差', async ({ request }) => {
    // 260g 液 × 1.35% / 20g 粉 = 17.55%
    const res = await request.post('/api/v1/brews/preview', { data: measured() });
    expect(res.ok(), await res.text()).toBeTruthy();
    const { data } = await res.json();

    expect(data.mode).toBe('MEASURED');
    expect(data.yield_text).toBe('17.55');
    // 未落进金杯区（低于 18%）应给出可执行的建议，而不是只报一个数
    expect(data.zone.in_gold_cup).toBe(false);
    expect(data.advice.length).toBeGreaterThan(0);
    // 建议必须带理由，否则用户不知道为什么要磨细
    expect(data.advice[0].rationale.length).toBeGreaterThan(10);
  });

  test('三分之一这类无限小数不产生累积误差', async ({ request }) => {
    // 200g × 1.2% / 15g = 16% 整
    const res = await request.post('/api/v1/brews/preview', {
      data: measured({
        dose_g: '15',
        total_water_g: '250',
        beverage_g: '200',
        tds_percent: '1.2',
      }),
    });
    const { data } = await res.json();
    expect(data.yield_text).toBe('16.00');
  });

  test('落在金杯区的参数被判为金杯', async ({ request }) => {
    // 金杯要求两个维度同时达标：萃取率 18–22% 且浓度 1.15–1.35%。
    // 308g × 1.30% / 20g = 20.02% 萃取率，浓度 1.30% ——
    // 两者都在窗口内。只盯萃取率会选出 TDS 超标的参数，那不是金杯。
    const res = await request.post('/api/v1/brews/preview', {
      data: measured({ total_water_g: '348', beverage_g: '308', tds_percent: '1.30' }),
    });
    const { data } = await res.json();
    expect(data.zone.in_gold_cup).toBe(true);
    expect(data.zone.label).toBe('金杯');
    // 落区偏移量供控制图定位，必须同时给数值与显示串
    expect(data.zone.yield_offset_text).toBeTruthy();
  });

  test('缺液重时用 LRR 推导，但仍算实测（TDS 是真的）', async ({ request }) => {
    // 300g 水 - 20g 粉 × LRR 2.0 = 260g 液，与显式给 260 应完全一致
    const withBev = await request.post('/api/v1/brews/preview', { data: measured() });
    const noBev = await request.post('/api/v1/brews/preview', {
      data: measured({ beverage_g: '' }),
    });
    const a = (await withBev.json()).data;
    const b = (await noBev.json()).data;

    expect(b.mode).toBe('MEASURED');
    expect(b.beverage_text).toBe(a.beverage_text);
    expect(b.yield_text).toBe(a.yield_text);
  });
});

test.describe('推算模式', () => {
  /** 不给 TDS 就没有实测依据，引擎必须转入推算并明确标注。 */
  function estimated(over: Record<string, unknown> = {}) {
    return {
      bean_id: beanID,
      method: 'FILTER',
      dose_g: '20',
      total_water_g: '300',
      grind_micron: 750,
      water_temp_c: 92,
      contact_seconds: 150,
      ...over,
    };
  }

  test('没有 TDS 时推算，并标注为不可作为判定依据', async ({ request }) => {
    const res = await request.post('/api/v1/brews/preview', { data: estimated() });
    expect(res.ok(), await res.text()).toBeTruthy();
    const { data } = await res.json();

    expect(data.mode).toBe('ESTIMATED');
    // advisory 驱动前端画虚线框与"推算"角标。漏了它，
    // 推算值会和实测值长得一模一样 —— 这是本功能最容易犯的错。
    expect(data.advisory).toBe(true);
    expect(data.estimation).not.toBeNull();

    const e = data.estimation;
    expect(e.confidence_tier).toMatch(/^(HIGH|MEDIUM|LOW)$/);
    expect(e.yield_range_text).toBeTruthy();
    // 免责声明必须存在且说清"不能替代折射仪"（DesignSpec §5.6）
    expect(e.disclaimer).toContain('折射仪');
    // 推算依据要可追溯，否则用户没有理由相信这个数
    expect(e.basis.length).toBeGreaterThan(0);
  });

  test('推算区间包含中心值且非退化', async ({ request }) => {
    const res = await request.post('/api/v1/brews/preview', { data: estimated() });
    const { data } = await res.json();
    const e = data.estimation;

    expect(e.yield_lower_percent).toBeLessThanOrEqual(e.yield_percent);
    expect(e.yield_percent).toBeLessThanOrEqual(e.yield_upper_percent);
    // 区间宽度为零意味着"推算得毫无不确定性"，那就该叫实测
    expect(e.yield_upper_percent).toBeGreaterThan(e.yield_lower_percent);
  });

  test('样本充足时用历史回归，并说明样本量', async ({ request }) => {
    const res = await request.post('/api/v1/brews/preview', { data: estimated() });
    const e = (await res.json()).data.estimation;
    if (e.estimator === 'HISTORY_REGRESSION') {
      expect(e.sample_size).toBeGreaterThan(0);
      expect(e.estimator_label).toContain('实测');
    } else {
      // 退化到动力学先验时也必须讲清依据，不能假装有历史数据
      expect(e.estimator).toBe('KINETIC_PRIOR');
      expect(e.sample_size).toBe(0);
    }
  });
});

test.describe('三向反解', () => {
  test('反解粉量后正算能回到目标萃取率', async ({ request }) => {
    const solve = await request.post('/api/v1/goldcup/solve', {
      data: {
        method: 'FILTER',
        target: 'dose',
        target_yield_percent: '20',
        tds_percent: '1.25',
        beverage_g: '260',
      },
    });
    expect(solve.ok(), await solve.text()).toBeTruthy();
    const { data } = await solve.json();

    // value_raw 是不带单位的纯小数串，专门用于回代；
    // value_text 带单位只用于显示。混用会让回代解析失败。
    expect(data.value_raw).toBeTruthy();
    expect(data.value_raw).not.toContain('g');
    expect(data.value_text).toContain('g');
    // 解释性文案要能独立读懂，而不是只丢一个数字
    expect(data.explanation).toContain('260');

    const back = await request.post('/api/v1/brews/preview', {
      data: measured({
        dose_g: data.value_raw,
        beverage_g: '260',
        tds_percent: '1.25',
      }),
    });
    const round = (await back.json()).data;
    // 只允许末位舍入带来的偏差
    expect(parseFloat(round.yield_text)).toBeGreaterThan(19.9);
    expect(parseFloat(round.yield_text)).toBeLessThan(20.1);
  });

  // 每个方向的必填字段不同，且 total_water 根本不需要萃取率
  // （它是"液重 + 粉量×LRR"的纯几何换算）。这些必填组合由 /meta 的
  // solve_targets.requires 声明，测试直接读它，而不是在这里抄一份
  // —— 抄一份就会和后端走散，而走散的那天测试会红在一个无关的断言上。
  test('四个反解方向都按 meta 声明的必填字段工作', async ({ request }) => {
    const meta = (await (await request.get('/api/v1/meta')).json()).data;
    const pool: Record<string, string> = {
      target_yield_percent: '20',
      tds_percent: '1.25',
      dose_g: '20',
      beverage_g: '260',
    };

    expect(meta.solve_targets.length).toBe(4);
    for (const t of meta.solve_targets) {
      const data: Record<string, string> = { method: 'FILTER', target: t.value };
      for (const field of t.requires) {
        expect(pool[field], `meta 声明了未知必填字段 ${field}`).toBeTruthy();
        data[field] = pool[field];
      }

      const res = await request.post('/api/v1/goldcup/solve', { data });
      expect(res.ok(), `target=${t.value}: ${await res.text()}`).toBeTruthy();
      const body = (await res.json()).data;
      expect(body.target).toBe(t.value);
      expect(body.value_text, `target=${t.value} 没给出显示串`).toBeTruthy();
      expect(body.explanation, `target=${t.value} 没给出解释`).toBeTruthy();
    }
  });

  test('缺必填字段时明确报错，而不是当成 0 算', async ({ request }) => {
    const res = await request.post('/api/v1/goldcup/solve', {
      data: { method: 'FILTER', target: 'dose', target_yield_percent: '20' },
    });
    // 4xx 都可接受：缺前置条件走 412，格式非法走 400。
    // 关键是别 200 —— 把缺失的 TDS 当 0 会让分母为零或算出无穷大的粉量。
    expect(res.status(), `实际 ${res.status()}`).toBeGreaterThanOrEqual(400);
    expect(res.status()).toBeLessThan(500);
    const body = await res.json();
    expect(body.ok).toBe(false);
    // 报错必须点名缺了什么，否则用户只知道"失败了"
    expect(JSON.stringify(body.error)).toMatch(/TDS|tds/);
  });

  test('物理上不可达的萃取率被拒绝，而不是给一个荒谬的配方', async ({ request }) => {
    // 咖啡豆可溶物总量约 28–30%，95% 萃取率不可能达成。
    // 这条曾经是真实缺陷：反解粉量会自信地回答"称取 3.42g" ——
    // 目标越离谱粉量越小，所以粉量上限那道检查完全拦不住。
    for (const y of ['31', '50', '95']) {
      const res = await request.post('/api/v1/goldcup/solve', {
        data: {
          method: 'FILTER',
          target: 'dose',
          target_yield_percent: y,
          tds_percent: '1.25',
          beverage_g: '260',
        },
      });
      expect(res.status(), `目标萃取率 ${y}% 应被拒绝`).toBe(400);
      const body = await res.json();
      expect(body.error.kind).toBe('VALIDATION');
      // 报错要点明 30% 这条物理上限，让用户意识到自己可能填错了单位
      expect(JSON.stringify(body.error)).toContain('30');
    }
  });

  test('可达区间内的萃取率照常工作', async ({ request }) => {
    // 防止上一条测试靠"全部拒绝"作弊通过
    for (const y of ['14', '20', '26', '30']) {
      const res = await request.post('/api/v1/goldcup/solve', {
        data: {
          method: 'FILTER',
          target: 'dose',
          target_yield_percent: y,
          tds_percent: '1.25',
          beverage_g: '260',
        },
      });
      expect(res.ok(), `目标萃取率 ${y}% 在物理上可达，不该被拒`).toBeTruthy();
    }
  });
});

test.describe('输入校验', () => {
  test('一次返回全部字段错误，而不是逐个报', async ({ request }) => {
    const res = await request.post('/api/v1/brews/preview', {
      data: {
        bean_id: beanID,
        method: 'FILTER',
        dose_g: '-5',
        total_water_g: 'abc',
        tds_percent: '999',
      },
    });
    expect(res.status()).toBe(400);
    const body = await res.json();
    // 逐个报错会让用户改三次才能提交成功
    expect(
      body.error.fields.length,
      `只报了 ${JSON.stringify(body.error.fields)}`,
    ).toBeGreaterThanOrEqual(2);
  });

  test('超出精度的输入被拒绝，而不是静默舍入', async ({ request }) => {
    const res = await request.post('/api/v1/brews/preview', {
      data: measured({ dose_g: '18.1234' }),
    });
    // 静默舍入会让用户以为记录的是 18.1234，实际存的是 18.123
    expect(res.status()).toBe(400);
    const body = await res.json();
    expect(body.error.fields.some((f: { field: string }) => f.field === 'dose_g')).toBe(
      true,
    );
  });

  test('金杯标准区间可读且自洽', async ({ request }) => {
    const res = await request.get('/api/v1/goldcup/profiles');
    const { data } = await res.json();
    expect(data.profiles.length).toBeGreaterThanOrEqual(2);
    for (const p of data.profiles) {
      expect(p.yield_min_percent).toBeLessThan(p.yield_max_percent);
      expect(p.strength_min_percent).toBeLessThan(p.strength_max_percent);
      expect(p.yield_min_text).toBeTruthy();
      // 上界不得超过可溶物总量这条物理天花板
      expect(p.yield_max_percent).toBeLessThanOrEqual(30);
    }
    // 九宫格：三档萃取率 × 三档浓度，其中恰有一个是金杯区
    expect(data.zones.length).toBe(9);
    expect(
      data.zones.filter((z: { in_gold_cup: boolean }) => z.in_gold_cup).length,
    ).toBe(1);
    // 每个格子都要有可读的诊断，否则控制图上的落区没有意义
    for (const z of data.zones) {
      expect(z.diagnosis.length, `落区 ${z.code} 缺诊断文案`).toBeGreaterThan(5);
    }
  });
});
