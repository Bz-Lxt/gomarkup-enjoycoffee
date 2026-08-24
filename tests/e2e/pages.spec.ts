import { expect, test } from '@playwright/test';

/**
 * 页面级冒烟。每条断言对应一件"用户能不能看到/做到"的事。
 *
 * 刻意不断言像素与文案措辞 —— 那种测试在每次改设计时都会红，
 * 于是很快就会被人加上 .skip。这里只钉住结构与数据流。
 */

// 任何页面上的 JS 报错都视为失败：一个 React 渲染异常会让整块 UI
// 变成白屏，而"白屏"在只检查 URL 的测试里完全看不出来。
test.beforeEach(async ({ page }) => {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  page.on('console', (m) => {
    if (m.type() === 'error') errors.push(m.text());
  });
  // 挂到 page 上供测试末尾检查
  (page as unknown as { __errors: string[] }).__errors = errors;
});

test.afterEach(async ({ page }) => {
  const errors = (page as unknown as { __errors: string[] }).__errors ?? [];
  // favicon 之类的次要 404 不该让测试红，但 JS 异常必须
  const real = errors.filter((e) => !/favicon|Failed to load resource/i.test(e));
  expect(real, `页面产生了 JS 错误:\n${real.join('\n')}`).toEqual([]);
});

test.describe('豆库看板', () => {
  test('展示种子豆并按新鲜度分组', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByRole('heading', { name: '豆库看板' })).toBeVisible();

    // 卡片渲染出来说明 /meta 与 /beans 两个请求都成功了 ——
    // meta 失败时整个 UI 会停在加载态，因为颜色语义和枚举都来自它。
    const cards = page.getByTestId('bean-card');
    await expect(cards.first()).toBeVisible();
    expect(await cards.count()).toBeGreaterThan(1);
  });

  test('生命周期条的几何来自后端，不是前端算的', async ({ page }) => {
    await page.goto('/');
    const bar = page.getByTestId('lifecycle-bar').first();
    await expect(bar).toBeVisible();
    // 各段宽度之和应为 100%，这是后端 width_percent 字段的性质。
    // 前端若自己算百分比，几乎必然凑不出整 100。
    const total = await bar.evaluate((el) =>
      Array.from(el.querySelectorAll('[data-width-percent]')).reduce(
        (s, seg) => s + parseFloat((seg as HTMLElement).dataset.widthPercent ?? '0'),
        0,
      ),
    );
    expect(Math.abs(total - 100), `各段宽度之和为 ${total}`).toBeLessThan(0.5);
  });

  // 这条测的是前端有没有把筛选条件用**后端认识的参数名**发出去。
  // 只断言"卡片数变少了"抓不到这个 bug —— 参数名写错时后端返回全量数据，
  // 页面看上去正常有内容。所以直接盯网络请求。
  test('筛选请求带上后端认识的参数名', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByTestId('bean-card').first()).toBeVisible();

    const [request] = await Promise.all([
      page.waitForRequest(
        (r) => r.url().includes('/api/v1/beans?') && r.url().includes('flavor'),
        { timeout: 8000 },
      ),
      page.getByTestId('flavor-filter-node').first().click(),
    ]);

    const q = new URL(request.url()).searchParams;
    expect(
      q.get('flavor_ids'),
      `实际发出的查询串是 ${new URL(request.url()).search} —— ` +
        `后端只认 flavor_ids，其他名字会被 StrictQuery 拒掉或（历史上）静默忽略`,
    ).toBeTruthy();
    // 参数名写错时后端现在会 400，页面应该弹错误而不是装作筛选成功了
    const res = await request.response();
    expect(res!.status(), '筛选请求被后端拒绝了').toBe(200);
  });

  test('新建豆子的表单会回显字段级错误', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: '+ 入库新豆' }).click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();

    // 空表单直接提交，后端应一次返回全部字段错误并逐格显示。
    // 只弹一个 toast 会让用户靠猜哪一格填错了（DesignSpec §5.2）。
    await dialog.getByRole('button', { name: /保存|确认|入库/ }).click();
    await expect(dialog.getByTestId('field-error').first()).toBeVisible();
  });
});

test.describe('萃取沙盘', () => {
  // 试算挂在具体豆子上（偏好曲线与历史落点按豆聚合），
  // 所以不选豆就没有读数 —— 这是页面的正常行为，不是缺陷。
  async function pickFirstBean(page: import('@playwright/test').Page) {
    const select = page.getByLabel('咖啡豆');
    await expect(select).toBeVisible();
    const value = await select
      .locator('option:not([value=""])')
      .first()
      .getAttribute('value');
    await select.selectOption(value!);
  }

  test('金杯控制图渲染且参数改动即时反馈', async ({ page }) => {
    await page.goto('/brew');
    await expect(page.getByRole('heading', { name: '萃取沙盘' })).toBeVisible();

    // canvas 必须有实际尺寸。DPR 处理写错时 canvas 会是 0×0，
    // 页面看起来是"图没出来"，但不会报任何错误。
    const canvas = page.getByTestId('goldcup-canvas');
    await expect(canvas).toBeVisible();
    const box = await canvas.boundingBox();
    expect(box!.width).toBeGreaterThan(200);
    expect(box!.height).toBeGreaterThan(200);

    await pickFirstBean(page);

    // 金杯要求萃取率 18–22% 且浓度 1.15–1.35% 同时达标。
    // 308 × 1.30 / 20 = 20.02%，浓度 1.30% —— 两个维度都在窗口内。
    await page.getByTestId('input-dose').fill('20');
    await page.getByTestId('input-water').fill('348');
    await page.getByTestId('input-beverage').fill('308');
    await page.getByTestId('input-tds').fill('1.30');

    const readout = page.getByTestId('goldcup-readout');
    await expect(readout).toContainText('金杯', { timeout: 10_000 });
  });

  test('缺少 TDS 时读数标注为推算', async ({ page }) => {
    await page.goto('/brew');
    await pickFirstBean(page);

    await page.getByTestId('input-dose').fill('20');
    await page.getByTestId('input-water').fill('300');
    await page.getByTestId('input-tds').fill(''); // 没有折光仪 → 转推算模式

    // 推算值必须显式标注，否则用户会把它当实测值信任（DesignSpec §5.6）。
    // 角标会出现多次：卡片头部一个，萃取率与浓度两个读数各一个 ——
    // 逐个读数都标是对的，用户视线落在哪个数字上都能看到它是推算的。
    const tags = page.getByTestId('goldcup-readout').getByTestId('advisory-tag');
    await expect(tags.first()).toBeVisible({ timeout: 10_000 });
    expect(await tags.count()).toBeGreaterThanOrEqual(2);

    // 免责声明也必须出现在页面上，而不只是躲在接口响应里。
    // 它会出现在多处（推算区块、建议区的【推算结果，非测量】前缀、警告列表），
    // 这是刻意的冗余 —— 用户从哪个入口读到这组数字都会被提醒。
    await expect(page.getByText('折射仪').first()).toBeVisible();
  });
});

test.describe('风味雷达墙', () => {
  test('选中豆子后画出雷达图层', async ({ page }) => {
    await page.goto('/radar');
    await expect(page.getByRole('heading', { name: /雷达墙/ })).toBeVisible();

    const options = page.getByTestId('radar-bean-option');
    await expect(options.first()).toBeVisible();
    await options.nth(0).click();

    const canvas = page.getByTestId('radar-canvas');
    await expect(canvas).toBeVisible();
    const box = await canvas.boundingBox();
    expect(box!.width).toBeGreaterThan(100);
  });

  test('超过六支时给出限制提示而不是静默丢弃', async ({ page }) => {
    await page.goto('/radar');
    const options = page.getByTestId('radar-bean-option');
    await expect(options.first()).toBeVisible();

    const n = Math.min(await options.count(), 7);
    for (let i = 0; i < n; i++) await options.nth(i).click();

    if (n > 6) {
      await expect(page.getByTestId('radar-limit-warning')).toBeVisible();
    }
  });
});

test.describe('风味树', () => {
  test('展示树并能展开层级', async ({ page }) => {
    await page.goto('/flavors');
    await expect(page.getByRole('heading', { name: '风味树' })).toBeVisible();

    const nodes = page.getByTestId('tree-node');
    await expect(nodes.first()).toBeVisible();
    const before = await nodes.count();

    await page.getByTestId('tree-toggle').first().click();
    await expect.poll(async () => nodes.count()).toBeGreaterThan(before);
  });

  test('删除确认必须说明影响范围', async ({ page }) => {
    await page.goto('/flavors');
    await expect(page.getByTestId('tree-node').first()).toBeVisible();

    // 悬停显示行内操作，点删除
    const row = page.getByTestId('tree-node').first();
    await row.hover();
    await row.getByTestId('tree-delete').click();

    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    // 不说明会删掉几个子节点就让用户点确认，是破坏性操作最常见的设计缺陷
    await expect(dialog.getByTestId('impact-scope')).toBeVisible();
  });
});

test.describe('设置', () => {
  test('金杯标准可编辑并展示当前生效值', async ({ page }) => {
    await page.goto('/settings');
    await expect(page.getByRole('heading', { name: '设置' })).toBeVisible();

    // LRR 是手冲推算模式的关键系数，必须能看到也能改
    await expect(page.getByTestId('input-lrr')).toBeVisible();
    await expect(page.getByText('九宫格落区说明')).toBeVisible();
  });
});

test.describe('导航与路由', () => {
  test('五个页面都能直接访问且刷新不 404', async ({ page }) => {
    for (const path of ['/', '/brew', '/radar', '/flavors', '/settings']) {
      const res = await page.goto(path);
      expect(res!.status(), `${path} 返回 ${res!.status()}`).toBeLessThan(400);
      // SPA 兜底生效的标志：深层路由刷新后仍拿到 index.html 并正常挂载
      await expect(page.locator('#root')).not.toBeEmpty();
    }
  });

  test('未知路由回落到豆库看板', async ({ page }) => {
    await page.goto('/no-such-page');
    await expect(page.getByRole('heading', { name: '豆库看板' })).toBeVisible();
  });
});
