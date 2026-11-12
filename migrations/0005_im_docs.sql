-- 0005_im_docs.sql — 材料到飞书文档的映射缓存
--
-- 一个材料版本对应一篇飞书文档。缓存 material_id → docx token/url，避免同一
-- 版本被重复导入（导入是三次 API 调用，且计入每日配额）。
--
-- 迁移 runner 在单个事务内回写 PRAGMA user_version = 5；本文件不要写 PRAGMA。

CREATE TABLE im_doc (
  material_id TEXT PRIMARY KEY,
  doc_token   TEXT NOT NULL,
  url         TEXT NOT NULL,
  create_time TEXT NOT NULL
);
