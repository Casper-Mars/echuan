/**
 * 工具名清单与 `exclude` 校验（任务-09）。
 *
 * 这里是「唯一真源」的自检：
 * - 15 写 + 8 读 = 23，顺序与分组固定；
 * - `TOOL_NAMES` 始终是全集，不随 `exclude` 变化；
 * - `validateExclude` 的 trim / 去重 / 空白报错 / 未知名称报错；
 * - 清单与 `createAllEchuanTools`（不传 exclude）实际产出的名字逐项一致。
 */
import { describe, expect, it } from 'vitest'
import { createClient } from '../src/client.js'
import { resolveConfig } from '../src/config.js'
import {
  READ_TOOL_NAMES,
  TOOL_NAMES,
  WRITE_TOOL_NAMES,
  validateExclude,
} from '../src/tool-names.js'
import { createAllEchuanTools } from '../src/tools/index.js'

const config = resolveConfig({ baseUrl: 'http://platform.test' })

function toolNames(exclude?: readonly string[]): string[] {
  const client = createClient(config, { fetchImpl: async () => new Response('{}') })
  const options = exclude === undefined
    ? { client, identity: { workerId: undefined } }
    : { client, identity: { workerId: undefined }, exclude }
  return createAllEchuanTools(options).map(tool => tool.name)
}

describe('工具名清单：15 写 + 8 读 = 23', () => {
  it('分组数量固定：23 / 15 / 8', () => {
    expect(WRITE_TOOL_NAMES).toHaveLength(15)
    expect(READ_TOOL_NAMES).toHaveLength(8)
    expect(TOOL_NAMES).toHaveLength(23)
  })

  it('全集无重复，且等于「写 + 读」的顺序拼接', () => {
    expect(new Set(TOOL_NAMES).size).toBe(23)
    expect([...TOOL_NAMES]).toEqual([...WRITE_TOOL_NAMES, ...READ_TOOL_NAMES])
  })

  it('读集合与写集合无交集', () => {
    const writes = new Set<string>(WRITE_TOOL_NAMES)
    expect(READ_TOOL_NAMES.filter(name => writes.has(name))).toEqual([])
  })

  it('与 createAllEchuanTools（不传 exclude）实际产出的名字一致', () => {
    expect(toolNames()).toEqual([...TOOL_NAMES])
  })

  it('exclude 不改变 TOOL_NAMES 全集语义', () => {
    expect(toolNames(['echuan_project_create', 'echuan_plan_submit']))
      .toEqual([...TOOL_NAMES].filter(name => name !== 'echuan_project_create'
        && name !== 'echuan_plan_submit'))
    expect(TOOL_NAMES).toHaveLength(23)
  })
})

describe('validateExclude：归一化', () => {
  it('undefined / null / 空数组都返回 []', () => {
    expect(validateExclude()).toEqual([])
    expect(validateExclude(undefined)).toEqual([])
    expect(validateExclude(null)).toEqual([])
    expect(validateExclude([])).toEqual([])
  })

  it('每项 trim，重复项去重且保留首次出现顺序', () => {
    expect(validateExclude([
      '  echuan_plan_submit  ',
      'echuan_task_claim',
      'echuan_plan_submit',
      ' echuan_task_claim ',
    ])).toEqual(['echuan_plan_submit', 'echuan_task_claim'])
  })

  it('返回新数组，不篡改入参', () => {
    const input = [' echuan_task_claim ']
    const output = validateExclude(input)
    expect(output).not.toBe(input)
    expect(input).toEqual([' echuan_task_claim '])
    expect(output).toEqual(['echuan_task_claim'])
  })
})

describe('validateExclude：报错', () => {
  it('空白项抛 TypeError（不静默丢弃）', () => {
    expect(() => validateExclude(['echuan_task_claim', '   '])).toThrow(TypeError)
    expect(() => validateExclude([''])).toThrow(/exclude 不能包含空白工具名/)
  })

  it('未知名称抛 TypeError，同时列出未知项与合法名单', () => {
    let error: unknown
    try {
      validateExclude(['echuan_task_claim', 'echuan_project_create2'])
    } catch (err) {
      error = err
    }
    expect(error).toBeInstanceOf(TypeError)
    const message = (error as Error).message
    expect(message).toContain('echuan_project_create2')
    // 合法名单以「23 个」开头并包含首尾两个真实工具名。
    expect(message).toContain(`合法工具名（${TOOL_NAMES.length} 个）`)
    expect(message).toContain('echuan_task_claim')
    expect(message).toContain('echuan_material_get')
    expect(message).toContain('echuan_worker_info')
  })

  it('多个未知项一次列全', () => {
    expect(() => validateExclude(['nope_a', 'nope_b']))
      .toThrow(/未知工具名：nope_a, nope_b/)
  })

  it('重复的未知项只报一次', () => {
    expect(() => validateExclude(['nope_a', ' nope_a '])).toThrow(/未知工具名：nope_a。/)
  })
})
