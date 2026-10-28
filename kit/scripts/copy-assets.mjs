/**
 * 资源搬运脚本：把 `src/**` 下的非 TS 资源复制到编译产物目录 `lib/**`。
 *
 * 背景：`tsc` 只产出 `.js` / `.d.ts`，不会搬运 `.md` / `.json` 等资源。
 * 后续阶段（任务-05 的技能指南、提示词模板）会以
 * `new URL('./assets/xxx.md', import.meta.url)` 读取，源码态 `src/assets/` 与
 * 编译态 `lib/assets/` 相对路径一致，因此构建后必须把资源一并复制过去。
 *
 * 用法：`node scripts/copy-assets.mjs`（由 `package.json` 的 `build` 调用）。
 */
import { cpSync, existsSync, mkdirSync, readdirSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const srcDir = path.join(root, 'src')
const outDir = path.join(root, 'lib')

/** 需要搬运的资源扩展名。 */
const ASSET_EXTENSIONS = new Set(['.md', '.json', '.yml', '.yaml', '.txt'])

/** 递归遍历目录，返回其中所有匹配扩展名的文件（相对 `srcDir` 的路径）。 */
function collectAssets(dir) {
  const results = []
  if (!existsSync(dir)) return results
  for (const entry of readdirSync(dir)) {
    const abs = path.join(dir, entry)
    const stat = statSync(abs)
    if (stat.isDirectory()) {
      results.push(...collectAssets(abs))
    } else if (ASSET_EXTENSIONS.has(path.extname(entry))) {
      results.push(path.relative(srcDir, abs))
    }
  }
  return results
}

const assets = collectAssets(srcDir)
for (const rel of assets) {
  const from = path.join(srcDir, rel)
  const to = path.join(outDir, rel)
  mkdirSync(path.dirname(to), { recursive: true })
  cpSync(from, to)
  console.log(`[dsh-echuan-assets] ${path.relative(root, from)} -> ${path.relative(root, to)}`)
}

console.log(`[dsh-echuan-assets] 共搬运 ${assets.length} 个资源文件到 lib/`)
