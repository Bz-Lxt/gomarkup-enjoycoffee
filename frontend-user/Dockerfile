# syntax=docker/dockerfile:1

# ---- 构建阶段 ----
# 不锁 platform：让 buildx 用宿主机架构原生构建，
# 在 Apple Silicon 上锁 amd64 会走 QEMU 模拟，构建时间涨到十几倍。
FROM node:22-alpine AS build

WORKDIR /app

# 刻意不接受 VITE_API_BASE：后端地址在运行期由 nginx 的同源代理决定，
# 不烘焙进产物。这样同一个镜像可以发布到任意端口/域名而无需重新构建。
# 详见 nginx.conf 里的 "API 同源代理" 一节。

# 先单独拷依赖清单：只要清单没变，这一层的缓存就命中，
# 改业务代码不会触发重新装依赖。
COPY package.json package-lock.json ./
RUN npm ci

COPY tsconfig.json vite.config.ts index.html ./
COPY public ./public
COPY src ./src

# 构建前跑类型检查：类型错误在这里失败比部署后在浏览器里发现要好。
RUN npx tsc --noEmit && npx vite build

# ---- 运行阶段 ----
FROM nginx:1.27-alpine

ENV TZ=Asia/Shanghai
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html

# 镜像里带 wget 供 compose 的 healthcheck 使用（alpine 的 busybox 自带）
EXPOSE 80

HEALTHCHECK --interval=10s --timeout=3s --start-period=3s --retries=5 \
  CMD wget -qO- http://127.0.0.1:80/ >/dev/null || exit 1

CMD ["nginx", "-g", "daemon off;"]
