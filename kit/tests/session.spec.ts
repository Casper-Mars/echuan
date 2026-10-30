/**
 * ensureSession 单测：三条路径 + 优先链五个分支 + 降级与幂等 + 修复回归（19 条）。
 *
 * 全部依赖注入，测试里不碰真实文件系统（`mkdir` / `cwd` 都是替身），
 * 断言同时覆盖 warn 文案里的项目号与选中路径。
 */
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import {
  ensureSession,
  type EnsureAgent,
  type EnsureAgentHandle,
  type EnsureAgentOptions,
  type EnsureAgentPreset,
  type EnsureCreateOptions,
  type EnsureResumeOptions,
  type EnsureSessionDeps,
  type EnsureSessionHeader,
  type EnsureWorkspaceLike,
} from '../src/session.js'

const PROJECT = 'APP-1'
const AGENT_SESSION = { id: PROJECT }
const AGENT: EnsureAgent = { id: PROJECT, session: AGENT_SESSION }
const FALLBACK_CWD = '/proc-cwd'
const CONFIGURED_ROOT = '/configured-root'
const PROJECT_CWD = join(CONFIGURED_ROOT, PROJECT)

/** 每个测试可改的注入点（在 deps 闭包里按调用时读取）。 */
interface HarnessParams {
  get?: (id: string) => EnsureAgent | undefined | null
  stat?: (id: string) => Promise<{ header?: EnsureSessionHeader } | undefined | null>
  list?: () => EnsureWorkspaceLike[]
  createError?: Error
  resumeError?: Error
  preset?: (id?: string) => EnsureAgentPreset | undefined
  selection?: () => EnsureAgentOptions | undefined
  supportsWorkspaceCreate?: boolean
  workspaceCreateError?: Error
}

interface Calls {
  get: string[]
  create: EnsureCreateOptions[]
  resume: EnsureResumeOptions[]
  stat: string[]
  resolve: Array<string | undefined>
  mount: Array<{ ctx: unknown; id: string }>
  mkdir: string[]
  attach: string[]
  workspaceCreate: string[]
  rename: Array<{ session: unknown; title: string }>
  permission: Array<{ session: unknown; preset: string }>
  selection: number
  list: number
}

function createHarness(
  params: HarnessParams = {},
  options: { workspaceRoot?: string } = {},
): {
  params: HarnessParams
  deps: EnsureSessionDeps
  calls: Calls
  warns: string[]
  infos: string[]
  handle: EnsureAgentHandle
} {
  const calls: Calls = {
    get: [],
    create: [],
    resume: [],
    stat: [],
    resolve: [],
    mount: [],
    mkdir: [],
    attach: [],
    workspaceCreate: [],
    rename: [],
    permission: [],
    selection: 0,
    list: 0,
  }
  const warns: string[] = []
  const infos: string[] = []
  const handle: EnsureAgentHandle = { agent: AGENT, dispose: async () => {} }

  const deps: EnsureSessionDeps = {
    agents: {
      get: (id) => {
        calls.get.push(id)
        return params.get?.(id)
      },
      create: async (options) => {
        calls.create.push(options)
        if (params.createError !== undefined) throw params.createError
        return handle
      },
      resume: async (options) => {
        calls.resume.push(options)
        if (params.resumeError !== undefined) throw params.resumeError
        return handle
      },
    },
    sessionPersistence: {
      stat: async (id) => {
        calls.stat.push(id)
        return params.stat?.(id)
      },
    },
    agentPresets: {
      resolve: (id) => {
        calls.resolve.push(id)
        return params.preset?.(id) ?? { id: id ?? 'default-preset' }
      },
      mount: async (ctx, id) => {
        calls.mount.push({ ctx, id })
      },
    },
    workspaceRegistry: {
      list: async () => {
        calls.list += 1
        return params.list?.() ?? []
      },
      ...(params.supportsWorkspaceCreate === true
        ? {
            create: async (path: string): Promise<EnsureWorkspaceLike> => {
              calls.workspaceCreate.push(path)
              if (params.workspaceCreateError !== undefined) throw params.workspaceCreateError
              return {
                path,
                sessionIds: [],
                attachSession: async (sessionId: string): Promise<void> => {
                  calls.attach.push(sessionId)
                },
              }
            },
          }
        : {}),
    },
    sessionTitle: {
      rename: (session, title) => {
        calls.rename.push({ session, title })
      },
    },
    permissionPresets: {
      set: (session, preset) => {
        calls.permission.push({ session, preset })
      },
    },
    agentDefaultModel: {
      currentSelection: () => {
        calls.selection += 1
        return params.selection?.() ?? { provider: 'deepseek', model: 'deepseek-chat' }
      },
    },
    warn: (message) => {
      warns.push(message)
    },
    info: (message) => {
      infos.push(message)
    },
    mkdir: async (path) => {
      calls.mkdir.push(path)
    },
    cwd: () => FALLBACK_CWD,
    ...(options.workspaceRoot === undefined ? {} : { workspaceRoot: options.workspaceRoot }),
  }

  return { params, deps, calls, warns, infos, handle }
}

// ---------------------------------------------------------------------------
// 三条路径
// ---------------------------------------------------------------------------

describe('ensureSession：三条路径', () => {
  it('1) live 命中 → 只调 agents.get，stat/create/resume 均为 0 次', async () => {
    const h = createHarness({ get: id => (id === PROJECT ? AGENT : undefined) })
    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('reuse')
    expect(result?.agent).toBe(AGENT)
    expect(result?.handle).toBeUndefined()
    expect(h.calls.get).toEqual([PROJECT])
    expect(h.calls.stat).toEqual([])
    expect(h.calls.create).toEqual([])
    expect(h.calls.resume).toEqual([])
  })

  it('2) 已持久化 → resume；不 mkdir、不 create、不改 meta，setup 挂 header preset', async () => {
    const h = createHarness({
      stat: async id => (id === PROJECT
        ? { header: { cwd: '/old-checkout/APP-1', agentPreset: 'legacy-preset' } }
        : undefined),
    })
    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('resumed')
    expect(h.calls.mkdir).toEqual([])
    expect(h.calls.create).toEqual([])
    expect(h.calls.resume).toHaveLength(1)

    const resumed = h.calls.resume[0]!
    expect(resumed.resumeSessionId).toBe(PROJECT)
    expect('meta' in resumed).toBe(false)
    expect(typeof resumed.setup).toBe('function')

    const agentCtx = { fake: true }
    await resumed.setup!(agentCtx)
    expect(h.calls.mount).toEqual([{ ctx: agentCtx, id: 'legacy-preset' }])
    expect(h.calls.resolve).toEqual(['legacy-preset'])
    // 用 header.cwd 记日志（resume 不参数化 cwd，但必须让人看到用的是旧目录）。
    expect(h.infos.some(message => message.includes('/old-checkout/APP-1'))).toBe(true)
  })

  it('3) 不存在 → create：mkdir、meta、agentOptions、attach/rename/permission 各 1 次', async () => {
    const h = createHarness({
      list: () => [{
        path: PROJECT_CWD,
        sessionIds: [],
        attachSession: async (sessionId) => {
          h.calls.attach.push(sessionId)
        },
      }],
    }, { workspaceRoot: CONFIGURED_ROOT })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.mkdir).toEqual([PROJECT_CWD])
    expect(h.calls.create).toHaveLength(1)

    const created = h.calls.create[0]!
    expect(created.sessionId).toBe(PROJECT)
    expect(created.meta).toEqual({ cwd: PROJECT_CWD, agentPreset: 'default-preset' })
    expect(created.agentOptions).toEqual({ provider: 'deepseek', model: 'deepseek-chat' })
    expect(h.calls.resolve).toEqual([undefined])

    const agentCtx = { agentCtx: true }
    await created.setup!(agentCtx)
    expect(h.calls.mount).toEqual([{ ctx: agentCtx, id: 'default-preset' }])

    expect(h.calls.attach).toEqual([PROJECT])
    expect(h.calls.rename).toEqual([{ session: AGENT_SESSION, title: PROJECT }])
    expect(h.calls.permission).toEqual([{ session: AGENT_SESSION, preset: 'workspace-write' }])
  })
})

// ---------------------------------------------------------------------------
// 优先链五个分支
// ---------------------------------------------------------------------------

describe('ensureSession：cwd 优先链', () => {
  it('4) 配了 workspaceRoot → 直接用它，不参与工作区选择', async () => {
    const h = createHarness({
      // 列表里放一个不同路径：若优先链误用了它，cwd 断言会失败。
      list: () => [{ path: '/some/other/ws' }],
    }, { workspaceRoot: CONFIGURED_ROOT })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.create[0]!.meta?.cwd).toBe(PROJECT_CWD)
    expect(h.warns.some(message =>
      message.includes('唯一工作区')
      || message.includes('最近有会话')
      || message.includes('回落到进程目录'))).toBe(false)
  })

  it('5) 未配置且只有一个工作区 → 用它，info 记路径、不 warn', async () => {
    const h = createHarness({ list: () => [{ path: '/ws/only' }] })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.create[0]!.meta?.cwd).toBe('/ws/only/APP-1')
    expect(h.infos.some(message =>
      message.includes('唯一工作区') && message.includes('/ws/only'))).toBe(true)
    // §4.1：单工作区是正常情况，不该产生 warn。
    expect(h.warns).toEqual([])
  })

  it('6) 多工作区 → 取 sessionIds[0].header.createdAt 最新者，空 sessionIds 跳过', async () => {
    const createdAt: Record<string, string> = {
      's-a': '2026-10-01T00:00:00.000Z',
      's-b': '2026-10-20T00:00:00.000Z',
    }
    const h = createHarness({
      stat: async id => (id in createdAt
        ? { header: { createdAt: createdAt[id]! } }
        : undefined),
      list: () => [
        { path: '/ws/a', sessionIds: ['s-a'] },
        { path: '/ws/b', sessionIds: ['s-b'] },
        { path: '/ws/c', sessionIds: [] },
      ],
    })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.create[0]!.meta?.cwd).toBe('/ws/b/APP-1')
    expect(h.warns.some(message =>
      message.includes('最近有会话') && message.includes('/ws/b'))).toBe(true)
    // 空 sessionIds 的工作区不参与 stat。
    expect(h.calls.stat).toEqual([PROJECT, 's-a', 's-b'])
  })

  it('7) 零工作区 → process.cwd() 并 warn 路径', async () => {
    const h = createHarness({ list: () => [] })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.create[0]!.meta?.cwd).toBe(join(FALLBACK_CWD, PROJECT))
    expect(h.warns.some(message =>
      message.includes('回落到进程目录') && message.includes(FALLBACK_CWD))).toBe(true)
  })

  it('8) 多工作区但 stat 全部失败/为空 → process.cwd() 并 warn 路径', async () => {
    const h = createHarness({
      stat: async (id) => {
        if (id === 's-a') throw new Error('stat boom')
        return undefined
      },
      list: () => [
        { path: '/ws/a', sessionIds: ['s-a'] },
        { path: '/ws/b', sessionIds: ['s-b'] },
        { path: '/ws/c', sessionIds: [] },
      ],
    })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.create[0]!.meta?.cwd).toBe(join(FALLBACK_CWD, PROJECT))
    expect(h.warns.some(message =>
      message.includes('没有可用的会话记录') && message.includes(FALLBACK_CWD))).toBe(true)
    expect(h.calls.stat).toEqual([PROJECT, 's-a', 's-b'])
  })
})

// ---------------------------------------------------------------------------
// 降级与幂等
// ---------------------------------------------------------------------------

describe('ensureSession：降级与幂等', () => {
  it('9) 连续两次 ensure → create 仅 1 次，第二次 outcome=reuse', async () => {
    const h = createHarness({ list: () => [] })

    const first = await ensureSession(PROJECT, h.deps)
    // 模拟第一次 create 后会话变为 live。
    h.params.get = id => (id === PROJECT ? AGENT : undefined)
    const second = await ensureSession(PROJECT, h.deps)

    expect(first?.outcome).toBe('created')
    expect(second?.outcome).toBe('reuse')
    expect(h.calls.create).toHaveLength(1)
    expect(h.calls.mkdir).toHaveLength(1)
  })

  it('10) create 抛错 → undefined + warn，不抛错、不做收尾', async () => {
    const h = createHarness({
      createError: new Error('disk full'),
    }, { workspaceRoot: CONFIGURED_ROOT })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result).toBeUndefined()
    expect(h.warns.some(message =>
      message.includes('create 失败') && message.includes(PROJECT))).toBe(true)
    expect(h.calls.attach).toEqual([])
    expect(h.calls.rename).toEqual([])
    expect(h.calls.permission).toEqual([])
  })

  it('11) sessionPersistence 缺失 → undefined + warn，live 仍可用', async () => {
    const h = createHarness({ get: id => (id === PROJECT ? AGENT : undefined) })
    delete h.deps.sessionPersistence

    const live = await ensureSession(PROJECT, h.deps)
    expect(live?.outcome).toBe('reuse')

    h.params.get = () => undefined
    const degraded = await ensureSession(PROJECT, h.deps)
    expect(degraded).toBeUndefined()
    expect(h.warns.some(message =>
      message.includes('sessionPersistence 服务缺失') && message.includes(PROJECT))).toBe(true)
    expect(h.calls.create).toEqual([])
  })

  it('12) agentPresets 缺失 → undefined + warn，不 create', async () => {
    const h = createHarness()
    delete h.deps.agentPresets

    const result = await ensureSession(PROJECT, h.deps)

    expect(result).toBeUndefined()
    expect(h.calls.create).toEqual([])
    expect(h.calls.mkdir).toEqual([])
    expect(h.warns.some(message =>
      message.includes('agentPresets 服务缺失') && message.includes(PROJECT))).toBe(true)
  })

  it('13) 可选服务全缺 → create 仍成功（不传 agentOptions、回落进程目录）', async () => {
    const h = createHarness()
    delete h.deps.workspaceRegistry
    delete h.deps.sessionTitle
    delete h.deps.permissionPresets
    delete h.deps.agentDefaultModel

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.mkdir).toEqual([join(FALLBACK_CWD, PROJECT)])
    expect('agentOptions' in h.calls.create[0]!).toBe(false)
    expect(h.calls.rename).toEqual([])
    expect(h.calls.permission).toEqual([])
    expect(h.warns.some(message =>
      message.includes('workspaceRegistry 缺失') && message.includes(FALLBACK_CWD))).toBe(true)
  })

  it('14) attachSession 抛错 → create 仍成功，只 warn', async () => {
    const h = createHarness({
      list: () => [{
        path: PROJECT_CWD,
        sessionIds: [],
        attachSession: async () => {
          throw new Error('attach boom')
        },
      }],
    }, { workspaceRoot: CONFIGURED_ROOT })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.warns.some(message =>
      message.includes('attachSession 失败') && message.includes(PROJECT))).toBe(true)
    expect(h.calls.rename).toHaveLength(1)
    expect(h.calls.permission).toHaveLength(1)
  })
})

// ---------------------------------------------------------------------------
// 修复回归：attach 常规路径 / reasoningEffort 透传 / legacy resume 不套默认 preset
// ---------------------------------------------------------------------------

describe('ensureSession：修复回归', () => {
  it('15) registry 只有 <root> 且支持 create → create(cwd) 与 attach 各 1 次', async () => {
    const h = createHarness({
      list: () => [{ path: CONFIGURED_ROOT, sessionIds: [] }],
      supportsWorkspaceCreate: true,
    })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.create[0]!.meta?.cwd).toBe(PROJECT_CWD)
    expect(h.calls.workspaceCreate).toEqual([PROJECT_CWD])
    expect(h.calls.attach).toEqual([PROJECT])
  })

  it('16) 创建工作区抛错 → 会话仍 created，只 warn、不 attach', async () => {
    const h = createHarness({
      list: () => [{ path: CONFIGURED_ROOT, sessionIds: [] }],
      supportsWorkspaceCreate: true,
      workspaceCreateError: new Error('registry boom'),
    })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.warns.some(message =>
      message.includes('创建工作区失败') && message.includes(PROJECT))).toBe(true)
    expect(h.calls.attach).toEqual([])
    expect(h.calls.rename).toHaveLength(1)
    expect(h.calls.permission).toHaveLength(1)
  })

  it('17) currentSelection 带 reasoningEffort → agentOptions 原样透传', async () => {
    const h = createHarness({
      list: () => [{ path: CONFIGURED_ROOT }],
      selection: () => ({ provider: 'deepseek', model: 'deepseek-chat', reasoningEffort: 'high' }),
    })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.create[0]!.agentOptions).toEqual({
      provider: 'deepseek',
      model: 'deepseek-chat',
      reasoningEffort: 'high',
    })
  })

  it('18) header 无 agentPreset → resume 不 mount 默认 preset，会话仍恢复', async () => {
    const h = createHarness({
      stat: async id => (id === PROJECT ? { header: { cwd: '/legacy/APP-1' } } : undefined),
    })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('resumed')
    expect(h.calls.resume).toHaveLength(1)
    expect('setup' in h.calls.resume[0]!).toBe(false)
    expect(h.calls.mount).toEqual([])
    expect(h.calls.resolve).toEqual([])
    expect(h.infos.some(message =>
      message.includes('未记录 agentPreset') && message.includes(PROJECT))).toBe(true)
  })

  it('19) header preset 未知 → resume 仍成功，不 mount preset，只 warn 一条（带 preset id）', async () => {
    const h = createHarness({
      stat: async id => (id === PROJECT
        ? { header: { cwd: '/legacy/APP-1', agentPreset: 'ghost-preset' } }
        : undefined),
      preset: () => {
        throw new Error('unknown preset: ghost-preset')
      },
    })

    const result = await ensureSession(PROJECT, h.deps)

    // 关键：不为一个找不到的 preset 放弃投递——会话照常 resume。
    expect(result?.outcome).toBe('resumed')
    expect(h.calls.resume).toHaveLength(1)
    expect('setup' in h.calls.resume[0]!).toBe(false)
    expect(h.calls.mount).toEqual([])
    expect(h.calls.create).toEqual([])

    // 只 warn 一条，且必须能定位是哪个 preset 失效。
    expect(h.warns).toHaveLength(1)
    expect(h.warns[0]).toContain('ghost-preset')
    expect(h.warns[0]).toContain(PROJECT)
    expect(h.warns[0]).toContain('继续恢复')
  })
})
