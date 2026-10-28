# dsh-echuan — 驿传平台接入工具包

这是驿传（echuan）的 dsh（Cordis 4）Agent 接入插件：把平台 HTTP 契约接进 Agent。装进一个 profile 后，Agent 能领卡、续租、交报告，平台也能把派单投递给它。

## 能力边界（先看这段）

插件**只做三件事**：

1. **定时心跳**：上报 agent 状态与 metadata，并带上平台下发的 `worker_id`（仅存内存）。
2. **投递派单**：把平台的 `run_task` 交给当前唯一的根 agent。
3. **提供 7 个工具**：
   - 写：`echuan_task_claim`（领卡）、`echuan_task_renew`（续租）、`echuan_task_report`（交报告）
   - 读：`echuan_inbox`（待办）、`echuan_project_get`（项目）、`echuan_task_get`（任务）、`echuan_material_get`（资料）

**不提供**：验收、返工、取消、改状态——所有门禁动作一律没有。
**不代交报告**：报告必须由 agent 自己调 `echuan_task_report`。
**不缓存、不落盘**：`worker_id` 只在内存，进程重启后重新注册（会拿到新的 `w_` id）。

## 与平台的关系：同仓

`kit/` 与平台在同一个仓库。平台改 `api/openapi.yaml` 契约时，**kit 的客户端与用例必须在同一个 PR 里同步更新**——`pnpm test` 里的契约断言会直接变红，不靠人肉比对。

## 安装

下文 `<name>` 替换成你的 profile 名，例如 `work`。

### 方式一：本地目录（开发时推荐）

在仓库根目录执行（`./kit` 指本仓库的工具包；换机器只需把仓库放到任意路径）：

```bash
# 1) 初始化 profile（已存在可跳过），会创建 ~/.dsh/profiles/<name>/
dsh plugin --profile <name> list

# 2) 装插件：--profile 必须在 add 之前
dsh plugin --profile <name> add ./kit
```

⚠️ `--profile` 位置错了（如 `dsh plugin add ... --profile <name>`）会报 `required option '--profile <name>' not specified`。

⚠️ 全新 profile 只含 `@deepseek-ai/dsh-base` 时，`dsh --profile <name> --help` 会挂起（没有 app bundle 消费启动参数）。在 `~/.dsh/profiles/<name>/package.json` 里补齐 `bundles`。

> `add` 会把 `dsh-echuan` 自动追加进 `bundles`。**手改时要在现有数组上追加，不要整段覆盖**，否则插件会静默不加载（表现为 0 条 `[dsh-echuan]` 日志、0 心跳）。

```json
"dsh": {
  "profile": {
    "bundles": ["@deepseek-ai/dsh-base", "@deepseek-ai/dsh-acp-app", "dsh-echuan"]
  }
}
```

改完 `dsh --profile <name> --help` 应在数秒内正常退出。

### 方式二：tgz 包

```bash
cd /Users/reachlucifer/project/opc/echuan/kit
pnpm build && pnpm pack            # 产出 dsh-echuan-0.1.0.tgz（已被 gitignore）
dsh plugin --profile <name> add ./dsh-echuan-0.1.0.tgz
```

同样受上面两条 ⚠️ 约束：`--profile` 在前；手工补 `bundles` 时要保留 `dsh-echuan`。

## 配置

编辑 `~/.dsh/profiles/<name>/cordis.patch.yml`（已有其他条目就追加这一条；重启 dsh 生效）：

```yaml
- id: echuan
  config:
    baseUrl: http://127.0.0.1:8720        # 平台地址，结尾斜杠会被去掉
    actor: agent:echuan                   # X-Actor 声明式身份，不是 worker_id
    requestTimeoutMs: 35000               # 单请求超时
    worker:
      poll:
        enable: false                     # 默认关闭；打开才会定时心跳
      metadata:                           # 原样上报，序列化后 ≤8KiB
        host: build-01
        os: darwin
        agent_version: 0.1.0
```

- `worker.poll.enable=false` 是默认值：不建定时器、零请求；**手动领卡照常可用**（工具与心跳开关无关）。
- `metadata` 序列化后超过 8KiB 时插件**不启动心跳循环**并 warn 一条（不是截断），避免每轮都被平台拒。
- 真实例子：本机起好平台后，把上面的 `enable` 改成 `true`，`baseUrl` 保持 `http://127.0.0.1:8720`，重启 dsh 即可。

## 确认它真的在工作

先按 profile 里 app bundle 的正常用法启动 dsh（如 `dsh --profile <name>`；ACP profile 会等待 stdin），保持进程不退出，然后在另一个终端：

```bash
curl -fsS http://127.0.0.1:8720/v1/workers | jq '.items[] | {worker_id, state, presence, current_task_id, last_seen}'
```

- 看到 `w_` 开头的 `worker_id` 且 `presence=active`：已注册。
- `current_task_id` 非空：正在干活。
- 超过 `worker_ttl`（默认 45s）没有心跳：`presence=offline`。

插件日志关键字（启动时 / 卸载时）：

```
[dsh-echuan] 插件加载 ... poll=true
[dsh-echuan] 已注册 7 个工具（3 写 + 4 读）：...
[dsh-echuan] 已注册技能 echuan-task-execution（source=runtime）
[dsh-echuan] worker 心跳已停止（plugin dispose）：定时器已清除，不再产生请求与日志
[dsh-echuan] 已注销技能 echuan-task-execution
```

日志里的 `worker_id` 一律打码（如 `w_01M6****`），不会输出完整 id。

## 常见故障对照

| 现象 | 原因 | 动作 |
|---|---|---|
| 心跳全部失败，日志 `warn` 网络错误 | `baseUrl` 写错 / 平台没起 / 端口不对 | `curl http://127.0.0.1:8720/readyz` 确认平台活着，再改 `baseUrl` |
| 日志反复出现 `409 worker_unknown` | 平台库里没有这个 id（被保留期清理或换库） | 插件会自动清 id 重注册；持续出现则检查平台是否被重置 |
| `echuan_task_claim` 报 `409 worker_busy` | 该 worker 已持有卡 | 先 `echuan_task_report` 交掉当前卡，不要并发抢卡 |
| 续租/报告报 `409 lease_lost` | 租约过期或被回收 | 立即停止该卡，旧结果不可再提交，重新 `echuan_task_claim` |
| `/v1/workers` 显示 `presence=offline` | 插件停了 / 心跳关了 / 超过 45s 没心跳 | 确认 `worker.poll.enable=true`、进程在跑、网络通 |
| 报告被拒 `422 material_required` | `summary_md` 为空 | 按「改了什么 / 怎么自检 / 遗留风险」三段重写 |

## 已知行为（实测）

- `dsh --profile <name> --help` 在 `worker.poll.enable=true` 时会**先发一次真实心跳**（插件在 apply 阶段启动循环），不是纯只读命令。
- 心跳间隔只认平台回包 `heartbeat_interval_ms`（默认 15s），客户端夹紧在 [500ms, 24h]。
- 连续 `worker_unknown` 会退避 1s→2s→5s→15s，不会打爆平台。

## 卸载

```bash
dsh plugin --profile <name> remove dsh-echuan
```

卸载后：工具与技能从会话消失、心跳停止，再次启动既无 `[dsh-echuan]` 日志也无请求。

## 给维护者：测试与验收

```bash
# 单测：typecheck + 197 条用例（在 kit/ 下）
cd kit && pnpm verify

# 真机端到端：独立 compose 栈 18720，约 90s，进/出不动 ~/echuan-data（在仓库根目录）
bash kit/scripts/e2e.sh
```
