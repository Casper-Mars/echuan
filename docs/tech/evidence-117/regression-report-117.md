# 回归报告：issue #117 前端产物重生成 + AC 全量回归（收口卡）

| 项 | 值 |
|---|---|
| 任务 | `t_01M81EYMCWG2YPWWWGESN308DG`（issue #117 三张卡的最后一张：产物重生成 + 全量回归取证） |
| 任务分支 | `task/t_01M81EYMCWG2YPWWWGESN308DG`（沿用项目 worktree，未新建分支 / 未切换） |
| 项目集成分支（PR base，未触碰） | `feature/echuan-5` |
| 基线 commit | `abb5fd0` = `abb5fd01caeb2acb31662fd634819d82db578724`（= 核销时的 `origin/feature/echuan-5`） |
| 本卡提交 | **`3cfe2ff`**（`3cfe2ffaedcf669af49591d41e9735a1f3271f9f`）= 产物重生成 + 本报告初版，diff 范围 `abb5fd0..3cfe2ff`；本行补记于其后一个 doc-only commit（仅更新本行，产物与其它章节未改），仓库内自证：`git show 3cfe2ff --stat` |
| 报告路径 | `docs/tech/evidence-117/regression-report-117.md`（本文件） |
| 方案依据 | `docs/tech/product-repo-link-117-v0.1.md` §2.2（P1–P4）/ §8 / §9–§10 / §11.2 / §12.2 |
| 本卡范围 | **不做新功能**：只重生成 `internal/web/dist`（一次提交）+ 本报告与截图 |

**结论（事实陈述，验收结论由 Tester 出）**：四条门禁退出码全 0；`internal/web/dist` 与 `web/dist` 逐字节一致（`diff -r` 无输出）且重生成两次结果一致（幂等）；P1–P4 在三视口共 **484/484** 项 DOM 判定全部通过（0 失败），全程无 alert/dialog；重生成前的旧产物确实**落后于源码**（不含 #118/#119 的外链渲染与 <768 命中区规则），本次重生成把它们补齐。

---

## 1 环境

| 项 | 值 |
|---|---|
| 执行位置 | agent 容器（**未建容器、未 `docker run`/`devcontainer up`**；依赖 `web/node_modules` 由 Leader 预装） |
| Node / pnpm | `v22.23.5` / `11.7.0` |
| 内核 | `Linux 6.10.14-linuxkit` |
| 前端栈 | React 18.3 + Vite 6.4.5（构建日志 `vite v6.4.5`）+ TypeScript 5.7（`tsc --noEmit`） |
| 页面托管 | 临时 node 服务（仓库外 `/workspace/.verify/117/serve-stub.mjs`）静态托管 `web/dist`，`/v1` 用 stub 提供 23 条 `git_url` 样本；地址 `http://dsh-agent:28701/`（端口 287xx 段，用完已停） |
| 浏览器 | 既有 `browserless`，`ws://browserless:3000?token=test-token`，`Chrome/156.0.8078.4`；`puppeteer-core` 从 `/workspace/.agent-env/browser-tools` 动态解析（未新增依赖、未另起 Chrome） |
| 截图 / 原始数据 | 原始截图与 JSON 在仓库外 `/workspace/.verify/117/`；入库截图见 §5 |

> `make` **不在** agent 容器内（实测 `which make` = 空，`apt-get install make` 无候选源），因此 `Makefile:56-60` 的 `web` 目标按四步**逐条等价**执行（见 §2），命令内容与 Makefile 一致，未改动 Makefile。

---

## 2 产物重生成（`make web` 逐条等价）

| # | 命令 | 退出码 | 关键输出行 |
|---|---|---|---|
| 1 | `node web/scripts/style-debt.mjs` | **0** | `[style-debt] 6/6 项达标` |
| 2 | `pnpm --dir web build`（= `tsc --noEmit && vite build && node scripts/check-size.mjs`） | **0** | `✓ 829 modules transformed.` / `✓ built in 1.45s` |
| 3 | `rm -rf internal/web/dist` | **0** | — |
| 4 | `cp -r web/dist internal/web/dist` | **0** | — |

产物一致性：

```
$ diff -r web/dist internal/web/dist && echo DIST_IDENTICAL=yes
DIST_IDENTICAL=yes
```

重生成后 `git status --short`（**只出现 `internal/web/dist/**`**，无其它路径）：

```
 D internal/web/dist/assets/TaskDetailRoute-qGfzARJG.js
 D internal/web/dist/assets/index-BJpntbbs.js
 D internal/web/dist/assets/index-CnjsYYgk.css
 M internal/web/dist/index.html
?? internal/web/dist/assets/TaskDetailRoute-BowXhyLx.js
?? internal/web/dist/assets/index-C8Or43x7.css
?? internal/web/dist/assets/index-Dr3IahRB.js
```

（`git add -A` 后同样只有这 5 条路径，rename 检测显示为 3 个 rename + 1 个 modify：`numstat` 合计 `1/1`·`1/0`·`0/1`·`4/4`·`2/2` 行。）

**为什么这次重生成不是空转**（旧产物落后于源码，AC2 的直接证据）：

| 检查 | 基线产物 `abb5fd0` | 重生成后 | 结论 |
|---|---|---|---|
| `grep -c 'noopener noreferrer'`（JS） | 0 | 1（`<a href… target="_blank" rel="noopener noreferrer">`） | 旧产物不含 #118 的外链渲染 |
| CSS 中 `data-link` 出现次数 | 0 | 1（`.fx-detail-fields__value[data-link=on] a{line-height:var(--ui-control-height-touch)…}`） | 旧产物不含 #119 的 <768 命中区规则 |
| 字节数 | js 546,559 / css 251,870 | js 546,902 / css 252,035 | 差异即上述两处源码改动 |

**幂等**（第二次构建 + 同步后 `git status` 不变）：

```
$ pnpm --dir web build            → 0（[size] 通过：JS gzip 183365 B <= 阈值 344056 B）
$ rm -rf internal/web/dist && cp -r web/dist internal/web/dist   → 0
$ diff status-run1.txt status-run2.txt   （两次 git status --short 排序后逐行比对）
（无输出）→ 产物可复现，不抖动
```

---

## 3 四条门禁（原始退出码，均为真跑）

| # | 命令 | 退出码 | 关键输出行（原文） |
|---|---|---|---|
| 1 | `node web/scripts/style-debt.mjs` | **0** | `PASS ① 未引用类名占比 < 20% — 1/240 = 0.4%`…`PASS ⑥ 死类子系统 grep = 0`；`[style-debt] 结论：样式债已出清（6/6 项达标）` |
| 2 | `node web/scripts/ui-imports-check.mjs` | **0** | `[ui] 导出总数：21 / 期望 21`；`[ui] 打包产物（external 掉 UI 库后）：js 31534 B，css 21678 B`；`[ui] 通过：21 个组件全部可 import` |
| 3 | `node web/scripts/repo-href-assert.mjs` | **0** | `合计：17 类 / 30 条输入；通过 30 条，失败 0 条`；`结论：17 类全部与方案一致（30/30），退出码 0` |
| 4 | `pnpm --dir web build` | **0** | `tsc --noEmit` 无 `error TS`；`✓ built in 1.45s`；`[size] JS  raw=576083 gzip=183365 files=2`；`[size] CSS raw=259067 gzip=38223 files=2`；`[size] 通过：JS gzip 183365 B <= 阈值 344056 B（剩余 160691 B）` |

> 体积门禁阈值取自 `web/scripts/check-size.mjs` 打印值：`阈值 JS gzip <= 344056 B（基线 88056 B + 预算 256000 B）`；本次实测 183,365 B，余量 160,691 B。

---

## 4 P1–P4 逐条结果（三视口 1440×900 / 1024×768 / 390×844）

取证脚本（仓库外临时脚本，未入库）：`/workspace/.verify/117/measure.mjs`（puppeteer-core + CDP）、`serve-stub.mjs`（静态 + `/v1` stub）、`samples.mjs`（23 条样本）。样本覆盖：合法 https / `http://` / 前后带空格 / 大写 scheme / 中文域名 / 超长地址（>100 字符）/ 空串 / 全空白 / P4 全 15 个非法值。

```
484/484 checks passed
```

**P1 合法 http(s) 可点**（判据：`a` 数量 = 1，`href` 逐字节 = 登记值，链接文本 = 原值）

| 样本 | 期望 href | 三视口实测 `a[href]`（getAttribute） | 结果 |
|---|---|---|---|
| `https://github.com/Casper-Mars/echuan` | 同值 | `https://github.com/Casper-Mars/echuan`（desktop/tablet/mobile 一致） | PASS |
| `http://example.com/x` | 同值 | `http://example.com/x` | PASS |
| `"  https://github.com/x/y  "` | trim 后 `https://github.com/x/y` | `https://github.com/x/y`（`dd` 文本与 `title` 仍为原值带空格） | PASS |
| `HTTPS://GitHub.com/X/Y`（大小写） | 原值 | `HTTPS://GitHub.com/X/Y`（未小写化） | PASS |
| `https://例子.中国/仓库`（非 ASCII） | 原值 | `https://例子.中国/仓库`（未 punycode 转写） | PASS |

**P2 长地址不撑破抽屉**（长地址 = `https://github.com/Casper-Mars/echuan/blob/main/web/src/features/shell/DetailFields.tsx#L38-L90-repo-link-regression-117`，127 字符）

| 视口 | 抽屉正文区 `scrollWidth / clientWidth` | 仓库字段 `dd` 宽 | `dd.scrollWidth / clientWidth` | 仓库行高 | `dd` 行高 / 字号 | `white-space` / `text-overflow` | `title` = 完整原值 | 结果 |
|---|---|---|---|---|---|---|---|---|
| 1440×900 | 719 / 719 | 591 | 939 / 591 | 21 | 20.02px / 13px | nowrap / ellipsis | true | PASS |
| 1024×768 | 719 / 719 | 591 | 939 / 591 | 21 | 20.02px / 13px | nowrap / ellipsis | true | PASS |
| 390×844 | 389 / 389 | 261 | 939 / 261 | 44 | 20.02px / 13px | nowrap / ellipsis | true | PASS |

（三视口 `[role=dialog] .ui-detail-sheet__body` 均 `scrollWidth === clientWidth`；`dd.scrollWidth > dd.clientWidth` 成立 → 单行省略生效。）

**P3 新 tab 打开**（真鼠标点击，CDP `targetcreated` 观察新 target）

| 视口 | `target` | `rel` | 新 target URL | 当前 tab `location.href` 前后 | 抽屉状态 |
|---|---|---|---|---|---|
| 1440×900 | `_blank` | `noopener noreferrer` | `https://github.com/Casper-Mars/echuan` | `http://dsh-agent:28701/products#/products` → 同值（不变） | 仍打开 |
| 1024×768 | `_blank` | `noopener noreferrer` | 同上 | 同上（不变） | 仍打开 |
| 390×844 | `_blank` | `noopener noreferrer` | 同上 | 同上（不变） | 仍打开 |

<768 命中区（方案 §7.2 / `web/scripts/ui-verify.mjs#checkTouchTargets` 的 44 口径）：390×844 下所有可点样本的 `a` 命中区高 **46.0px**（`line-height: 44px` + 上下 padding），字段行高 21 → **44**；desktop/tablet 为 15px 文本高 + 21px 行高（文本口径，不判 44）。
例：`[mobile] S-DEFAULT <768 命中区 ≥44×44 — a=289.6×46 · 行高=44px · dd 行高=44 · a line-height=44px`。

**P4 非法地址不可点**（15 个值 × 三视口 = 45 组；判据：仓库字段 `a` 数量 = 0、`dd a[href]` = 0、文本原样、`title` = 原值、抽屉内 `a[href^="javascript"]` = 0、无 alert）

| 样本值 | 三视口 `a` 数量 | 文本原样 | 结果 |
|---|---|---|---|
| `github.com/x/y` | 0 / 0 / 0 | ✅ | PASS |
| `git@github.com:x/y.git` | 0 / 0 / 0 | ✅ | PASS |
| `ssh://git@github.com/x/y.git` | 0 / 0 / 0 | ✅ | PASS |
| `file:///etc/passwd` | 0 / 0 / 0 | ✅ | PASS |
| `ftp://example.com/a` | 0 / 0 / 0 | ✅ | PASS |
| `/relative/path` | 0 / 0 / 0 | ✅ | PASS |
| `//github.com/x/y` | 0 / 0 / 0 | ✅ | PASS |
| `javascript:alert(1)` | 0 / 0 / 0 | ✅ | PASS |
| `JAVASCRIPT:alert(1)` | 0 / 0 / 0 | ✅ | PASS |
| `data:text/html,<script>alert(1)</script>` | 0 / 0 / 0 | ✅ | PASS |
| `vbscript:msgbox(1)` | 0 / 0 / 0 | ✅ | PASS |
| `https://github.com/x/y https://github.com/a/b` | 0 / 0 / 0 | ✅ | PASS |
| `https://github.com/x/y 附录` | 0 / 0 / 0 | ✅ | PASS |
| `https://exa mple.com/a` | 0 / 0 / 0 | ✅ | PASS |
| `https://` | 0 / 0 / 0 | ✅ | PASS |

空值分支：`""` 与 `"   "` → 仓库字段文本 = `未设置`、`title` = `未设置`、`a` = 0（三视口 6 组全 PASS）。全程 `alert/dialog` 捕获次数 = **0**；`document.querySelectorAll('[role=dialog] a[href^="javascript"]').length === 0` 在三视口 69 组抽屉状态下均成立。

---

## 5 截图（入库路径：`docs/tech/evidence-117/`）

| 状态 | 1440×900 | 1024×768 | 390×844 |
|---|---|---|---|
| `default`（合法短地址） | `regression-default-desktop.png` | `regression-default-tablet.png` | `regression-default-mobile.png` |
| `long-url`（>100 字符，单行省略） | `regression-long-url-desktop.png` | `regression-long-url-tablet.png` | `regression-long-url-mobile.png` |
| `spaced`（前后空格 → 仍可点） | `regression-spaced-desktop.png` | `regression-spaced-tablet.png` | `regression-spaced-mobile.png` |
| `invalid`（`javascript:alert(1)` → 纯文本） | `regression-invalid-desktop.png` | `regression-invalid-tablet.png` | `regression-invalid-mobile.png` |

改前基线对照可用同目录既有 `before-*.png` / `after-*.png`（#118 卡留下的截图，本卡未改动）。

---

## 6 未覆盖项（如实登记）

| # | 未覆盖 | 原因 | 影响 |
|---|---|---|---|
| 1 | `make web` 本身未执行 | agent 容器无 `make`（`which make` 空，`apt-get install make` 无候选源）；也未建容器 | 四步等价执行已覆盖 `web` 目标内的全部命令；未验证 Makefile 的 `if [ -f web/package.json ]` 分支与 `echo` 收尾行 |
| 2 | `go build` / `go test` / `make build` 未执行 | agent 容器无 Go toolchain（`which go` 空），本卡不改 Go 代码 | `go:embed internal/web/dist` 的编译期接纳未实测（产物均为 vite 正常输出，无异常文件名 / 符号链接） |
| 3 | `web/scripts/ui-verify.mjs` 的 `checkTouchTargets` 未在产品抽屉上跑 | 本卡约束 `web/scripts/**` 零改动，无法把抽屉用例接进该脚本 | 命中区按同一口径（<768 ≥44×44）在临时脚本里单独量（实测 46px），非 `ui-verify.mjs` 的原始输出 |
| 4 | 键盘路径（Tab 聚焦 → Enter 打开新 tab，方案 §2.2 P3-3）未走全链路 | 临时脚本只采集了 focus 相关 computed 样式 | P3 的鼠标点击路径已验；键盘「同一个按键先开抽屉又开链接」的回归风险仍靠 Tester 覆盖 |
| 5 | 亮色主题、`ProjectDetailSheet`（不传 `link`，行为应逐字节不变）未取证 | 暗色为验收基准；项目抽屉需项目侧数据 | 项目抽屉的「零变化」是代码推断（`DetailField.link` 可选），未实测 |
| 6 | 新 tab 内目标站点未加载验证 | 点击后立即关闭新 target（避免拉真实外网） | 只验到「新 target URL = `href`」，未验目标站点可否打开（跨域、非本应用可观测面，方案 §11.3 已登记） |
| 7 | 未在真实二进制（`go:embed` + `/v1` 真后端）里跑一遍 | 需 Go toolchain / 容器 | 用 `web/dist` 静态托管 + `/v1` stub 代替；stub 的 `git_url` 直出、未过后端 `TrimSpace` |

## 7 残余风险

| # | 风险 | 说明 / 缓解 |
|---|---|---|
| R1 | 产物 hash 文件名跨机器可能不同 | 本机两次构建 hash 名一致（幂等已验证）；换 vite/依赖小版本或不同机器可能产生新 hash，属正常构建差异，不是源码漂移（lockfile 未改） |
| R2 | `spaced` 样本在生产库里可能不存在 | 后端 `TrimSpace` 后入库（方案 §3.3），「前后空格」是前端口径兜底（防御性），不是真实脏数据形态 |
| R3 | 23 条样本由 stub 提供，非真实库数据 | 只证明渲染层对各类登记值的表现；后端把非法值写库的现状（方案 U-02）未变、仍是纯文本兜底 |
| R4 | 报告无法自引用自身 commit hash | 单 commit 约束下 hash 在提交后才确定；`<commit>` 见交付回报，`git log --oneline -1` 可复核 |
| R5 | 截图 12 张共约 800 KB 入库 | 仅 4 状态 × 3 视口，未额外留原始全量截图（原始在仓库外 `/workspace/.verify/117/shots/`） |
