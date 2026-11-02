-- 0003_workers.sql — worker 表（技术方案 v0.6 §2.1）
--
-- 记录每个 worker 的心跳与占用中的任务：worker_id 由平台生成（w_ + ULID），
-- metadata_json 原样保存（≤8KiB，由调用方校验）。current_task_id 指向 tasks.id，
-- 部分唯一索引保证一个任务同时只能被一个 worker 占用；NULL 不参与唯一性，
-- 因此空闲 worker 可以有任意多个。不做数据回填，也不修改 0001/0002。
--
-- 时间字段统一为 RFC3339 毫秒 UTC 字符串（沿用现有约定）。
-- 文件内不要写 PRAGMA user_version：版本号由 internal/store 的迁移执行器回写。

CREATE TABLE workers (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  worker_id        TEXT NOT NULL UNIQUE,
  metadata_json    TEXT NOT NULL DEFAULT '{}',
  state            TEXT NOT NULL DEFAULT 'idle' CHECK (state IN ('idle','busy')),
  current_task_id  INTEGER REFERENCES tasks(id),
  current_lease_id TEXT,
  first_seen       TEXT NOT NULL,
  last_seen        TEXT NOT NULL,
  create_time      TEXT NOT NULL,
  update_time      TEXT NOT NULL
);

CREATE INDEX ix_workers_last_seen ON workers(last_seen);

CREATE UNIQUE INDEX ux_workers_current_task ON workers(current_task_id)
  WHERE current_task_id IS NOT NULL;
