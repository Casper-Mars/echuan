# 驿传任务执行指南

本指南约束 Agent 在驿传（echuan）平台接到任务卡之后的完整行为：怎么开工、先读什么上下文、什么时候必须汇报、失败怎么办、什么时候需要续租、哪些门禁不能碰。按下面的顺序执行即可，不需要额外的启动许可。

## 1. 收到派单：直接开工

- 任务来自 Worker 派单：可能是 `echuan_task_claim` 领到的卡，也可能是 Worker 心跳投递到当前会话的派单消息。**收到派单直接开工**，不要反问能否开始，不要停在原地等人点头。
- 派单消息里通常已带 `task_id`、`lease_id` 与方案摘要，够用就直接进入第 3 步；字段缺失时用平台只读工具补齐，不要回头向派单人索要。
- 仓库不在时按 `git_url` 拉取；每张卡在自己的 worktree 里做，按第 2 节创建，除此之外不另建平行目录、不改与本卡无关的模块。
- 一张卡对应一次完整交付：同步 → 实现 → 自检 → 推送 → 汇报。中途不要换卡，也不要顺手处理别的卡。

## 2. 开工前：先同步远程，再建 worktree

每张卡都在独立 worktree 里做，且必须基于**远程最新**的 base 分支。顺序固定，不许跳步：

```bash
# 1) 确认在仓库内（不在就按 git_url 克隆后进入）
git rev-parse --show-toplevel

# 2) 同步远程：每张卡开工前必做，禁止跳过
git fetch --prune origin

# 3) 确认 base（默认 origin/HEAD，取不到就 main）
git symbolic-ref refs/remotes/origin/HEAD --short

# 4) 从远程最新 base 建 worktree + 任务分支
git worktree add ../wt-<task_id> -b task/<task_id> origin/main
```

- 分支名固定 `task/<task_id>`，task_id 里的非法字符换成 `-`。
- base 必须用 `origin/<base>` 引用，**不得用本地 `main`/`master`**，否则会基于过期提交，白改。默认分支不是 main 时，把命令里的 `origin/main` 换成 `origin/<base>`。
- 一张卡一个 worktree，不复用上一张卡的。上一张已合并就先 `git worktree remove ../wt-<旧task_id>`；未合并则保留供 review，新卡仍从最新 `origin/<base>` 起。
- `fetch` 失败（网络/权限）就停下，按第 5 节汇报卡在哪，不要凭本地状态开工。
- 建好后 `cd ../wt-<task_id>`，后续所有命令都在这里执行。

## 3. 开工前先读上下文

1. 不知道仓库地址时先 `echuan_product_list` 找 `git_url`，不要猜路径。
2. `echuan_project_get`：先看项目概况与**当前生效的方案**（spec 版本与 material_id）。需求以这里的方案为准，不要凭任务标题猜需求；正文按需 `echuan_material_get`。
3. `echuan_material_get`：需要方案全文、附件或资料正文时取全文再动手。只看到标题就开始改代码，是最常见的返工来源。
4. `echuan_task_get`：需要单卡细节（正文、验收标准、历史尝试次数、当前状态）时读它。
5. 三者读完后，先定下「改哪里、怎么验证」，再落笔改文件；读到的约束与派单描述冲突时，以平台方案为准，并在报告里点明这处冲突。
6. 上下文只读一次就够：同一张卡不要反复拉同一份资料，把结论留在自己的计划里。

## 4. 干完必须汇报

交付 = 代码已提交并推送到远程。**没 push 成功就等于没交付**，不得调 `echuan_task_report` 报「已完成」。顺序固定：自检通过 → commit → push → 校验 → 汇报。

```bash
git add -A
git commit -m "<type>(<scope>): <一句话>"
git push -u origin task/<task_id>

# 校验：远程分支指向的 commit 必须等于本地 HEAD
git ls-remote --heads origin task/<task_id>
git rev-parse HEAD
```

- 两条输出的 commit hash 必须一致；远程查不到分支或 hash 不同，一律视为 push 未完成，按第 5 节汇报。
- 禁止 `push --force`；禁止直接推 `main`/`master`；`git status --porcelain` 必须为空，未跟踪文件不得留在 worktree。
- 仓库有 `gh` 且已登录时，push 后建 PR（base = 远程默认分支），把链接写进报告；没有 `gh` 就写 compare 链接。

任何一次交付都必须调 `echuan_task_report(task_id, lease_id, summary_md)`。只在会话里说一句「做完了」不算交付，平台侧看不到。

`summary_md` 固定下列内容，缺一段视为未完成：

1. **改了什么**：列出改动的文件或模块，以及行为上的变化，一条一句，不要写「优化了一下」这种空话。
2. **怎么自检**：写实际执行过的命令（构建、类型检查、测试）与结果，贴关键输出行；没跑过的命令不要写。
3. **git 交付**：分支 `task/<task_id>`；commit 短 hash；远程分支 `origin/task/<task_id>`（已推送，附 `ls-remote` 与 `rev-parse HEAD` 一致的校验结果）；PR 或 compare 链接。
4. **遗留风险**：没覆盖的边界、未验证的假设、需要后续跟进的事项；确实没有就写「无」，不要留空。

汇报不是终局：平台要求补充信息时，按同样格式再汇报一次，不要只回一句「已补充」。
提交成功后不要用旧 `lease_id` 重复提交同一张卡；等待平台侧结论即可。

## 5. 做不下去也要汇报

卡住时同样调 `echuan_task_report`，把情况写清楚：

- **卡在哪**：具体的报错、失败的命令、缺失的前置条件（含 `git fetch` 或 `git push` 的原始输出）。
- **缺什么**：权限、数据、依赖、环境，还是需要人拍板的决策。
- **需要谁决策**：指向具体角色（平台侧/需求方/运维），不要写「等通知」。

禁止静默失败，禁止把没做完的活写成「已完成」，也禁止为了凑一次汇报而伪造自检结果。

## 6. 租约：只有手动模式才要自己续租

- **手动模式**：自己调 `echuan_task_claim` 领到的卡，租约由自己负责。任务耗时可能超过租约 TTL 时，在到期前调 `echuan_task_renew(task_id, lease_id)`（任务卡续租）拿新的 `lease_id`，之后**一律用新 lease_id** 提交报告。
- **Worker 派发模式**：心跳循环派发的卡由心跳托管租约，**不要**调用续租工具，否则会和心跳抢租约语义，把卡搞成 lease_lost。
- 续租返回 lease_lost：说明租约已失效，之前的工作结果不能再提交；重新领卡，不要拿旧 lease_id 硬提交。
- 领卡结果里带 `ttl_seconds` 与到期时间；剩余时间不够收尾时就该续租，别等过期再救。
- 短任务（明显在 TTL 内能收尾）不必续租，少一次调用少一次失败面。

## 7. 门禁红线：只实现，不裁决

- 不验收：验收结论由平台侧人工给出，Agent 不写「已通过验收」这类判断。
- 不返工判定、不取消任务、不改任务状态与优先级，也不自造状态流转。
- 不调用任何门禁类接口；报告里只陈述事实（改了什么、跑过什么），不做结论性裁决。
- 任务卡的状态变化只由平台产生。Agent 的职责只有两件事：实现、汇报。

## 8. 常见错误（出现即按返工处理）

- 只读标题就改代码：回第 3 步补齐 `echuan_project_get` / `echuan_material_get`。
- 干完只在会话里说一句「完成」：平台没有记录，等于没交，必须调 `echuan_task_report`。
- 干完不 push：review 的人拿不到代码，按未交付处理，补 push 加校验后重新汇报。
- 第二张卡不 `fetch` 就建 worktree，或用本地 `main` 建分支：base 已过期，删掉 worktree 与分支，回第 2 节重来。
- `push --force` 或直接推 `main`/`master`：禁止，会覆盖他人提交。
- 手动领的卡快到期还在写代码：先 `echuan_task_renew`，再继续。
- 把 Worker 派发的卡也去续租：与心跳抢租约，直接 lease_lost。
- 领卡返回无活就连续重试：制造请求风暴，应当等下一轮。
- 顺手把卡标成通过：越界做门禁，禁止。

## 9. 无活不是错误

`echuan_task_claim` 返回「当前无活」（HTTP 204）是正常结果：不要重试风暴、不要连续刷请求、不要自己造任务。停下等下一轮派单即可。

## 10. 一页速查

| 场景 | 动作 |
| --- | --- |
| 收到派单 | 直接开工，不反问能否开始 |
| 开工前同步 | `git fetch --prune origin`，base 用 `origin/<base>`，禁止本地 main |
| 建工作区 | `git worktree add ../wt-<task_id> -b task/<task_id> origin/<base>` |
| 开工前 | `echuan_project_get` → 需要全文再 `echuan_material_get` |
| 单卡细节 | `echuan_task_get` |
| 交付完成 | commit → `git push -u origin task/<task_id>` → `ls-remote` 校验 → summary_md（含 git 交付） |
| 推送失败/无权限 | 不算交付，按第 5 节汇报卡在哪 |
| 做不下去 | 同样 `echuan_task_report`，写清卡在哪、缺什么、谁决策 |
| 手动领的卡快到期 | `echuan_task_renew`，换新 lease_id |
| Worker 派发的卡 | 不碰续租工具，心跳托管 |
| 验收 / 返工 / 取消 / 改状态 | 不做，属平台侧人工决策 |
| 领卡返回无活 | 等下一轮，不重试风暴 |

## 11. 一句话总结

派单即开工 → 先同步远程 → 从最新 `origin/<base>` 建 worktree → 读方案与资料 → 实现并自检 → commit + push 并校验 → 用 summary_md 汇报（含 git 交付） → 租约只在自己手动领卡时续 → 门禁与状态一律不碰。做到这几步，这张卡就是一次合格交付。
