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
| `im_doc_folder_token` | `ECHUAN_IM_DOC_FOLDER` | 材料文档目录；为空则卡片不带全文链接 |

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
im card sent  subject=APP-1 message_id=om_xxx
```

重启不会重发历史卡片：`im_card` 里已有 message_id，指纹没变就跳过。

## 故障排查

### 卡片一直没出现

1. `im_enabled` 是否为 true（`docker exec echuan printenv | grep ECHUAN_IM`）
2. 日志里有没有 `im long connection ready`；没有就是长连接没建起来
3. `/v1/inbox` 是否有待办——空的话本来就不会发卡
4. 是否在免打扰时段（默认 23:00–08:00 不推新卡）

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

### 「看全文」按钮不出现

- `ECHUAN_IM_DOC_FOLDER` 为空 → 启动日志有 `im doc folder token is empty`
- 导入失败 → 日志 `im doc import failed`，卡片仍保留摘要，审批不受影响
- 应用需要 `docs:document:import` + `drive:drive`

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
