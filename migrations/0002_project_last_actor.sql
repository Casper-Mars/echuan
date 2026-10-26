-- 0002_project_last_actor.sql — 给 projects 补 last_actor（任务-06 / §6）
--
-- 背景：§3.2 的 projects 建表语句没有 last_actor，但任务-06 要求每次状态
-- 变更在同一事务里写 status + reason + last_actor + update_time + version；
-- §6 也把 last_actor + update_time 作为「最近谁动过」的唯一载体。tasks 表
-- 本来就有该列，补上这一列让项目与任务留痕方式一致。
--
-- 可空：既有行保持 NULL，读取时渲染为 null；不修改 0001_init.sql。
ALTER TABLE projects ADD COLUMN last_actor TEXT;
