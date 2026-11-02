-- 0001_init.sql — 驿传平台二期（v2）初始表结构（技术方案 v0.8 §6）
--
-- v2 是全新项目、全新数据库、全新契约：只有这一个初始 schema 文件，不写
-- 迁移文件、不读 v1 数据。PRAGMA user_version 由 internal/store 的迁移执行
-- 器回写在单个事务内，本文件不要写 PRAGMA user_version；本文件对应的版本是 1。
--
-- 表顺序：products → materials → projects → project_specs → work_items →
-- work_inputs → work_deps → work_item_events → workers → worker_kinds。
-- 父表在前，避免外键目标的前向引用：
--   projects.requirement_material_id / spec_material_id → materials(id)
--   project_specs.material_id                          → materials(id)
--   work_items.project_id / parent_id                  → projects(id) / work_items(id)
--   work_items.result_material_id                      → materials(id)
--   work_inputs.work_item_id / material_id             → work_items(id) / materials(id)
--   work_deps.work_item_id / depends_on_id             → work_items(id)
--   work_item_events.work_item_id                      → work_items(id)
--   workers.current_work_item_id                       → work_items(id)
--
-- v2 不再创建 v1 的 tasks / task_deps：队列语义统一收口到 work_items，
-- work_deps 取代 task_deps。
--
-- 时间字段统一为 RFC3339 毫秒 UTC 字符串（沿用 v1 约定），SQL 按字典序比较。

CREATE TABLE products (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  product_id  TEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL,
  git_url     TEXT NOT NULL,
  description TEXT,
  license     TEXT,
  create_time TEXT NOT NULL,
  update_time TEXT NOT NULL
);

CREATE TABLE materials (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  material_id          TEXT NOT NULL UNIQUE,
  kind                 INTEGER NOT NULL,
  version              INTEGER NOT NULL,
  title                TEXT NOT NULL,
  sha256               TEXT NOT NULL,
  size                 INTEGER NOT NULL,
  path                 TEXT NOT NULL,
  uploaded_by          TEXT NOT NULL,
  previous_material_id INTEGER REFERENCES materials(id),
  create_time          TEXT NOT NULL,
  update_time          TEXT NOT NULL
);
CREATE INDEX ix_materials_chain ON materials(previous_material_id);

-- 项目九态状态机沿用 v1：0 requirement_draft / 1 requirement_review /
-- 2 spec_draft / 3 spec_review / 4 tasks_draft / 5 tasks_review /
-- 6 pending_done / 7 completed / 8 cancelled。
CREATE TABLE projects (
  id                      INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id              TEXT NOT NULL UNIQUE,
  product_id              INTEGER NOT NULL REFERENCES products(id),
  seq_no                  INTEGER NOT NULL,
  title                   TEXT NOT NULL,
  requirement_material_id INTEGER REFERENCES materials(id),
  spec_material_id        INTEGER REFERENCES materials(id),
  status                  INTEGER NOT NULL DEFAULT 0 CHECK (status BETWEEN 0 AND 8),
  reason                  TEXT,
  version                 INTEGER NOT NULL DEFAULT 1,
  create_time             TEXT NOT NULL,
  update_time             TEXT NOT NULL,
  last_actor              TEXT,
  tasks_review_reason     TEXT,
  tasks_review_rejected_at TEXT,
  UNIQUE (product_id, seq_no)
);
CREATE INDEX ix_projects_inbox ON projects(status, update_time);

-- 独立方案表：项目下按 seq_no 递增的方案版本。
CREATE TABLE project_specs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  spec_id     TEXT NOT NULL UNIQUE,
  project_id  INTEGER NOT NULL REFERENCES projects(id),
  seq_no      INTEGER NOT NULL,
  material_id INTEGER NOT NULL REFERENCES materials(id),
  create_time TEXT NOT NULL,
  update_time TEXT NOT NULL,
  UNIQUE (project_id, seq_no)
);

-- 统一工作项：v2 唯一的派单实体。kind 即队列（四类，无 merge）。
-- state 七态：blocked / queued / leased / submitted / accepted / failed / cancelled
-- （v1 的 backlog/ready 收敛为 blocked/queued）。
CREATE TABLE work_items (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  work_id            TEXT NOT NULL UNIQUE,        -- 统一 w_ + ULID
  project_id         INTEGER NOT NULL REFERENCES projects(id),
  parent_id          INTEGER REFERENCES work_items(id),
  kind               TEXT NOT NULL CHECK (kind IN ('requirement','design','breakdown','dev')),
  seq_no             INTEGER NOT NULL DEFAULT 0,
  title              TEXT NOT NULL,
  body_md            TEXT NOT NULL DEFAULT '',
  priority           INTEGER NOT NULL DEFAULT 0,
  state              TEXT NOT NULL DEFAULT 'queued'
                     CHECK (state IN ('blocked','queued','leased','submitted','accepted','failed','cancelled')),
  attempt            INTEGER NOT NULL DEFAULT 0,
  max_attempts       INTEGER NOT NULL DEFAULT 3,
  not_before         TEXT,
  lease_id           TEXT,
  lease_until        TEXT,
  worker_id          TEXT,
  result_material_id INTEGER REFERENCES materials(id),
  reason             TEXT,
  version            INTEGER NOT NULL DEFAULT 1,
  create_time        TEXT NOT NULL,
  update_time        TEXT NOT NULL
);

-- 一个 kind 一条逻辑队列：同表，用索引区分。
CREATE INDEX ix_work_queue ON work_items(kind, state, priority DESC, seq_no);
-- 租约回收扫描。
CREATE INDEX ix_work_lease ON work_items(state, lease_until);
-- 阶段工作项一个项目同一时刻最多一条活跃（blocked/queued/leased/submitted）；
-- accepted / failed / cancelled 不参与，允许同一阶段有多条历史项。
CREATE UNIQUE INDEX ux_work_active_phase ON work_items(project_id, kind)
  WHERE kind IN ('requirement','design','breakdown')
    AND state IN ('blocked','queued','leased','submitted');
-- 一个 worker 同时只持一条租约（串行处理，声明多个 kind 也不并发）。
CREATE UNIQUE INDEX ux_work_worker ON work_items(worker_id)
  WHERE state = 'leased' AND worker_id IS NOT NULL;

-- 工作项输入资料：门禁要按资料版本追溯，必须可查询、可加外键，故用关联表。
CREATE TABLE work_inputs (
  work_item_id INTEGER NOT NULL REFERENCES work_items(id),
  material_id  INTEGER NOT NULL REFERENCES materials(id),
  PRIMARY KEY (work_item_id, material_id)
);

-- 工作项依赖，取代 v1 的 task_deps。
CREATE TABLE work_deps (
  work_item_id  INTEGER NOT NULL REFERENCES work_items(id),
  depends_on_id INTEGER NOT NULL REFERENCES work_items(id),
  PRIMARY KEY (work_item_id, depends_on_id)
);

-- 工作项事件：只追加，供时间线与审计。
CREATE TABLE work_item_events (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  work_item_id INTEGER NOT NULL REFERENCES work_items(id),
  event        TEXT NOT NULL,
  actor        TEXT,
  detail_json  TEXT NOT NULL DEFAULT '{}',
  create_time  TEXT NOT NULL
);
CREATE INDEX ix_work_events_item ON work_item_events(work_item_id, id);

-- worker 心跳与当前占用的工作项。current_work_item_id 指向 work_items(id)；
-- 部分唯一索引保证一条工作项同时只能被一个 worker 占用，NULL 不参与唯一性，
-- 因此空闲 worker 可以有任意多个。
CREATE TABLE workers (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  worker_id           TEXT NOT NULL UNIQUE,
  metadata_json       TEXT NOT NULL DEFAULT '{}',
  state               TEXT NOT NULL DEFAULT 'idle' CHECK (state IN ('idle','busy')),
  current_work_item_id INTEGER REFERENCES work_items(id),
  current_lease_id    TEXT,
  first_seen          TEXT NOT NULL,
  last_seen           TEXT NOT NULL,
  create_time         TEXT NOT NULL,
  update_time         TEXT NOT NULL
);

CREATE INDEX ix_workers_last_seen ON workers(last_seen);

CREATE UNIQUE INDEX ux_workers_current_work_item ON workers(current_work_item_id)
  WHERE current_work_item_id IS NOT NULL;

-- worker 声明的能力：平台派单的唯一依据（一个 worker 一个 agent，不做 kind→agent 映射）。
CREATE TABLE worker_kinds (
  worker_id TEXT NOT NULL,
  kind      TEXT NOT NULL CHECK (kind IN ('requirement','design','breakdown','dev')),
  PRIMARY KEY (worker_id, kind)
);
