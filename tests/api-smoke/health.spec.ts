import { expect, test } from '@playwright/test';

test.describe('健康与就绪', () => {
  test('存活探针不依赖数据库', async ({ request }) => {
    const res = await request.get('/api/v1/health');
    expect(res.ok()).toBeTruthy();
    const body = await res.json();
    expect(body.ok).toBe(true);
    expect(body.data.status).toBe('ok');
    expect(body.data.service).toBe('enjoycoffee-backend');
  });

  test('就绪探针报告数据库与风味树两项', async ({ request }) => {
    const res = await request.get('/api/v1/ready');
    expect(res.ok()).toBeTruthy();
    const { data } = await res.json();

    expect(data.database.ok).toBe(true);
    expect(data.flavor_tree.ok).toBe(true);
    // 风味树是内存物化的，节点数为 0 说明装载失败 —— 此时筛选会全部返回空
    expect(data.flavor_tree.nodes).toBeGreaterThan(0);
    expect(data.pour_source).toBe('simulator');
  });

  test('时间戳落在 GMT+8', async ({ request }) => {
    // 全站的养豆天数、评分时间都按北京时间的"民用日"算。
    // 容器时区若回落到 UTC，跨零点前后八小时内的天数会差一天，
    // 而这个偏差只在特定时段出现 —— 用断言把它钉死在这里。
    const res = await request.get('/api/v1/health');
    const { data } = await res.json();
    expect(data.time).toMatch(/\+08:00$/);
  });
});

test.describe('错误信封', () => {
  test('不存在的路径返回结构化 NOT_FOUND', async ({ request }) => {
    const res = await request.get('/api/v1/no-such-endpoint');
    expect(res.status()).toBe(404);
    const body = await res.json();
    expect(body.ok).toBe(false);
    expect(body.error.kind).toBe('NOT_FOUND');
  });

  test('不存在的资源 ID 返回 NOT_FOUND 而非 500', async ({ request }) => {
    const res = await request.get('/api/v1/beans/999999');
    expect(res.status()).toBe(404);
    const body = await res.json();
    expect(body.error.kind).toBe('NOT_FOUND');
  });

  test('非法路径参数返回 VALIDATION 而非 500', async ({ request }) => {
    const res = await request.get('/api/v1/beans/abc');
    expect(res.status()).toBe(400);
    const body = await res.json();
    expect(body.error.kind).toBe('VALIDATION');
  });
});
