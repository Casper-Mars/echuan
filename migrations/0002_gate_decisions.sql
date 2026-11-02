-- 0002_gate_decisions.sql — 门禁决策记录（技术方案 §5，task-0011）
--
-- 每一次人工批准/驳回都写一条，记录「批的是哪一版资料、谁批、为什么」。
-- material_id 指向 materials 表的物理 id；同一条版本链的每一版都有独立 id，
-- 因此资料升到 v2 后，旧记录仍然精确指向 v1，不需要额外存版本号。
--
-- material_id 允许 NULL：计划批次在异常/历史路径下可能没有 breakdown 结果
-- 资料；正常路径（项目 requirement/spec、单卡 result、breakdown 计划）都会写。
--
-- 自动结清（settlePhaseWorkItem）不是人工决策，不写本表。
--
-- 迁移 runner 在单个事务内回写 PRAGMA user_version = 2；本文件不要写 PRAGMA。

CREATE TABLE gate_decisions (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  subject_type  TEXT NOT NULL CHECK (subject_type IN ('project', 'work_item')),
  subject_id    TEXT NOT NULL,
  gate          TEXT NOT NULL,
  material_id   INTEGER REFERENCES materials(id),
  decision      TEXT NOT NULL CHECK (decision IN ('approve', 'reject')),
  decided_by    TEXT NOT NULL,
  reason        TEXT,
  create_time   TEXT NOT NULL
);

CREATE INDEX ix_gate_decisions_subject ON gate_decisions(subject_type, subject_id, id);
CREATE INDEX ix_gate_decisions_material ON gate_decisions(material_id);
