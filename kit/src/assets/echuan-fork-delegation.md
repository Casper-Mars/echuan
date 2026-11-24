# 驿传 Fork 委派协议

本文只讲“什么时候把文档任务 fork 出去、怎么交、怎么收”，不定义文档格式、章节模板或验收标准。
文档怎么写，遵循当前角色自己的 skill / persona。

## 1. 什么时候该 fork

适合 fork：

- 需要产出一篇长文档或长报告，正文会明显占用上下文；
- 子任务必须继承当前会话已经形成的上下文、决策和约束；
- 主会话不应该反复重放正文，应该只拿最终产物；
- 任务边界清楚，可以写成一份 Work Order。

不适合 fork：

- 只是短回答、小改动、单段说明；
- 当前上下文还没有进入已完成轮次，子 agent 看不到关键信息；
- 任务还需要主 agent 继续和用户来回确认；
- 没有明确的输出路径和返回契约。

## 2. fork 前检查

fork 之前必须确认：

1. 父会话已经存在，并且关键上下文已经落在**已完成轮次**里；
2. 当前正在执行的这一轮不会被子 agent 继承，所以当前任务的关键信息要写进 Work Order；
3. 输入物料有 `material_id` 的，优先传 ID，不复制整篇正文；
4. 输出文件路径明确；
5. 返回结构明确；
6. 当前 preset 提供 `subagent_fork` 工具。没有这个工具时，不要假装 fork，按角色规范直接执行或走普通委派。

## 3. Work Order 模板

```text
DOC WORK ORDER

goal: 一句话说明要产出什么
task_id: t_xxx
inputs:
  - material_id=mat_a
  - material_id=mat_b
output_path: /workspace/out/<name>.md
max_chars: 12000

must:
- 基于 inputs 和继承的已完成轮次写作
- 写入 output_path
- 按当前角色的文档规范自检
- 返回 { path, title, summary }

must_not:
- 不调用 echuan_task_report
- 不修改项目 / 任务状态
- 不派单、不验收、不再 fork
- 不处理 Work Order 之外的工作

stop:
- 文件写完并通过自检后立即返回
- 返回后不再做额外探索或优化
```

Work Order 要短、可执行、可验收；不要把整个会话复制进去。

## 4. 子 agent 的边界

子 agent 继承父会话的已完成轮次和同一 preset，但它不是主 agent：

- 只负责 Work Order 里的那一件事；
- 只写文件，不直接交平台报告；
- 不改任务状态、不写验收结论；
- 不再 fork 子 agent；
- 完成后只返回 `path / title / summary`；
- 遇到超出权限或缺少输入，直接说明限制，不自行扩大范围。

## 5. 父 agent 怎么收

父 agent 收到子 agent 的结果后：

1. 校验 `path` 存在、文件非空、标题与任务匹配；
2. 需要上传时，读文件并上传为 material；
3. 用 `echuan_task_report(material_id=...)` 提交；
4. 主会话只保留 `material_id + 摘要`，不要把正文再读回上下文；
5. 文档内容是否合格，按当前角色的文档规范和平台门禁判断，不由本 skill 定义。

## 6. 超时、失败与收手

- fork 要有超时；超时后中断子 agent，不接收半成品；
- 子 agent 返回空文件、缺字段或未通过自检，视为失败，重试或回报卡点；
- 子 agent 跑偏到 Work Order 之外，父 agent 应中断并要求按 Work Order 重做；
- 失败时不要用半成品直接 `echuan_task_report`。

## 7. 反模式

- 没有上下文就 fork，子 agent 只能猜；
- 把整段对话粘进 Work Order；
- 让子 agent 直接调用 `echuan_task_report`；
- 子 agent 继续派单，形成嵌套 fork；
- fork 后父 agent 又把正文读回来，重新制造重放；
- 用 fork 处理短任务，成本高于直接做。

## 8. 与角色规范的关系

本 skill 只定义 fork 协议：

- 需求文档怎么写：看 PM 角色规范；
- 技术方案怎么写：看 CTO 角色规范；
- 开发报告怎么写：看 Developer 角色规范；
- 验收标准是什么：看对应角色规范和平台门禁。

本 skill 不重复、不覆盖这些定义。
