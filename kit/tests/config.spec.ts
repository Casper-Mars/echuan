/**
 * 配置归一化单测（验收项 4：不写任何配置时 6 项均取默认值）。
 */
import { describe, expect, it } from 'vitest'
import {
  DEFAULT_ACTOR,
  DEFAULT_BASE_URL,
  DEFAULT_CONFIG,
  DEFAULT_REQUEST_TIMEOUT_MS,
  metadataByteLength,
  resolveConfig,
} from '../src/config.js'

describe('resolveConfig：默认值', () => {
  it('无配置时 6 项全部取默认值', () => {
    const resolved = resolveConfig()
    expect(resolved).toEqual(DEFAULT_CONFIG)
    expect(resolved.baseUrl).toBe('http://127.0.0.1:8720')
    expect(resolved.actor).toBe('agent:echuan')
    expect(resolved.requestTimeoutMs).toBe(35_000)
    // 默认不排除任何工具（全量 23 个）。
    expect(resolved.exclude).toEqual([])
    // 嵌套：worker.poll.enable / worker.metadata
    expect(resolved.worker.poll.enable).toBe(false)
    expect(resolved.worker.metadata).toEqual({})
  })

  it('空对象与 null 等价于无配置', () => {
    expect(resolveConfig({})).toEqual(DEFAULT_CONFIG)
    expect(resolveConfig(null)).toEqual(DEFAULT_CONFIG)
  })

  it('默认值常量与 Schema 保持一致', () => {
    expect(DEFAULT_CONFIG.baseUrl).toBe(DEFAULT_BASE_URL)
    expect(DEFAULT_CONFIG.actor).toBe(DEFAULT_ACTOR)
    expect(DEFAULT_CONFIG.requestTimeoutMs).toBe(DEFAULT_REQUEST_TIMEOUT_MS)
    expect(DEFAULT_CONFIG.exclude).toEqual([])
  })
})

describe('resolveConfig：exclude 归一化', () => {
  it('null / 空数组 / 不配置三者等价', () => {
    expect(resolveConfig({ exclude: null }).exclude).toEqual([])
    expect(resolveConfig({ exclude: [] }).exclude).toEqual([])
    expect(resolveConfig({}).exclude).toEqual([])
    expect(resolveConfig({ exclude: [] })).toEqual(DEFAULT_CONFIG)
  })

  it('每项 trim，重复项去重且保留首次出现顺序', () => {
    expect(resolveConfig({
      exclude: ['  echuan_plan_submit ', 'echuan_task_claim', 'echuan_plan_submit'],
    }).exclude).toEqual(['echuan_plan_submit', 'echuan_task_claim'])
  })

  it('空白项抛 TypeError，不静默丢弃', () => {
    expect(() => resolveConfig({ exclude: ['echuan_task_claim', '  '] }))
      .toThrow(TypeError)
    expect(() => resolveConfig({ exclude: ['echuan_task_claim', '  '] }))
      .toThrow(/exclude 不能包含空白工具名/)
  })

  it('非法类型按 Schema 语义抛错', () => {
    // @ts-expect-error 故意传错类型：exclude 必须是字符串数组
    expect(() => resolveConfig({ exclude: 'echuan_task_claim' })).toThrow(TypeError)
    // @ts-expect-error 元素必须是字符串
    expect(() => resolveConfig({ exclude: [1] })).toThrow(TypeError)
  })

  it('不改变其他字段的默认值', () => {
    const resolved = resolveConfig({ exclude: ['echuan_project_create'] })
    expect(resolved.baseUrl).toBe(DEFAULT_BASE_URL)
    expect(resolved.actor).toBe(DEFAULT_ACTOR)
    expect(resolved.requestTimeoutMs).toBe(DEFAULT_REQUEST_TIMEOUT_MS)
    expect(resolved.worker.poll.enable).toBe(false)
    expect(resolved.worker.metadata).toEqual({})
  })
})

describe('resolveConfig：覆盖与归一化', () => {
  it('嵌套覆盖 worker.poll.enable 与 worker.metadata', () => {
    const metadata = { host: 'mac-01', tags: ['a', 'b'], nested: { ok: true }, n: 3 }
    const resolved = resolveConfig({
      baseUrl: 'https://echuan.example.com',
      actor: 'agent:echuan:worker-7',
      requestTimeoutMs: 5_000,
      worker: { poll: { enable: true }, metadata },
    })

    expect(resolved.baseUrl).toBe('https://echuan.example.com')
    expect(resolved.actor).toBe('agent:echuan:worker-7')
    expect(resolved.requestTimeoutMs).toBe(5_000)
    expect(resolved.worker.poll.enable).toBe(true)
    // metadata 原样保留：不改键、不改值形态
    expect(resolved.worker.metadata).toEqual(metadata)
  })

  it('只覆盖 metadata 时其余字段回落默认值', () => {
    const resolved = resolveConfig({ worker: { metadata: { region: 'cn' } } })
    expect(resolved.worker.metadata).toEqual({ region: 'cn' })
    expect(resolved.baseUrl).toBe(DEFAULT_BASE_URL)
    expect(resolved.actor).toBe(DEFAULT_ACTOR)
    expect(resolved.requestTimeoutMs).toBe(DEFAULT_REQUEST_TIMEOUT_MS)
    expect(resolved.worker.poll.enable).toBe(false)
  })

  it('baseUrl 去掉结尾斜杠，空白回落默认值', () => {
    expect(resolveConfig({ baseUrl: 'http://127.0.0.1:8720/' }).baseUrl).toBe('http://127.0.0.1:8720')
    expect(resolveConfig({ baseUrl: 'http://host:8720///' }).baseUrl).toBe('http://host:8720')
    expect(resolveConfig({ baseUrl: '   ' }).baseUrl).toBe(DEFAULT_BASE_URL)
  })

  it('S-02：actor 做 trim；trim 后为空回落默认值', () => {
    expect(resolveConfig({ actor: '  agent:echuan:worker-7  ' }).actor).toBe('agent:echuan:worker-7')
    expect(resolveConfig({ actor: '   ' }).actor).toBe(DEFAULT_ACTOR)
    expect(resolveConfig({ actor: '' }).actor).toBe(DEFAULT_ACTOR)
  })

  it('S-03：baseUrl 做 URL 形态校验，非法时抛可读错误', () => {
    expect(() => resolveConfig({ baseUrl: 'not a url' })).toThrow(/baseUrl 不是合法的 URL/)
    expect(() => resolveConfig({ baseUrl: 'http://' })).toThrow(/baseUrl 不是合法的 URL/)
    expect(() => resolveConfig({ baseUrl: 'ftp://host:21' })).toThrow(/只支持 http\/https/)
    expect(() => resolveConfig({ baseUrl: 'http://user:pw@host:8720' })).toThrow(/不能包含账号或密码/)
    expect(() => resolveConfig({ baseUrl: 'http://host:8720/?a=1' })).toThrow(/不能带查询串或片段/)
    expect(() => resolveConfig({ baseUrl: 'http://host:8720/#x' })).toThrow(/不能带查询串或片段/)

    expect(resolveConfig({ baseUrl: 'https://echuan.example.com/prefix/' }).baseUrl)
      .toBe('https://echuan.example.com/prefix')
  })

  it('非法类型按 Schema 语义抛错，不静默兜底', () => {
    // @ts-expect-error 故意传错类型：运行期应被 Schema 拒绝
    expect(() => resolveConfig({ requestTimeoutMs: 'abc' })).toThrow(TypeError)
  })
})

describe('resolveConfig：worker.workspaceRoot', () => {
  it('默认未配置（undefined，不是空串）', () => {
    expect(resolveConfig().worker.workspaceRoot).toBeUndefined()
    expect(DEFAULT_CONFIG.worker.workspaceRoot).toBeUndefined()
    expect('workspaceRoot' in resolveConfig().worker).toBe(false)
  })

  it('绝对路径原样保留（只去首尾空白）', () => {
    expect(resolveConfig({ worker: { workspaceRoot: '  /Users/you/project/opc  ' } }).worker.workspaceRoot)
      .toBe('/Users/you/project/opc')
  })

  it('相对路径抛 TypeError（中文说明）', () => {
    expect(() => resolveConfig({ worker: { workspaceRoot: 'project/opc' } }))
      .toThrow(TypeError)
    expect(() => resolveConfig({ worker: { workspaceRoot: 'project/opc' } }))
      .toThrow(/worker\.workspaceRoot 必须是绝对路径/)
  })

  it('空串 / 空白串视同未配置', () => {
    expect(resolveConfig({ worker: { workspaceRoot: '' } }).worker.workspaceRoot).toBeUndefined()
    expect(resolveConfig({ worker: { workspaceRoot: '   ' } }).worker.workspaceRoot).toBeUndefined()
  })
})

describe('metadataByteLength', () => {
  it('按 UTF-8 计字节；空对象为 2 字节', () => {
    expect(metadataByteLength({})).toBe(2)
    expect(metadataByteLength({ a: 1 })).toBe(Buffer.byteLength('{"a":1}', 'utf8'))
    expect(metadataByteLength({ k: '驿传' })).toBe(Buffer.byteLength('{"k":"驿传"}', 'utf8'))
  })

  it('S-04：循环引用抛可读错误，不再静默返回 0', () => {
    const cyclic: Record<string, unknown> = {}
    cyclic.self = cyclic
    expect(() => metadataByteLength(cyclic)).toThrow(/无法序列化为 JSON/)
    expect(() => metadataByteLength(cyclic)).toThrow(/循环引用/)
  })

  it('S-04：BigInt 同样抛可读错误', () => {
    expect(() => metadataByteLength({ n: 1n })).toThrow(/无法序列化为 JSON/)
  })
})
