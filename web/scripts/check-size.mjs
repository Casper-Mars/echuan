// echuan 前端产物体积门禁
//
// 只做一件事：量 dist 产物体积 → 判阈值 → 打印 → 退出码。
// 已串进 `pnpm build`（tsc --noEmit && vite build && node scripts/check-size.mjs），
// 因此 `make web` 与镜像构建都会跑到；样式债统计 / lint / 类型检查不在这里（属 T11 范围）。
//
// 用法：
//   pnpm --dir web build                     # 构建 + 门禁（推荐入口）
//   node web/scripts/check-size.mjs           # 单独复跑门禁（需已有 dist）
//   SIZE_JS_GZIP_MAX=1024 node web/scripts/check-size.mjs   # 覆盖阈值，演示失败路径
//
// 判据：dist/ 下所有 .js 的 gzip 体积之和 ≤ SIZE_JS_GZIP_MAX。
//   SIZE_JS_GZIP_MAX 默认 = 基线 88,056 B + 预算 250 KB(256,000 B) = 344,056 B。
//   基线来自 ADR-0001 §2 C7 的实测值（引入 Mantine 前的 JS gzip）。
//   gzip 用 Node zlib level 9，与 vite build 的 "computing gzip size" 口径一致。
//
// 退出码：
//   0 通过（在阈值内）
//   1 超标（打印超标项、当前值与阈值）
//   2 环境/用法错误（dist 缺失或阈值非法）

import { readdirSync, readFileSync, statSync } from 'node:fs'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { gzipSync } from 'node:zlib'

const BASELINE_JS_GZIP = 88_056
const BUDGET_BYTES = 250 * 1024
const DEFAULT_JS_GZIP_MAX = BASELINE_JS_GZIP + BUDGET_BYTES

const WEB_DIR = dirname(dirname(fileURLToPath(import.meta.url)))
const DIST_DIR = join(WEB_DIR, 'dist')

function fail(code, message) {
  console.error(`[size] 错误：${message}`)
  process.exit(code)
}

const rawMax = process.env.SIZE_JS_GZIP_MAX
let jsGzipMax = DEFAULT_JS_GZIP_MAX
if (rawMax !== undefined && rawMax !== '') {
  if (!/^\d+$/.test(rawMax.trim())) {
    fail(2, `SIZE_JS_GZIP_MAX 必须是非负整数（收到 "${rawMax}"）`)
  }
  jsGzipMax = Number(rawMax.trim())
}

try {
  statSync(DIST_DIR)
} catch {
  fail(2, `找不到构建产物目录 ${relative(process.cwd(), DIST_DIR) || DIST_DIR}，请先 pnpm build`)
}

function walk(dir) {
  const out = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name)
    if (entry.isDirectory()) out.push(...walk(full))
    else if (entry.isFile()) out.push(full)
  }
  return out
}

const files = walk(DIST_DIR)
const measured = (ext) =>
  files
    .filter((f) => f.endsWith(ext))
    .map((f) => {
      const buf = readFileSync(f)
      return { file: relative(DIST_DIR, f), raw: buf.length, gzip: gzipSync(buf, { level: 9 }).length }
    })
    .sort((a, b) => b.gzip - a.gzip)

const js = measured('.js')
const css = measured('.css')
const sum = (items, key) => items.reduce((acc, item) => acc + item[key], 0)

const jsRaw = sum(js, 'raw')
const jsGzip = sum(js, 'gzip')
const cssRaw = sum(css, 'raw')
const cssGzip = sum(css, 'gzip')

console.log(`[size] JS  raw=${jsRaw} gzip=${jsGzip} files=${js.length}`)
console.log(`[size] CSS raw=${cssRaw} gzip=${cssGzip} files=${css.length}`)
console.log(
  `[size] 阈值 JS gzip <= ${jsGzipMax} B` +
    `（基线 ${BASELINE_JS_GZIP} B + 预算 ${BUDGET_BYTES} B = ${DEFAULT_JS_GZIP_MAX} B` +
    `${jsGzipMax === DEFAULT_JS_GZIP_MAX ? '' : '，已被 SIZE_JS_GZIP_MAX 覆盖'}）`,
)

if (jsGzip > jsGzipMax) {
  console.error(`[size] 超标：JS gzip 当前 ${jsGzip} B > 阈值 ${jsGzipMax} B，超出 ${jsGzip - jsGzipMax} B`)
  console.error('[size] 超标项（按 gzip 降序）：')
  for (const item of js) {
    console.error(`[size]   ${item.file}  raw=${item.raw} gzip=${item.gzip}`)
  }
  process.exit(1)
}

console.log(`[size] 通过：JS gzip ${jsGzip} B <= 阈值 ${jsGzipMax} B（剩余 ${jsGzipMax - jsGzip} B）`)
