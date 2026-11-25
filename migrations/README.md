# migrations

SQLite schema scripts. `internal/store` applies them at startup in ascending
version order; each file runs inside a **single transaction** together with the
`PRAGMA user_version` write-back.

This repository is the v3 platform (`echuan_v2`). V3 is a bottom-up entity
refactor: the project is only a container, the task is the single execution
unit, and the workflow is expressed by `task_deps` instead of a project state
machine.

`0001_init.sql` creates the complete V3 schema at `user_version = 1`. There is
no v2 *data* migration: a v2 database (`user_version = 6`) is rejected by the
`SchemaVersionError` guard at startup, so a development database must be
recreated from scratch.

| Version | File | Change |
|---------|------|--------|
| 1 | `0001_init.sql` | complete V3 schema |
| 2 | `0002_material_source.sql` | `materials.source_material_id` + `ux_materials_source` |
| 3 | `0003_kind_from_config.sql` | stage ids (kind/gate) become `> 0`; `tasks.kind_parallel`; `ux_tasks_open_per_kind` replaces `ux_tasks_active_doc` |

`0003` is a **structural** migration (ADR-0003 D6″): it rebuilds `tasks`,
`gate_decisions` and `worker_kinds` to widen their CHECK from `IN (1,2,3,4)` to
`> 0` and adds `tasks.kind_parallel`, but not a single stored value changes.
`kind_parallel` is backfilled with today's behaviour (`kind = 4 → 1`, everything
else `0`). It has **no down SQL**: rolling back means restoring the pre-upgrade
database snapshot together with the previous binary, because the old code would
insert dev cards with the default `kind_parallel = 0` and hit the new index.

## Tables

| Table | Purpose |
|-------|---------|
| `products` | 产品 |
| `materials` | 资料内容与版本链（`previous_material_id`） |
| `projects` | 项目容器：名称、产品、背景上下文、`open/closed` |
| `tasks` | 唯一执行与派单单元；环节 id（`kind > 0`，词汇由流程配置裁决）、`kind_parallel` 快照、七态 status |
| `task_deps` | 任务依赖 DAG（上游 `done` 才算满足） |
| `attachments` | 任务/项目的输入与交付物，必须且只能挂一个属主 |
| `gate_decisions` | 任务级评审决策（只追加，精确指向 material 版本） |
| `task_events` | 任务事件（只追加，时间线与审计） |
| `workers` | worker 心跳与当前任务租约（`current_task_id`） |
| `worker_kinds` | worker 声明的可处理 kind |
| `im_card` | 飞书卡片投影 |
| `im_action_log` | 卡片动作幂等 |
| `im_doc` | material → 飞书文档映射缓存 |

## Enums

All enums are `INTEGER` and start at 1; 0 is invalid and is rejected by CHECK.

| Enum | Values |
|------|--------|
| `projects.status` | 1=open, 2=closed |
| `tasks.kind` / `worker_kinds.kind` / `gate_decisions.gate` | 正整数（`> 0`）；内置默认流程 1=requirement, 2=design, 3=breakdown, 4=dev，其余 id 由 `flow.stages` 配置定义（ADR-0003 D2″） |
| `tasks.status` | 1=pending, 2=ready, 3=in_progress, 4=in_review, 5=done, 6=failed, 7=cancelled |
| `materials.kind` | 1=requirement, 2=design, 3=plan, 4=report, 5=context, 6=other |
| `attachments.role` | 1=input, 2=output |
| `gate_decisions.decision` | 1=approve, 2=reject |
| `task_events.event` | 1=created … 11=reset |
| `workers.state` | 1=idle, 2=busy |
| `im_card.subject_kind` | 1=project, 2=task |

## Indexes (names are part of the contract)

| Index | Definition |
|-------|------------|
| `ix_tasks_queue` | `tasks(kind, status, priority DESC, id)` |
| `ix_tasks_lease` | `tasks(status, lease_until)` |
| `ix_tasks_project` | `tasks(project_id, kind, id)` |
| `ux_tasks_open_per_kind` | 唯一，`tasks(project_id, kind)`，仅 `kind_parallel = 0` 的活跃态（pending/ready/in_progress/in_review）；`kind_parallel = 1` 的环节可并存多张 |
| `ux_tasks_worker_lease` | 唯一，`tasks(worker_id)`，仅 `status = 3` |
| `ix_task_deps_upstream` | `task_deps(depends_on_task_id)` |
| `ix_task_events_task` | `task_events(task_id, id)` |
| `ix_gate_decisions_task` | `gate_decisions(task_id, id)` |
| `ix_attachments_task` / `ix_attachments_project` | 附件按属主与角色查询 |
| `ux_workers_current_task` | 唯一，`workers(current_task_id)`，仅非 NULL |
| `ix_projects_status` / `ix_projects_product` | 项目列表与产品内序号 |
| `ix_materials_chain` | `materials(previous_material_id)` |

## 命名规则

```
NNNN_<description>.sql
```

- `NNNN`：4 位零填充版本号，从 `0001` 开始。
- `<description>`：小写字母/数字/下划线，例如 `0001_init.sql`。
- 只打包 `migrations/*.sql`（见 `migrations.go` 的 `//go:embed`），
  `README.md` 不会被嵌入。
- 文件名不符合规则会直接报错，避免拼写错误导致迁移被静默跳过。

## V3 的约定

1. **已上线的 schema 只加迁移，不改历史文件**。`0001_init.sql` 冻结；放宽
   CHECK、加列、换索引一律走新的 `NNNN_*.sql`（`0003` 是第一个这样的迁移）。
2. 只写 DDL/DML，**不要**写 `PRAGMA user_version`（由代码统一回写）。
3. 文件内可包含多条语句，执行器会按分号拆分（能识别注释与引号）。
4. 时间字段统一为 RFC3339 毫秒 UTC 字符串，SQL 按字典序比较。
5. 枚举列禁止 0；新增枚举值必须同时改 SQL CHECK、Go 常量与测试。
