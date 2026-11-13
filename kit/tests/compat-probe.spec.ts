/**
 * 兼容探针清单与源码真源的一致性（CI 假绿防线）。
 *
 * 背景：`scripts/compat-probe.mjs` 里的 `EXPECTED_TOOLS` 是**独立声明**的清单——
 * 探针的意义就是拿一份不依赖编译产物的清单去核对真实 dsh 运行时，所以不允许改成
 * 「从 `src/tool-names.ts` 动态读」（那就变成自己核对自己）。独立声明的代价是**必须手工同步**：
 * 上一次新增 `echuan_product_update` / `echuan_product_delete` 时漏改探针，
 * 运行时探针只断言它自己那份过期列表，CI 于是拿 23 个工具当 25 个来验收。
 *
 * 本文件把这份同步关系钉在 `pnpm test` 里：
 * 1. `EXPECTED_TOOLS` 与 `TOOL_NAMES` 逐项、逐序一致（25 个 = 17 写 + 8 读）；
 * 2. 不一致时报告能指出**首个**不一致项（否则「漏了一项」只会看到一句无信息量的 not equal）；
 * 3. 探针文件保持独立声明，不出现对 `tool-names` 的 import；
 * 4. `EXPECTED_SKILLS` 与 `src/skill.ts` 的 `ECHUAN_SKILL_NAME` 一致。
 */
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { ECHUAN_SKILL_NAME } from '../src/skill.js'
import { TOOL_NAMES } from '../src/tool-names.js'

/** `scripts/compat-probe.mjs` 暴露给测试的两个清单（.mjs 无类型声明，这里手写契约）。 */
interface ProbeModule {
  readonly EXPECTED_TOOLS: readonly string[]
  readonly EXPECTED_SKILLS: readonly string[]
}

const probeUrl = new URL('../scripts/compat-probe.mjs', import.meta.url)

/**
 * 运行期动态导入探针文件。
 *
 * 不用静态 `import '...mjs'`：`scripts/` 不在 `tsconfig.test.json` 的 include 里、
 * 也没有 `.d.ts`，静态导入会让 `tsc -p tsconfig.test.json --noEmit` 报 TS7016。
 * 动态导入走真实 ESM 解析，拿到的就是探针文件本身（顶层无副作用，可安全导入）。
 */
async function loadProbe(): Promise<ProbeModule> {
  return (await import(probeUrl.href)) as ProbeModule
}

/**
 * 生成人可读的差异报告：**首个**不一致项优先，长度差异次之；完全一致时返回空串。
 *
 * 报告为空 ⇔ 逐项逐序相等（长度与每一项都比对过），所以调用方用一条 `toBe('')` 即覆盖全部。
 */
function describeMismatch(probeNames: readonly string[], sourceNames: readonly string[]): string {
  const shared = Math.min(probeNames.length, sourceNames.length)
  for (let index = 0; index < shared; index += 1) {
    const probeName = probeNames[index]
    const sourceName = sourceNames[index]
    if (probeName !== sourceName) {
      return `第 ${index + 1} 项不一致：探针=${probeName ?? '(缺)'} 源码=${sourceName ?? '(缺)'}`
    }
  }
  if (probeNames.length === sourceNames.length) return ''

  const extraProbe = probeNames.slice(shared)
  const extraSource = sourceNames.slice(shared)
  return `长度不一致：探针=${probeNames.length} 个，源码=${sourceNames.length} 个`
    + `；探针多出=${extraProbe.length > 0 ? extraProbe.join(', ') : '无'}`
    + `；源码多出=${extraSource.length > 0 ? extraSource.join(', ') : '无'}`
}

describe('兼容探针清单与源码真源一致', () => {
  it('EXPECTED_TOOLS 与 TOOL_NAMES 逐项一致（25 个 = 17 写 + 8 读，顺序一致）', async () => {
    const { EXPECTED_TOOLS } = await loadProbe()
    const sourceNames = [...TOOL_NAMES]
    const mismatch = describeMismatch(EXPECTED_TOOLS, sourceNames)

    expect(sourceNames).toHaveLength(25)
    expect(
      mismatch,
      `scripts/compat-probe.mjs 的 EXPECTED_TOOLS 与 src/tool-names.ts 的 TOOL_NAMES 不一致：${mismatch}。`
        + '新增 / 改名 / 调序工具后，必须同步探针清单（两处都要改）。',
    ).toBe('')
  })

  it('探针保持独立声明：不 import 源码的 tool-names', () => {
    const source = readFileSync(probeUrl, 'utf8')

    // 静态 import（含 `import type`）与动态 import() 两种绕过形式都不允许：
    // 探针一旦「动态读真源」，就退化成自己核对自己，CI 永远绿。
    expect(source).not.toMatch(/from\s+['"][^'"]*tool-names[^'"]*['"]/)
    expect(source).not.toMatch(/(?:import|require)\s*\(\s*[^)]*tool-names/)
  })

  it('不一致报告能指出首个不一致项（断言非空转自证）', () => {
    const source = ['alpha', 'beta', 'gamma']

    expect(describeMismatch(['alpha', 'beta', 'gamma'], source)).toBe('')
    expect(describeMismatch(['alpha', 'beta'], source)).toContain('长度不一致：探针=2 个，源码=3 个')
    expect(describeMismatch(['alpha', 'beta'], source)).toContain('源码多出=gamma')
    expect(describeMismatch(['alpha', 'gamma', 'beta'], source)).toBe(
      '第 2 项不一致：探针=gamma 源码=beta',
    )
    expect(describeMismatch(['alpha', 'beta', 'gamma', 'delta'], source)).toContain('探针多出=delta')
  })

  it('EXPECTED_SKILLS 非空且与 skill.ts 注册的技能名一致', async () => {
    const { EXPECTED_SKILLS } = await loadProbe()

    expect(EXPECTED_SKILLS.length).toBeGreaterThan(0)
    for (const name of EXPECTED_SKILLS) expect(name.trim()).not.toBe('')
    expect([...EXPECTED_SKILLS]).toEqual([ECHUAN_SKILL_NAME])
  })
})
