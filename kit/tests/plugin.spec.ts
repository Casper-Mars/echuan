/**
 * 插件入口单测：host 服务、效果生命周期、preset 工具子路径。
 *
 * 结构性证明：
 * - host `apply` 只访问 `ctx.effect` / `ctx.logger` / `ctx.skills` / `ctx.agents`，
 *   不注册任何工具；
 * - `echuan` 服务由 host 异步提供；preset 子路径（leader / requester / readonly）
 *   自己注入 `tools` + `echuan` 并注册白名单工具；
 * - 默认配置（poll=false）下零定时器；心跳 / metadata 守卫保持原行为。
 */
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Context } from '@deepseek-ai/cordis'
import { apply, inject, metadataByteLength, name } from '../src/index.js'
import { READ_TOOL_NAMES, REQUESTER_TOOL_NAMES, TOOL_NAMES } from '../src/tool-names.js'
import { FIRST_HEARTBEAT_DELAY_MS } from '../src/worker-shared.js'
import { apply as applyLeader } from '../src/tools/leader.js'
import { apply as applyRequester } from '../src/tools/requester.js'
import { apply as applyReadonly } from '../src/tools/readonly.js'

const here = path.dirname(fileURLToPath(import.meta.url))
const kitRoot = path.resolve(here, '..')

interface FakeContext {
  ctx: Context
  labels: string[]
  disposeAll(): void
  forbiddenAccess: string[]
  agentAccess: string[]
  registrations: string[]
  skillRegistrations: string[]
  services: Map<string, unknown>
}

function createFakeContext(): FakeContext {
  const labels: string[] = []
  const disposers: Array<() => void> = []
  const forbiddenAccess: string[] = []
  const agentAccess: string[] = []
  const registrations: string[] = []
  const skillRegistrations: string[] = []
  const services = new Map<string, unknown>()

  const base = {
    effect(execute: () => (() => void) | void, label?: string) {
      labels.push(label ?? '')
      const cleanup = execute()
      if (typeof cleanup === 'function') disposers.push(cleanup)
      return { dispose: async () => {} }
    },
    provide(name: string, value: unknown) {
      services.set(name, value)
    },
    tools: {
      register(tool: { name: string }) {
        registrations.push(tool.name)
        return () => {
          const index = registrations.indexOf(tool.name)
          if (index >= 0) registrations.splice(index, 1)
        }
      },
    },
    skills: {
      register(skill: { name: string }) {
        skillRegistrations.push(skill.name)
        return () => {
          const index = skillRegistrations.indexOf(skill.name)
          if (index >= 0) skillRegistrations.splice(index, 1)
        }
      },
    },
    agents: {
      roots: () => {
        agentAccess.push('agents')
        return []
      },
      get: () => undefined,
    },
    logger: {
      info: () => {},
      warn: () => {},
      debug: () => {},
      error: () => {},
    },
  }

  const FORBIDDEN_SERVICES = new Set(['llm', 'models', 'sessions', 'http', 'server'])
  const ctx = new Proxy(base, {
    get(target, key, receiver) {
      if (typeof key === 'string' && FORBIDDEN_SERVICES.has(key)) {
        forbiddenAccess.push(key)
        throw new Error(`本插件不应访问 ctx.${key}`)
      }
      if (key === 'agents') agentAccess.push('agents')
      if (typeof key === 'string' && services.has(key)) return services.get(key)
      return Reflect.get(target, key, receiver)
    },
  }) as unknown as Context

  return {
    ctx,
    labels,
    forbiddenAccess,
    agentAccess,
    registrations,
    skillRegistrations,
    services,
    disposeAll() {
      for (const disposer of [...disposers].reverse()) disposer()
    },
  }
}

async function waitForService(fake: FakeContext): Promise<void> {
  await vi.waitFor(() => {
    expect(fake.services.has('echuan')).toBe(true)
  })
}

function logLines(spy: { mock: { calls: unknown[][] } }): string[] {
  return spy.mock.calls.map(call => String(call[0]))
}

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('插件标识', () => {
  it('id 为 echuan，host 只依赖 skills/agents', () => {
    expect(name).toBe('echuan')
    expect(inject).toEqual(['skills', 'agents'])
  })

  it('patch 行与入口声明一致（id / name / inject）', () => {
    const patch = readFileSync(path.join(kitRoot, 'cordis.patch.yml'), 'utf8')
    expect(patch).toContain('- insert:')
    expect(patch).toContain('id: echuan')
    expect(patch).toContain('name: dsh-echuan')
    expect(patch).toContain("inject: ['skills', 'agents']")
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

describe('apply：host 加载与生命周期', () => {
  it('打印加载日志并含关键字段', async () => {
    const spy = vi.spyOn(console, 'log').mockImplementation(() => {})
    const fake = createFakeContext()
    apply(fake.ctx, undefined)

    const lines = logLines(spy)
    const loaded = lines.find(line => line.startsWith('[dsh-echuan]'))
    expect(loaded).toBeDefined()
    expect(loaded).toContain('name=echuan')
    expect(loaded).toContain('inject=[skills, agents]')
    expect(loaded).toContain('baseUrl=http://127.0.0.1:8720')
    expect(loaded).toContain('actor=agent:echuan')
    expect(loaded).toContain('poll=false')

    await waitForService(fake)
  })

  it('注册 service + skill + runtime 三个 effect，host 不注册工具', async () => {
    vi.spyOn(console, 'log').mockImplementation(() => {})
    const fake = createFakeContext()
    apply(fake.ctx, undefined)

    expect(fake.labels).toEqual(['echuan.service()', 'echuan.skill()', 'echuan.runtime()'])
    expect(fake.forbiddenAccess).toEqual([])
    expect(fake.agentAccess).toEqual([])

    await waitForService(fake)
    expect(fake.registrations).toEqual([])
    const lines = logLines(vi.mocked(console.log))
    expect(lines.some(line => line.includes('host 服务就绪'))).toBe(true)
  })

  it('同步注册技能 echuan-task-execution，dispose 后注销', async () => {
    const spy = vi.spyOn(console, 'log').mockImplementation(() => {})
    const fake = createFakeContext()
    apply(fake.ctx, undefined)
    await waitForService(fake)

    expect(fake.skillRegistrations).toEqual(['echuan-task-execution', 'echuan-fork-delegation'])
    expect(logLines(spy).some(line => line.includes('已注册技能 echuan-task-execution'))).toBe(true)
    expect(logLines(spy).some(line => line.includes('echuan-fork-delegation'))).toBe(true)

    fake.disposeAll()
    expect(fake.skillRegistrations).toEqual([])
    expect(logLines(spy).some(line => line.includes('已注销技能 echuan-task-execution'))).toBe(true)
    expect(logLines(spy).some(line => line.includes('echuan-fork-delegation'))).toBe(true)
  })

  it('默认配置（poll=false）下零定时器', async () => {
    vi.spyOn(console, 'log').mockImplementation(() => {})
    const setIntervalSpy = vi.spyOn(globalThis, 'setInterval')
    const fake = createFakeContext()
    apply(fake.ctx, undefined)
    expect(setIntervalSpy).not.toHaveBeenCalled()
    await waitForService(fake)
  })

  it('poll=true 时启动心跳循环（首轮延迟 5s），disposer 清除定时器且重复清理无副作用', async () => {
    const fetchSpy = vi.fn(async () => { throw new Error('offline') })
    vi.stubGlobal('fetch', fetchSpy)
    const spy = vi.spyOn(console, 'log').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const setSpy = vi.spyOn(globalThis, 'setTimeout')
    const clearSpy = vi.spyOn(globalThis, 'clearTimeout')
    const fake = createFakeContext()
    const firstBeatHandle = (): unknown => setSpy.mock.calls
      .map((call, index) => ({ ms: call[1], value: setSpy.mock.results[index]?.value }))
      .find(entry => entry.ms === FIRST_HEARTBEAT_DELAY_MS)?.value

    apply(fake.ctx, { worker: { poll: { enable: true }, presetByKind: { dev: 'dev-leader' } } })
    await waitForService(fake)
    await vi.waitFor(() => { expect(firstBeatHandle()).toBeDefined() })
    expect(fetchSpy).not.toHaveBeenCalled()
    expect(fake.agentAccess).toEqual([])

    const handle = firstBeatHandle()
    fake.disposeAll()
    expect(clearSpy.mock.calls.some(([cleared]) => cleared === handle)).toBe(true)
    const afterDispose = spy.mock.calls.length
    expect(logLines(spy).some(line => line.includes('心跳已停止'))).toBe(true)
    fake.disposeAll()
    expect(spy.mock.calls.length).toBe(afterDispose)
  })
})

describe('exclude 校验', () => {
  it('未知工具名同步抛 TypeError，不留 effect', () => {
    const fake = createFakeContext()
    expect(() => apply(fake.ctx, { exclude: ['echuan_work_claim'] })).toThrow(TypeError)
    expect(() => apply(fake.ctx, { exclude: ['echuan_task_createt'] }))
      .toThrow(/echuan_task_createt/)
    expect(fake.labels).toEqual([])
  })

  it('空白项同步抛 TypeError', () => {
    const fake = createFakeContext()
    expect(() => apply(fake.ctx, { exclude: ['  '] })).toThrow(/exclude 不能包含空白工具名/)
  })
})

describe('preset 工具子路径', () => {
  it('leader 注册 18 个（10 写 + 8 读），dispose 后全部注销', async () => {
    vi.spyOn(console, 'log').mockImplementation(() => {})
    const fake = createFakeContext()
    apply(fake.ctx, undefined)
    await waitForService(fake)

    applyLeader(fake.ctx)
    expect(fake.registrations).toEqual([...TOOL_NAMES])
    fake.disposeAll()
    expect(fake.registrations).toEqual([])
  })

  it('requester 注册 15 个：8 读 + 7 写，不含 claim/renew/product_create', async () => {
    vi.spyOn(console, 'log').mockImplementation(() => {})
    const fake = createFakeContext()
    apply(fake.ctx, undefined)
    await waitForService(fake)

    applyRequester(fake.ctx)
    expect([...fake.registrations].sort()).toEqual([...REQUESTER_TOOL_NAMES].sort())
    for (const name of ['echuan_task_claim', 'echuan_task_renew', 'echuan_product_create']) {
      expect(fake.registrations).not.toContain(name)
    }
  })

  it('readonly 只注册 8 个读工具', async () => {
    vi.spyOn(console, 'log').mockImplementation(() => {})
    const fake = createFakeContext()
    apply(fake.ctx, undefined)
    await waitForService(fake)

    applyReadonly(fake.ctx)
    expect(fake.registrations).toEqual([...READ_TOOL_NAMES])
    expect(fake.registrations).not.toContain('echuan_task_report')
  })

  it('host exclude 会传递到子路径', async () => {
    vi.spyOn(console, 'log').mockImplementation(() => {})
    const fake = createFakeContext()
    apply(fake.ctx, { exclude: ['echuan_project_create', 'echuan_task_create'] })
    await waitForService(fake)

    applyLeader(fake.ctx)
    expect(fake.registrations).toHaveLength(16)
    expect(fake.registrations).not.toContain('echuan_project_create')
    expect(fake.registrations).not.toContain('echuan_task_create')
  })
})

describe('apply：R-02 metadata 超限不启动心跳循环', () => {
  it('poll=true 但 metadata 超 8KiB：零请求、零定时器，warn 含实际字节数与上限', async () => {
    const fetchSpy = vi.fn(async () => { throw new Error('超限时不应发心跳') })
    vi.stubGlobal('fetch', fetchSpy)
    vi.spyOn(console, 'log').mockImplementation(() => {})
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const setSpy = vi.spyOn(globalThis, 'setTimeout')
    const fake = createFakeContext()
    const metadata = { blob: 'x'.repeat(9_000) }
    const bytes = metadataByteLength(metadata)

    apply(fake.ctx, { worker: { poll: { enable: true }, presetByKind: { dev: 'dev-leader' }, metadata } })
    await waitForService(fake)
    await new Promise<void>(resolve => { setTimeout(resolve, 20) })

    expect(fetchSpy).not.toHaveBeenCalled()
    expect(setSpy.mock.calls.some(([, ms]) => ms === 15_000)).toBe(false)
    expect(fake.agentAccess).toEqual([])
    const warns = warnSpy.mock.calls.map(call => String(call[0]))
    const limitWarn = warns.find(line => line.includes('不启动心跳循环'))
    expect(limitWarn).toBeDefined()
    expect(limitWarn).toContain(`${bytes}B`)
    expect(limitWarn).toContain('8192B')
    fake.disposeAll()
  })
})
