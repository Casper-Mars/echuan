# Worker 运维手册

面向部署与值班：说明 Worker 注册表相关配置、注册/心跳流程、常见错误码，以及如何判断一个 Worker 是在线、离线还是已经被清理。

相关实现：`internal/app/worker.go`（注册与派卡）、`internal/app/claim.go`（30s 回收器与保留期清理）、`internal/api/workers.go`（HTTP 层）、`api/openapi.yaml`（契约）。

---

## 1. 配置项

配置来源优先级：**环境变量 `ECHUAN_*` > TOML 文件 > 内置默认值**。TOML 为扁平 `key = value`，支持 `#` 注释；样例见 `configs/echuan.toml`。

| 键 | 环境变量 | 默认值 | 含义 | 调参注意 |
|---|---|---|---|---|
| `listen` | `ECHUAN_LISTEN` | `127.0.0.1:8720` | HTTP 监听地址 | 容器内需 `0.0.0.0:8720`；本机默认只绑回环 |
| `data_dir` | `ECHUAN_DATA_DIR` | `~/.echuan` | SQLite、objects 等运行数据根目录 | 备份/迁移要整目录一起 |
| `workspace_dir` | `ECHUAN_WORKSPACE_DIR` | `~/.echuan/workspaces` | 每个项目/任务的工作区根目录 | 派卡响应里的 `context.workdir` 由它派生 |
| `read_pool_size` | `ECHUAN_READ_POOL_SIZE` | `4` | SQLite 读连接池大小（写池固定 1） | 读多时上调，写不会因此并行 |
| `lease_ttl` | `ECHUAN_LEASE_TTL` | `90s` | 任务租约有效期；派卡与续租都写这个时长 | 必须明显大于 Worker 心跳间隔，否则一次网络抖动就丢卡；建议 ≥ 3×`heartbeat_interval` |
| `max_attempts` | `ECHUAN_MAX_ATTEMPTS` | `3` | 单任务最大尝试次数 | 新建任务时写入，改配置不影响存量任务 |
| `poll_timeout` | `ECHUAN_POLL_TIMEOUT` | `25s` | `POST /v1/tasks/claim` 长轮询超时 | Worker 侧 HTTP 超时应大于它 |
| `review_stale_hours` | `ECHUAN_REVIEW_STALE_HOURS` | `72` | 评审/验收超过多少小时视为 stale | 仅影响收件箱标记 |
| `worker_ttl` | `ECHUAN_WORKER_TTL` | `45s` | 最后一次心跳后多久判定离线（presence 推导窗口） | **必须远大于 `heartbeat_interval`**；建议 ≥ 3×，否则 Worker 会被频繁误判 offline |
| `heartbeat_interval` | `ECHUAN_HEARTBEAT_INTERVAL` | `15s` | 服务端建议 Worker 使用的心跳间隔 | 只通过响应下发，服务端不强制；Worker 不应硬编码 |
| `worker_metadata_max_bytes` | `ECHUAN_WORKER_METADATA_MAX_BYTES` | `8192` | 注册/心跳 metadata 序列化后的最大字节数 | 超限返回 413；metadata 是整体替换，不是合并 |
| `worker_retention` | `ECHUAN_WORKER_RETENTION` | `168h` | 离线且不持卡的 Worker 行保留多久后被删除 | **必须大于 `worker_ttl`**，否则行会在变为 offline 之前就被删掉；默认 7 天用于审计与排障 |
| `material_max_bytes` | `ECHUAN_MATERIAL_MAX_BYTES` | `20971520` | 单个物料最大字节数（20 MiB） | 影响报告/规格上传 |
| `log_level` | `ECHUAN_LOG_LEVEL` | `info` | `debug/info/warn/error` | 排查注册/派卡问题时用 `debug` |

配置校验：所有时长必须为正；`worker_ttl`、`heartbeat_interval`、`worker_retention` 任一 ≤ 0 都会导致进程启动失败。`worker_ttl` 与 `heartbeat_interval` 的倍数关系当前不做强校验，需要部署方自行保证。

> `worker_retention` 的判定基于 `last_seen`（而不是“进入 offline 之后又过了多久”），所以把它设得比 `worker_ttl` 还小，会让 Worker 在还显示 active 时就被删除。压测/验收需要缩短保留期时，至少要满足 `worker_retention > worker_ttl`（例如 `worker_ttl=45s`、`worker_retention=46s`）。

---

## 2. 注册与心跳

### 2.1 首次注册

`POST /v1/workers/heartbeat`，请求体不带 `worker_id`：

```bash
curl -sS -X POST http://127.0.0.1:8720/v1/workers/heartbeat \
  -H 'Content-Type: application/json' \
  -H 'X-Actor: agent:worker' \
  -d '{"state":"idle","metadata":{"host":"build-01","os":"linux","agent_version":"0.6.0"}}'
```

响应 `200`：

```json
{
  "worker": {
    "worker_id": "w_01M5Z45MXYVDV3FAHG543RZ5JQ",
    "presence": "active",
    "state": "idle",
    "current_task_id": null,
    "metadata": {"host": "build-01"},
    "first_seen": "2026-10-27T16:47:51.230Z",
    "last_seen": "2026-10-27T16:47:51.230Z",
    "heartbeat_interval_ms": 15000,
    "lease_ttl_seconds": 90
  },
  "commands": []
}
```

- `worker_id` 由服务端生成，客户端只读，格式 `w_` + ULID。
- 客户端必须持久化这个 id；后续每次心跳回填。
- `heartbeat_interval_ms` / `lease_ttl_seconds` 是服务端下发的节奏，Worker 应按它安排心跳与续租，不要写死。
- `metadata` 必须在 JSON 层面是对象；数组/字符串/null 会 400。提供即**整体替换**，不提供则保留旧文档。

### 2.2 持续心跳与派卡

```bash
curl -sS -X POST http://127.0.0.1:8720/v1/workers/heartbeat \
  -H 'Content-Type: application/json' \
  -d '{"worker_id":"w_01M5...","state":"idle"}'
```

派卡规则：

| Worker 现状 | 心跳 state | 结果 |
|---|---|---|
| 未持有任务 | `idle` | 若队列有可领取卡，`commands` 返回一条 `kind=run_task`，同时绑定指针与租约；否则 `commands=[]` |
| 未持有任务 | `busy` | 不派新卡（`commands=[]`），等待 Worker 自己声明 idle |
| 已持有任务 | `busy` | 续租：`lease_until = now + lease_ttl`，不回传新 command |
| 已持有任务 | `idle` | 不续租，租约自然到期；到期后卡回到 ready，指针由回收器清理 |

注意：

- `state` 是**声明值**，不代表占用事实；占用与否只看 `current_task_id`。
- 如果 Worker 声明 `idle` 但还持有卡，它不会拿到第二张卡；同时租约不再续期。
- 一个 Worker 最多持有一张卡（`ux_workers_current_task` 唯一索引保证）。
- `POST /v1/tasks/claim` 带 `worker_id` 也能领卡；与心跳派卡共用同一套指针/租约规则。

### 2.3 `409 worker_unknown` 的恢复

心跳或 claim 带了注册表里不存在的 `worker_id`（从未注册，或行已被保留期清理）时：

```json
{"code":"worker_unknown","message":"worker is unknown","details":[]}
```

处理方式：**丢弃旧 id，重新走 2.1 注册拿新 id**，不要原 id 重试。Worker 侧建议把 `worker_unknown` 当作可恢复错误：清空本地 id → 注册 → 继续心跳/领卡。

---

## 3. 常见错误码

所有错误响应统一为 `{"code":"...","message":"...","details":[]}`，并带 `X-Request-Id`。

| HTTP | `code` | 出现场景 | 处理 |
|---|---|---|---|
| 400 | `invalid_argument` | `state` 不是 `idle/busy`；`metadata` 不是 JSON 对象；列表 `limit/cursor` 非法；`?id=` 为空 | 修正请求；不要重试同一 body |
| 404 | `not_found` | `GET /v1/workers?id=` 的 id 不存在；任务/项目不存在 | 查询语义用 404；Worker 详情查不到说明已被清理或 id 写错 |
| 409 | `worker_unknown` | 心跳/claim 携带未注册 id | 丢弃旧 id，重新注册 |
| 409 | `worker_busy` | claim 时该 worker 已持有任务卡 | 先完成/报告当前卡；不要并发抢卡 |
| 409 | `lease_lost` | 任务心跳/报告时租约已过期、已被回收或 lease_id 不匹配 | 停止执行该卡；重新领卡（旧结果不可再提交） |
| 409 | `invalid_state` | 任务/项目状态不允许该动作（如对 pending_acceptance 再报告） | 重新读取状态后再决定 |
| 409 | `invalid_version` | 决策接口 `If-Match` 与当前版本不一致 | 重新 GET 拿最新 `version` 再决策 |
| 413 | `too_large` | metadata 超过 `worker_metadata_max_bytes`；物料超限 | 缩小 metadata；不要重试原 body |
| 204 | — | `POST /v1/tasks/claim` 无卡可领（空响应体） | 按退避轮询，不是错误 |

补充：

- `POST /v1/tasks/claim` 的并发抢卡失败统一返回 **204**，不是 409；409 只用于 `worker_unknown` / `worker_busy`。
- Worker 详情查询用 404 `not_found`，与心跳的 409 `worker_unknown` 区分：前者是“查不到”，后者是“你给的 id 我不认识”。

---

## 4. 观察 Worker 状态

### 4.1 API

**列表**（按 `last_seen` 倒序）：

```bash
curl -sS 'http://127.0.0.1:8720/v1/workers?limit=200'
```

响应 `{"items":[...],"next_cursor":"..."}`；`next_cursor` 只在还有下一页时出现。

**详情**：

```bash
curl -sS 'http://127.0.0.1:8720/v1/workers?id=w_01M5...'
```

响应是裸 `Worker` 对象（没有 `items` 包装）；`?id=` 为空 400，id 不存在 404。

字段判读：

| 字段 | 含义 |
|---|---|
| `presence` | 由 `now - last_seen` 与 `worker_ttl` 推导，**不落库**；超过 TTL 即 `offline` |
| `state` | Worker 最近一次声明的 `idle/busy`；不代表是否持有卡 |
| `current_task_id` | 真正表示占用的指针；`null` 才是空闲 |
| `first_seen` / `last_seen` | 首次注册 / 最近一次成功心跳（UTC，毫秒定宽） |
| `heartbeat_interval_ms` | 服务端期望心跳间隔 |
| `lease_ttl_seconds` | 服务端发放的租约时长，与 claim 同源 |

推导规则：`presence = active` 当且仅当 `last_seen` 距当前时间不超过 `worker_ttl`；超过即 `offline`。`offline` 只表示“没心跳”，行仍在注册表里，直到满足保留期清理条件。

### 4.2 清理规则

后台 `Reclaimer` 每 **30s** 扫一轮（进程启动时先扫一次），同一轮做两件事：

1. 回收过期任务租约，把卡放回 ready，并清掉对应 Worker 指针；
2. 删除 `current_task_id IS NULL` 且 `last_seen` 早于 `now - worker_retention` 的 Worker 行。

因此：

- 持卡 Worker 即使离线也不会被删（先等租约回收/上报释放指针）；
- 无卡离线 Worker 在保留期到点后的下一轮 sweep 被删除；
- 删除后 `GET /v1/workers?id=` 变为 404，原 id 心跳变为 409 `worker_unknown`。

### 4.3 UI

Web 控制台侧栏 **Worker**（路由 `#/workers`）展示注册表：在线数/总数、presence、state、当前任务。页面数据来自 `GET /v1/workers`，与 API 同源；判定规则一致。

---

## 5. 验收脚本

```bash
bash scripts/e2e-worker.sh   # 或 make e2e
```

- 每次新建临时目录 + 随机高位端口 + 独立 TOML，不触碰 `~/.echuan` 或仓库内数据；
- 覆盖：注册 → 派卡 → busy 续租 / idle 不续租 → 报告 → 重启恢复 → 45s 离线 → 保留期清理；
- 任一步失败会打印 HTTP 状态码、响应体、临时目录和服务日志尾部，便于定位；
- 完整跑一轮约 90s（其中约 60s 用于等待离线与 30s sweep）。

脚本把 `worker_retention` 设为 `46s`（而不是更小的值），原因见第 1 节：保留期从 `last_seen` 起算，必须大于 `worker_ttl` 才能先观察到 `offline`、再观察到清理。
