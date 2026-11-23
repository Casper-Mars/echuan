# 镜像发布：GitHub Actions → 阿里云 ACR（个人版）

合并进 `main` 自动构建并推送镜像。只改 `**.md` / `docs/` / `design/` 的合并不会触发。

## 1. 在 ACR 控制台准备

1. 地域：选离服务器最近的一个（如 `cn-hangzhou` / `cn-shanghai` / `cn-shenzhen`）。
   个人版公网地址形如 `registry.cn-hangzhou.aliyuncs.com`。
2. 命名空间：个人版账号下建一个命名空间（如 `<aliyun账号昵称>`）。
3. 仓库：在命名空间下建仓库 `echuan`，**访问级别设为「私有」**。
4. 固定密码：个人版 →「访问凭证」→ 设置 **Registry 登录密码**。
   - 用户名 = 阿里云账号全名（或子账号名）
   - 密码 = 上一步设置的固定密码（不是账号登录密码）
   - K8s/服务器上拉私有镜像也用这组凭证。

## 2. 在 GitHub 仓库配置

Settings → Secrets and variables → Actions

**Variables（非机密）**

| 名称 | 示例 |
| --- | --- |
| `ACR_REGISTRY` | `registry.cn-hangzhou.aliyuncs.com` |
| `ACR_NAMESPACE` | `mynamespace` |
| `ACR_REPO` | `echuan`（可留空，默认 echuan） |

**Secrets（机密）**

| 名称 | 值 |
| --- | --- |
| `ACR_USERNAME` | ACR 用户名 |
| `ACR_PASSWORD` | ACR 固定密码 |

命令行等价设置：

```bash
gh variable set ACR_REGISTRY  --repo Casper-Mars/echuan --body registry.cn-hangzhou.aliyuncs.com
gh variable set ACR_NAMESPACE --repo Casper-Mars/echuan --body mynamespace
gh variable set ACR_REPO      --repo Casper-Mars/echuan --body echuan
gh secret   set ACR_USERNAME  --repo Casper-Mars/echuan   # 交互输入
gh secret   set ACR_PASSWORD  --repo Casper-Mars/echuan
```

## 3. 产出

每次合并进 `main` 推三个 tag：

- `latest`
- `main`
- `sha-<short>`（如 `sha-94f89e8`）

版本信息来自 `git describe --tags --always` 与 `git rev-parse --short HEAD`，
写进二进制的 `platform.Version` / `platform.Commit`，`/readyz` 之外的版本接口可查。

## 4. 部署侧拉取

```bash
docker login registry.cn-hangzhou.aliyuncs.com -u <用户名>
docker pull registry.cn-hangzhou.aliyuncs.com/<namespace>/echuan:latest
```

私有仓库需要在目标机器上 `docker login` 一次（或配置 K8s imagePullSecret）。
若要替换 `sanding/deployment` 里的本地构建，把 `ECHUAN_IMAGE` 改成 ACR 地址，
并在 compose 里去掉 `build:` 段即可回退到纯拉取。

## 5. 手动触发（可选）

Actions → `docker publish (ACR)` → Run workflow，可指定目标架构：

- `linux/amd64`（默认，服务器常用）
- `linux/arm64`
- `linux/amd64,linux/arm64`（多架构，QEMU 模拟，前端构建会慢 3~5 倍）

## 已知坑

- 个人版 ACR 仓库必须先手动创建，或开启「自动创建仓库」，否则 push 报 `denied`。
- 密码用「固定密码」；用账号登录密码会 401。
- 首次 push 前跑一次 `Run workflow` 验证凭证，避免合并后才发现配置缺失。
