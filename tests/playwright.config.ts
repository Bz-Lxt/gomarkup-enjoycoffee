import { defineConfig, devices } from '@playwright/test';

/**
 * 两个 project 分工明确：
 *   api  —— 直连后端，验证契约与计算。不开浏览器，快。
 *   e2e  —— 走前端页面，验证用户真的能完成一件事。
 *
 * 地址从环境变量来，容器内是 compose 服务名，宿主机上是映射端口。
 * 写死任何一种都会让另一种跑不起来。
 */
export const API_BASE = process.env.API_BASE ?? 'http://localhost:31410';
export const WEB_BASE = process.env.WEB_BASE ?? 'http://localhost:31411';

export default defineConfig({
  testDir: '.',
  // 串行执行。这些测试共享一个数据库，并行会让"列表里有几支豆"
  // 这类断言随另一个 worker 的写入而随机失败 —— 那种失败查起来极贵，
  // 而这套测试全跑完只要几十秒，并行省下的时间不值得。
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  // 不自动重试。重试会把偶发失败变成绿色，而偶发失败恰恰是最该看的信号。
  retries: 0,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  reporter: [['list'], ['json', { outputFile: 'results/report.json' }]],
  outputDir: 'results/artifacts',

  projects: [
    {
      name: 'api',
      testMatch: /api-smoke\/.*\.spec\.ts/,
      use: { baseURL: API_BASE },
    },
    {
      name: 'e2e',
      testMatch: /e2e\/.*\.spec\.ts/,
      dependencies: ['api'],
      use: {
        ...devices['Desktop Chrome'],
        baseURL: WEB_BASE,
        viewport: { width: 1440, height: 900 },
        // 失败时才留证据：全量留存会让 results/ 迅速膨胀到几百 MB
        screenshot: 'only-on-failure',
        video: 'retain-on-failure',
        trace: 'retain-on-failure',
        locale: 'zh-CN',
        timezoneId: 'Asia/Shanghai',
      },
    },
  ],
});
