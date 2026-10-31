# 驿传任务执行指南

本指南约束 Agent 在驿传（echuan）平台接到任务卡之后的完整行为：怎么判定分支归属、怎么开工、先读什么上下文、什么时候必须汇报、失败怎么办、什么时候需要续租、哪些门禁不能碰。按下面的顺序执行即可，不需要额外的启动许可。

## 1. 收到派单：直接开工

- 任务来自 Worker 派单：可能是 `echuan_task_claim` 领到的卡，也可能是 Worker 心跳投递到当前会话的派单消息。**收到派单直接开工**，不要反问能否开始，不要停在原地等人点头。
- 派单消息里通常已带 `task_id`、`lease_id` 与方案摘要，够用就直接进入第 2 步；字段缺失时用平台只读工具补齐，不要回头向派单人索要。
- 仓库不在时按 `git_url` 拉取；每张卡在自己的 worktree 里做，按第 2 节创建，除此之外不另建平行目录、不改与本卡无关的模块。
- 一张卡对应一次完整交付：同步 → 实现 → 自检 → 推送 → 汇报。中途不要换卡，也不要顺手处理别的卡。

## 2. 开工前：先判定分支归属，再建 worktree

每张卡都在独立 worktree 里做，且必须基于**项目分支 `feature/<project_id>` 的最新远程提交**。顺序固定，不许跳步。

### 2.1 分支归属判定（顺序固定）

开工第一步先判定这条项目分支该落在哪，**不要先猜，按表查**。判定决定这个项目后续所有卡落在哪，只做一次：

| 顺序 | 检查 | 结论 |
| --- | --- | --- |
| 1 | 原仓库已有 `feature/<project_id>` | 走**原仓库路径**（项目已在原仓库开工，即便当前有推送权限也不改道） |
| 2 | 本人 fork 已有 `feature/<project_id>` | 走**fork 路径**（项目已在 fork 开工，后续卡必须留在 fork） |
| 3 | 都没有，且 `gh api repos/<owner>/<repo> --jq .permissions.push` 为 `true` | 走**原仓库路径**，新建项目分支 |
| 4 | 都没有，且无推送权限 | 走**fork 路径**，fork 后新建项目分支 |

- `project_id` 取派单消息里的「项目」；缺失时用 `echuan_task_get` 补齐，**不得编造**。
- 分支名固定：项目分支 `feature/<project_id>`，任务分支 `task/<task_id>`（`task_id` 形如 `APP-1-T0001`，天然带项目号）。
- **一条项目自始至终只在一个仓库里完成**，不得中途改道：权限后来变了，也不在原仓库重开一条同名分支，那会把项目劈成两条互不相干的线。

### 2.2 原仓库路径（有推送权限）

`origin` 就是原仓库，后续所有推送都走 `origin`：

```bash
# 1) 确认在仓库内（不在就按 git_url 克隆后进入）
git rev-parse --show-toplevel

UPSTREAM=<owner>/<repo>                 # 原仓库 owner/repo，来自 git_url
WORK=feature/<project_id>

# 2) 同步远程：每张卡开工前必做，禁止跳过
git fetch --prune origin

# 3) 推导默认分支名（取不到 origin/HEAD 就回落 main）
BASE=$(git symbolic-ref refs/remotes/origin/HEAD --short 2>/dev/null | sed 's#^origin/##')
[ -n "$BASE" ] || BASE=main
PUSH_REMOTE=origin

# 4) 项目分支不存在就在原仓库新建（幂等；已存在直接跳过）
git ls-remote --heads "$PUSH_REMOTE" "$WORK" | grep -q . \
  || git push "$PUSH_REMOTE" "origin/$BASE:refs/heads/$WORK"

# 5) 从项目分支的最新远程提交建 worktree + 任务分支
git worktree add ../wt-<task_id> -b task/<task_id> "origin/$WORK"
```

### 2.3 fork 路径（无推送权限）

原仓库改名为 `upstream`，`origin` 指向本人 fork；项目分支建在 fork 上：

```bash
# 1) 确认在仓库内（仓库还没有时先克隆原仓库）
git rev-parse --show-toplevel

UPSTREAM=<owner>/<repo>                 # 原仓库 owner/repo，来自 git_url
WORK=feature/<project_id>
LOGIN=$(gh api user --jq .login)
FORK="$LOGIN/$(basename "$UPSTREAM")"   # 本人 fork 的 owner/repo

# 2) fork 幂等：已存在就不再 fork
gh api "repos/$FORK" >/dev/null 2>&1 || gh repo fork "$UPSTREAM" --clone=false

# 3) 原仓库改名 upstream（保留已有提交），origin 指向本人 fork
git remote rename origin upstream 2>/dev/null || true
git remote set-url upstream "https://github.com/$UPSTREAM.git" 2>/dev/null \
  || git remote add upstream "https://github.com/$UPSTREAM.git"
git remote set-url origin "git@github.com:$FORK.git" 2>/dev/null \
  || git remote add origin "git@github.com:$FORK.git"

# 4) 两边都同步；从 upstream 的默认分支推导 BASE
git fetch --prune upstream
git fetch --prune origin
git remote set-head upstream -a 2>/dev/null || true
BASE=$(git symbolic-ref refs/remotes/upstream/HEAD --short 2>/dev/null | sed 's#^upstream/##')
[ -n "$BASE" ] || BASE=main
PUSH_REMOTE=origin

# 5) 项目分支从 upstream/$BASE 最新提交建到 fork（幂等；已存在直接跳过）
git ls-remote --heads "$PUSH_REMOTE" "$WORK" | grep -q . \
  || git push "$PUSH_REMOTE" "upstream/$BASE:refs/heads/$WORK"

# 6) 从项目分支的最新远程提交建 worktree + 任务分支
git worktree add ../wt-<task_id> -b task/<task_id> "origin/$WORK"
```

### 2.4 两条路径共同的纪律

- 任务分支一律从 `origin/$WORK` 起，**禁止以默认分支为基线开工**。`BASE` 只用于新建项目分支的起点与收尾合并 PR 的目标。
- base 必须用远程引用（`origin/$WORK`），**不得用本地 `main`/`master`**，否则会基于过期提交，白改。
- **禁止向 upstream push**：所有推送一律走 `origin`（原仓库路径 origin 即原仓库，fork 路径 origin 即本人 fork）。这条统一了两种路径的后续命令。
- `-b ... "origin/$WORK"` 会把任务分支的上游暂时设为 base，`git status` 会显示相对 base 的 ahead；首次 `git push -u origin task/<task_id>` 后上游即改为 `origin/task/<task_id>`，属正常现象，不要因此重推或 force。
- 一张卡一个 worktree，不复用上一张卡的。上一张已合并就先 `git worktree remove ../wt-<旧task_id>`；未合并则保留供 review，新卡仍从最新 `origin/$WORK` 起。
- `BASE` 取不到时回落 `main`；默认分支不是 `main` 的仓库改成实际分支名（如 `master`），不要照抄。
- `../wt-<task_id>` 是相对当前 shell 目录的路径，先 `cd` 到仓库根目录再执行，避免建到子目录里。
- 分支建不出来（无建分支权限、分支保护拦截、fork 被组织策略禁止）就停下，按第 5 节汇报，**不得「先按 main 做」**。
- 工作分支已存在（他人已建）属正常：`fetch` 后直接使用，不要删了重建。
- 建好后 `cd ../wt-<task_id>`，后续所有命令都在这里执行。

## 3. 开工前先读上下文

1. 不知道仓库地址时先 `echuan_product_list` 找 `git_url`，不要猜路径。
2. `echuan_project_get`：先看项目概况与**当前生效的方案**（spec 版本与 material_id）。需求以这里的方案为准，不要凭任务标题猜需求；正文按需 `echuan_material_get`。
3. `echuan_material_get`：需要方案全文、附件或资料正文时取全文再动手。只看到标题就开始改代码，是最常见的返工来源。
4. `echuan_task_get`：需要单卡细节（正文、验收标准、历史尝试次数、当前状态）时读它；`project_id` 缺失也用它补齐。
5. 三者读完后，先定下「改哪里、怎么验证」，再落笔改文件；读到的约束与派单描述冲突时，以平台方案为准，并在报告里点明这处冲突。
6. 上下文只读一次就够：同一张卡不要反复拉同一份资料，把结论留在自己的计划里。

## 4. 干完必须汇报（任务 PR base = 项目分支）

交付 = 代码已提交并推送到远程。**没 push 成功就等于没交付**，不得调 `echuan_task_report` 报「已完成」。顺序固定：自检通过 → commit → push → 校验 → 建任务 PR → 汇报。

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

任务 PR 的 base 必须是项目分支，`--base` **必须显式给出，不得省略**（省略会落回仓库默认分支，这正是要修的 bug）：

```bash
# 原仓库路径
gh pr create --base "feature/<project_id>" --head "task/<task_id>" \
  --title "<任务标题>" --body "Closes <task_id>"

# fork 路径：PR 开在 fork 内部（同一仓库的 base/head）
gh pr create --repo "$FORK" --base "feature/<project_id>" --head "task/<task_id>" \
  --title "<任务标题>" --body "Closes <task_id>"
```

- 任务 PR **不开到原仓库**；原仓库只在项目收尾时收到一个合并 PR（第 6 节）。
- 没有 `gh` 时，给出 compare 链接与目标 base/head，由人创建。
- 任务分支在合并前不要删，方便 review 与追溯。

任何一次交付都必须调 `echuan_task_report(task_id, lease_id, summary_md)`。只在会话里说一句「做完了」不算交付，平台侧看不到。

`summary_md` 固定下列内容，缺一段视为未完成：

1. **改了什么**：列出改动的文件或模块，以及行为上的变化，一条一句，不要写「优化了一下」这种空话。
2. **怎么自检**：写实际执行过的命令（构建、类型检查、测试）与结果，贴关键输出行；没跑过的命令不要写。
3. **git 交付**：项目分支 `feature/<project_id>` 落在原仓库还是本人 fork；任务分支 `task/<task_id>`；commit 短 hash；远程分支 `origin/task/<task_id>`（已推送，附 `ls-remote` 与 `rev-parse HEAD` 一致的校验结果）；任务 PR 链接（base = `feature/<project_id>`，fork 路径注明 PR 开在 fork 内）或 compare 链接。
4. **遗留风险**：没覆盖的边界、未验证的假设、需要后续跟进的事项；确实没有就写「无」，不要留空。

汇报不是终局：平台要求补充信息时，按同样格式再汇报一次，不要只回一句「已补充」。
提交成功后不要用旧 `lease_id` 重复提交同一张卡；等待平台侧结论即可。

## 5. 做不下去也要汇报

卡住时同样调 `echuan_task_report`，把情况写清楚：

- **卡在哪**：具体的报错、失败的命令、缺失的前置条件（含 `git fetch` / `git push` / `gh` 的原始输出）。
- **缺什么**：权限、数据、依赖、环境，还是需要人拍板的决策。
- **需要谁决策**：指向具体角色（平台侧/需求方/运维），不要写「等通知」。

禁止静默失败，禁止把没做完的活写成「已完成」，也禁止为了凑一次汇报而伪造自检结果。

分支模型相关的常见失败，按表处理：

| 情况 | 动作 |
| --- | --- |
| `git fetch` 失败（网络/权限） | 停下汇报，附原始输出；不得凭本地状态开工 |
| 直推 `feature/*` 被分支保护拒绝 | `gh api repos/{owner}/{repo}/git/refs -f ref=refs/heads/feature/<id> -f sha=$(git rev-parse "origin/$BASE")` 建引用（fork 路径把 `origin/$BASE` 换成 `upstream/$BASE`） |
| 建引用仍失败 | 停下汇报，**不得退回默认分支开工** |
| `gh repo fork` 失败（组织策略禁止 fork） | 停下汇报，附 gh 原始报错；不得擅自改用其他账号 |
| fork 已存在但落后主干 | 不影响：项目分支始终从 `upstream/$BASE` 最新提交建 |
| 工作分支已存在（他人已建） | 正常，`fetch` 后直接使用 |

## 6. 项目收尾：合并 PR（feature → 默认分支）

1. 用 `echuan_task_list(project_id=…)` 看同项目其它卡：其它卡都处于 `pending_acceptance` / `done` / `cancelled` → 本卡即最后一张。
2. 只在最后一张卡收尾时建合并 PR：

```bash
# 原仓库路径：base 是默认分支，head 是项目分支
gh pr create --repo "$UPSTREAM" --base "$BASE" --head "feature/<project_id>" \
  --title "<项目> 收尾合并" --body "<本项目的交付摘要>"

# fork 路径：head 必须带 fork 账号前缀，base 指向原仓库
gh pr create --repo "$UPSTREAM" --base "$BASE" \
  --head "$LOGIN:feature/<project_id>" \
  --title "<项目> 收尾合并" --body "<本项目的交付摘要>"
```

3. 链接写进报告「git 交付」段，注明「待人工合并」。

边界：

- **agent 不合并、不删分支**：合并与清理是人的动作。
- **建不出来不算交付失败**：没有 `gh`、跨仓 PR 被组织策略禁止、或判断不准时，报告里写清 `feature/<project_id>` → 默认分支的 compare 链接，由人手动提 PR。合并本来就要人做，这一步不阻塞交付。
- 判断失误（其它卡后来被驳回重做）只会提前留一个待合并 PR，人工合并前仍可拦下。
- 不是最后一张卡就不要建合并 PR；拿不准就写 compare 链接交人工。

## 7. 租约：只有手动模式才要自己续租

- **手动模式**：自己调 `echuan_task_claim` 领到的卡，租约由自己负责。任务耗时可能超过租约 TTL 时，在到期前调 `echuan_task_renew(task_id, lease_id)`（任务卡续租）拿新的 `lease_id`，之后**一律用新 lease_id** 提交报告。
- **Worker 派发模式**：心跳循环派发的卡由心跳托管租约，**不要**调用续租工具，否则会和心跳抢租约语义，把卡搞成 lease_lost。
- 续租返回 lease_lost：说明租约已失效，之前的工作结果不能再提交；重新领卡，不要拿旧 lease_id 硬提交。
- 领卡结果里带 `ttl_seconds` 与到期时间；剩余时间不够收尾时就该续租，别等过期再救。
- 短任务（明显在 TTL 内能收尾）不必续租，少一次调用少一次失败面。

## 8. 门禁红线：只实现，不裁决

- 不验收：验收结论由平台侧人工给出，Agent 不写「已通过验收」这类判断。
- 不返工判定、不取消任务、不改任务状态与优先级，也不自造状态流转。
- 不调用任何门禁类接口；报告里只陈述事实（改了什么、跑过什么），不做结论性裁决。
- 任务卡的状态变化只由平台产生。Agent 的职责只有两件事：实现、汇报。

## 9. 常见错误（出现即按返工处理）

- 只读标题就改代码：回第 3 步补齐 `echuan_project_get` / `echuan_material_get`。
- 用默认分支建任务分支，或任务 PR 不写 `--base`：base 会落回 `main`，项目没有独立分支载体，删 worktree 与分支，回第 2 节重来。
- 向 `upstream` push：禁止，会把分支推到别人的原仓库；所有推送只走 `origin`。
- 干完只在会话里说一句「完成」：平台没有记录，等于没交，必须调 `echuan_task_report`。
- 干完不 push：review 的人拿不到代码，按未交付处理，补 push 加校验后重新汇报。
- 第二张卡不 `fetch` 就建 worktree：base 已过期，删掉 worktree 与分支，回第 2 节重来。
- 分支建不出来就「先按 main 做」：禁止，停下按第 5 节汇报。
- 手动领的卡快到期还在写代码：先 `echuan_task_renew`，再继续。
- 把 Worker 派发的卡也去续租：与心跳抢租约，直接 lease_lost。
- 领卡返回无活就连续重试：制造请求风暴，应当等下一轮。
- 顺手把卡标成通过：越界做门禁，禁止。

## 10. 无活不是错误

`echuan_task_claim` 返回「当前无活」（HTTP 204）是正常结果：不要重试风暴、不要连续刷请求、不要自己造任务。停下等下一轮派单即可。

## 11. 一页速查

| 场景 | 动作 |
| --- | --- |
| 收到派单 | 直接开工，不反问能否开始 |
| 分支归属 | 按第 2.1 节四步表判定：原仓库有分支 → 原仓库；fork 有分支 → fork；都没有看推送权限 |
| 开工前同步 | `git fetch --prune origin`（fork 路径再 `git fetch --prune upstream`）；任务分支一律从 `origin/feature/<project_id>` 起，禁止以默认分支为基线 |
| 建项目分支 | 不存在则从最新默认分支建 `feature/<project_id>`，推 `$PUSH_REMOTE`；禁止向 upstream push |
| 建工作区 | `git worktree add ../wt-<task_id> -b task/<task_id> "origin/feature/<project_id>"`，一卡一个 |
| 开工前 | `echuan_project_get` → 需要全文再 `echuan_material_get` |
| 单卡细节 | `echuan_task_get`（`project_id` 缺失也用它补齐） |
| 交付完成 | commit → `git push -u origin task/<task_id>` → `ls-remote` 校验 → 任务 PR（`--base feature/<project_id>`，不得省略）→ summary_md（含 git 交付） |
| 项目最后一张卡 | 建合并 PR `feature/<project_id>` → 默认分支，链接写进报告待人工合并；不合并、不删分支 |
| 推送失败/无权限 | 不算交付，按第 5 节汇报卡在哪 |
| 做不下去 | 同样 `echuan_task_report`，写清卡在哪、缺什么、谁决策 |
| 手动领的卡快到期 | `echuan_task_renew`，换新 lease_id |
| Worker 派发的卡 | 不碰续租工具，心跳托管 |
| 验收 / 返工 / 取消 / 改状态 | 不做，属平台侧人工决策 |
| 领卡返回无活 | 等下一轮，不重试风暴 |

## 12. 一句话总结

派单即开工 → 判定分支归属（原仓库或 fork）→ 建/复用 `feature/<project_id>` → 从它建 `task/<task_id>` worktree → 读方案与资料 → 实现并自检 → commit + push 并校验 → 任务 PR 的 base 写项目分支 → 最后一张卡建合并 PR 交人工 → 用 summary_md 汇报（含 git 交付） → 租约只在自己手动领卡时续 → 门禁与状态一律不碰。做到这几步，这张卡就是一次合格交付。
