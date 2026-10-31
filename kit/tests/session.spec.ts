/**
 * ensureSession 单测：三条路径 + cwd 选择六个分支 + 降级与幂等 + 修复回归。
 *
 * 全部依赖注入，测试里不碰真实文件系统（`isDirectory` / `cwd` 都是替身），
 * 断言同时覆盖 warn 文案里的项目号与选中路径。
 *
 * P0 回归：新建会话的 cwd 取**会话数最多的 workspace**（项目号只用于 sessionId），
 * 既不拼 `<root>/<projectId>`、也不建子目录；目录不存在时只 warn 放弃。
 */
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
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
const OTHER_ROOT = '/ws/other'

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
  /** 视为「不存在 / 不是目录」的路径（默认所有路径都存在且是目录）。 */
  missing?: readonly string[]
}

interface Calls {
  get: string[]
  create: EnsureCreateOptions[]
  resume: EnsureResumeOptions[]
  stat: string[]
  resolve: Array<string | undefined>
  mount: Array<{ ctx: unknown; id: string }>
  probe: string[]
  attach: string[]
  workspaceCreate: string[]
  rename: Array<{ session: unknown; title: string }>
  permission: Array<{ session: unknown; preset: string }>
  selection: number
  list: number
}

function createHarness(
  params: HarnessParams = {},
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
    probe: [],
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
    isDirectory: (target) => {
      calls.probe.push(target)
      return !(params.missing ?? []).includes(target)
    },
    cwd: () => FALLBACK_CWD,
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
    expect(h.calls.probe).toEqual([])
    expect(h.calls.create).toEqual([])
    expect(h.calls.resume).toHaveLength(1)

    const resumed = h.calls.resume[0]!
    expect(resumed.resumeSessionId).toBe(PROJECT)
    expect('meta' in resumed).toBe(false)
    expect(resumed.agentOptions).toEqual({ provider: 'deepseek', model: 'deepseek-chat' })
    expect(typeof resumed.setup).toBe('function')

    const agentCtx = { fake: true }
    await resumed.setup!(agentCtx)
    expect(h.calls.mount).toEqual([{ ctx: agentCtx, id: 'legacy-preset' }])
    expect(h.calls.resolve).toEqual(['legacy-preset'])
    // 用 header.cwd 记日志（resume 不参数化 cwd，但必须让人看到用的是旧目录）。
    expect(h.infos.some(message => message.includes('/old-checkout/APP-1'))).toBe(true)
  })

  it('3) 不存在 → create：cwd 就是根目录、无子目录，attach/rename/permission 各 1 次', async () => {
    const h = createHarness({
      list: () => [{
        path: CONFIGURED_ROOT,
        sessionIds: ['s-1'],
        attachSession: async (sessionId) => {
          h.calls.attach.push(sessionId)
        },
      }],
    })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    // 探测的是根目录本身；没有任何地方会去创建 <root>/<projectId>。
    expect(h.calls.probe).toEqual([CONFIGURED_ROOT])
    expect(h.calls.workspaceCreate).toEqual([])
    expect(h.calls.create).toHaveLength(1)

    const created = h.calls.create[0]!
    expect(created.sessionId).toBe(PROJECT)
    expect(created.meta?.cwd).toBe(CONFIGURED_ROOT)
    expect(created.meta?.cwd).not.toBe(`${CONFIGURED_ROOT}/${PROJECT}`)
    expect(created.meta).toEqual({ cwd: CONFIGURED_ROOT, agentPreset: 'default-preset' })
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
// cwd 选择六个分支
// ---------------------------------------------------------------------------

describe('ensureSession：cwd 选择', () => {
  it('4) 两个 workspace（2 vs 1 个会话）→ 选会话数多的', async () => {
    const h = createHarness({
      list: () => [
        { path: '/ws/few', sessionIds: ['s-1'] },
        { path: '/ws/many', sessionIds: ['s-1', 's-2'] },
      ],
    })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.create[0]!.meta?.cwd).toBe('/ws/many')
    expect(h.infos.some(message =>
      message.includes('使用会话数最多的 workspace')
      && message.includes('/ws/many'))).toBe(true)
    expect(h.warns).toEqual([])
  })

  it('5) 只有一个工作区且有会话 → 用它，info 记路径、不 warn', async () => {
    const h = createHarness({ list: () => [{ path: '/ws/only', sessionIds: ['s-1'] }] })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.create[0]!.meta?.cwd).toBe('/ws/only')
    expect(h.infos.some(message =>
      message.includes('会话数最多') && message.includes('/ws/only'))).toBe(true)
    expect(h.warns).toEqual([])
  })

  it('6) 会话数并列 → 取 sessionIds[0].header.createdAt 最新者', async () => {
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
    expect(h.calls.create[0]!.meta?.cwd).toBe('/ws/b')
    expect(h.infos.some(message =>
      message.includes('会话数并列') && message.includes('/ws/b'))).toBe(true)
    // 0 会话的 /ws/c 不参与 stat。
    expect(h.calls.stat).toEqual([PROJECT, 's-a', 's-b'])
  })

  it('7) 零工作区 → process.cwd() 并 warn 路径', async () => {
    const h = createHarness({ list: () => [] })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.create[0]!.meta?.cwd).toBe(FALLBACK_CWD)
    expect(h.warns.some(message =>
      message.includes('没有可用的工作区') && message.includes(FALLBACK_CWD))).toBe(true)
  })

  it('7b) workspaceRegistry 缺失 → process.cwd() 并 warn', async () => {
    const h = createHarness()
    delete h.deps.workspaceRegistry

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.create[0]!.meta?.cwd).toBe(FALLBACK_CWD)
    expect(h.warns.some(message =>
      message.includes('workspaceRegistry 缺失') && message.includes(FALLBACK_CWD))).toBe(true)
  })

  it('8) 所有工作区都是 0 会话 → process.cwd() 并 warn 路径', async () => {
    const h = createHarness({
      list: () => [
        { path: '/ws/a', sessionIds: [] },
        { path: '/ws/b', sessionIds: [] },
      ],
    })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.create[0]!.meta?.cwd).toBe(FALLBACK_CWD)
    expect(h.warns.some(message =>
      message.includes('所有工作区都没有会话记录')
      && message.includes(FALLBACK_CWD))).toBe(true)
    // 0 会话时不需要任何 stat。
    expect(h.calls.stat).toEqual([PROJECT])
  })

  it('8b) 会话数并列且 stat 全失败 → 取列表第一个', async () => {
    const h = createHarness({
      stat: async (id) => {
        if (id === 's-a') throw new Error('stat boom')
        return undefined
      },
      list: () => [
        { path: '/ws/a', sessionIds: ['s-a'] },
        { path: '/ws/b', sessionIds: ['s-b'] },
      ],
    })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.create[0]!.meta?.cwd).toBe('/ws/a')
    expect(h.infos.some(message =>
      message.includes('取列表第一个 workspace') && message.includes('/ws/a'))).toBe(true)
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
    expect(h.calls.probe).toEqual([FALLBACK_CWD])
  })

  it('10) create 抛错 → undefined + warn，不抛错、不做收尾', async () => {
    const h = createHarness({
      createError: new Error('disk full'),
    })

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
    expect(h.calls.probe).toEqual([])
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
    expect(h.calls.probe).toEqual([FALLBACK_CWD])
    expect(h.calls.create[0]!.meta?.cwd).toBe(FALLBACK_CWD)
    expect('agentOptions' in h.calls.create[0]!).toBe(false)
    expect(h.calls.rename).toEqual([])
    expect(h.calls.permission).toEqual([])
    expect(h.warns.some(message =>
      message.includes('workspaceRegistry 缺失') && message.includes(FALLBACK_CWD))).toBe(true)
  })

  it('14) attachSession 抛错 → create 仍成功，只 warn', async () => {
    const h = createHarness({
      list: () => [{
        path: CONFIGURED_ROOT,
        sessionIds: ['s-1'],
        attachSession: async () => {
          throw new Error('attach boom')
        },
      }],
    })

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
  it('15) registry 已有同路径根目录 → 直接 attach，不 create 工作区', async () => {
    const h = createHarness({
      list: () => [{
        path: `${CONFIGURED_ROOT}/`,
        sessionIds: ['s-1'],
        attachSession: async (sessionId) => {
          h.calls.attach.push(sessionId)
        },
      }],
      supportsWorkspaceCreate: true,
    })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.create[0]!.meta?.cwd).toBe(`${CONFIGURED_ROOT}/`)
    expect(h.calls.workspaceCreate).toEqual([])
    expect(h.calls.attach).toEqual([PROJECT])
  })

  it('15b) registry 里没有同路径目录且支持 create → 兜底 create(根目录) 并 attach', async () => {
    const h = createHarness({
      // 第一次 list 用于选择 cwd；第二次（attach）时该目录已不在列表里。
      list: () => (h.calls.list === 1
        ? [{ path: CONFIGURED_ROOT, sessionIds: ['s-1'] }]
        : [{ path: OTHER_ROOT, sessionIds: [] }]),
      supportsWorkspaceCreate: true,
    })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result?.outcome).toBe('created')
    expect(h.calls.create[0]!.meta?.cwd).toBe(CONFIGURED_ROOT)
    expect(h.calls.workspaceCreate).toEqual([CONFIGURED_ROOT])
    expect(h.calls.attach).toEqual([PROJECT])
  })

  it('16) 创建工作区抛错 → 会话仍 created，只 warn、不 attach', async () => {
    const h = createHarness({
      list: () => (h.calls.list === 1
        ? [{ path: CONFIGURED_ROOT, sessionIds: ['s-1'] }]
        : [{ path: OTHER_ROOT, sessionIds: [] }]),
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
      list: () => [{ path: CONFIGURED_ROOT, sessionIds: ['s-1'] }],
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

  it('20) resume 传 agentOptions（与 create 同源，含 reasoningEffort）', async () => {
    const h = createHarness({
      stat: async id => (id === PROJECT ? { header: { cwd: '/legacy/APP-1' } } : undefined),
      selection: () => ({
        provider: 'deepseek',
        model: 'deepseek-reasoner',
        reasoningEffort: 'high',
      }),
    })

    const result = await ensureSession(PROJECT, h.deps)

    // 关键：恢复出的 agent 必须拿到模型，否则回合只有 turn/start → turn/end。
    expect(result?.outcome).toBe('resumed')
    expect(h.calls.resume).toHaveLength(1)
    expect(h.calls.resume[0]!.agentOptions).toEqual({
      provider: 'deepseek',
      model: 'deepseek-reasoner',
      reasoningEffort: 'high',
    })
    expect(h.calls.probe).toEqual([])
    expect(h.calls.create).toEqual([])
  })

  it('22) 两个不同 projectId 在同一 root 下：cwd 相同、sessionId 不同', async () => {
    const h = createHarness({
      list: () => [{ path: CONFIGURED_ROOT, sessionIds: ['s-1'] }],
    })

    const first = await ensureSession('APP-1', h.deps)
    const second = await ensureSession('APP-2', h.deps)

    expect(first?.outcome).toBe('created')
    expect(second?.outcome).toBe('created')
    expect(h.calls.create).toHaveLength(2)
    expect(h.calls.create.map(options => options.sessionId)).toEqual(['APP-1', 'APP-2'])
    // 项目号只决定 sessionId；工作目录永远是同一个 root。
    expect(h.calls.create.map(options => options.meta?.cwd)).toEqual([
      CONFIGURED_ROOT,
      CONFIGURED_ROOT,
    ])
    expect(h.calls.probe).toEqual([CONFIGURED_ROOT, CONFIGURED_ROOT])
  })

  it('23) 根目录不存在 → 放弃 create，只 warn（不自动建目录）', async () => {
    const h = createHarness({
      list: () => [{ path: CONFIGURED_ROOT, sessionIds: ['s-1'] }],
      missing: [CONFIGURED_ROOT],
    })

    const result = await ensureSession(PROJECT, h.deps)

    expect(result).toBeUndefined()
    expect(h.calls.create).toEqual([])
    expect(h.calls.workspaceCreate).toEqual([])
    expect(h.calls.attach).toEqual([])
    expect(h.warns.some(message =>
      message.includes(CONFIGURED_ROOT)
      && message.includes(PROJECT)
      && message.includes('不存在'))).toBe(true)
    // 明确告诉用户「插件不会创建目录」，避免以为会自动 mkdir -p。
    expect(h.warns.join('\n')).toContain('不会创建目录')
  })

  it('24) 源码不再拼接 cwd、不再 mkdir 子目录', () => {
    const source = readFileSync(
      path.join(path.dirname(fileURLToPath(import.meta.url)), '../src/session.ts'),
      'utf8',
    )
    expect(source).not.toMatch(/\bmkdir\s*\(/)
    expect(source).not.toContain("from 'node:fs/promises'")
    expect(source).not.toContain("from 'node:path'")
    expect(source).not.toMatch(/join\(\s*root/)
    // 反向自证：create 分支确实把 root 直接当 cwd 用。
    expect(source).toContain('const cwd = await resolveWorkspaceRoot(')
  })

  it('21) agentDefaultModel 缺失 → resume 仍调用且不带 agentOptions', async () => {
    const h = createHarness({
      stat: async id => (id === PROJECT ? { header: { cwd: '/legacy/APP-1' } } : undefined),
    })
    const deps: EnsureSessionDeps = { ...h.deps }
    delete deps.agentDefaultModel

    const result = await ensureSession(PROJECT, deps)

    expect(result?.outcome).toBe('resumed')
    expect(h.calls.resume).toHaveLength(1)
    expect('agentOptions' in h.calls.resume[0]!).toBe(false)
  })
})
