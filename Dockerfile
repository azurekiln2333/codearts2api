# 多平台镜像（buildx: linux/amd64, linux/arm64）。
#   docker buildx build --platform linux/amd64,linux/arm64 \
#     --build-arg BUILD_COMMIT=$(git rev-parse HEAD) \
#     --build-arg BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ) -t <image> .
# Go 编译阶段跑在构建机架构上，用 GOOS/GOARCH 交叉编译，无需模拟。
# 最终 Alpine RUN 仍需构建环境提供目标架构原生或模拟执行支持。
# 基础镜像取官方仍在维护的标签（2026-10-26 核对：1.27/3.24 均在持续更新，
# 两者都有 amd64/arm64 manifest）；旧 1.24/3.20 已停更。
# 只相当于换构建工具链，go.mod 仍为 go 1.22，不涉及应用代码升级。
ARG GO_VERSION=1.27
ARG ALPINE_VERSION=3.24

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
ARG TARGETOS
ARG TARGETARCH
# 镜像里没有 .git，无法回读 VCS 信息，因此显式注入构建身份；
# 留空时服务显示“未知构建”，不会猜测。
ARG BUILD_COMMIT=""
ARG BUILD_TIME=""
ENV CGO_ENABLED=0
WORKDIR /src
# go.mod 无第三方依赖，因此没有 go.sum。
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN set -eu; \
    ldflags="-X codearts2api/internal/server.BuildCommit=${BUILD_COMMIT} -X codearts2api/internal/server.BuildTime=${BUILD_TIME}"; \
    for cmd in server login credit apply; do \
      out="codearts2api"; \
      if [ "$cmd" != "server" ]; then out="codearts2api-$cmd"; fi; \
      GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
        go build -trimpath -ldflags "$ldflags" -o "/out/$out" "./cmd/$cmd"; \
    done

FROM alpine:${ALPINE_VERSION}
ARG BUILD_COMMIT=""
ARG BUILD_TIME=""
LABEL org.opencontainers.image.title="codearts2api" \
      org.opencontainers.image.source="https://github.com/azurekiln2333/codearts2api" \
      org.opencontainers.image.revision="${BUILD_COMMIT}" \
      org.opencontainers.image.created="${BUILD_TIME}"
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/ /usr/local/bin/
COPY config.example.json ./config.example.json
VOLUME ["/app/auths", "/app/data"]
EXPOSE 7866
# 监听端口由 Compose 从单个 CA2A_PORT 派生为 CA2A_LISTEN，这里优先用 CA2A_PORT，
# 其次从 CA2A_LISTEN 取端口部分（兼容 docker run 只给 CA2A_LISTEN 的用法），
# 都没有时回落到 7866；避免在镜像里硬编码第二个端口。
HEALTHCHECK --interval=30s --timeout=5s --start-period=40s --retries=3 \
    CMD port=${CA2A_PORT:-}; \
        [ -n "$port" ] || { port=${CA2A_LISTEN:-}; port=${port##*:}; }; \
        [ -n "$port" ] || port=7866; \
        wget -q -T 4 -O /dev/null "http://127.0.0.1:$port/healthz" || exit 1
CMD ["codearts2api", "-config", "config.json"]
