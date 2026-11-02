-- 0001_init.sql — 驿传平台初始表结构（技术方案 §3.2）
--
-- 由 internal/store 的迁移执行器在单个事务内执行，文件内不要写
-- PRAGMA user_version：版本号由代码统一回写。
--
-- 表顺序刻意调整为 products → materials → projects → tasks → task_deps：
-- projects.requirement_material_id / spec_material_id 与
-- tasks.report_material_id 都引用 materials(id)，先建父表可以避免对外键
-- 目标的前向引用，同时字段与文档完全一致。

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

CREATE TABLE projects (
  id                      INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id              TEXT NOT NULL UNIQUE,
  product_id              INTEGER NOT NULL REFERENCES products(id),
  seq_no                  INTEGER NOT NULL,
  title                   TEXT NOT NULL,
  requirement_material_id INTEGER REFERENCES materials(id),
  spec_material_id        INTEGER REFERENCES materials(id),
  status                  INTEGER NOT NULL DEFAULT 0 CHECK (status BETWEEN 0 AND 7),
  reason                  TEXT,
  version                 INTEGER NOT NULL DEFAULT 1,
  create_time             TEXT NOT NULL,
  update_time             TEXT NOT NULL,
  UNIQUE (product_id, seq_no)
);
CREATE INDEX ix_projects_inbox ON projects(status, update_time);

CREATE TABLE tasks (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id            TEXT NOT NULL UNIQUE,
  project_id         INTEGER NOT NULL REFERENCES projects(id),
  seq_no             INTEGER NOT NULL,
  title              TEXT NOT NULL,
  body_md            TEXT NOT NULL,
  priority           INTEGER NOT NULL DEFAULT 0,
  status             INTEGER NOT NULL DEFAULT 0 CHECK (status BETWEEN 0 AND 7),
  attempt_count      INTEGER NOT NULL DEFAULT 0,
  max_attempts       INTEGER NOT NULL DEFAULT 3,
  lease_id           TEXT,
  lease_until        TEXT,
  last_actor         TEXT,
  report_material_id INTEGER REFERENCES materials(id),
  reason             TEXT,
  version            INTEGER NOT NULL DEFAULT 1,
  create_time        TEXT NOT NULL,
  update_time        TEXT NOT NULL,
  UNIQUE (project_id, seq_no)
);
CREATE INDEX ix_tasks_queue ON tasks(status, priority DESC, seq_no);
CREATE INDEX ix_tasks_lease ON tasks(status, lease_until);

CREATE TABLE task_deps (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id     INTEGER NOT NULL REFERENCES tasks(id),
  depends_on  INTEGER NOT NULL REFERENCES tasks(id),
  create_time TEXT NOT NULL,
  update_time TEXT NOT NULL,
  UNIQUE (task_id, depends_on)
);
