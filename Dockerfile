# syntax=docker/dockerfile:1
#
# echuan 多阶段构建：前端产物 → Go 单二进制（go:embed）→ 最小运行镜像。
#
# 运行镜像只包含：echuan 二进制、ca-certificates、tzdata、sqlite3 CLI。
# Go / Node / pnpm 工具链全部留在 builder 阶段，不会进入最终镜像。
#
# 依赖源全部可通过 build-arg 覆盖（国内网络加速，详见 docs/deploy.md 第 3.5 节）：
#   NPM_REGISTRY  默认 https://registry.npmmirror.com
#   APK_MIRROR    默认 mirrors.aliyun.com（置空 = 保持官方源）
#   GOPROXY       默认 https://goproxy.cn,...
#   IMAGE_MIRROR  默认空 = Docker Hub 官方镜像

# IMAGE_MIRROR 是可选的基础镜像仓库前缀，默认空。
# 例：--build-arg IMAGE_MIRROR=dockerproxy.net/library/
#     → dockerproxy.net/library/node:22-alpine
# 权衡：镜像名不带 registry 域名（如 node:22-alpine）时会命中 daemon 的
# registry-mirrors 加速器；一旦显式带了域名（含本前缀），加速器不再参与，
# 拉取完全由该域名提供。因此默认留空，只在确实需要时显式指定。
ARG IMAGE_MIRROR=
ARG NODE_IMAGE=node:22-alpine
ARG GO_IMAGE=golang:1.26-alpine
ARG RUNTIME_IMAGE=alpine:3.20

# ---- 阶段 1：构建前端 -------------------------------------------------------
FROM ${IMAGE_MIRROR}${NODE_IMAGE} AS web

# 国内访问 registry.npmjs.org 很慢；默认走 npmmirror。
# 切回官方：--build-arg NPM_REGISTRY=https://registry.npmjs.org
ARG NPM_REGISTRY=https://registry.npmmirror.com
# pnpm 11 已不读 npm_config_* 环境变量（实测 `pnpm config get registry` 仍返回
# registry.npmjs.org），所以这里三重保险：ENV + ~/.npmrc + 显式 --registry。
# 仓库内不写 .npmrc，registry 只由 build-arg 决定。
ENV npm_config_registry=${NPM_REGISTRY}

# 与宿主机保持同一 pnpm 大版本，锁文件为 pnpm-lock.yaml。
# 用 npm 安装而不是 corepack，避免 corepack 的签名校验在无网/弱网环境下失败。
# `npm config set --location=user` 写入 /root/.npmrc，pnpm 会读取它。
RUN npm install -g pnpm@11.7.0 --registry=${NPM_REGISTRY} \
 && npm config set registry ${NPM_REGISTRY} --location=user

WORKDIR /src
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./web/
# pnpm store 走 BuildKit cache mount：跨构建复用已下载的 tarball。
# 显式 --registry 是真正生效的那一层（命令行选项优先级最高）。
RUN --mount=type=cache,id=pnpm-store,target=/root/.local/share/pnpm/store,sharing=locked \
    pnpm --dir web install --frozen-lockfile --registry=${NPM_REGISTRY}
COPY web ./web
RUN pnpm --dir web build

# ---- 阶段 2：编译 Go 单二进制 ----------------------------------------------
FROM ${IMAGE_MIRROR}${GO_IMAGE} AS build

ARG VERSION=dev
ARG COMMIT=none
# 国内网络访问 proxy.golang.org 很慢；goproxy.cn 作为首选镜像，
# 校验仍由 go.sum 保证。可用 --build-arg GOPROXY=... 覆盖。
ARG GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /src
COPY go.mod go.sum ./
# module 缓存与编译缓存都用 cache mount，重复构建不再全量下载/编译。
RUN --mount=type=cache,id=gomod,target=/go/pkg/mod \
    go mod download
COPY . .
# 前端产物进入 go:embed 目录；先删除仓库里的旧产物，避免残留旧 hash 资源。
RUN rm -rf internal/web/dist
COPY --from=web /src/web/dist internal/web/dist
RUN --mount=type=cache,id=gomod,target=/go/pkg/mod \
    --mount=type=cache,id=gobuild,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w \
        -X github.com/Casper-Mars/echuan/internal/platform.Version=${VERSION} \
        -X github.com/Casper-Mars/echuan/internal/platform.Commit=${COMMIT}" \
      -o /out/echuan ./cmd/echuan

# ---- 阶段 3：运行镜像 -------------------------------------------------------
FROM ${IMAGE_MIRROR}${RUNTIME_IMAGE} AS runtime

# 国内访问 dl-cdn.alpinelinux.org 很慢；默认换成 aliyun 镜像。
# 置空（--build-arg APK_MIRROR=）则保持镜像内官方源。
ARG APK_MIRROR=mirrors.aliyun.com

# sqlite 提供 sqlite3 CLI（备份脚本在容器内 fallback 时使用）；
# tzdata 让 TZ 生效；ca-certificates 供将来出站 HTTPS 使用。
# 注意：先换源再 apk add；替换后仍残留官方域名视为失败，直接报错退出，
# 避免静默回落到慢源。
RUN set -eu; \
    if [ -n "${APK_MIRROR}" ]; then \
      sed -i "s|dl-cdn\.alpinelinux\.org|${APK_MIRROR}|g" /etc/apk/repositories; \
      if grep -q 'dl-cdn\.alpinelinux\.org' /etc/apk/repositories; then \
        echo "ERROR: 无法把 /etc/apk/repositories 切换到 ${APK_MIRROR}，当前内容：" >&2; \
        cat /etc/apk/repositories >&2; \
        exit 1; \
      fi; \
    fi; \
    apk add --no-cache ca-certificates tzdata sqlite \
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
    ECHUAN_DATA_DIR=/data

# 非 root 运行：UID/GID 10001。
USER 10001:10001

EXPOSE 8720

# busybox wget 自带于 alpine，无需额外安装 curl。
#
# 必须先剥离代理环境变量（实测结论，勿删）：
#   - Docker Desktop 会把 ~/.docker/config.json 里的 proxies 注入每个容器，
#     包括 HTTP_PROXY/HTTPS_PROXY/ALL_PROXY 及小写变体；
#   - busybox wget 会读取 http_proxy 并把 127.0.0.1 的请求也发给代理，
#     代理返回 502 → FailingStreak 持续上涨 → 容器长期 unhealthy；
#   - busybox wget 完全不认 NO_PROXY/no_proxy（已实测，两个大小写都无效），
#     所以靠 NO_PROXY 白名单救不回来，只能在执行前 unset。
# 健康检查只打本机回环，不需要任何代理，剥离后行为与无代理环境一致。
# 注：compose 的 healthcheck.test 会覆盖镜像里的 HEALTHCHECK，两处必须保持一致。
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=5 \
  CMD env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY \
          -u http_proxy -u https_proxy -u all_proxy \
      wget -q -O /dev/null http://127.0.0.1:8720/readyz || exit 1

# 不用 ENTRYPOINT，保留 `docker run <img> sh -c ...` 的调试能力；
# 直接 `docker run <img>` 时执行下面的默认命令。
CMD ["/usr/local/bin/echuan", "-config", "/etc/echuan/echuan.toml"]
