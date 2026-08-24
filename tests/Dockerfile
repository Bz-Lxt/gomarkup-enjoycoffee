# syntax=docker/dockerfile:1

# 官方 Playwright 镜像自带浏览器与全部系统依赖。
# 自己在 node:alpine 上装 Chromium 需要补十几个 .so，且 alpine 的
# musl 与官方浏览器构建不兼容 —— 那条路每次升级都会重新踩一遍。
# 版本号必须与 package.json 里的 @playwright/test 一致，
# 否则容器启动时会报 "browser was not installed"。
FROM mcr.microsoft.com/playwright:v1.49.1-jammy

ENV TZ=Asia/Shanghai \
    # 浏览器已随镜像预装，跳过 postinstall 的重复下载
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 \
    CI=1
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

WORKDIR /qa

COPY package.json package-lock.json* ./
RUN npm install --no-audit --no-fund

COPY tsconfig.json playwright.config.ts ./
COPY api-smoke ./api-smoke
COPY e2e ./e2e

# 默认跑全量。只跑某个 project: docker compose run --rm qa npx playwright test --project=api
CMD ["npx", "playwright", "test"]
