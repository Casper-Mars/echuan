-- 0006：项目开发分支。
--
-- 一个项目对应一条 feature 分支（dsh-agent-dev 0.6.0 起的开发流程），派单时
-- 随上下文下发给 dev worker。空字符串表示尚未生成：创建项目时由平台按
-- feat/<project_id>-<title slug> 生成，存量项目在首次派单时惰性回填。
ALTER TABLE projects ADD COLUMN dev_branch TEXT NOT NULL DEFAULT '';

-- 同一产品下分支名唯一；空值（尚未生成）不参与约束。
CREATE UNIQUE INDEX ux_projects_dev_branch
  ON projects(product_id, dev_branch)
  WHERE dev_branch <> '';
