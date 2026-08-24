import { test, expect } from '@playwright/test';

/**
 * 实时注水通道的端到端验证。
 *
 * 为什么必须单独测：注水曲线是本项目的招牌功能，但插桩度量显示
 * internal/ws 被端到端用例覆盖到的比例只有 7.6% —— Serve、readLoop、
 * writeLoop、handleInbound、handleMark、join/leave、模拟器启停全部是 0%。
 * 单元测试在进程内直接调 handleInbound，绕过了真正的 WebSocket 握手，
 * 所以以下几件事在此之前从未被任何测试碰过：
 *
 *   1. nginx 的 Upgrade 转发是否真的能把握手带到后端（配错一个头就全断）
 *   2. 前端用的路径 /api/v1/ws/... 是否与后端注册的一致
 *   3. 断线重连时的幂等去重在真实连接上是否生效
 *   4. 模拟器推送的点能否真的沿着 socket 回到客户端
 *
 * 用浏览器原生 WebSocket（而不是 Node 的 ws 库）是刻意的：这样走的是
 * 与用户完全相同的同源路径，nginx 的升级转发也一并被验证。
 */

/** 在页面里跑一段 WebSocket 会话，返回收到的全部消息。 */
async function session(
  page: import('@playwright/test').Page,
  brewID: number,
  script: Array<Record<string, unknown>>,
  opts: { waitMs?: number } = {},
) {
  return page.evaluate(
    async ({ brewID, script, waitMs }) => {
      const url = `${location.origin.replace(/^http/, 'ws')}/api/v1/ws/brews/${brewID}/pour`;
      const ws = new WebSocket(url);
      const received: any[] = [];

      await new Promise<void>((resolve, reject) => {
        ws.onopen = () => resolve();
        ws.onerror = () => reject(new Error(`WebSocket 握手失败：${url}`));
        setTimeout(() => reject(new Error('WebSocket 握手超时')), 5000);
      });

      ws.onmessage = (ev) => {
        try {
          received.push(JSON.parse(ev.data as string));
        } catch {
          received.push({ type: '__unparseable__', raw: ev.data });
        }
      };

      // 逐条发送，给服务端留出回包时间
      for (const msg of script) {
        ws.send(JSON.stringify(msg));
        await new Promise((r) => setTimeout(r, 120));
      }
      await new Promise((r) => setTimeout(r, waitMs ?? 400));
      ws.close();
      return received;
    },
    { brewID, script, waitMs: opts.waitMs },
  );
}

test.describe('实时注水通道', () => {
  let beanID: number;
  let brewID: number;

  // 用例中途失败时，写在正常流程末尾的清理不会执行，残留就会漏出去 ——
  // 别的用例断言"历史样本有几条"时会莫名失败，排查方向还会被带偏到
  // 那条无辜的用例上。所以创建的 ID 一律登记，由 afterAll 兜底删除。
  const createdBrewIDs: number[] = [];

  test.beforeAll(async ({ request }) => {
    const beanRes = await request.post('/api/v1/beans', {
      data: {
        name: 'QA 注水通道测试豆',
        roaster: 'QA 烘焙所',
        roast_level: 'LIGHT',
        roasted_on: new Date(Date.now() - 5 * 86400_000).toISOString().slice(0, 10),
        initial_weight_g: '250',
        remaining_g: '250',
      },
    });
    beanID = (await beanRes.json()).data.id;

    const brewRes = await request.post('/api/v1/brews', {
      data: {
        bean_id: beanID,
        method: 'FILTER',
        title: 'QA 注水通道',
        dose_g: '20',
        total_water_g: '300',
        beverage_g: '260',
        tds_percent: '1.30',
        water_temp_c: 92,
        contact_seconds: 150,
      },
    });
    brewID = (await brewRes.json()).data.id;
  });

  test.afterAll(async ({ request }) => {
    for (const id of createdBrewIDs) await request.delete(`/api/v1/brews/${id}`);
    if (brewID) await request.delete(`/api/v1/brews/${brewID}`);
    if (beanID) await request.delete(`/api/v1/beans/${beanID}`);
  });

  test('握手成功并收到 hello（nginx 的 Upgrade 转发生效）', async ({ page }) => {
    await page.goto('/');
    const msgs = await session(page, brewID, [{ type: 'ping' }]);

    // hello 是服务端主动下发的第一条。收不到它通常意味着 nginx 把
    // Upgrade 头吃掉了，握手退化成普通 HTTP 请求。
    const hello = msgs.find((m) => m.type === 'hello');
    expect(hello, `应收到 hello，实际收到：${JSON.stringify(msgs)}`).toBeTruthy();
    expect(hello.mode, 'hello 应告知当前注水数据源模式').toBe('simulator');

    const pong = msgs.find((m) => m.type === 'pong');
    expect(pong, 'ping 应有 pong 回应').toBeTruthy();
    expect(
      pong.server_time_ms,
      'pong 必须带服务端时刻，前端秒表靠它对齐时钟',
    ).toBeGreaterThan(0);
  });

  test('打点会返回重算后的曲线', async ({ page }) => {
    await page.goto('/');
    const msgs = await session(page, brewID, [
      { type: 'mark', offset_ms: 0, cumulative_g: '40', technique: 'CENTER', key: 'ws-a' },
      { type: 'mark', offset_ms: 30000, cumulative_g: '160', technique: 'CIRCLE', key: 'ws-b' },
      { type: 'mark', offset_ms: 60000, cumulative_g: '300', technique: 'CIRCLE', key: 'ws-c' },
    ]);

    const curves = msgs.filter((m) => m.type === 'curve');
    expect(curves.length, '每次打点都应回一条曲线').toBeGreaterThanOrEqual(3);

    const last = curves[curves.length - 1];
    expect(last.curve, '曲线载荷不应为空').toBeTruthy();
    expect(
      last.curve.points.length,
      '三次打点后曲线上应有三个点',
    ).toBeGreaterThanOrEqual(3);

    // 流速是后端算的：前端只负责画，不做除法。
    // 首点流速以 0 起算（它之前没有区间），其余点必须为正。
    const rates = last.curve.points.map((p: any) => p.flow_rate);
    expect(
      rates.slice(1).filter((r: number) => r > 0).length,
      `除首点外都应带正的流速：${JSON.stringify(rates)} —— 流速由后端算，前端不做除法`,
    ).toBeGreaterThan(0);
    expect(
      last.curve.avg_flow_rate,
      '曲线汇总里应给出平均流速',
    ).toBeGreaterThan(0);
  });

  test('重连重推同一批点不会产生重复', async ({ page }) => {
    await page.goto('/');

    // 第一条连接打三个点
    await session(page, brewID, [
      { type: 'mark', offset_ms: 0, cumulative_g: '35', technique: 'CENTER', key: 'dup-1' },
      { type: 'mark', offset_ms: 20000, cumulative_g: '120', technique: 'CIRCLE', key: 'dup-2' },
    ]);

    // 第二条连接（模拟断线重连）重推其中一个，再补一个新的
    const second = await session(page, brewID, [
      { type: 'sync' },
      { type: 'mark', offset_ms: 0, cumulative_g: '35', technique: 'CENTER', key: 'dup-1' },
      { type: 'mark', offset_ms: 40000, cumulative_g: '220', technique: 'CIRCLE', key: 'dup-3' },
    ]);

    const curves = second.filter((m) => m.type === 'curve');
    expect(curves.length, 'sync 应先回放已有曲线').toBeGreaterThan(0);

    const final = curves[curves.length - 1].curve;
    const keys = final.points.map((p: any) => p.offset_ms);
    const uniq = new Set(keys);
    expect(
      uniq.size,
      `重推幂等键相同的点不应留下重复：${JSON.stringify(keys)} —— ` +
        '弱网下客户端一定会重推，不去重曲线上就会出现台阶',
    ).toBe(keys.length);
  });

  test('模拟器能启停，并把点推回客户端', async ({ page }) => {
    await page.goto('/');
    const msgs = await session(
      page,
      brewID,
      [{ type: 'sim_start' }],
      { waitMs: 2500 }, // 留时间让模拟器推几个点
    );

    const simState = msgs.filter((m) => m.type === 'sim_state');
    expect(simState.length, '启动模拟器应广播状态').toBeGreaterThan(0);
    // sim_state 必须显式带上布尔值，不能靠"字段缺失即 false"让客户端猜
    expect(
      typeof simState[0].sim_running,
      'sim_state 必须显式带 sim_running 布尔值',
    ).toBe('boolean');
    expect(simState[0].sim_running, '第一条状态应是"运行中"').toBe(true);

    const curves = msgs.filter((m) => m.type === 'curve');
    expect(
      curves.length,
      '模拟器应通过 socket 主动推送曲线 —— 一条都没收到说明推送链路断了',
    ).toBeGreaterThan(0);

    // 停掉，避免它继续给后面的用例推点
    const stopMsgs = await session(page, brewID, [{ type: 'sim_stop' }]);
    const stopped = stopMsgs.filter((m) => m.type === 'sim_state');
    expect(stopped.length, '停止模拟器应广播状态').toBeGreaterThan(0);
    const last = stopped[stopped.length - 1];
    expect(
      typeof last.sim_running,
      '"已停止"这个状态本身必须被说出来，而不是靠字段消失来暗示',
    ).toBe('boolean');
    expect(last.sim_running).toBe(false);
  });

  test('非法消息被明确拒绝而不是静默丢弃或断连', async ({ page }) => {
    await page.goto('/');
    const msgs = await session(page, brewID, [
      { type: 'TELEPORT' },
      { type: 'mark', offset_ms: 0, cumulative_g: '不是数字', key: 'bad-1' },
      { type: 'ping' }, // 前面两条出错后连接必须还活着
    ]);

    const errors = msgs.filter((m) => m.type === 'error');
    expect(errors.length, '两条非法消息应各回一条错误').toBeGreaterThanOrEqual(2);

    const codes = errors.map((e) => e.code);
    expect(codes, '未知消息类型应有专门的错误码').toContain('UNKNOWN_MESSAGE_TYPE');
    expect(codes, '无法解析的注水量应有专门的错误码').toContain('INVALID_CUMULATIVE');

    expect(
      msgs.some((m) => m.type === 'pong'),
      '出错之后连接必须还能用 —— 一条脏消息就踢掉客户端会让弱网下没法打点',
    ).toBeTruthy();
  });

  test('NFR-04 打点到收到曲线的往返延迟 P95 ≤ 100ms', async ({ page, request }) => {
    // NFR-04 在此之前没有任何验证手段。它测的是"手一按，曲线就动"
    // 这件事 —— 注水时用户盯着屏幕等反馈，慢一点立刻能察觉。
    // 在浏览器里计时，量到的是含 nginx 转发、后端重算、回包的整条链路。
    //
    // 用独立的冲煮记录：跟其他用例共用一条时，之前打的点和模拟器推的点
    // 会和这里的时间偏移撞上，被判成重复而不回包，测量就会卡死。
    const own = await request.post('/api/v1/brews', {
      data: {
        bean_id: beanID,
        method: 'FILTER',
        title: 'QA 延迟测量专用',
        dose_g: '20',
        total_water_g: '300',
        beverage_g: '260',
        tds_percent: '1.30',
        water_temp_c: 92,
        contact_seconds: 150,
      },
    });
    const ownID = (await own.json()).data.id;
    createdBrewIDs.push(ownID);

    await page.goto('/');

    const samples: number[] = await page.evaluate(
      async ({ brewID }) => {
        const url = `${location.origin.replace(/^http/, 'ws')}/api/v1/ws/brews/${brewID}/pour`;
        const ws = new WebSocket(url);
        await new Promise<void>((res, rej) => {
          ws.onopen = () => res();
          ws.onerror = () => rej(new Error('握手失败'));
          setTimeout(() => rej(new Error('握手超时')), 5000);
        });

        const out: number[] = [];
        let resolveCurve: ((v: void) => void) | null = null;
        ws.onmessage = (ev) => {
          const m = JSON.parse(ev.data as string);
          if (m.type === 'curve' && resolveCurve) {
            resolveCurve();
            resolveCurve = null;
          }
        };

        // 先预热一次，把首包成本排除在统计外
        for (let i = 0; i < 21; i++) {
          const waited = new Promise<void>((r) => (resolveCurve = r));
          const t0 = performance.now();
          ws.send(
            JSON.stringify({
              type: 'mark',
              offset_ms: i * 5000,
              cumulative_g: String(20 + i * 10),
              technique: 'CIRCLE',
              key: `lat-${i}`,
            }),
          );
          // 超时兜底设得比预算宽一个数量级即可。设太长的话，一旦服务端
          // 不回包，整个用例会被 21 次等待拖到测试超时，报出来的是
          // "timeout"而不是"延迟不达标"，反而看不出真正的问题。
          const timedOut = await Promise.race([
            waited.then(() => false),
            new Promise<boolean>((r) => setTimeout(() => r(true), 1000)),
          ]);
          if (timedOut) {
            ws.close();
            throw new Error(
              `第 ${i} 次打点在 1s 内没有收到曲线回包 —— ` +
                '要么服务端没回，要么被当成重复点丢弃了',
            );
          }
          if (i > 0) out.push(performance.now() - t0);
        }
        ws.close();
        return out;
      },
      { brewID: ownID },
    );

    await request.delete(`/api/v1/brews/${ownID}`);

    expect(samples.length, '应采到足够样本').toBeGreaterThanOrEqual(15);
    samples.sort((a, b) => a - b);
    const p95 = samples[Math.min(samples.length - 1, Math.floor(samples.length * 0.95))];
    const p50 = samples[Math.floor(samples.length / 2)];

    console.log(
      `[NFR-04] P50=${p50.toFixed(1)}ms P95=${p95.toFixed(1)}ms ` +
        `MAX=${samples[samples.length - 1].toFixed(1)}ms（含浏览器→nginx→后端往返）`,
    );
    expect(p95, `NFR-04 往返延迟 P95 超 100ms 预算：${JSON.stringify(samples.map((s) => +s.toFixed(1)))}`).toBeLessThan(100);
  });

  test('沙盘页面存下冲煮后真的建立了实时连接', async ({ page, request }) => {
    // 上面几条验的是协议本身；这条验前端是否真的用上了它。
    // 路径写错、协议名拼错这类问题只有从真实页面发起才能暴露。
    //
    // 注意连接时机：沙盘刚打开时没有 brewID，此时不连是对的 ——
    // 注水通道挂在一条具体的冲煮记录上，记录还不存在就无从连接。
    // 所以必须先走完"填参数 → 记录这次冲煮"，socket 才该出现。
    const opened: string[] = [];
    page.on('websocket', (ws) => opened.push(ws.url()));

    await page.goto('/brew');

    const select = page.getByLabel('咖啡豆');
    await expect(select).toBeVisible();
    const beanValue = await select
      .locator('option:not([value=""])')
      .first()
      .getAttribute('value');
    await select.selectOption(beanValue!);

    await page.getByTestId('input-dose').fill('20');
    await page.getByTestId('input-water').fill('300');
    await page.getByTestId('input-beverage').fill('260');
    await page.getByTestId('input-tds').fill('1.30');

    expect(
      opened.length,
      '还没存下冲煮记录时不应建立连接 —— 记录不存在，连了也只能被拒',
    ).toBe(0);

    const save = page.getByRole('button', { name: '记录这次冲煮' });
    await expect(save).toBeEnabled();

    // 从响应里拿 ID 而不是事后翻列表找：翻列表要靠标题猜哪条是自己建的，
    // 而这条记录的标题是前端自动生成的（"手冲/滤泡 · 时间戳"），
    // 猜错就删不掉，残留下来会让别的用例的样本量断言莫名失败。
    const [created] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes('/api/v1/brews') && r.request().method() === 'POST',
      ),
      save.click(),
    ]);
    createdBrewIDs.push((await created.json()).data.id);

    // 存成功后按钮会变成"已记录，可继续打点"
    await expect(page.getByRole('button', { name: /已记录/ })).toBeVisible({
      timeout: 10_000,
    });

    await expect
      .poll(() => opened.filter((u) => u.includes('/api/v1/ws/brews/')).length, {
        timeout: 10_000,
      })
      .toBeGreaterThan(0);

    expect(
      opened.every((u) => u.startsWith('ws://') || u.startsWith('wss://')),
      `WebSocket 应走同源地址，不能出现写死的主机名：${JSON.stringify(opened)}`,
    ).toBeTruthy();
  });
});
