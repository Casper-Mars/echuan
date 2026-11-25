-- 0003_kind_from_config.sql — 让环节（kind）由流程配置裁决，而不是写死 4 个值
--
-- 依据 docs/decisions/ADR-0003-workflow-config-stage-model.md（D2″/D4″/D6″、「迁移」一节）：
-- 迁移是**结构性**的（放宽 CHECK + 加一列 + 换索引），不改任何存量值（D6″）。
-- 存量 kind 1–4、标题、描述、上下文、状态、依赖一律原样复制，零值转换。
--
-- 三张表重建（SQLite 无法就地放宽 CHECK）：
--   tasks           kind CHECK  IN (1,2,3,4) → > 0；新增 kind_parallel
--   gate_decisions  gate CHECK  IN (1,2,3,4) → > 0
--   worker_kinds    kind CHECK  IN (1,2,3,4) → > 0
-- 重建按 SQLite 官方顺序：建新表（外键直接写最终表名）→ 带 id 复制 → DROP 旧表
-- → RENAME 新表。迁移期间 foreign_keys 由 runner 关闭（internal/store/migrate.go），
-- 迁移完成后 runner 再跑 PRAGMA foreign_key_check 验证 task_deps / attachments /
-- workers.current_task_id / gate_decisions 的引用全部有父行。
--
-- 回滚：**不写 down SQL**。三表重建 + 旧二进制版本守卫让「就地降级」既无意义又留隐患
-- （旧代码插入开发卡时 kind_parallel 取默认 0 会撞 ux_tasks_open_per_kind）；
-- 真实回滚 = 恢复升级前的库快照，必须连同镜像一起做（ADR-0003「迁移」一节）。

-- ── tasks：kind 放宽 + kind_parallel 快照 + 索引换血 ────────────────────
CREATE TABLE tasks_new (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id        TEXT NOT NULL UNIQUE,
  project_id     INTEGER NOT NULL REFERENCES projects(id),
  parent_task_id INTEGER REFERENCES tasks(id),
  kind           INTEGER NOT NULL CHECK (kind > 0),
  kind_parallel  INTEGER NOT NULL DEFAULT 0 CHECK (kind_parallel IN (0, 1)),
  title          TEXT NOT NULL,
  description_md TEXT NOT NULL DEFAULT '',
  context_md     TEXT NOT NULL DEFAULT '',
  priority       INTEGER NOT NULL DEFAULT 0,
  status         INTEGER NOT NULL DEFAULT 1 CHECK (status IN (1, 2, 3, 4, 5, 6, 7)),
  attempt        INTEGER NOT NULL DEFAULT 0 CHECK (attempt >= 0),
  max_attempts   INTEGER NOT NULL DEFAULT 3 CHECK (max_attempts > 0),
  not_before     TEXT,
  lease_id       TEXT,
  lease_until    TEXT,
  worker_id      TEXT,
  reason         TEXT,
  create_time    TEXT NOT NULL,
  update_time    TEXT NOT NULL,
  CHECK (status <> 3 OR (lease_id IS NOT NULL AND lease_until IS NOT NULL AND worker_id IS NOT NULL))
);

-- 带 id 复制把 sqlite_sequence 抬到 max(id)，删表再改名后新卡不会重号。
-- kind_parallel 是「今天的行为快照」：默认流程里只有开发（kind = 4）可并行。
INSERT INTO tasks_new (id, task_id, project_id, parent_task_id, kind, kind_parallel,
                       title, description_md, context_md, priority, status, attempt,
                       max_attempts, not_before, lease_id, lease_until, worker_id,
                       reason, create_time, update_time)
SELECT id, task_id, project_id, parent_task_id, kind, CASE WHEN kind = 4 THEN 1 ELSE 0 END,
       title, description_md, context_md, priority, status, attempt,
       max_attempts, not_before, lease_id, lease_until, worker_id,
       reason, create_time, update_time
  FROM tasks;

DROP TABLE tasks;
ALTER TABLE tasks_new RENAME TO tasks;

CREATE INDEX ix_tasks_queue ON tasks(kind, status, priority DESC, id);
CREATE INDEX ix_tasks_lease ON tasks(status, lease_until);
CREATE INDEX ix_tasks_project ON tasks(project_id, kind, id);

-- 一个 worker 同时只持一条租约（列序与 0001 一致）。
CREATE UNIQUE INDEX ux_tasks_worker_lease ON tasks(worker_id)
  WHERE status = 3 AND worker_id IS NOT NULL;

-- D4″：同项目同环节的未完成卡上限由 kind_parallel 决定。
-- kind_parallel = 0 → 至多一张活跃卡；= 1 → 可并存多张（默认流程里的开发）。
-- 索引取代 0001 的 ux_tasks_active_doc（那条只覆盖文档类 kind 1–3）。
CREATE UNIQUE INDEX ux_tasks_open_per_kind ON tasks(project_id, kind)
  WHERE kind_parallel = 0 AND status IN (1, 2, 3, 4);

-- ── gate_decisions：gate 放宽 ───────────────────────────────────────────
CREATE TABLE gate_decisions_new (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id     TEXT NOT NULL REFERENCES tasks(task_id),
  gate        INTEGER NOT NULL CHECK (gate > 0),
  material_id INTEGER REFERENCES materials(id),
  decision    INTEGER NOT NULL CHECK (decision IN (1, 2)),
  decided_by  TEXT NOT NULL,
  reason      TEXT,
  create_time TEXT NOT NULL
);

INSERT INTO gate_decisions_new (id, task_id, gate, material_id, decision, decided_by, reason, create_time)
SELECT id, task_id, gate, material_id, decision, decided_by, reason, create_time
  FROM gate_decisions;

DROP TABLE gate_decisions;
ALTER TABLE gate_decisions_new RENAME TO gate_decisions;

CREATE INDEX ix_gate_decisions_task ON gate_decisions(task_id, id);
CREATE INDEX ix_gate_decisions_material ON gate_decisions(material_id);

-- ── worker_kinds：kind 放宽 ─────────────────────────────────────────────
CREATE TABLE worker_kinds_new (
  worker_id TEXT NOT NULL,
  kind      INTEGER NOT NULL CHECK (kind > 0),
  PRIMARY KEY (worker_id, kind)
);

INSERT INTO worker_kinds_new (worker_id, kind) SELECT worker_id, kind FROM worker_kinds;

DROP TABLE worker_kinds;
ALTER TABLE worker_kinds_new RENAME TO worker_kinds;
