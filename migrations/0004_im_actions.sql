-- 0004_im_actions.sql — 卡片动作幂等与反查索引
--
-- 卡片回调是同步的，飞书不补推；但用户会连点、客户端也会重放，所以同一次
-- 点击必须只生效一次：idem_key = message_id + action + 渲染时的项目 version。
--
-- 回调里只有 message_id，反查卡片需要唯一索引，否则一次点击会扫全表。
--
-- 迁移 runner 在单个事务内回写 PRAGMA user_version = 4；本文件不要写 PRAGMA。

CREATE TABLE im_action_log (
  idem_key    TEXT PRIMARY KEY,
  subject     TEXT NOT NULL,
  action      TEXT NOT NULL,
  result      TEXT NOT NULL,
  create_time TEXT NOT NULL
);

CREATE UNIQUE INDEX ux_im_card_message ON im_card(message_id);
