# 飞书机器人（IM）运维手册

面向部署后的日常运维。设计背景见技术方案：<https://www.feishu.cn/docx/VFphdksaSoeJyLxLNibcutpJn8g>

## 开关与配置

`im_enabled = false` 时平台行为与接入前完全一致：不建连接、不起轮询、不发任何请求。

| 配置键 | 环境变量 | 说明 |
|---|---|---|
| `im_enabled` | `ECHUAN_IM_ENABLED` | 总开关 |
| `im_app_id` | `ECHUAN_IM_APP_ID` | 自建应用 ID |
| `im_app_secret` | `ECHUAN_IM_APP_SECRET` | **只放环境变量**，不落配置文件 |
| `im_allowed_chat_ids` | `ECHUAN_IM_CHAT_IDS` | 卡片操作白名单，逗号分隔 |
| `im_poll_interval` | `ECHUAN_IM_POLL_INTERVAL` | 轮询间隔，默认 30s |
| `im_quiet_hours` | `ECHUAN_IM_QUIET_HOURS` | 免打扰时段，默认 `23:00-08:00` |
| `im_max_cards` | `ECHUAN_IM_MAX_CARDS` | 同时维护的卡片上限，默认 10 |
| `im_doc_folder_token` | `ECHUAN_IM_DOC_FOLDER` | 材料文档目录；为空则卡片上的文件名不带链接 |

> 注意：配置键是**扁平**的（`im_enabled` 而不是 `[im]` 段）。本仓库的 TOML 解析器忽略 section 头，写段会让键静默丢失。

## 启动与重启

```bash
cd ~/project/opc/sanding/deployment
docker compose build echuan && docker compose up -d echuan
docker logs -f --tail 50 echuan | grep '"msg":"im'
```

期望日志顺序：

```
im runtime starting  app_id=... chats=1 poll_interval=30s
im long connection ready
im card rendered  task=t_xxx message_id=om_xxx action=send set_size=3 links_total=0 links_missing=3
```

重启不会重发历史卡片：`im_card` 里已有 message_id，指纹没变就跳过。

## 卡片长什么样（值班先看这条）

验收卡的**卡片头是任务标题**，不是任务号：

```
┌ 补齐库存接口                       ← header.title = 任务标题（逐字，不截断）
│ 驿传 · 交付验收                    ← header.subtitle = 项目名 · 卡类型
├ 已等待 2 小时 · dev-1 第 2/3 次提交  ← 正文首行 = 等待时长 + worker + 尝试次数
│ [方案-v2.md](https://…/docx/aaa)   ← 一行一个文件名，顺序与 Web 附件区一致
│ 接口清单.xlsx                      ← 这一份还没导入成功：只有文件名，没有链接
│ [交付说明.md](https://…/docx/ccc)
├ 驳回理由（通过可留空） [验收通过] [打回]   ← 表单与按钮，与链接是否就绪无关
└ 任务 t_01M84QA8E3S4ZT47MD3P76RSF4 · 报告 r0099aabb   ← 灰色 12px 辅助信息行
```

- **任务号只在最后一行**（灰色小字，可复制），排查时不要再按卡片头找 `t_xxx`。
- 链接文字就是 Web 任务详情附件区的显示名；点开就是该文件的云文档。
- 一份文件导入失败/未就绪时，**只有那一行没有链接**，不出现「生成中 / 失败 / 暂无」之类提示文案；审批按钮始终可用。
- 处理完的卡片：头部仍是任务标题，正文变成 `处理结果：已处理` + 链接行 + 辅助信息行，表单消失。

## 故障排查

### 卡片一直没出现

1. `im_enabled` 是否为 true（`docker exec echuan printenv | grep ECHUAN_IM`）
2. 日志里有没有 `im long connection ready`；没有就是长连接没建起来
3. `/v1/inbox` 是否有待办——空的话本来就不会发卡
4. 是否在免打扰时段（默认 23:00–08:00 不推新卡）
5. 日志里是否有 `im attachments resolve failed`（连续出现说明附件投影读不出来，本轮跳过、下轮自动重试）
6. 日志里是否有 `im v3: send card` / `im v3: patch card`

### 长连接反复重连

`OnError` 会打出 `im long connection error`。常见原因：服务器时间不同步、`app_secret` 被轮换、应用被停用。先确认 `app_id/app_secret` 能换到 token：

```bash
curl -s -X POST https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal \
  -H 'Content-Type: application/json' \
  -d '{"app_id":"cli_xxx","app_secret":"***"}' | head -c 200
```

### 点按钮没反应

- 后台「事件与回调 → 回调配置」必须订阅**新版** `card.action.trigger`；旧版 `card.action.trigger_v1` 走不了长连接
- 卡片发出超过 14 天后不可交互，会收到「该卡片已过期」
- 白名单不含该会话时会被丢弃，日志里是 `im card action from unknown chat`

### 提示「内容已更新，请回 Web 查看」

卡片渲染时的项目 version 与当前不一致（Agent 在你点之前改过）。回 Web 看最新内容再决定，不需要重试。

### 卡片上某个文件名没有链接

验收卡**先发卡片与审批按钮**，云文档就绪后再把对应行补成链接（同一 `message_id` 上 patch），所以刚发出时没有链接是正常的。
一张卡上的行数 = 该任务「当前版本」的附件数（与 Web 任务详情的附件区同集合、同顺序；同一份正文的旧版本不会上卡）。

逐行判定顺序：

1. `ECHUAN_IM_DOC_FOLDER` 为空 → 文档功能整体关闭：启动日志 `im doc folder token is empty`，
   之后每张卡记一条 `im doc fill skipped reason=docs_disabled`，**所有行都是纯文件名**，不会再有导入请求。
2. 任务没有附件（`attachments` 为空）→ 没有可导入的材料，卡片只有状态行与辅助信息行。
3. 导入失败（错 app_secret、断网、目录无权限、素材为空）→ 日志成对出现：
   `im doc import attempt`（`result=failed`、`duration_ms`）+ `im doc import failed`，
   两者都带 `task` / `material` / `material_title` / `attempt` / `error`。失败不写 `im_doc`，下一轮会重试。
4. 同一材料在同一张卡上**最多尝试 2 次**；到上限后日志只出现 `im doc fill skipped reason=attempt_cap`，
   该行保持纯文件名，直到附件集合/状态/驳回原因变化（指纹变化会清零计数）。
5. 导入成功 → `im doc import attempt result=ok`，随后一条 `im card rendered action=patch`
   （`links_total` 增加、`links_missing` 减少）；同一材料之后的轮询命中 `im_doc` 缓存，不再导入。
6. 应用需要 `docs:document:import` + `drive:drive`。

> 一轮填充结束还是一条 `im doc fill finished`（`set_size` / `links_total` / `links_missing` / `attempts_used`）。
> 自洽校验：`links_total + links_missing == set_size`。**卡片改版后不再有「看全文」按钮，也没有 `open_doc` 组件。**

### 终态卡标题变成了「交付验收」

关单时进程内缓存里没有该任务的标题，回查也失败了，日志是 `im card close meta missing`（带 `task` / `error`）。
此时卡片头只有卡类型文字（**不会退回任务号**），需要在群里人工补一句任务名，并查该任务是否已被删除。

### 日志速查

| `msg` | 关键字段 | 含义 / 处置 |
|---|---|---|
| `im card rendered` | `task` `message_id` `action`(send/patch) `set_size` `links_total` `links_missing` `links_missing_ids` `card_bytes` `fingerprint` `title_len` | 每次发卡或更新一条；`links_missing > 0` 超过 30 分钟看导入配额与 `im_doc` |
| `im card title long` | `task` `title_len` | 标题 > 60 字符；客户端最多显示 4 行后自己省略，服务端**不截断**，属观察信号 |
| `im card close meta missing` | `task` `error` | 关单取不到标题，卡片头退回卡类型文字 |
| `im doc import attempt` | `task` `material` `material_title` `attempt` `result` `duration_ms` `error` | 每份每次尝试一条 |
| `im doc import failed` | `task` `material` `material_title` `attempt` `error` | 保留原名，runbook 按它排障 |
| `im doc fill skipped` | `task` `material` `attempt` `reason`(attempt_cap / docs_disabled) | 解释「这一行为什么没链接」 |
| `im doc fill finished` | `task` `set_size` `links_total` `links_missing` `attempts_used` | 一轮填充收尾 |
| `im attachments resolve failed` | `task` `error` | 附件投影读不出来，本轮跳过该卡（旧卡保持），连续 3 轮查 DB 可读性 |
| `im currentver suspect ref` | `task` `material` `role` `kind` | 附件缺字段（宁多不藏，仍会渲染该行） |

同一 `material` 24 小时内出现 ≥4 条 `im doc import failed` 时按配置型故障处理（app_secret / 文件夹权限 / 断网），重试无意义。

### 文档打不开（无权限）

应用云空间的文档，用户**无法在网页端自助申请权限**。需把用户加为协作者：

```bash
# 对文件夹授权一次即可，之后该目录下的文档继承
curl -s -X POST "https://open.feishu.cn/open-apis/drive/v1/permissions/<folder_token>/members?type=folder" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"member_type":"email","member_id":"user@example.com","perm":"edit"}'
```

## 数据与迁移

| 表 | 用途 |
|---|---|
| `im_card` | 待办 → 消息映射；`closed_at` 非空表示已终态 |
| `im_action_log` | 幂等记录，键为 `message_id|action|version` |
| `im_doc` | 材料版本 → 文档 URL（一个版本一篇） |

迁移版本：`0003` 建 `im_card`，`0004` 建 `im_action_log`，`0005` 建 `im_doc`。

**迁移一旦应用就不能改**：`user_version` 已写过的文件不会重跑，改动会静默不生效。新增表一律新建迁移。

## 回滚

```bash
docker compose down echuan          # 只停这一个服务
# 把镜像指回旧 tag，或用 git 回退后重建
ECHUAN_IM_ENABLED=false docker compose up -d echuan   # 或者先只关 IM
```

关掉 `im_enabled` 是最轻的回滚：卡片不再产生，平台其余功能不受影响。

回滚后的观感变化（预期内）：已发出的多链接卡在下一次指纹变化时会被旧代码 patch 回「看全文」形态，
卡片头回到任务号；新导入的文档名不会改回任务标题（`im_doc` 命中，不重导入）。
