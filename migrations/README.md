# migrations

SQLite 迁移脚本。由 `internal/store` 的迁移执行器在启动时按版本升序应用，
每个迁移在**单个事务**内执行并回写 `PRAGMA user_version`。

`0004_project_specs.sql` 新增独立方案表，将旧 `projects.spec_material_id` 对应方案及原历史链保留为该项目第 1 份方案（编号 `项目编号-S0001`）。迁移不修改旧资料或重写对象文件，也不改旧项目版本。首次写入多份方案时，由用例事务生成完整兼容方案包。升级前备份数据库与对象目录；升级到第 4 版后应使用本版及后续程序，不直接用旧二进制降级。

## 命名规则

```
NNNN_<description>.sql
```

- `NNNN`：4 位零填充版本号，从 `0001` 开始，全局唯一、递增。
- `<description>`：小写字母/数字/下划线，例如 `0002_add_task_notes.sql`。
- 只打包 `migrations/*.sql`（见 `migrations.go` 的 `//go:embed`），
  `README.md` 不会被嵌入。
- 文件名不符合规则会直接报错，避免拼写错误导致迁移被静默跳过。

## 如何新增迁移

1. 取当前最大版本号 +1，新建 `0002_xxx.sql`；
2. 只写 DDL/DML，**不要**写 `PRAGMA user_version`（由代码统一回写）；
3. 文件内可包含多条语句，执行器会按分号拆分（能识别注释与引号）；
4. 启动时只执行 `version > PRAGMA user_version` 的文件，已应用的不会重复执行；
5. 更新本文档无需改代码，`go test ./migrations` 会校验命名与版本唯一性。

## 现有迁移

| 版本 | 文件 | 内容 |
|------|------|------|
| 1 | `0001_init.sql` | products / materials / projects / tasks / task_deps 五张表与四个索引 |
| 2 | `0002_project_last_actor.sql` | projects 新增 `last_actor` 列（可空） |
| 3 | `0003_workers.sql` | workers 表（worker 心跳与任务占用）、`ix_workers_last_seen` 与部分唯一索引 `ux_workers_current_task` |
