# EnjoyCoffee 后端镜像
#
# 跨平台约束（SOP Redline 1）：golang:1.25-alpine 与 alpine:3.20 官方镜像
# 都提供 linux/amd64 与 linux/arm64 清单，因此本文件在 Apple Silicon 与
# x86 服务器上都能直接构建，无需 --platform 或 QEMU 模拟。
#
# 刻意不用 scratch 作为运行基座：健康检查需要一个 HTTP 客户端，
# 而 scratch 里连 wget 都没有。alpine 只比 scratch 大 7 MB，
# 换来的是容器内可以 exec 进去排查问题 —— 对一个交付给他人运行的
# 项目来说这点体积非常划算。

# ---------------------------------------------------------------------------
# 构建阶段
# ---------------------------------------------------------------------------
FROM golang:1.25-alpine AS builder

# 国内网络下 proxy.golang.org 常年不可达，构建会卡在拉依赖上直到超时。
# 用镜像源并保留 direct 兜底，使无网络限制的环境也能正常工作。
ENV GOPROXY=https://goproxy.cn,direct \
    GOSUMDB=sum.golang.google.cn \
    CGO_ENABLED=0

WORKDIR /src

# 先只拷依赖清单再下载：源码改动不会让这一层的缓存失效，
# 后续每次改代码重建都能跳过整个依赖下载过程。
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# -trimpath 去掉构建机的绝对路径，避免把 /Users/xxx 这类信息烧进二进制。
# -ldflags "-s -w" 剥离符号表与调试信息，产物从约 20 MB 降到约 13 MB。
# GOOS/GOARCH 不写死，交给 buildx 或宿主机架构决定，这是跨平台的关键。
RUN go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# ---------------------------------------------------------------------------
# 运行阶段
# ---------------------------------------------------------------------------
FROM alpine:3.20

# tzdata 是必需的而非可选：容器默认只有 UTC，而本项目的"烘后第几天"
# 按 GMT+8 自然日边界计算。缺了它，晚上八点之后录入的豆子会被算成
# 前一天烘焙的 —— 一个只在特定时段出现、极难复现的日期错误。
# ca-certificates 为将来接入外部服务（智能秤云端 API）预留。
RUN apk add --no-cache tzdata ca-certificates wget && \
    cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime && \
    echo "Asia/Shanghai" > /etc/timezone

ENV TZ=Asia/Shanghai

# 非 root 运行。容器逃逸的成本对一个本地项目不高，但这条几乎零成本，
# 而且能挡住"容器内进程误删挂载卷里宿主机文件"这类事故。
RUN addgroup -g 10001 -S coffee && \
    adduser -u 10001 -S coffee -G coffee

COPY --from=builder /out/server /app/server

USER coffee
WORKDIR /app

EXPOSE 8080

# HEALTHCHECK 走 /health（存活）而非 /ready（就绪）：
# 就绪探针会查数据库，数据库短暂抖动时不该导致一个健康的后端容器被重启。
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=12 \
    CMD wget -qO- http://127.0.0.1:8080/api/v1/health >/dev/null || exit 1

ENTRYPOINT ["/app/server"]
