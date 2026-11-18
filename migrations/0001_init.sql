-- 0001_init.sql — 驿传平台 V3 初始表结构（技术方案 v2.0）
--
-- V3 是全新契约：项目只是容器，任务是唯一执行单元，流程由 task_deps 表达。
-- 只有这一个初始 schema 文件，不写增量迁移；旧 v2 库（user_version=6）会被
-- SchemaVersionError 拒绝，开发库直接重建。
--
-- 表顺序：products → materials → projects → tasks → task_deps → attachments →
-- gate_decisions → task_events → workers → worker_kinds → im_card →
-- im_action_log → im_doc。父表在前，避免外键目标的前向引用。
--
-- 枚举一律 INTEGER，从 1 开始；0 不使用（Go 零值会被 CHECK 拦住）。
-- 时间字段统一为 RFC3339 毫秒 UTC 字符串，SQL 按字典序比较。

-- ── 产品 ────────────────────────────────────────────────────────────────
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

-- ── 资料：内容寻址 + 版本链 ─────────────────────────────────────────────
-- kind: 1=requirement 2=design 3=plan 4=report 5=context 6=other
CREATE TABLE materials (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  material_id          TEXT NOT NULL UNIQUE,
  kind                 INTEGER NOT NULL CHECK (kind BETWEEN 1 AND 6),
  version              INTEGER NOT NULL CHECK (version >= 1),
  title                TEXT NOT NULL,
  sha256               TEXT NOT NULL,
  size                 INTEGER NOT NULL CHECK (size >= 0),
  path                 TEXT NOT NULL,
  uploaded_by          TEXT NOT NULL,
  previous_material_id INTEGER REFERENCES materials(id),
  create_time          TEXT NOT NULL,
  update_time          TEXT NOT NULL
);
CREATE INDEX ix_materials_chain ON materials(previous_material_id);

-- ── 项目：只有名称 / 产品 / 背景上下文 / 开关状态 ──────────────────────
-- status: 1=open 2=closed
CREATE TABLE projects (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id  TEXT NOT NULL UNIQUE,
  product_id  INTEGER NOT NULL REFERENCES products(id),
  seq_no      INTEGER NOT NULL CHECK (seq_no >= 1),
  name        TEXT NOT NULL,
  context_md  TEXT NOT NULL DEFAULT '',
  status      INTEGER NOT NULL DEFAULT 1 CHECK (status IN (1, 2)),
  create_time TEXT NOT NULL,
  update_time TEXT NOT NULL,
  closed_at   TEXT,
  last_actor  TEXT,
  UNIQUE (product_id, seq_no)
);
CREATE INDEX ix_projects_status ON projects(status, update_time);
CREATE INDEX ix_projects_product ON projects(product_id, seq_no);

-- ── 任务：唯一执行与派单单元 ───────────────────────────────────────────
-- kind:   1=requirement 2=design 3=breakdown 4=dev
-- status: 1=pending 2=ready 3=in_progress 4=in_review 5=done 6=failed 7=cancelled
-- status=3 必须同时持有租约（lease_id/lease_until/worker_id 非空）。
CREATE TABLE tasks (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id        TEXT NOT NULL UNIQUE,
  project_id     INTEGER NOT NULL REFERENCES projects(id),
  parent_task_id INTEGER REFERENCES tasks(id),
  kind           INTEGER NOT NULL CHECK (kind IN (1, 2, 3, 4)),
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
CREATE INDEX ix_tasks_queue ON tasks(kind, status, priority DESC, id);
CREATE INDEX ix_tasks_lease ON tasks(status, lease_until);
CREATE INDEX ix_tasks_project ON tasks(project_id, kind, id);

-- 文档类同项目同 kind 只允许一条活跃任务（pending/ready/in_progress/in_review）。
CREATE UNIQUE INDEX ux_tasks_active_doc ON tasks(project_id, kind)
  WHERE kind IN (1, 2, 3) AND status IN (1, 2, 3, 4);

-- 一个 worker 同时只持一条租约。
CREATE UNIQUE INDEX ux_tasks_worker_lease ON tasks(worker_id)
  WHERE status = 3 AND worker_id IS NOT NULL;

-- ── 依赖 DAG ───────────────────────────────────────────────────────────
CREATE TABLE task_deps (
  task_id            INTEGER NOT NULL REFERENCES tasks(id),
  depends_on_task_id INTEGER NOT NULL REFERENCES tasks(id),
  PRIMARY KEY (task_id, depends_on_task_id),
  CHECK (task_id <> depends_on_task_id)
);
CREATE INDEX ix_task_deps_upstream ON task_deps(depends_on_task_id);

-- ── 附件：唯一交付载体；必须且只能挂项目或任务 ────────────────────────
-- role: 1=input 2=output
CREATE TABLE attachments (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id  INTEGER REFERENCES projects(id),
  task_id     INTEGER REFERENCES tasks(id),
  material_id INTEGER NOT NULL REFERENCES materials(id),
  role        INTEGER NOT NULL CHECK (role IN (1, 2)),
  sort_no     INTEGER NOT NULL DEFAULT 0,
  create_time TEXT NOT NULL,
  CHECK ((project_id IS NULL) <> (task_id IS NULL))
);
CREATE INDEX ix_attachments_task ON attachments(task_id, role, id);
CREATE INDEX ix_attachments_project ON attachments(project_id, role, id);
CREATE INDEX ix_attachments_material ON attachments(material_id);

-- ── 评审决策：只追加；决策对象是任务及某一版资料 ──────────────────────
-- decision: 1=approve 2=reject
CREATE TABLE gate_decisions (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id     TEXT NOT NULL REFERENCES tasks(task_id),
  gate        INTEGER NOT NULL CHECK (gate IN (1, 2, 3, 4)),
  material_id INTEGER REFERENCES materials(id),
  decision    INTEGER NOT NULL CHECK (decision IN (1, 2)),
  decided_by  TEXT NOT NULL,
  reason      TEXT,
  create_time TEXT NOT NULL
);
CREATE INDEX ix_gate_decisions_task ON gate_decisions(task_id, id);
CREATE INDEX ix_gate_decisions_material ON gate_decisions(material_id);

-- ── 任务事件：只追加，供时间线与审计 ─────────────────────────────────
-- event: 1=created 2=claimed 3=renewed 4=reported 5=approved 6=rejected
--        7=failed 8=cancelled 9=flow.auto_create 10=promoted 11=reset
CREATE TABLE task_events (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id     INTEGER NOT NULL REFERENCES tasks(id),
  event       INTEGER NOT NULL CHECK (event BETWEEN 1 AND 11),
  actor       TEXT,
  detail_json TEXT NOT NULL DEFAULT '{}',
  create_time TEXT NOT NULL
);
CREATE INDEX ix_task_events_task ON task_events(task_id, id);

-- ── worker 心跳与当前租约 ────────────────────────────────────────────
-- state: 1=idle 2=busy
CREATE TABLE workers (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  worker_id        TEXT NOT NULL UNIQUE,
  metadata_json    TEXT NOT NULL DEFAULT '{}',
  state            INTEGER NOT NULL DEFAULT 1 CHECK (state IN (1, 2)),
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

-- worker 声明的能力：平台按 kind 派单的唯一依据。
CREATE TABLE worker_kinds (
  worker_id TEXT NOT NULL,
  kind      INTEGER NOT NULL CHECK (kind IN (1, 2, 3, 4)),
  PRIMARY KEY (worker_id, kind)
);

-- ── 飞书卡片投影与动作幂等 ───────────────────────────────────────────
-- subject_kind: 1=project 2=task
CREATE TABLE im_card (
  subject_kind INTEGER NOT NULL CHECK (subject_kind IN (1, 2)),
  subject_id   TEXT NOT NULL,
  chat_id      TEXT NOT NULL,
  message_id   TEXT NOT NULL,
  fingerprint  TEXT NOT NULL,
  version      INTEGER NOT NULL DEFAULT 0,
  sent_at      TEXT NOT NULL,
  rendered_at  TEXT NOT NULL,
  closed_at    TEXT,
  PRIMARY KEY (subject_kind, subject_id)
);
CREATE INDEX ix_im_card_open ON im_card(closed_at);
CREATE UNIQUE INDEX ux_im_card_message ON im_card(message_id);

CREATE TABLE im_action_log (
  idem_key    TEXT PRIMARY KEY,
  subject     TEXT NOT NULL,
  action      TEXT NOT NULL,
  result      TEXT NOT NULL,
  create_time TEXT NOT NULL
);

CREATE TABLE im_doc (
  material_id TEXT PRIMARY KEY,
  doc_token   TEXT NOT NULL,
  url         TEXT NOT NULL,
  create_time TEXT NOT NULL
);
