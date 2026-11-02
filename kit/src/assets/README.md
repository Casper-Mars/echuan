# src/assets

插件运行期资源目录（预留）。

后续任务会在此放：

- 面向模型的技能 / 提示词指南（`.md`，经 `new URL('./assets/xxx.md', import.meta.url)` 读取）
- 需要随包分发的静态 JSON 契约片段

`scripts/copy-assets.mjs` 会在 `pnpm build` 时把本目录下的
`.md` / `.json` / `.yml` / `.yaml` / `.txt` 原样复制到 `lib/assets/`，
保证源码态与编译态的相对路径一致。本任务阶段目录为空，不声明任何资源。
