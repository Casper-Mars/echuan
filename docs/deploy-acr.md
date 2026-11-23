# 镜像发布：GitHub Actions → 阿里云 ACR（个人版）

合并进 `main` 自动构建并推送镜像。只改 `**.md` / `docs/` / `design/` 的合并不会触发。

## 1. 在 ACR 控制台准备

1. 地域：选离服务器最近的一个（如 `cn-hangzhou` / `cn-shanghai` / `cn-shenzhen`）。
   **域名格式随实例新旧不同，不要凭记忆填**：
   - 2024-09-09 及之后新建的实例：`crpi-xxxxxxxx.cn-<地域>.personal.cr.aliyuncs.com`
   - 更早的旧实例：`registry.cn-<地域>.aliyuncs.com`

   最稳的做法：控制台「个人版实例 → 仓库管理 → 访问凭证」，页面直接给出
   `docker login --username=<登录名> <域名>`，把域名原样抄进 `ACR_REGISTRY`。
2. 命名空间：个人版账号下建一个命名空间（如 `<aliyun账号昵称>`）。
3. 仓库：在命名空间下建仓库 `echuan`，访问级别先选「私有」。
   之后可在仓库详情 →「基本信息」→ 修改里随时切换私有/公开，镜像与 tag 不受影响。
4. 固定密码：个人版 →「访问凭证」→ 设置 **Registry 登录密码**。
   - 用户名 = 阿里云账号全名（RAM 用户则是去掉 `@xxx.onaliyun.com` 后的前缀）
   - 密码 = 上一步设置的固定密码（不是账号登录密码，也不是 AccessKey）
   - K8s/服务器上拉私有镜像也用这组凭证。

## 2. 在 GitHub 仓库配置

Settings → Secrets and variables → Actions

**Variables（非机密）**

| 名称 | 示例 |
| --- | --- |
| `ACR_REGISTRY` | `crpi-xxxxxxxx.cn-hangzhou.personal.cr.aliyuncs.com`（旧实例为 `registry.cn-hangzhou.aliyuncs.com`） |
| `ACR_NAMESPACE` | `mynamespace` |
| `ACR_REPO` | `echuan`（可留空，默认 echuan） |

**Secrets（机密）**

| 名称 | 值 |
| --- | --- |
| `ACR_USERNAME` | ACR 用户名 |
| `ACR_PASSWORD` | ACR 固定密码 |

命令行等价设置：

```bash
gh variable set ACR_REGISTRY  --repo Casper-Mars/echuan --body crpi-xxxxxxxx.cn-hangzhou.personal.cr.aliyuncs.com
gh variable set ACR_NAMESPACE --repo Casper-Mars/echuan --body mynamespace
gh variable set ACR_REPO      --repo Casper-Mars/echuan --body echuan
gh secret   set ACR_USERNAME  --repo Casper-Mars/echuan   # 交互输入
gh secret   set ACR_PASSWORD  --repo Casper-Mars/echuan
```

## 3. 产出

每次合并进 `main` 只推一个 tag：`latest`，**是 amd64 + arm64 双架构的 manifest list**。
拉取方按自身 CPU 架构自动选对应镜像。

要追溯某个 `latest` 对应哪次提交，看 GitHub Actions 的 run（镜像 label 里带 `org.opencontainers.image.revision`）。

多架构构建的提速设计：Dockerfile 里 web / build 两个阶段固定 `--platform=$BUILDPLATFORM`，
前端 pnpm 与 Go 编译都跑在 runner 原生架构上，Go 用 `GOOS/GOARCH` 交叉编译；
只有 runtime 阶段的 `apk add` 会在 QEMU 下跑 amd64。这样避免把整条前端构建丢进模拟器。

验证已推上去的架构：

```bash
docker manifest inspect <ACR_REGISTRY>/sanding/echuan:latest | grep architecture
```

版本信息来自 `git describe --tags --always` 与 `git rev-parse --short HEAD`，
写进二进制的 `platform.Version` / `platform.Commit`，`/readyz` 之外的版本接口可查。

## 4. 部署侧拉取

```bash
docker login <ACR_REGISTRY> -u <用户名>
docker pull <ACR_REGISTRY>/<namespace>/echuan:latest
```

私有仓库需要在目标机器上 `docker login` 一次（或配置 K8s imagePullSecret）。
若要替换 `sanding/deployment` 里的本地构建，把 `ECHUAN_IMAGE` 改成 ACR 地址，
并在 compose 里去掉 `build:` 段即可回退到纯拉取。

## 5. 手动触发（可选）

Actions → `docker publish (ACR)` → Run workflow，可指定目标架构：

- 默认 `linux/amd64,linux/arm64`（双架构单 tag）
- 只出单架构时可填 `linux/amd64` 或 `linux/arm64`

## 已知坑

- 个人版 ACR 仓库必须先手动创建，或开启「自动创建仓库」，否则 push 报 `denied`。
- 密码用「固定密码」；用账号登录密码或 AccessKey 会 401。
- 域名有两种形态（`crpi-*.personal.cr.aliyuncs.com` 与 `registry.cn-*.aliyuncs.com`），一律以「访问凭证」页给出的登录命令为准。
- buildx 默认带 provenance attestation，ACR 控制台会把镜像大小显示为 0 或空。workflow 已关掉（`provenance: false`）。
- 若 ACR 个人版对 manifest list 支持有差异（拉取时报 `no matching manifest`），退路是双 tag 分开推：
  `--platform linux/amd64` → `:amd64`、`--platform linux/arm64` → `:arm64`，部署侧在 compose 里写 `platform:`。
- 首次 push 前跑一次 `Run workflow` 验证凭证，避免合并后才发现配置缺失。
