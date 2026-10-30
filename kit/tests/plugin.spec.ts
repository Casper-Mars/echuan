/**
 * 插件入口单测：加载日志、effect 生命周期、工具与技能注册。
 *
 * 结构性证明：
 * - `apply` 只访问 `ctx.effect` / `ctx.logger` / `ctx.tools` / `ctx.skills` /
 *   `ctx.agents`，不触碰其余服务（用 Proxy 拦截其他服务名并抛错）；
 * - 工具注册走惰性 import，技能注册同步完成（正文为构建产物内的小文件）；
 * - 默认配置（poll=false）下 `apply` 不创建任何定时器，也不读根 agent 列表；
 * - effect 的 disposer 执行后：定时器清除、工具与技能一并注销、只多卸载日志，再调用无副作用。
 */
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Context } from '@deepseek-ai/cordis'
import { apply, inject, metadataByteLength, name } from '../src/index.js'
import { FIRST_HEARTBEAT_DELAY_MS } from '../src/worker.js'

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
  /** 已注册的工具名（注销时移除）。 */
  registrations: string[]
  /** 已注册的技能名（注销时移除）。 */
  skillRegistrations: string[]
}

/**
 * 构造只提供 effect / logger / agents / tools / skills 的假上下文。
 *
 * `tools` 记录工具注册（16 个：9 写 + 7 读）；`skills` 记录技能注册（1 个）；
 * `agents` 记录访问次数，用于断言 poll=false 时连 agent 列表都不读。
 */
function createFakeContext(): FakeContext {
  const labels: string[] = []
  const disposers: Array<() => void> = []
  const forbiddenAccess: string[] = []
  const agentAccess: string[] = []
  const registrations: string[] = []
  const skillRegistrations: string[] = []

  const base = {
    effect(execute: () => (() => void) | void, label?: string) {
      labels.push(label ?? '')
      const cleanup = execute()
      if (typeof cleanup === 'function') disposers.push(cleanup)
      return { dispose: async () => {} }
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
    },
    logger: {
      info: () => {},
      warn: () => {},
      debug: () => {},
      error: () => {},
    },
  }

  // 本插件不该触碰的服务：访问即抛错，锁定「只依赖 tools/skills/agents」。
  const FORBIDDEN_SERVICES = new Set(['llm', 'models', 'sessions', 'http', 'server'])

  const ctx = new Proxy(base, {
    get(target, key, receiver) {
      if (typeof key === 'string' && FORBIDDEN_SERVICES.has(key)) {
        forbiddenAccess.push(key)
        throw new Error(`本插件不应访问 ctx.${key}`)
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
    registrations,
    skillRegistrations,
    disposeAll() {
      for (const disposer of [...disposers].reverse()) disposer()
    },
  }
}

/** 等惰性 import 完成、16 个工具注册就绪（避免异步日志干扰后续断言）。 */
async function settleTools(fake: FakeContext): Promise<void> {
  await vi.waitFor(() => {
    expect(fake.registrations).toHaveLength(16)
  })
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
  it('打印 [dsh-echuan] 加载日志并含关键字段', async () => {
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

    await settleTools(fake)
  })

  it('注册 tools + skill + runtime 三个 effect，不访问额外服务', async () => {
    vi.spyOn(console, 'log').mockImplementation(() => {})
    const fake = createFakeContext()

    apply(fake.ctx, undefined)

    expect(fake.labels).toEqual(['echuan.tools()', 'echuan.skill()', 'echuan.runtime()'])
    expect(fake.forbiddenAccess).toEqual([])
    // poll=false：连根 agent 列表都不读。
    expect(fake.agentAccess).toEqual([])

    await settleTools(fake)
  })

  it('同步注册技能 echuan-task-execution，dispose 后注销', async () => {
    const spy = vi.spyOn(console, 'log').mockImplementation(() => {})
    const fake = createFakeContext()

    apply(fake.ctx, undefined)
    await settleTools(fake)

    // 技能注册是同步的：apply 返回时即已就绪。
    expect(fake.skillRegistrations).toEqual(['echuan-task-execution'])
    const lines = spy.mock.calls.map(call => String(call[0]))
    expect(lines.some(line => line.includes('已注册技能 echuan-task-execution'))).toBe(true)

    fake.disposeAll()
    expect(fake.skillRegistrations).toEqual([])
    expect(spy.mock.calls.map(call => String(call[0]))
      .some(line => line.includes('已注销技能 echuan-task-execution'))).toBe(true)
  })

  it('惰性注册 16 个工具（9 写 + 7 读）并打印注册日志', async () => {
    const spy = vi.spyOn(console, 'log').mockImplementation(() => {})
    const fake = createFakeContext()

    apply(fake.ctx, undefined)
    await settleTools(fake)

    expect(fake.registrations).toEqual([
      'echuan_task_claim',
      'echuan_task_renew',
      'echuan_task_report',
      'echuan_project_create',
      'echuan_project_requirement_write',
      'echuan_project_submit',
      'echuan_spec_create',
      'echuan_spec_revise',
      'echuan_plan_submit',
      'echuan_inbox',
      'echuan_product_list',
      'echuan_project_list',
      'echuan_task_list',
      'echuan_project_get',
      'echuan_task_get',
      'echuan_material_get',
    ])
    const lines = spy.mock.calls.map(call => String(call[0]))
    expect(lines.some(line => line.includes('已注册 16 个工具'))).toBe(true)
    expect(lines.some(line => line.includes('9 写 + 7 读'))).toBe(true)

    fake.disposeAll()
    expect(fake.registrations).toEqual([])
  })

  it('默认配置（poll=false）下零定时器', async () => {
    vi.spyOn(console, 'log').mockImplementation(() => {})
    const setIntervalSpy = vi.spyOn(globalThis, 'setInterval')
    const fake = createFakeContext()

    apply(fake.ctx, undefined)

    expect(setIntervalSpy).not.toHaveBeenCalled()
    await settleTools(fake)
  })

  it('poll=true 时启动心跳循环（首轮延迟 5s），disposer 清除定时器且重复清理无副作用', async () => {
    // 假 fetch：真实请求不应发生在延迟期内（如发生也会被这里记下）。
    const fetchSpy = vi.fn(async () => {
      throw new Error('offline')
    })
    vi.stubGlobal('fetch', fetchSpy)
    const spy = vi.spyOn(console, 'log').mockImplementation(() => {})
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    const setSpy = vi.spyOn(globalThis, 'setTimeout')
    const clearSpy = vi.spyOn(globalThis, 'clearTimeout')
    const fake = createFakeContext()

    const firstBeatHandle = (): unknown => setSpy.mock.calls
      .map((call, index) => ({ ms: call[1], value: setSpy.mock.results[index]?.value }))
      .find(entry => entry.ms === FIRST_HEARTBEAT_DELAY_MS)?.value

    apply(fake.ctx, { worker: { poll: { enable: true } } })
    await settleTools(fake)

    // 惰性 import → start() 只排一个 5s 后的首轮定时器，延迟期内零请求。
    await vi.waitFor(() => {
      expect(firstBeatHandle()).toBeDefined()
    })
    expect(fetchSpy).not.toHaveBeenCalled()
    expect(fake.agentAccess).toEqual([])

    const handle = firstBeatHandle()
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
    await settleTools(fake)
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
