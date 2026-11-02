# migrations

SQLite schema scripts. `internal/store` applies them at startup in ascending
version order; each file runs inside a **single transaction** together with the
`PRAGMA user_version` write-back.

This repository is the v2 platform (`echuan_v2`): a brand-new project with a
brand-new database and no compatibility layer. There is therefore **one initial
schema file only** — no incremental migrations and no v1 data import.

`0001_init.sql` creates the v2 schema at `user_version = 1`:

| Table | Purpose |
|-------|---------|
| `products` | 产品 |
| `materials` | 资料（需求 / 方案 / 报告 / 任务计划，按 `kind` 区分，版本链由 `previous_material_id` 表达） |
| `projects` | 项目（九态状态机，`status` 0–8） |
| `project_specs` | 独立方案表（项目下按 `seq_no` 递增） |
| `work_items` | 统一工作项，v2 唯一的派单实体 |
| `work_inputs` | 工作项输入资料关联表 |
| `work_deps` | 工作项依赖 |
| `work_item_events` | 工作项事件（只追加，时间线与审计） |
| `workers` | worker 心跳与当前占用的工作项（`current_work_item_id`） |
| `worker_kinds` | worker 声明的能力（kind 列表），派单的唯一依据 |

v1 的 `tasks` / `task_deps` 不再创建：队列语义已收口到 `work_items`，
`work_deps` 取代 `task_deps`。

索引（名称同时是契约，见技术方案 §6）：

| Index | Definition |
|-------|------------|
| `ix_work_queue` | `work_items(kind, state, priority DESC, seq_no)` |
| `ix_work_lease` | `work_items(state, lease_until)` |
| `ux_work_active_phase` | 唯一，`work_items(project_id, kind)`，仅阶段 kind 的活跃态 |
| `ux_work_worker` | 唯一，`work_items(worker_id)`，仅 `state = 'leased'` |
| `ix_work_events_item` | `work_item_events(work_item_id, id)` |
| `ux_workers_current_work_item` | 唯一，`workers(current_work_item_id)`，仅非 NULL |

启动时还会校验 schema 版本：数据库版本高于二进制内置的最新版本时直接报
`SchemaVersionError`，不尝试兼容。

## 命名规则

```
NNNN_<description>.sql
```

- `NNNN`：4 位零填充版本号，从 `0001` 开始，全局唯一、递增。
- `<description>`：小写字母/数字/下划线，例如 `0001_init.sql`。
- 只打包 `migrations/*.sql`（见 `migrations.go` 的 `//go:embed`），
  `README.md` 不会被嵌入。
- 文件名不符合规则会直接报错，避免拼写错误导致迁移被静默跳过。

## v2 的约定

1. **不新增迁移文件**。schema 变化直接改写 `0001_init.sql`，开发库重建。
   等二期正式发版、出现需要保留的线上数据后，才恢复按版本增量迁移的做法。
2. 只写 DDL/DML，**不要**写 `PRAGMA user_version`（由代码统一回写）。
3. 文件内可包含多条语句，执行器会按分号拆分（能识别注释与引号）。
4. 时间字段统一为 RFC3339 毫秒 UTC 字符串，SQL 按字典序比较。

## 现有迁移

| 版本 | 文件 | 内容 |
|------|------|------|
| 1 | `0001_init.sql` | v2 完整初始 schema（领域表 + 工作项五表 + workers 外键改造） |
