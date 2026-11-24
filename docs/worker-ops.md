# Worker 运维手册

面向部署与值班：说明 Worker 注册表相关配置、注册/心跳/领卡流程、常见错误码，以及如何判断一个 Worker 是在线、离线还是已经被清理。

相关实现：`internal/app/v3/worker.go`（注册与注册表用例）、`internal/store/v3/worker.go`（占用指针的绑定/归还/清理）、`internal/store/v3/task.go`（claim / 续租 / 报告 / 验收）、`internal/api/v3_handlers.go`（HTTP 层）、`cmd/echuan/main.go`（30s 维护循环）、`api/openapi.yaml`（契约）。

---

## 1. 配置项

配置来源优先级：**环境变量 `ECHUAN_*` > TOML 文件 > 内置默认值**。TOML 为扁平 `key = value`，支持 `#` 注释；样例见 `configs/echuan.toml`。

| 键 | 环境变量 | 默认值 | 含义 | 调参注意 |
|---|---|---|---|---|
| `listen` | `ECHUAN_LISTEN` | `127.0.0.1:8720` | HTTP 监听地址 | 容器内需 `0.0.0.0:8720`；本机默认只绑回环 |
| `data_dir` | `ECHUAN_DATA_DIR` | `~/.echuan` | SQLite、objects 等运行数据根目录 | 备份/迁移要整目录一起 |
| `read_pool_size` | `ECHUAN_READ_POOL_SIZE` | `4` | SQLite 读连接池大小（写池固定 1） | 读多时上调，写不会因此并行 |
| `lease_ttl` | `ECHUAN_LEASE_TTL` | `90s` | 任务租约有效期 | claim 与续租都按这个值写 `lease_until = now + lease_ttl`（`cmd/echuan/main.go` 的 `newTaskService` 显式接线 `WithLeaseTTL(cfg.LeaseTTL)`）；`GET /v1/workers?id=` 发布的 `lease_ttl_seconds` 与 claim 的实际租期同源，改配置后两处一起变 |
| `max_attempts` | `ECHUAN_MAX_ATTEMPTS` | `3` | 单任务最大尝试次数 | 建卡时写入，显式卡、流程派生卡、拆解计划子卡都一样；改配置不影响存量卡 |
| `poll_timeout` | `ECHUAN_POLL_TIMEOUT` | `25s` | 保留配置项 | V3 的 `POST /v1/tasks/claim` 是一次查询、没有长轮询，当前不消费 |
| `review_stale_hours` | `ECHUAN_REVIEW_STALE_HOURS` | `72` | 保留配置项 | `GET /v1/inbox` 只取 `ready` / `in_review`，当前不消费 |
| `worker_ttl` | `ECHUAN_WORKER_TTL` | `45s` | 最后一次心跳后多久算离线（presence 窗口） | 服务端唯一的过期判据：`GET /v1/workers` 每项与 `GET /v1/workers?id=` 的 `presence` 都由 `last_seen + worker_ttl` 推导（`active` / `offline`），客户端不得再用自己的阈值猜；必须严格小于 `worker_retention` |
| `heartbeat_interval` | `ECHUAN_HEARTBEAT_INTERVAL` | `15s` | 服务端建议的心跳间隔 | 由 `GET /v1/workers?id=` 的 `heartbeat_interval_ms` 发布（列表不带该字段）；心跳本身是 204 空体，不回这个值，Worker 侧按自己的配置/兜底常量发心跳 |
| `worker_metadata_max_bytes` | `ECHUAN_WORKER_METADATA_MAX_BYTES` | `8192` | metadata 上限 | 当前 V3 心跳未做大小校验（超限不会 413），配置值只参与校验 |
| `worker_retention` | `ECHUAN_WORKER_RETENTION` | `1h` | 离线且不持卡的 Worker 行保留多久后被删除 | **必须严格大于 `worker_ttl`**，否则进程拒绝启动；判定基于 `last_seen`（不是「进入 offline 之后又过了多久」） |
| `material_max_bytes` | `ECHUAN_MATERIAL_MAX_BYTES` | `20971520` | 单个物料最大字节数（20 MiB） | 超限 413 `too_large`（`POST /v1/materials`） |
| `log_level` | `ECHUAN_LOG_LEVEL` | `info` | `debug/info/warn/error` | 排查注册/派卡问题时用 `debug` |

配置校验：所有时长必须为正；`worker_retention` 必须严格大于 `worker_ttl`，否则进程启动失败；`log_level` 只接受四个枚举值；配置文件里的未知键被忽略（老二进制遇到新配置不会崩）。

> `worker_retention` 的判定基于 `last_seen`，所以把它设得比 `worker_ttl` 还小没有意义（会在 Worker 还显示活跃时就被删）。压测/验收需要缩短保留期时，至少要满足 `worker_retention > worker_ttl`（例如 `worker_ttl=45s`、`worker_retention=46s`）。

---

## 2. 注册、心跳与领卡

### 2.1 注册（心跳即注册）

`POST /v1/workers/heartbeat`，`worker_id` **必填且由客户端生成**（服务端不再生成、也不下发 id）：

```bash
curl -sS -X POST http://127.0.0.1:8721/v1/workers/heartbeat \
  -H 'Content-Type: application/json' \
  -H 'X-Actor: agent:worker' \
  -d '{"worker_id":"w_build_01","kinds":["requirement","design","breakdown","dev"],
       "metadata":{"host":"build-01","os":"linux","agent_version":"0.6.0"}}'
```

响应 **204 No Content（空响应体）**：

- 没有 `worker` 对象、没有 `commands`：注册与续命的结果只能靠 `GET /v1/workers` 复核；
- `worker_id` 是自己写的字符串（建议 `w_` 前缀 + 稳定标识），必须自行持久化；同一个 id 重复心跳就是 upsert，不会报错；
- `kinds` 每次心跳**整体替换**（允许为空数组）；不传 `kinds` 等于把能力清单清空，之后真的领不到卡；
- `metadata` 同样是整体替换：不传会被写成 `{}`，不是在旧文档上合并；
- 请求体里的 `state` 字段在 V3 被忽略：占用事实只由 `current_task_id` 表达（见 2.2）。

### 2.2 心跳做什么、不做什么

心跳只做两件事：把注册行 upsert 进 `workers`（刷新 `last_seen`）、整体替换 `kinds` / `metadata`。

- **不派卡**、不改 `state`、不动 `current_task_id` / `current_lease_id`；
- 占用（谁持哪张卡）只由 claim / 报告 / 取消 / 失败 / 项目关闭改写；
- 所以「心跳还在但卡没了」不是心跳干的，要去查 4.2 的清理规则或 2.4 的取消路径；
- 反过来，持卡的 Worker 继续心跳既不会丢卡，也不会换租约——续租只有 `POST /v1/tasks/lease` 一条路。

### 2.3 领卡（claim）

```bash
curl -sS -X POST http://127.0.0.1:8721/v1/tasks/claim \
  -H 'Content-Type: application/json' \
  -d '{"worker_id":"w_build_01","kind":"dev"}'
```

派卡规则（`internal/store/v3/task.go` 的 `ClaimTask`）：kind 匹配、`status=ready`、所属项目 `open`、依赖全部 `done`，按 `priority DESC, id ASC` 取第一条。

| 结果 | 含义 |
|---|---|
| `200` + 派单信封 | 卡转 `in_progress`（`attempt+1`、写 `lease_id`/`lease_until`/`worker_id`），Worker 行转 `busy` 并绑定指针 |
| `204`（空体） | 当前没有可领的卡：队列空、kind 不匹配、项目已关闭、或依赖还没结清（不是错误，按退避重试） |
| `409` `conflict`，message 含「未注册」 | `worker_id` 不在注册表里（从未心跳，或已过保留期被清理）：先 `POST /v1/workers/heartbeat` 再 claim |
| `409` `conflict`，message 含「已持卡」 | 该 Worker 还持着一张 `in_progress` 的卡（一个 Worker 同时只持一张）：先 `POST /v1/tasks/report` 结清，或等租约过期被回收后再 claim；队列空时仍然只是 `204` |

派单信封字段：

```json
{
  "task":   {"task_id": "t_...", "status": "in_progress", "attempt": 1, "lease_id": "l_...", "lease_until": "...", "worker_id": "w_build_01"},
  "project": {"project_id": "APP-1", "name": "...", "context_md": "..."},
  "product": {"product_id": "APP", "name": "...", "git_url": "..."},
  "inputs":  [{"material_id": "...", "title": "...", "kind": "requirement", "role": "input"}],
  "upstream": [{"task_id": "t_upstream", "material_id": "...", "title": "..."}],
  "has_downstream_dev": false
}
```

- `inputs` 是挂在这张卡上的输入附件，`upstream` 是依赖卡及其已验收交付物；
- 一个 Worker 同时只持一张卡（`ux_tasks_worker_lease` 唯一索引）：先报告结清，再领下一张；
- claim 先校验注册表：没心跳过的 `worker_id` 是 `409 conflict`（message 含「未注册」），不会拿到卡，所以**先注册再领卡**；已持卡的 Worker 再 claim（且队列里有卡）同样是 `409`，message 含「已持卡」。

### 2.4 续租、报告、验收与取消

| 动作 | 端点 | 结果 |
|---|---|---|
| 续租 | `POST /v1/tasks/lease` `{task_id,lease_id}` | `200 {task_id,lease_id,lease_until}`：`lease_id` 不变，只延长到期时间；lease 不匹配/已释放 → `409`，message 含 `lease lost` |
| 报告 | `POST /v1/tasks/report` `{task_id,lease_id,material_id}` | `200`：卡转 `in_review`，释放租约与 `worker_id`，Worker 回 `idle` 并归还指针；`material_id` 必须是已存在的物料（先 `POST /v1/materials`） |
| 验收 | `POST /v1/tasks/decide` `{task_id,decision,decided_by,reason?,material_id?}` | `200`：`approve` → `done` 并按 kind 派生下一阶段卡（requirement→design→breakdown→dev，拆解按计划物料落成 dev 子卡）；`reject` → `ready`，**必须带 `reason`**（缺 reason 是 `400`），同时写一条 `gate_decisions` |
| 取消 | `POST /v1/projects/close?id=<项目>&force=1` | `204`：项目内 `pending`/`ready`/`in_progress`/`in_review` 的卡批量转 `cancelled`，**当场归还持卡 Worker 的指针**（不必等 sweep） |

取消/失败的现状（本构建）：**没有单任务的取消 / 失败路由**。`DELETE /v1/tasks?id=` 只允许 `pending`/`ready`（硬删），对 `in_progress`/`in_review` 是 `409`。要停掉一张在跑的卡，只能 `POST /v1/projects/close?force=1` 把整个项目关闭并批量取消。

---

## 3. 常见错误码

所有错误响应统一为 `{"code":"...","message":"...","details":[],"request_id":"..."}`，并带 `X-Request-Id`。

| HTTP | `code` | 出现场景 | 处理 |
|---|---|---|---|
| 400 | `invalid_argument` | 请求体字段缺失/类型错/未知字段（严格解码）；枚举非法（`kind`/`status`/`role`/`decision`）；驳回不带 `reason`；心跳缺 `worker_id`；`GET /v1/workers?id=` 的 `id` 为空/纯空白（`details[0].field=id`） | 修正请求；不要重试同一 body |
| 404 | `not_found` | 产品/项目/任务/物料不存在；`GET /v1/workers?id=` 的 id 不在册（从未注册或已被保留期清理）；**未注册的 `/v1` 路径**（V3 已删除的旧路由也走这里） | 查询语义用 404；路径写错就改路径 |
| 409 | `conflict` | 状态冲突：`invalid task transition`（如硬删在跑的卡）、`lease lost`（续租/报告时租约过期或 lease_id 不匹配）、`unfinished`（未结清的项目关闭没带 force）、项目已关闭、claim 时 worker 未注册（message 含「未注册」）或已持卡（message 含「已持卡」） | 重新读取状态再决定；租约丢失就重新领卡，旧结果不能再提交；未注册就先发心跳 |
| 413 | `too_large` | 物料超过 `material_max_bytes` | 缩小物料；不要重试原 body |
| 204 | — | claim 无卡可领；心跳 / 关闭 / 重开 / 删除成功 | 都不是错误 |

补充：

- V3 的 409 统一用 `code=conflict`，**具体原因看 `message`**（`lease lost` / `invalid task transition` / `unfinished` / 「未注册」/「已持卡」）。claim 的两种 409 都是 `conflict`：心跳是 upsert（没有「未知 worker」错误路径），在册校验与「一个 Worker 只持一张卡」约束因此都折算成同一种码，客户端按 message 文案区分处置。
- 心跳本身只回 204 或 400，**永远不返回 409**。

---

## 4. 观察 Worker 状态

### 4.1 API

**列表**（按 `last_seen` 倒序，无分页游标；`limit` 缺省 100）：

```bash
curl -sS 'http://127.0.0.1:8721/v1/workers?limit=200'
```

```json
{"items":[{"worker_id":"w_holder","kinds":["dev"],"state":"busy","current_task_id":1,
           "current_work_item_id":"t_01M86JKRMV8YJ56VV89SB481VN",
           "current_lease_id":"l_01M86JKRN3QQRNVQGTEW6CDWFC",
           "last_seen":"2026-11-24T10:46:18.531Z","presence":"active"}]}
```

**详情**（`?id=` 存在即走详情：裸对象，不再包 `items`）：

```bash
curl -sS --get --data-urlencode 'id=w_holder' 'http://127.0.0.1:8721/v1/workers'
```

```json
{"worker_id":"w_holder","kinds":["dev"],"state":"busy",
 "current_task_id":1,"current_work_item_id":"t_01M86JKRMV8YJ56VV89SB481VN",
 "current_lease_id":"l_01M86JKRN3QQRNVQGTEW6CDWFC",
 "last_seen":"2026-11-24T10:46:18.531Z","presence":"active",
 "first_seen":"2026-11-24T10:46:18.523Z","heartbeat_interval_ms":15000,
 "lease_ttl_seconds":90,"metadata":{"host":"race-box"}}
```

字段判读（列表 6–8 个键：空闲行省略 `current_task_id` / `current_lease_id`；详情固定 12 个键，详情 ⊇ 列表）：

| 字段 | 列表 | 详情 | 含义 |
|---|---|---|---|
| `worker_id` | ✓ | ✓ | 客户端生成并持久化的注册 id |
| `kinds` | ✓ | ✓ | 最近一次心跳声明的能力清单（按 kind 数值序；不传 `kinds` 等于清空） |
| `state` | ✓ | ✓ | `idle` / `busy`；V3 里由占用推导（claim 置 busy，报告/取消/失败/关闭置 idle），不是心跳里的声明值 |
| `current_task_id` | 持卡时出现 | 总是出现 | 任务内部行 id，历史字段；空闲时为 `null` |
| `current_work_item_id` | ✓ | ✓ | 真正的占用指针，业务标识 `t_...`；空闲时是**显式 `null`** |
| `current_lease_id` | 持卡时出现 | 总是出现 | 当前租约；空闲时为 `null` |
| `last_seen` | ✓ | ✓ | 最近一次成功心跳（UTC，毫秒定宽） |
| `presence` | ✓ | ✓ | 服务端推导的在线态：`active` / `offline`（见下） |
| `first_seen` | — | ✓ | 首次注册时间 |
| `heartbeat_interval_ms` | — | ✓ | 服务端建议的心跳间隔（与 `heartbeat_interval` 同源，缺省 15000） |
| `lease_ttl_seconds` | — | ✓ | claim 实际写入的租约时长（与 `lease_ttl` 同源，缺省 90） |
| `metadata` | — | ✓ | 最近一次心跳上报的 metadata（整体替换；脏行退化为 `{}`） |

`presence` 口径：

- 只由服务端推导：`now - last_seen > worker_ttl`（缺省 45s）→ `offline`，否则 `active`；不可解析的 `last_seen` 一律 `offline`；
- 同一次响应内只取一次时钟，同一 `last_seen` 不会落在 TTL 两侧；同一行在列表与详情里得到同一个值；
- 调用方（控制台、脚本、SDK）**不得**再用 `last_seen` 自己算过期——服务端字段缺失或取值非法时按「不是 offline」处理，不要兜底推导。

详情错误路径（`?id=` 这个键**存在**即走进详情，值空不回落成列表）：

| 请求 | 结果 |
|---|---|
| `GET /v1/workers?id=<在册 id>` | `200` + 裸详情对象 |
| `GET /v1/workers?id=` / `?id=%20%20`（空串、纯空白） | `400 invalid_argument`，`details[0].field == "id"` |
| `GET /v1/workers?id=<从未注册 / 已被保留期清理>` | `404 not_found` |
| `GET /v1/workers`（不带 `id`） | `200` + `{"items":[...]}` |

判断某行是否还在，用列表里是否出现这个 `worker_id`（`presence=offline` 只代表心跳过期，不代表行已被删，见 4.2）。

### 4.2 清理规则

后台 `Reclaimer` 每 **30s** 扫一轮（进程启动时先扫一次），同一轮按固定顺序做三件事：

1. **回收过期租约**：`in_progress` 且 `lease_until` 已过期的卡放回 `ready`（尝试次数用尽则 `failed`），并清掉持卡 Worker 的指针；
2. **释放悬空指针**：`current_task_id` 指向的卡已不在 `in_progress`（被取消、失败，或行被硬删）时清空指针——`last_seen` 不动，让该行照常按保留期退休；
3. **按保留期删行**：`current_task_id IS NULL` 且 `last_seen < now - worker_retention` 的 Worker 行连同 `worker_kinds` 一起删除。

因此：

- 持卡 Worker 即使离线也不会被删（`current_task_id` 是唯一的占用记录，先等租约回收 / 报告 / 取消 / 关闭归还指针）；
- 取消、失败、项目 force 关闭在事务里**当场**归还指针，不依赖 sweep；
- 无卡离线 Worker 在保留期到点后的下一轮 sweep 被删除；删除后列表不再含该行；
- 指向已被硬删任务、或指向非 `in_progress` 卡的指针由第 2 步收敛，不会永久残留。

时间线示例（验收脚本用的参数：`worker_ttl=45s`、`worker_retention=46s`、sweep 30s）：最后一次心跳后约 **60s** 该行从列表消失（保留期到点 + 最多一轮 sweep）。

### 4.3 UI

Web 控制台侧栏 **worker**（路由 `#/workers`）展示注册表：页头「N 个忙碌 · M 个空闲 · K 个心跳过期」、节点、状态、当前任务、心跳、操作。页面数据来自 `GET /v1/workers`，与 API 同源；**过期只认响应里的 `presence`**：

- `presence=offline`：整行淡底 + 红色心跳徽标 + 「心跳过期」，并计入页头「K 个心跳过期」（忙碌/空闲计数不含过期行）；
- `presence=active`：正常行；
- `presence` 缺失或取值非法：不淡底、不给红徽标、不计数——前端不再有 `last_seen + 阈值` 的客户端兜底。

心跳列显示的相对时长（如 `4m12s 前`）只是文案，不参与过期判定。

### 4.4 SQL 巡检

派卡只看三件事：卡是 `ready`、项目 `open`、依赖全部 `done`。所以「看板有卡、claim 却 204」最常见的原因只有两类：卡还在 `pending`（依赖没结清），或项目已 `closed`。下面两条 SQL 直接粘进 `sqlite3 state.db` 即可（数据目录见 `docs/deploy.md` §3.1）：

```bash
sqlite3 "${ECHUAN_DATA_DIR:-$HOME/.echuan}/state.db" '<SQL>'
```

状态码：task `status` 1=pending 2=ready 3=in_progress 4=in_review 5=done 6=failed 7=cancelled；project `status` 1=open 2=closed；`kind` 1=requirement 2=design 3=breakdown 4=dev。

**SQL A：停在 pending（依赖未结清）的卡**

```sql
SELECT t.task_id, p.project_id, t.kind, t.title
  FROM tasks t JOIN projects p ON p.id = t.project_id
 WHERE t.status = 1
 ORDER BY p.project_id, t.id;
```

用途：这些卡任何 claim 都看不到。逐条看它的依赖什么时候结清——正常路径是把上游卡推进到 `done`。

**SQL B：已 ready 但依赖还没 done 的卡**

```sql
SELECT t.task_id, p.project_id, dep.task_id AS waiting_on, dep.status AS waiting_status
  FROM tasks t
  JOIN projects p ON p.id = t.project_id
  JOIN task_deps d ON d.task_id = t.id
  JOIN tasks dep ON dep.id = d.depends_on_task_id
 WHERE t.status = 2 AND dep.status <> 5
 ORDER BY p.project_id, t.id;
```

用途：`waiting_on` / `waiting_status` 指出在等哪张卡、它到哪一步了。这是审批后的正常中间态，不是故障。

处理：推进 `waiting_on`（派发 / 报告 / 验收），不要手工改下游卡的状态；B 为空而 claim 仍 204 时，再看项目状态（`closed` 不派卡）和注册表的 `current_work_item_id`（是否还持有旧卡）。

---

## 5. 验收脚本

```bash
bash scripts/e2e-worker.sh   # 或 make e2e
```

- 每次新建临时目录（`TMPDIR`，默认 `/tmp`）+ 随机高位端口 + 独立 TOML，不触碰 `~/.echuan` 或仓库内数据；空数据目录启动会自动跑迁移并生成 `state.db`；
- 覆盖 9 步：
  1. 空数据目录一键启动（自动迁移）、`/healthz` `/readyz` `/version`；
  2. 产品 / 项目容器语义（建项不建卡）、未注册 `/v1` 路径 404、参数错误 400；
  3. Worker 注册：`worker_id` 客户端生成、心跳 204 空体、注册表 `state` / `kinds` / `last_seen` / `current_work_item_id` 的契约；
  4. 建卡 → claim 拿到派单信封（task / project / product / inputs / upstream），队列空时 claim 204；
  5. 显式续租 `lease_until` 递增且 `lease_id` 不变、错误 lease 409 `lease lost`、持卡心跳不归还占用；`GET /v1/workers?id=` 的 `lease_ttl_seconds` 等于配置 `lease_ttl`（与 claim 同源）；
  6. 报告（卡转 `in_review` 且归还占用）→ 驳回（缺 reason 400、决策落库）→ 重新领取（attempt 递增）→ 验收 → 派生方案卡并带上游交付物；
  7. 方案验收派生拆解卡 → 计划物料落成 dev 卡 → 派发 / 报告 / 验收；项目材料视图与待办为空；项目关闭 / 重开；
  8. 重启后注册表与任务仍在；持卡 Worker 再 claim（队列里有卡）是 `409 conflict`「已持卡」且排队卡未被改动；项目 `force=1` 关闭批量取消持卡卡并**当场**归还指针（卡 A）；无卡 Worker 只注册不领卡（#164 前置）；
  9. 保留期前不删、超过 `worker_retention` 且一轮 sweep 后两行都从注册表消失（卡 A 后半 + #164 防回归），并核对服务日志里的 `pruned stale workers`；
- **未覆盖（脚本侧已知缺口）**：列表 `presence` 推导与详情 `404` 语义尚未进脚本，对应位置仍以 `TODO(issue #165)` 标注（服务端契约已落地，见 §4.1；断言补齐留给脚本卡，本卡不改脚本）；`GET /v1/workers?id=` 详情只断言了 `lease_ttl_seconds` 一项，其余字段语义由 `internal/api/v3_worker_detail_test.go` 覆盖；
- 任一步失败会打印步骤名、HTTP 状态码、响应体、临时目录和服务日志尾部，便于定位；
- 完整跑一轮约 70s（其中约 60s 用于等保留期 + 一轮 sweep）；
- 依赖 `bash` / `go` / `jq` / `curl`；随机端口的占用检测用 bash 的 `/dev/tcp`（镜像里的 `lsof` 是 busybox 兼容层，`-iTCP/-sTCP` 过滤参数无效，会恒判「占用」）。

脚本把 `worker_retention` 设为 `46s`（而不是更小的值），原因见第 1 节：保留期从 `last_seen` 起算，必须大于 `worker_ttl` 才能先观察到「离线仍在册」、再观察到清理。

---

## 6. 发布说明：行为变化（worker 注册表契约收敛）

本节是本次发版面向 Worker 运维与集成方的**已上线行为变化**（服务端已合入，控制台与工具包已同步）。
所有条目都能用 §4.1 的 curl 复现，无需看实现。

### R1 · 45–90s 没心跳的节点由「在线」变成「过期」

- **以前**：`GET /v1/workers` 不返回在线态，谁会过期由调用方各算一套（控制台按 `last_seen` + 固定 90s 阈值）。
- **现在**：列表每一项与 `GET /v1/workers?id=` 都返回服务端推导的 `presence`（`active` / `offline`），判据是
  `now - last_seen > worker_ttl`（缺省 45s，严格大于：恰好 45s 仍算 `active`）。所以最后一次成功心跳**超过 45s**
  该节点就报 `offline`，控制台整行淡底 + 红色「心跳过期」徽标并计入页头「K 个心跳过期」。
- **影响面**：任何按「90s 才算过期」写的看板/告警/脚本，现在会**更早**看到过期（45s–90s 区间由「在线」翻成「过期」）；
  这不是节点掉线，而是口径统一到服务端的 `worker_ttl`。
- **处置**：要调整窗口，改 `worker_ttl`（必须严格小于 `worker_retention`），不要在前端/脚本里另设阈值；
  只认 `presence`，不要再用 `last_seen` 自己算。`presence=offline` 不等于行已删除（删除仍按 `worker_retention`，见 §4.2）。
- **可复现**：心跳一次后等待，`GET /v1/workers?id=<id>` 的 `presence` 在 `worker_ttl` 前后由 `active` 翻 `offline`。

### R2 · claim 对未注册的 `worker_id` 新增 409，恢复动作 = 先发一次心跳

- **以前**：`POST /v1/tasks/claim` 只看队列，`worker_id` 写错/被清理也能一直拿到 204「无活」。
- **现在**：领卡前先查注册表，`worker_id` 未注册（从未心跳，或已过 `worker_retention` 被清理）直接
  `409 conflict`，message 为 `worker "<id>" 未注册，请先发一次心跳`；队列空时同样是 409（不是 204）。
- **恢复动作**：`POST /v1/workers/heartbeat`（心跳即注册，204 空体）→ 再 `POST /v1/tasks/claim`。
  工具包插件在每轮心跳里自动重注册，所以宿主侧只需等下一轮心跳成功（首轮延迟 5s），不要并发硬重试 claim。
- **影响面**：绕过心跳直接调 claim 的脚本/巡检/压测会从「拿不到卡」变成「409」；把 409 当 500 重试的调用方会做无用功。
- **可复现**：`curl -X POST -d '{"worker_id":"w_ghost","kind":"dev"}' .../v1/tasks/claim` → 409「未注册」；
  同一 id 先 `heartbeat` 再 claim → 200（队列里有卡）或 204（队列空）。
- **关联**：一个 Worker 同时只持一张卡，已持卡再 claim 同样是 `409 conflict`（message 含「已持卡」），恢复动作是
  先上报交付或等租约过期。
