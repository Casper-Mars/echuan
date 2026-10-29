-- 0005_project_status_range.sql — projects.status 放宽到 0–8（九态状态机）
--
-- 任务-01 把项目状态机扩展为九个状态：
--   0 requirement_draft / 1 requirement_review / 2 spec_draft /
--   3 spec_review / 4 tasks_draft / 5 tasks_review / 6 pending_done /
--   7 completed / 8 cancelled
-- 0001_init.sql 的 CHECK 只允许 0–7，无法写入 cancelled(8)。SQLite 不能直接
-- 修改 CHECK 约束，只能按官方推荐步骤重建表。
--
-- defer_foreign_keys(ON) 把外键检查推迟到提交时刻：tasks.project_id 与
-- project_specs.project_id 都引用 projects(id)，本迁移在同一事务里以相同 id
-- 重建 projects，因此提交时引用仍然成立。
--
-- 除 CHECK 范围外，列、默认值、唯一约束与索引均与 0001(+0002) 完全一致。

PRAGMA defer_foreign_keys = ON;

CREATE TABLE projects_new (
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
  UNIQUE (product_id, seq_no)
);

INSERT INTO projects_new (id, project_id, product_id, seq_no, title,
                          requirement_material_id, spec_material_id, status,
                          reason, version, create_time, update_time, last_actor)
SELECT id, project_id, product_id, seq_no, title,
       requirement_material_id, spec_material_id, status,
       reason, version, create_time, update_time, last_actor
  FROM projects;

DROP TABLE projects;

ALTER TABLE projects_new RENAME TO projects;

CREATE INDEX ix_projects_inbox ON projects(status, update_time);
