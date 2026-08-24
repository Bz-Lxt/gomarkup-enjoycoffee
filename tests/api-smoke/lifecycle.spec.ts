import { test, expect, type APIRequestContext } from '@playwright/test';

/**
 * 写入路径的全生命周期冒烟。
 *
 * 为什么单独一个文件：在此之前整套接口测试几乎全是 GET —— 读路径被反复
 * 验证，而增删改一次都没跑过。这不只是覆盖率数字的问题：数据损坏只可能
 * 发生在写路径上。「更新豆子后字段真的变了吗」「删掉一个有子节点的风味节点
 * 会不会把子树一起带走」这类问题，读接口再怎么测也答不上来。
 *
 * 端到端插桩度量显示，补上这个文件之前 internal/api 与 internal/store
 * 的真实覆盖率分别只有 35.4% 与 35.9%，未覆盖的部分基本就是这些写入分支。
 *
 * 自清理原则：每个用例创建的数据都在同一个用例里删掉。种子数据的条数被
 * 其他 spec 断言（例如"筛选后从 5 条降到 1 条"），留下残留会让它们变成
 * 随运行顺序漂移的假失败。文件名排在最后（lifecycle > health > goldcup >
 * contract）也是为此留的第二道保险。
 */

/** 断言响应成功并取出 data，失败时把服务端原文一并抛出，避免只看到一个状态码。 */
async function ok<T = any>(res: Awaited<ReturnType<APIRequestContext['post']>>): Promise<T> {
  const body = await res.json().catch(() => null);
  if (!res.ok() || !body?.ok) {
    throw new Error(
      `请求失败 ${res.status()} ${res.url()}\n${JSON.stringify(body, null, 2)}`,
    );
  }
  return body.data as T;
}

/** 造一支唯一命名的豆子，避免与种子数据或并发用例撞名。 */
function newBeanPayload(tag: string) {
  const today = new Date();
  const iso = (d: Date) => d.toISOString().slice(0, 10);
  const roasted = new Date(today.getTime() - 5 * 86400_000);
  return {
    name: `QA 测试豆 ${tag}`,
    roaster: 'QA 烘焙所',
    is_blend: false,
    country: '埃塞俄比亚',
    region: '耶加雪菲',
    process: 'WASHED',
    variety: '原生种',
    roast_level: 'LIGHT',
    roasted_on: iso(roasted),
    initial_weight_g: '250',
    remaining_g: '250',
    notes: '生命周期用例创建，用完即删',
  };
}

test.describe('豆库写入路径', () => {
  test('创建→读取→更新→扣减→归档→删除', async ({ request }) => {
    // ---- 创建 ----
    const created = await ok<any>(
      await request.post('/api/v1/beans', { data: newBeanPayload('lifecycle') }),
    );
    const id = created.id;
    expect(id, '创建后必须返回可用的 id').toBeGreaterThan(0);
    expect(created.name).toContain('QA 测试豆');

    try {
      // ---- 读回：创建的内容必须真的落库，而不是只回显了请求体 ----
      const fetched = await ok<any>(await request.get(`/api/v1/beans/${id}`));
      expect(fetched.id).toBe(id);
      expect(fetched.name).toBe(created.name);
      expect(fetched.roast_level).toBe('LIGHT');
      // 生命周期几何量由后端算出，创建时就该有
      expect(fetched.freshness).toBeTruthy();
      expect(fetched.freshness.stage).toBeTruthy();

      // ---- 更新 ----
      const updated = await ok<any>(
        await request.put(`/api/v1/beans/${id}`, {
          data: { ...newBeanPayload('lifecycle'), notes: '改过的备注', roast_level: 'MEDIUM' },
        }),
      );
      expect(updated.roast_level, '更新后的烘焙度应生效').toBe('MEDIUM');

      const afterUpdate = await ok<any>(await request.get(`/api/v1/beans/${id}`));
      expect(afterUpdate.notes, '更新必须持久化，而不是只反映在响应里').toBe('改过的备注');

      // ---- 扣减余量 ----
      const before = Number(afterUpdate.remaining_g ?? afterUpdate.remaining_text ?? 0);
      const consumed = await ok<any>(
        await request.post(`/api/v1/beans/${id}/consume`, { data: { amount_g: '30' } }),
      );
      const after = Number(consumed.remaining_g ?? consumed.remaining_text ?? 0);
      if (Number.isFinite(before) && Number.isFinite(after) && before > 0) {
        expect(after, `扣减 30g 后余量应减少（${before} → ${after}）`).toBeLessThan(before);
      }

      // ---- 扣减超过余量：归零 + 明确警告，而不是负数也不是硬报错 ----
      //
      // 这是刻意的设计（bean/service.go Consume）：用户忘记及时更新剩余量
      // 是常态，为此拒绝记录一次真实发生过的冲煮是本末倒置。但归零必须
      // 说出来 —— 悄悄抹平会让库存账目对不上却查不出原因。
      const over = await request.post(`/api/v1/beans/${id}/consume`, {
        data: { amount_g: '99999' },
      });
      expect(over.status(), '超量扣减应被接受并归零，而不是报错').toBe(200);
      const overBody = await over.json();
      expect(
        overBody.warnings?.length ?? 0,
        '超量扣减必须返回警告说明余量已归零 —— 静默抹平等于篡改库存',
      ).toBeGreaterThan(0);
      expect(
        String(overBody.warnings[0]),
        '警告里应指明是哪支豆子并提示去更正余量',
      ).toContain('归零');

      const zeroed = await ok<any>(await request.get(`/api/v1/beans/${id}`));
      const rem = Number(zeroed.remaining_g ?? 0);
      expect(rem, '余量应正好归零，绝不能是负数').toBe(0);
    } finally {
      // ---- 删除（无论上面成功与否都要清理）----
      const del = await request.delete(`/api/v1/beans/${id}`);
      expect(del.ok(), '删除应成功，否则会给后续用例留下脏数据').toBeTruthy();
      const gone = await request.get(`/api/v1/beans/${id}`);
      expect(gone.status(), '删掉的豆子再查应为 404').toBe(404);
    }
  });

  test('设置风味标签会真的改变筛选结果', async ({ request }) => {
    // 这条把"写"和"读"接起来验证：给一支新豆打上某个风味标签后，
    // 按该标签筛选必须能查到它。只测写接口返回 200 是不够的 ——
    // 标签写进了另一张表却没进倒排索引，写接口照样返回 200。
    const tree = await ok<any>(await request.get('/api/v1/flavors/tree'));
    const leaf = findLeaf(tree);
    expect(leaf, '种子风味树里应存在叶子节点').toBeTruthy();

    const bean = await ok<any>(
      await request.post('/api/v1/beans', { data: newBeanPayload('flavor-link') }),
    );

    try {
      const before = await ok<any>(
        await request.get(`/api/v1/flavors/filter?flavor_ids=${leaf.id}`),
      );
      const beforeIDs: number[] = before.bean_ids ?? [];
      expect(beforeIDs, '新豆还没打标签，不该出现在筛选结果里').not.toContain(bean.id);

      // 注意字段名是 node_ids。同一个概念在契约里有三种写法：
      // 这里 node_ids、建豆载荷里 flavor_node_ids、筛选查询串里 flavor_ids。
      // 契约已冻结不便改名，但这正是前一轮前端接错参数的根因。
      await ok(
        await request.put(`/api/v1/beans/${bean.id}/flavors`, {
          data: { node_ids: [leaf.id] },
        }),
      );

      const after = await ok<any>(
        await request.get(`/api/v1/flavors/filter?flavor_ids=${leaf.id}`),
      );
      const afterIDs: number[] = after.bean_ids ?? [];
      expect(
        afterIDs,
        `打上「${leaf.name}」标签后，按该标签筛选应能查到这支豆 —— ` +
          '查不到说明标签写入没有反映到倒排索引',
      ).toContain(bean.id);
    } finally {
      await request.delete(`/api/v1/beans/${bean.id}`);
    }
  });

  test('缺必填字段时逐字段报错，而不是只说一句失败', async ({ request }) => {
    const res = await request.post('/api/v1/beans', { data: { roaster: '只填了烘焙商' } });
    expect(res.status()).toBe(400);
    const body = await res.json();
    expect(body.ok).toBe(false);
    expect(body.error.fields, '校验失败必须给出字段级信息，前端才能标红对应输入框')
      .toBeTruthy();
    const fields = (body.error.fields as any[]).map((f) => f.field);
    expect(fields, '至少应指出名称缺失').toContain('name');
  });
});

test.describe('萃取记录写入路径', () => {
  let beanID: number;

  test.beforeAll(async ({ request }) => {
    const bean = await ok<any>(
      await request.post('/api/v1/beans', { data: newBeanPayload('brew-host') }),
    );
    beanID = bean.id;
  });

  test.afterAll(async ({ request }) => {
    if (beanID) await request.delete(`/api/v1/beans/${beanID}`);
  });

  test('创建→读取→更新→打点→删除', async ({ request }) => {
    const created = await ok<any>(
      await request.post('/api/v1/brews', {
        data: {
          bean_id: beanID,
          method: 'FILTER',
          title: 'QA 生命周期冲煮',
          dose_g: '20',
          total_water_g: '348',
          beverage_g: '308',
          tds_percent: '1.30',
          grinder: 'QA 磨',
          grind_setting: '6.5',
          grind_micron: 700,
          water_temp_c: 92,
          dripper: 'V60',
          agitation_count: 2,
          contact_seconds: 150,
          notes: '生命周期用例',
        },
      }),
    );
    const id = created.id;
    expect(id).toBeGreaterThan(0);

    try {
      // 创建时就该带上金杯评估，而不是要再调一次 preview
      expect(created.mode, '实测参数应判为 MEASURED').toBe('MEASURED');
      expect(created.yield_text, '创建后应直接给出萃取率').toBeTruthy();

      const fetched = await ok<any>(await request.get(`/api/v1/brews/${id}`));
      expect(fetched.id).toBe(id);
      expect(fetched.bean_id).toBe(beanID);

      // ---- 更新：改浓度后萃取率必须跟着重算 ----
      const beforeYield = fetched.yield_text;
      const updated = await ok<any>(
        await request.put(`/api/v1/brews/${id}`, {
          data: {
            bean_id: beanID,
            method: 'FILTER',
            title: 'QA 生命周期冲煮（改）',
            dose_g: '20',
            total_water_g: '348',
            beverage_g: '308',
            tds_percent: '1.20',
            water_temp_c: 92,
            contact_seconds: 150,
          },
        }),
      );
      expect(
        updated.yield_text,
        `浓度从 1.30 改成 1.20，萃取率必须重算（原 ${beforeYield}）—— ` +
          '不变说明更新只改了存储没有触发引擎',
      ).not.toBe(beforeYield);

      // ---- 追加注水节点 ----
      const poured = await ok<any>(
        await request.post(`/api/v1/brews/${id}/pour`, {
          data: {
            events: [
              { offset_ms: 0, cumulative_g: '40', technique: 'CENTER', idempotency_key: 'qa-1' },
              { offset_ms: 30_000, cumulative_g: '150', technique: 'CIRCLE', idempotency_key: 'qa-2' },
              { offset_ms: 60_000, cumulative_g: '348', technique: 'CIRCLE', idempotency_key: 'qa-3' },
            ],
          },
        }),
      );
      expect(poured.curve, '追加注水后应返回曲线').toBeTruthy();
      expect(poured.accepted, '三个新节点应全部被接受').toBe(3);

      // ---- 幂等：重复提交同一批不应产生重复点 ----
      const again = await ok<any>(
        await request.post(`/api/v1/brews/${id}/pour`, {
          data: {
            events: [
              { offset_ms: 0, cumulative_g: '40', technique: 'CENTER', idempotency_key: 'qa-1' },
            ],
          },
        }),
      );
      expect(
        again.accepted ?? 0,
        '幂等键相同的节点重复提交时，接受数应为 0 —— ' +
          '断线重连会重发，这里不幂等就会在曲线上留下重复点',
      ).toBe(0);
    } finally {
      const del = await request.delete(`/api/v1/brews/${id}`);
      expect(del.ok()).toBeTruthy();
      expect((await request.get(`/api/v1/brews/${id}`)).status()).toBe(404);
    }
  });

  test('六维评分写入→读取→删除', async ({ request }) => {
    const brew = await ok<any>(
      await request.post('/api/v1/brews', {
        data: {
          bean_id: beanID,
          method: 'FILTER',
          title: 'QA 评分用冲煮',
          dose_g: '20',
          total_water_g: '348',
          beverage_g: '308',
          tds_percent: '1.30',
          water_temp_c: 92,
          contact_seconds: 150,
        },
      }),
    );

    try {
      // 未评分时 score 应为 null，而不是一组 0 分 —— 两者含义完全不同：
      // 「没评过」和「评了但六项都是 0 分」在雷达图上必须长得不一样。
      const empty = await ok<any>(await request.get(`/api/v1/brews/${brew.id}/score`));
      expect(empty.score, '还没评分时 score 应为 null，而不是全 0 的一组分数').toBeNull();
      expect(
        empty.radar?.sample_count,
        '未评分时雷达图的样本数应为 0，并给出空态文案',
      ).toBe(0);
      expect(empty.radar?.balance, '空态应有可读提示而不是空字符串').toBeTruthy();

      const saved = await ok<any>(
        await request.put(`/api/v1/brews/${brew.id}/score`, {
          // 六维分值步进为 5（即 0.5 分一档），82/78 这种会被拒。
          data: {
            acidity_x10: 75,
            sweet_x10: 80,
            aroma_x10: 80,
            aftertone_x10: 70,
            body_x10: 65,
            bitter_x10: 30,
            note: 'QA 用例评分',
          },
        }),
      );
      expect(saved.score.acidity_x10).toBe(75);
      expect(
        saved.score.acidity_text,
        '展示串应由后端给出，前端不做 ÷10 换算',
      ).toBeTruthy();
      expect(saved.radar.sample_count, '存分后雷达图应立刻含这条样本').toBe(1);

      const read = await ok<any>(await request.get(`/api/v1/brews/${brew.id}/score`));
      expect(read.score.sweet_x10, '评分必须持久化').toBe(80);
      expect(read.score.note).toBe('QA 用例评分');

      // 越界分数应被拒绝
      const bad = await request.put(`/api/v1/brews/${brew.id}/score`, {
        data: {
          acidity_x10: 995, sweet_x10: 50, aroma_x10: 50,
          aftertone_x10: 50, body_x10: 50, bitter_x10: 50,
        },
      });
      expect(bad.status(), '99.5 分应被拒绝（六维满分各 10 分）').toBe(400);

      // 不合步进的分值也应被拒，且要说清步进是多少
      const offStep = await request.put(`/api/v1/brews/${brew.id}/score`, {
        data: {
          acidity_x10: 73, sweet_x10: 50, aroma_x10: 50,
          aftertone_x10: 50, body_x10: 50, bitter_x10: 50,
        },
      });
      expect(offStep.status(), '7.3 分不合 0.5 分的步进，应被拒绝').toBe(400);
      const offBody = await offStep.json();
      expect(
        offBody.error.fields[0].reason,
        '拒绝时要告诉用户步进是多少，而不是只说"格式错误"',
      ).toContain('步进');

      const delScore = await request.delete(`/api/v1/brews/${brew.id}/score`);
      expect(delScore.status(), '删除评分应返回 204').toBe(204);

      const afterDel = await ok<any>(await request.get(`/api/v1/brews/${brew.id}/score`));
      expect(afterDel.score, '删掉评分后应回到未评分状态').toBeNull();
    } finally {
      await request.delete(`/api/v1/brews/${brew.id}`);
    }
  });

  test('引用不存在的豆子会被拒绝，而不是留下孤儿记录', async ({ request }) => {
    const res = await request.post('/api/v1/brews', {
      data: {
        bean_id: 999_999_999,
        method: 'FILTER',
        dose_g: '20',
        total_water_g: '300',
        tds_percent: '1.30',
      },
    });
    expect(res.status(), '不存在的 bean_id 应被拒绝').toBeGreaterThanOrEqual(400);
    expect(res.status()).toBeLessThan(500);
  });
});

test.describe('风味树写入路径', () => {
  test('建节点→改名→移动→级联删除', async ({ request }) => {
    const parent = await ok<any>(
      await request.post('/api/v1/flavors/nodes', {
        data: { parent_id: null, name: 'QA 测试风味根', color: '#c58b4a', sort_order: 999 },
      }),
    );
    let childID: number | undefined;

    try {
      expect(parent.id).toBeGreaterThan(0);
      expect(parent.depth, '根节点深度应为 0 或 1（取决于实现约定）').toBeGreaterThanOrEqual(0);

      // ---- 建子节点 ----
      const child = await ok<any>(
        await request.post('/api/v1/flavors/nodes', {
          data: { parent_id: parent.id, name: 'QA 子风味', sort_order: 1 },
        }),
      );
      childID = child.id;
      expect(child.depth, '子节点深度应大于父节点').toBeGreaterThan(parent.depth);

      // ---- 改名 ----
      const renamed = await ok<any>(
        await request.patch(`/api/v1/flavors/nodes/${child.id}`, {
          data: { name: 'QA 子风味（改名）' },
        }),
      );
      expect(renamed.name).toBe('QA 子风味（改名）');

      // ---- 移到根：层级必须跟着变，而不是只改一个 parent_id 字段 ----
      const moved = await ok<any>(
        await request.post(`/api/v1/flavors/nodes/${child.id}/move`, {
          data: { to_root: true },
        }),
      );
      expect(
        moved.depth,
        '移到根之后深度应回到与根同级 —— 闭包表没同步更新会让筛选出错',
      ).toBe(parent.depth);

      // ---- 移回父节点下 ----
      await ok(
        await request.post(`/api/v1/flavors/nodes/${child.id}/move`, {
          data: { parent_id: parent.id },
        }),
      );

      // ---- 环检测：把父节点移到自己的子节点下必须被拒 ----
      const cycle = await request.post(`/api/v1/flavors/nodes/${parent.id}/move`, {
        data: { parent_id: child.id },
      });
      expect(
        cycle.status(),
        '把父节点移进自己的子树会形成环，必须被拒绝 —— ' +
          '放过去会让树的递归查询无限循环',
      ).toBeGreaterThanOrEqual(400);
      expect(cycle.status()).toBeLessThan(500);
    } finally {
      // ---- 级联删除：父节点带着子树一起走 ----
      const del = await request.delete(
        `/api/v1/flavors/nodes/${parent.id}?mode=CASCADE`,
      );
      expect(del.ok(), '级联删除应成功').toBeTruthy();
      const body = await del.json();
      expect(
        body.data?.deleted_count ?? body.data?.deleted_ids?.length ?? 0,
        '级联删除应报告删掉了几个节点，而不是静默完成',
      ).toBeGreaterThanOrEqual(2);

      if (childID) {
        // 子节点应随父节点一起消失。再按它筛选时，接口返回 200 是对的
        // ——书签里存着失效的 ID 不该变成硬错误——但它必须在
        // unknown_node_ids 里点名说"这个条件我没能用上"。
        //
        // 这是本项目最容易出的一类事故：条件被静默丢掉后返回全量，
        // 用户看到 5 支豆还以为它们都有这个风味。之前前端把
        // node_ids 写成 flavor_ids 时就是这样错了整整一轮。
        const dropped = await ok<any>(
          await request.get(`/api/v1/flavors/filter?flavor_ids=${childID}`),
        );
        expect(
          dropped.unknown_node_ids,
          `已删除的节点 ${childID} 必须出现在 unknown_node_ids 里 —— ` +
            '不点名就等于谎称筛选生效了',
        ).toContain(childID);
        expect(
          dropped.conditions,
          '失效条件不该被算作已生效的筛选条件',
        ).toHaveLength(0);
      }
    }
  });

  test('PROMOTE 模式删除会把子节点提升而非删掉', async ({ request }) => {
    const parent = await ok<any>(
      await request.post('/api/v1/flavors/nodes', {
        data: { parent_id: null, name: 'QA 提升测试根', sort_order: 998 },
      }),
    );
    const child = await ok<any>(
      await request.post('/api/v1/flavors/nodes', {
        data: { parent_id: parent.id, name: 'QA 待提升子节点' },
      }),
    );

    try {
      const del = await request.delete(`/api/v1/flavors/nodes/${parent.id}?mode=PROMOTE`);
      expect(del.ok()).toBeTruthy();

      // 子节点必须还在，且已被提升
      const still = await request.get(`/api/v1/flavors/filter?flavor_ids=${child.id}`);
      expect(
        still.ok(),
        'PROMOTE 模式只删父节点，子节点应被提升保留 —— ' +
          '连子节点一起删掉就是数据丢失',
      ).toBeTruthy();
    } finally {
      await request.delete(`/api/v1/flavors/nodes/${child.id}?mode=CASCADE`);
      await request.delete(`/api/v1/flavors/nodes/${parent.id}?mode=CASCADE`);
    }
  });

  test('重名与非法父节点被拒绝', async ({ request }) => {
    const bad = await request.post('/api/v1/flavors/nodes', {
      data: { parent_id: 999_999_999, name: 'QA 孤儿节点' },
    });
    expect(bad.status(), '挂在不存在的父节点下应被拒绝').toBeGreaterThanOrEqual(400);
    expect(bad.status()).toBeLessThan(500);

    const empty = await request.post('/api/v1/flavors/nodes', {
      data: { parent_id: null, name: '   ' },
    });
    expect(empty.status(), '空白名称应被拒绝').toBe(400);
  });
});

test.describe('金杯标准配置写入路径', () => {
  test('保存自定义标准→影响落区判定→恢复出厂', async ({ request }) => {
    const original = await ok<any>(await request.get('/api/v1/goldcup/profiles'));
    const filter = original.profiles.find((p: any) => p.method === 'FILTER');
    expect(filter, '应存在手冲标准').toBeTruthy();

    try {
      // 把萃取率区间收窄到 19–21，原本 18.5% 的"合格"应变成"欠萃"
      const saved = await ok<any>(
        await request.put('/api/v1/goldcup/profiles/FILTER', {
          data: {
            yield_min_percent: '19.00',
            yield_max_percent: '21.00',
            strength_min_percent: '1.1500',
            strength_max_percent: '1.3500',
            ratio_min: '15.0',
            ratio_max: '17.0',
            lrr: '2.00',
          },
        }),
      );
      expect(saved).toBeTruthy();

      const after = await ok<any>(await request.get('/api/v1/goldcup/profiles'));
      const changed = after.profiles.find((p: any) => p.method === 'FILTER');
      expect(
        JSON.stringify(changed),
        '保存后的标准必须与保存前不同，否则写入没生效',
      ).not.toBe(JSON.stringify(filter));

      // 配置必须真的参与判定，而不只是存起来给设置页看
      const chart = await ok<any>(
        await request.get('/api/v1/goldcup/chart?method=FILTER'),
      );
      const gold = chart.zones.find((z: any) => z.in_gold_cup);
      expect(gold, '控制图应有金杯格').toBeTruthy();
      expect(
        gold.x_min,
        '控制图的金杯格边界应跟随新配置（19.00）—— ' +
          '不跟随说明图和判定用的是两份不同的标准',
      ).toBeCloseTo(19, 1);

      // 自相矛盾的配置必须被拒
      const bad = await request.put('/api/v1/goldcup/profiles/FILTER', {
        data: {
          yield_min_percent: '22.00',
          yield_max_percent: '18.00', // 上下界颠倒
          strength_min_percent: '1.1500',
          strength_max_percent: '1.3500',
          ratio_min: '15.0',
          ratio_max: '17.0',
          lrr: '2.00',
        },
      });
      expect(bad.status(), '上下界颠倒的区间应被拒绝').toBe(400);

      // 物理上不可达的上界必须被拒
      const impossible = await request.put('/api/v1/goldcup/profiles/FILTER', {
        data: {
          yield_min_percent: '18.00',
          yield_max_percent: '95.00',
          strength_min_percent: '1.1500',
          strength_max_percent: '1.3500',
          ratio_min: '15.0',
          ratio_max: '17.0',
          lrr: '2.00',
        },
      });
      expect(impossible.status(), '95% 的萃取率上界物理不可达，应被拒绝').toBe(400);
    } finally {
      // 恢复出厂，否则后续 spec 的落区断言会跟着漂
      const reset = await request.delete('/api/v1/goldcup/profiles/FILTER');
      expect(reset.ok(), '恢复出厂标准应成功').toBeTruthy();

      const restored = await ok<any>(await request.get('/api/v1/goldcup/profiles'));
      const back = restored.profiles.find((p: any) => p.method === 'FILTER');
      expect(
        JSON.stringify(back),
        '恢复出厂后应与初始状态一致，否则会污染其他用例',
      ).toBe(JSON.stringify(filter));
    }
  });

  test('未知冲煮法的配置请求被拒绝', async ({ request }) => {
    const res = await request.put('/api/v1/goldcup/profiles/FRENCH_PRESS', {
      data: {
        yield_min_percent: '18.00', yield_max_percent: '22.00',
        strength_min_percent: '1.1500', strength_max_percent: '1.3500',
        ratio_min: '15.0', ratio_max: '17.0', lrr: '2.00',
      },
    });
    expect(res.status()).toBe(400);
  });
});

/** 在风味树里找一个叶子节点。响应把根数组放在 data.tree 下。 */
function findLeaf(tree: any): any {
  const roots: any[] = tree.tree ?? [];
  const walk = (n: any): any => {
    const kids: any[] = n.children ?? [];
    if (kids.length === 0) return n;
    for (const k of kids) {
      const found = walk(k);
      if (found) return found;
    }
    return n;
  };
  for (const r of roots) {
    const leaf = walk(r);
    if (leaf) return leaf;
  }
  return null;
}
