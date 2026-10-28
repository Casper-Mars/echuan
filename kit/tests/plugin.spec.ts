/**
 * 插件入口单测：加载日志、effect 生命周期、以及「不碰 tools/skills/agents 服务」。
 *
 * 验收项 7/8 的结构性证明：
 * - `apply` 只访问 `ctx.effect` 与 `ctx.logger`，**不访问** `ctx.tools` / `ctx.skills` /
 *   `ctx.agents`（用 Proxy 在访问时直接抛错来锁定这一点）——本阶段没有工具注册；
 * - 默认配置（poll=false）下 `apply` 不创建任何定时器；
 * - effect 的 disposer 执行后：定时器清除、只多一条卸载日志，再调用无副作用。
 */
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Context } from '@deepseek-ai/cordis'
import { apply, inject, metadataByteLength, name } from '../src/index.js'

const here = path.dirname(fileURLToPath(import.meta.url))
const kitRoot = path.resolve(here, '..')

interface FakeContext {
  ctx: Context
  /** effect 的 label 列表。 */
  labels: string[]
  /** 依次执行所有 disposer（逆序，模拟 fiber 卸载）。 */
  disposeAll(): void
  /** 被禁止访问的服务名。 */
  forbiddenAccess: string[]
  /** `ctx.agents` 被访问的次数（poll=true 时投递需要读根 agent）。 */
  agentAccess: string[]
}

/**
 * 构造只提供 effect / logger / agents 的假上下文。
 *
 * `tools` / `skills` 仍禁止访问（本插件不注册工具）；`agents` 允许访问并记录，
 * 用于断言 poll=false 时连 agent 列表都不读。
 */
function createFakeContext(): FakeContext {
  const labels: string[] = []
  const disposers: Array<() => void> = []
  const forbiddenAccess: string[] = []
  const agentAccess: string[] = []

  const base = {
    effect(execute: () => (() => void) | void, label?: string) {
      labels.push(label ?? '')
      const cleanup = execute()
      if (typeof cleanup === 'function') disposers.push(cleanup)
      return { dispose: async () => {} }
    },
    agents: {
      roots: () => {
        agentAccess.push('agents')
        return []
      },
    },
    logger: {
      info: () => {},
      warn: () => {},
      debug: () => {},
      error: () => {},
    },
  }

  const ctx = new Proxy(base, {
    get(target, key, receiver) {
      if (key === 'tools' || key === 'skills') {
        forbiddenAccess.push(String(key))
        throw new Error(`本阶段不应访问 ctx.${String(key)}`)
      }
      if (key === 'agents') agentAccess.push('agents')
      return Reflect.get(target, key, receiver)
    },
  }) as unknown as Context

  return {
    ctx,
    labels,
    forbiddenAccess,
    agentAccess,
    disposeAll() {
      for (const disposer of [...disposers].reverse()) disposer()
    },
  }
}

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('插件标识', () => {
  it('id 为 echuan，依赖 tools/skills/agents', () => {
    expect(name).toBe('echuan')
    expect(inject).toEqual(['tools', 'skills', 'agents'])
  })

  it('patch 行与入口声明一致（id / name / inject）', () => {
    const patch = readFileSync(path.join(kitRoot, 'cordis.patch.yml'), 'utf8')
    expect(patch).toContain('- insert:')
    expect(patch).toContain('id: echuan')
    expect(patch).toContain('name: dsh-echuan')
    expect(patch).toContain("inject: ['tools', 'skills', 'agents']")
  })

  it('package.json 声明 dsh.bundle.patch 且指向存在的 patch 文件', () => {
    const pkg = JSON.parse(readFileSync(path.join(kitRoot, 'package.json'), 'utf8')) as {
      name: string
      type: string
      main: string
      dsh: { bundle: { patch: string } }
    }
    expect(pkg.type).toBe('module')
    expect(pkg.dsh.bundle.patch).toBe('./cordis.patch.yml')
    expect(() => readFileSync(path.join(kitRoot, pkg.dsh.bundle.patch), 'utf8')).not.toThrow()
  })
})

describe('apply：加载与生命周期', () => {
  it('打印 [dsh-echuan] 加载日志并含关键字段', () => {
    const spy = vi.spyOn(console, 'log').mockImplementation(() => {})
    const fake = createFakeContext()

    apply(fake.ctx, undefined)

    const lines = spy.mock.calls.map(call => String(call[0]))
    expect(lines.length).toBeGreaterThan(0)
    const loaded = lines.find(line => line.startsWith('[dsh-echuan]'))
    expect(loaded).toBeDefined()
    expect(loaded).toContain('name=echuan')
    expect(loaded).toContain('inject=[tools, skills, agents]')
    expect(loaded).toContain('baseUrl=http://127.0.0.1:8720')
    expect(loaded).toContain('actor=agent:echuan')
    expect(loaded).toContain('poll=false')
  })

  it('只注册一个 effect（echuan.runtime()），且不访问 tools/skills/agents', () => {
    vi.spyOn(console, 'log').mockImplementation(() => {})
    const fake = createFakeContext()

    apply(fake.ctx, undefined)

    expect(fake.labels).toEqual(['echuan.runtime()'])
    expect(fake.forbiddenAccess).toEqual([])
    // poll=false：连根 agent 列表都不读。
    expect(fake.agentAccess).toEqual([])
  })

  it('默认配置（poll=false）下零定时器', () => {
    vi.spyOn(console, 'log').mockImplementation(() => {})
    const setIntervalSpy = vi.spyOn(globalThis, 'setInterval')
    const fake = createFakeContext()

    apply(fake.ctx, undefined)

    expect(setIntervalSpy).not.toHaveBeenCalled()
  })

  it('poll=true 时启动心跳循环，disposer 清除定时器且重复清理无副作用', async () => {
    // 假 fetch：首轮心跳失败（网络错误），循环会按兜底 15s 排下一轮。
    vi.stubGlobal('fetch', vi.fn(async () => {
      throw new Error('offline')
    }))
    const spy = vi.spyOn(console, 'log').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const setSpy = vi.spyOn(globalThis, 'setTimeout')
    const clearSpy = vi.spyOn(globalThis, 'clearTimeout')
    const fake = createFakeContext()

    const heartbeatHandle = (): unknown => setSpy.mock.calls
      .map((call, index) => ({ ms: call[1], value: setSpy.mock.results[index]?.value }))
      .find(entry => entry.ms === 15_000)?.value

    apply(fake.ctx, { worker: { poll: { enable: true } } })

    // 惰性 import → 首轮心跳（假 fetch 失败）→ 才会按兜底间隔排下一轮。
    await vi.waitFor(() => {
      expect(fake.agentAccess.length).toBeGreaterThan(0)
      expect(heartbeatHandle()).toBeDefined()
    })

    const handle = heartbeatHandle()
    fake.disposeAll()
    expect(clearSpy.mock.calls.some(([cleared]) => cleared === handle)).toBe(true)

    const afterDispose = spy.mock.calls.length
    const lines = spy.mock.calls.map(call => String(call[0]))
    expect(lines.some(line => line.includes('心跳已停止'))).toBe(true)

    fake.disposeAll()
    expect(spy.mock.calls.length).toBe(afterDispose)
  })
})

describe('apply：R-02 metadata 超限不启动心跳循环', () => {
  it('poll=true 但 metadata 超 8KiB：零请求、零定时器，warn 含实际字节数与上限', async () => {
    const fetchSpy = vi.fn(async () => {
      throw new Error('超限时不应发心跳')
    })
    vi.stubGlobal('fetch', fetchSpy)
    vi.spyOn(console, 'log').mockImplementation(() => {})
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const setSpy = vi.spyOn(globalThis, 'setTimeout')
    const fake = createFakeContext()
    const metadata = { blob: 'x'.repeat(9_000) }
    const bytes = metadataByteLength(metadata)
    expect(bytes).toBeGreaterThan(8192)

    apply(fake.ctx, { worker: { poll: { enable: true }, metadata } })
    // 留出一轮惰性 import 的时间：正常路径这里会走 createWorkerLoop + start()。
    await new Promise<void>(resolve => { setTimeout(resolve, 20) })

    expect(fetchSpy).not.toHaveBeenCalled()
    expect(setSpy.mock.calls.some(([, ms]) => ms === 15_000)).toBe(false)
    // 循环根本没创建 → 连根 agent 都不读。
    expect(fake.agentAccess).toEqual([])

    const warns = warnSpy.mock.calls.map(call => String(call[0]))
    const limitWarn = warns.find(line => line.includes('不启动心跳循环'))
    expect(limitWarn).toBeDefined()
    expect(limitWarn).toContain(`${bytes}B`)
    expect(limitWarn).toContain('8192B')

    // disposer 仍可安全执行。
    fake.disposeAll()
  })
})
