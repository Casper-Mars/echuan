// 仓库链接判定层断言（issue #117 / 方案 §4.1、§4.2、§11.2）
//
// 做什么：对 `web/src/lib/url.ts#repoHref` 跑方案 §4.2 脏数据分类表的 17 类输入，
// 逐行打印「输入 → 期望 → 实际」，任一行与「本方案」列不一致 → 非 0 退出。
// 位置与既有门禁脚本（style-debt.mjs / ui-imports-check.mjs / check-size.mjs）同级，
// 供后续 dev 卡与收口卡直接复跑，不额外落证据文件。
//
// 为什么 import 真实实现而不是复制函数体：
//   Node 22 默认开启 TS 类型擦除（`--experimental-strip-types` 已转正），可直接
//   `import '../src/lib/url.ts'`。复制函数体进脚本的话，实现回退（比如删掉第 (3) 层）
//   脚本依旧全绿，等于没有回归保护——所以这里只 import，不复制。
//
// 用法：
//   node web/scripts/repo-href-assert.mjs      # 退出码 0 = 17 类全部与方案一致
//
// 退出码：
//   0 通过（17 类 / 全部子项与方案 §4.2「本方案」列一致）
//   1 有行不一致（打印每条不一致的输入、期望与实际）
//   2 环境错误（导出的 repoHref 不是函数 / 实现文件缺失时 Node 自身以 ERR_MODULE_NOT_FOUND 非 0 退出）
//
// 口径要点（与方案逐条对应，写死在这里便于 review）：
//   * 第 1-12、15-17 行与基线一致；第 13、14 行是本方案唯一的收紧点（由可点翻转为不可点）；
//   * 「可点」不仅判非 null，还判**返回值逐字节等于 `raw.trim()`**（方案 §4.1 P1-1 要求），
//     避免有人把返回值改成 `new URL(...).href`（会补尾斜杠 / 转 punycode）却仍然全绿；
//   * 第 17 行额外断言**不抛异常**：`as any` 传数字 / 对象 / Symbol 必须安静返回 null
//     （web/src 无 ErrorBoundary，抛出会打掉整个抽屉）。

import { repoHref } from '../src/lib/url.ts'

const LINK = '可点'
const PLAIN = '不可点'

/** 判定表：id 对应方案 §4.2 行号；每行可挂多条输入（同属一类脏数据）。 */
const CASES = [
  { id: 1, inputs: [{ raw: 'https://github.com/x/y', want: 'link' }] },
  { id: 2, inputs: [{ raw: 'http://example.com/x', want: 'link' }] },
  { id: 3, inputs: [{ raw: 'HTTPS://GitHub.com/X/Y', want: 'link' }] },
  { id: 4, inputs: [{ raw: '  https://github.com/x/y  ', want: 'link' }] },
  {
    id: 5,
    inputs: [null, undefined, '', '   ', ' \t\n '].map((raw) => ({ raw, want: 'plain' })),
  },
  { id: 6, inputs: [{ raw: 'https://', want: 'plain' }] },
  { id: 7, inputs: [{ raw: 'github.com/x/y', want: 'plain' }] },
  { id: 8, inputs: [{ raw: 'git@github.com:x/y.git', want: 'plain' }] },
  {
    id: 9,
    inputs: [
      'ssh://git@github.com/x/y.git',
      'git://github.com/x/y.git',
      'file:///etc/passwd',
      'ftp://example.com/x',
    ].map((raw) => ({ raw, want: 'plain' })),
  },
  {
    id: 10,
    inputs: [
      'javascript:alert(1)',
      'JAVASCRIPT:alert(1)',
      'data:text/html,<script>alert(1)</script>',
      'vbscript:msgbox(1)',
      // AC2 追加：object: 同样不在白名单（方案 §4.1 只允许 http:/https:）
      'object:embed',
    ].map((raw) => ({ raw, want: 'plain' })),
  },
  { id: 11, inputs: [{ raw: '//github.com/x/y', want: 'plain' }] },
  { id: 12, inputs: [{ raw: '/relative/path', want: 'plain' }] },
  // 第 13、14 行：#117 的收紧点，基线里这两条会被渲染成一个可点链接（点进去 404）。
  { id: 13, inputs: [{ raw: 'https://github.com/x/y https://github.com/a/b', want: 'plain' }] },
  { id: 14, inputs: [{ raw: 'https://github.com/x/y 附录', want: 'plain' }] },
  { id: 15, inputs: [{ raw: 'https://exa mple.com/a', want: 'plain' }] },
  { id: 16, inputs: [{ raw: 'https://例子.中国/仓库', want: 'link' }] },
  { id: 17, inputs: [{ raw: 123, want: 'plain' }, { raw: {}, want: 'plain' }, { raw: Symbol('x'), want: 'plain' }] },
]

/** 把任意入参渲染成一行可读文本（Symbol 不能 String()，对象走 JSON）。 */
function fmt(value) {
  if (typeof value === 'string') return JSON.stringify(value)
  if (typeof value === 'symbol') return value.toString()
  if (value === null) return 'null'
  if (value === undefined) return 'undefined'
  if (typeof value === 'object') {
    try {
      return JSON.stringify(value)
    } catch {
      return Object.prototype.toString.call(value)
    }
  }
  return String(value)
}

/** 调用真实实现，异常不外抛，只记录。 */
function probe(raw) {
  try {
    return { value: repoHref(raw), threw: null }
  } catch (err) {
    return { value: undefined, threw: err }
  }
}

/** 判一条输入：期望可点时还要逐字节等于 trim 后的原值。 */
function judge({ raw, want }) {
  const got = probe(raw)
  if (got.threw) {
    const name = got.threw && got.threw.constructor ? got.threw.constructor.name : 'Error'
    return { ok: false, expected: want === 'link' ? LINK : PLAIN, actual: `抛异常 ${name}` }
  }
  if (want === 'link') {
    const expected = typeof raw === 'string' ? raw.trim() : null
    if (got.value === expected) return { ok: true, expected: LINK, actual: LINK }
    if (got.value === null) return { ok: false, expected: LINK, actual: PLAIN }
    return { ok: false, expected: LINK, actual: `${LINK}但值不符 ${fmt(got.value)}` }
  }
  if (got.value === null) return { ok: true, expected: PLAIN, actual: PLAIN }
  return { ok: false, expected: PLAIN, actual: `${LINK} ${fmt(got.value)}` }
}

/** 等宽列宽：CJK / 全角按 2 列算，否则中文列会错位。 */
function width(text) {
  let n = 0
  for (const ch of text) {
    const code = ch.codePointAt(0)
    const wide =
      (code >= 0x1100 && code <= 0x115f) ||
      (code >= 0x2e80 && code <= 0xa4cf) ||
      (code >= 0xac00 && code <= 0xd7a3) ||
      (code >= 0xf900 && code <= 0xfaff) ||
      (code >= 0xfe30 && code <= 0xfe6f) ||
      (code >= 0xff00 && code <= 0xff60) ||
      (code >= 0xffe0 && code <= 0xffe6)
    n += wide ? 2 : 1
  }
  return n
}

function pad(text, target) {
  return text + ' '.repeat(Math.max(0, target - width(text)))
}

if (typeof repoHref !== 'function') {
  console.error('环境错误：web/src/lib/url.ts 未导出函数 repoHref')
  process.exit(2)
}

const rows = []
for (const { id, inputs } of CASES) {
  const results = inputs.map((item) => ({ item, ...judge(item) }))
  rows.push({
    id,
    inputs,
    results,
    ok: results.every((r) => r.ok),
  })
}

const failures = []
const lines = []
for (const row of rows) {
  const head = `#${String(row.id).padStart(2, '0')}`
  const inputCell =
    row.inputs.length === 1 ? fmt(row.inputs[0].raw) : `${row.inputs.length} 条（逐条见下）`
  const expectedCell =
    row.results[0].expected + (row.inputs.length > 1 && !row.ok ? '（含不一致项）' : '')
  const actualCell =
    row.results.length === 1 ? row.results[0].actual : row.ok ? row.results[0].expected : '有子项不一致'
  lines.push(
    `${head} │ ${pad(inputCell, 50)} │ 期望 ${pad(expectedCell, 6)} │ 实际 ${pad(actualCell, 6)} │ ${row.ok ? 'PASS' : 'FAIL'}`,
  )
  if (row.inputs.length > 1) {
    for (const r of row.results) {
      lines.push(
        `     ↳ ${pad(fmt(r.item.raw), 50)} │ 期望 ${pad(r.expected, 6)} │ 实际 ${pad(r.actual, 6)} │ ${r.ok ? 'PASS' : 'FAIL'}`,
      )
    }
  }
  if (!row.ok) {
    failures.push(
      `#${row.id} ${row.results.filter((r) => !r.ok).map((r) => `${fmt(r.item.raw)} 期望 ${r.expected} 实际 ${r.actual}`).join('；')}`,
    )
  }
}

const totalInputs = rows.reduce((n, row) => n + row.inputs.length, 0)
const passedInputs = rows.reduce((n, row) => n + row.results.filter((r) => r.ok).length, 0)

console.log('repoHref 判定表断言（方案 §4.2 / issue #117）')
console.log('判定来源：web/src/lib/url.ts#repoHref（真实 import，未复制函数体）')
console.log('')
console.log(lines.join('\n'))
console.log('')
console.log(`合计：${rows.length} 类 / ${totalInputs} 条输入；通过 ${passedInputs} 条，失败 ${totalInputs - passedInputs} 条`)

if (failures.length > 0) {
  console.error('')
  console.error('与方案「本方案」列不一致：')
  for (const line of failures) console.error(`  - ${line}`)
  process.exit(1)
}

console.log(`结论：17 类全部与方案一致（${passedInputs}/${totalInputs}），退出码 0`)
