-- 0006_tasks_review_rejection.sql — 任务批次驳回意见
--
-- 任务-02 为任务评审（tasks_review=5 / pending_done=6）补上 reject 分支：
--   tasks_review_reason        驳回原因（trim 后的请求文本）
--   tasks_review_rejected_at   驳回时间（与 update_time 同格式）
--
-- 两列均可空、无默认值、不回填历史行：NULL 表示该项目当前没有被驳回。
-- 同一事务里的 approve 或任意加卡/改卡/删卡都会把两列置回 NULL。

ALTER TABLE projects ADD COLUMN tasks_review_reason TEXT;
ALTER TABLE projects ADD COLUMN tasks_review_rejected_at TEXT;
