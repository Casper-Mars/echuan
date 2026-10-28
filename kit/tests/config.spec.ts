/**
 * 配置归一化单测（验收项 4：不写任何配置时 5 项均取默认值）。
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
  it('无配置时 5 项全部取默认值', () => {
    const resolved = resolveConfig()
    expect(resolved).toEqual(DEFAULT_CONFIG)
    expect(resolved.baseUrl).toBe('http://127.0.0.1:8720')
    expect(resolved.actor).toBe('agent:echuan')
    expect(resolved.requestTimeoutMs).toBe(35_000)
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

  it('非法类型按 Schema 语义抛错，不静默兜底', () => {
    // @ts-expect-error 故意传错类型：运行期应被 Schema 拒绝
    expect(() => resolveConfig({ requestTimeoutMs: 'abc' })).toThrow(TypeError)
  })
})

describe('metadataByteLength', () => {
  it('按 UTF-8 计字节；空对象为 2 字节', () => {
    expect(metadataByteLength({})).toBe(2)
    expect(metadataByteLength({ a: 1 })).toBe(Buffer.byteLength('{"a":1}', 'utf8'))
    expect(metadataByteLength({ k: '驿传' })).toBe(Buffer.byteLength('{"k":"驿传"}', 'utf8'))
  })

  it('不可序列化时返回 0，不抛错', () => {
    const cyclic: Record<string, unknown> = {}
    cyclic.self = cyclic
    expect(metadataByteLength(cyclic)).toBe(0)
  })
})
