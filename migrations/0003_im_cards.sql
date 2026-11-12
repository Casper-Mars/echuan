-- 0003_im_cards.sql — 飞书卡片投影（技术方案 §5.8）
--
-- 记录「哪个待办对应聊天里的哪张卡」。这张表是纯投影：任何一次写操作都会
-- 回源读最新状态，这里的 fingerprint / version 只用来判断是否需要重绘。
--
-- message_id 是飞书消息 id；卡片超过 14 天不可 patch，所以 sent_at 必须落库，
-- 由轮询器判断是否重发。
--
-- closed_at 非空表示该待办已消失、卡片已渲染为终态，不再参与 diff。
--
-- 迁移 runner 在单个事务内回写 PRAGMA user_version = 3；本文件不要写 PRAGMA。

CREATE TABLE im_card (
  subject_kind TEXT NOT NULL CHECK (subject_kind IN ('project', 'work_item')),
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

-- 幂等：卡片回调会重复投递（用户连点、客户端重试），同一次点击只能生效一次。
-- idem_key = message_id + action + 渲染时的项目 version。
CREATE TABLE im_action_log (
  idem_key    TEXT PRIMARY KEY,
  subject     TEXT NOT NULL,
  action      TEXT NOT NULL,
  result      TEXT NOT NULL,
  create_time TEXT NOT NULL
);

-- 回调只带 message_id，必须能反查卡片对应的待办。
CREATE UNIQUE INDEX ux_im_card_message ON im_card(message_id);
