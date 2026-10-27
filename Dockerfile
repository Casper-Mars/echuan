# syntax=docker/dockerfile:1
#
# echuan 多阶段构建：前端产物 → Go 单二进制（go:embed）→ 最小运行镜像。
#
# 运行镜像只包含：echuan 二进制、ca-certificates、tzdata、sqlite3 CLI。
# Go / Node / pnpm 工具链全部留在 builder 阶段，不会进入最终镜像。

ARG NODE_IMAGE=node:22-alpine
ARG GO_IMAGE=golang:1.26-alpine
ARG RUNTIME_IMAGE=alpine:3.20

# ---- 阶段 1：构建前端 -------------------------------------------------------
FROM ${NODE_IMAGE} AS web

# 与宿主机保持同一 pnpm 大版本，锁文件为 pnpm-lock.yaml。
# 用 npm 安装而不是 corepack，避免 corepack 的签名校验在无网/弱网环境下失败。
RUN npm install -g pnpm@11.7.0

WORKDIR /src
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./web/
RUN pnpm --dir web install --frozen-lockfile
COPY web ./web
RUN pnpm --dir web build

# ---- 阶段 2：编译 Go 单二进制 ----------------------------------------------
FROM ${GO_IMAGE} AS build

ARG VERSION=dev
ARG COMMIT=none
# 国内网络访问 proxy.golang.org 很慢；goproxy.cn 作为首选镜像，
# 校验仍由 go.sum 保证。可用 --build-arg GOPROXY=... 覆盖。
ARG GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# 前端产物进入 go:embed 目录；先删除仓库里的旧产物，避免残留旧 hash 资源。
RUN rm -rf internal/web/dist
COPY --from=web /src/web/dist internal/web/dist
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w \
        -X github.com/Casper-Mars/echuan/internal/platform.Version=${VERSION} \
        -X github.com/Casper-Mars/echuan/internal/platform.Commit=${COMMIT}" \
      -o /out/echuan ./cmd/echuan

# ---- 阶段 3：运行镜像 -------------------------------------------------------
FROM ${RUNTIME_IMAGE} AS runtime

# sqlite 提供 sqlite3 CLI（备份脚本在容器内 fallback 时使用）；
# tzdata 让 TZ 生效；ca-certificates 供将来出站 HTTPS 使用。
RUN apk add --no-cache ca-certificates tzdata sqlite \
 && addgroup -S -g 10001 echuan \
 && adduser -S -D -H -u 10001 -G echuan -h /app -s /sbin/nologin echuan \
 && mkdir -p /data \
 && chown 10001:10001 /data \
 && chmod 0755 /data

WORKDIR /app
COPY --from=build /out/echuan /usr/local/bin/echuan

LABEL org.opencontainers.image.title="echuan" \
      org.opencontainers.image.description="驿传单二进制服务（Docker 部署）" \
      com.echuan.managed="true"

# 容器内默认监听 0.0.0.0；数据统一落 /data（compose 绑定宿主机目录）。
ENV TZ=Asia/Shanghai \
    ECHUAN_LISTEN=0.0.0.0:8720 \
    ECHUAN_DATA_DIR=/data \
    ECHUAN_WORKSPACE_DIR=/data/workspaces

# 非 root 运行：UID/GID 10001。
USER 10001:10001

EXPOSE 8720

# busybox wget 自带于 alpine，无需额外安装 curl。
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=5 \
  CMD wget -q -O /dev/null http://127.0.0.1:8720/readyz || exit 1

# 不用 ENTRYPOINT，保留 `docker run <img> sh -c ...` 的调试能力；
# 直接 `docker run <img>` 时执行下面的默认命令。
CMD ["/usr/local/bin/echuan", "-config", "/etc/echuan/echuan.toml"]
