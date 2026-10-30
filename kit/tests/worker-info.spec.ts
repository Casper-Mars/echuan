/**
 * `echuan_worker_info` 单测（任务-04 / K3）。
 *
 * 这个工具的全部价值在「排障时不产生副作用」，因此用例重点不是文案细节，而是：
 * 1. 输出确实覆盖 poll / worker_id / inflight / 会话三态 / preset / 工作区；
 * 2. 任一宿主服务缺失只降级展示，工具整体不抛错；
 * 3. **零 HTTP**：整条执行链路不碰 fetch（哨兵 0 次），也不注册任何工具/定时器。
 */
import { describe, expect, it, vi } from 'vitest'
import { validateJsonSchemaValue, type ToolDefinition } from '@deepseek-ai/dsh-tools'
import { createClient, type FetchLike } from '../src/client.js'
import { resolveConfig } from '../src/config.js'
import {
  TOOL_NAMES,
  createAllEchuanTools,
  type EchuanToolOptions,
  type WorkerInfoDeps,
} from '../src/tools/index.js'
import { createWorkerInfoTool } from '../src/tools/worker-info.js'
import { maskWorkerId } from '../src/worker.js'

const WORKER_ID = 'w_TEST01ABCDEFGHIJKLMNOP'
const exec = { signal: new AbortController().signal } as unknown as Parameters<
  ToolDefinition['execute']
>[1]

/** 记录每次 fetch 的假客户端：诊断工具不该产生任何一条。 */
function createFetchSpy(): { calls: number; fetchImpl: FetchLike } {
  const state = { calls: 0 }
  const fetchImpl: FetchLike = async () => {
    state.calls += 1
    throw new Error('echuan_worker_info 不应发出 HTTP 请求')
  }
  return {
    get calls() {
      return state.calls
    },
    fetchImpl,
  } as { calls: number; fetchImpl: FetchLike }
}

/** 只读的依赖替身；默认给一份「全都可用」的诊断环境。 */
function createDeps(overrides: Partial<WorkerInfoDeps> = {}): WorkerInfoDeps {
  const now = Date.now()
  const deps: WorkerInfoDeps = {
    pollEnabled: true,
    workspaceRoot: '/ws',
    workerId: () => WORKER_ID,
    inflight: () => [
      { taskId: 'APP-1-T0001', projectId: 'APP-1', leaseUntil: new Date(now + 65_000).toISOString() },
      { taskId: 'APP-2-T0003', projectId: 'APP-2', leaseUntil: new Date(now - 5_000).toISOString() },
    ],
    agents: { get: id => (id === 'APP-1' ? { status: 'running' } : undefined) },
    sessionPersistence: {
      stat: async id => (id === 'APP-2'
        ? { header: { cwd: '/ws/APP-2', agentPreset: 'coder' } }
        : undefined),
    },
    agentPresets: { resolve: async () => ({ id: 'default' }) },
    workspaceRegistry: { list: () => [{ path: '/ws' }, { path: '/ws/APP-2' }] },
    ...overrides,
  }
  return deps
}

async function run(
  options: EchuanToolOptions,
  args: Record<string, unknown> = {},
): Promise<{ text: string; value: unknown; tool: ToolDefinition }> {
  const tool = createWorkerInfoTool(options)
  const value = await tool.execute(args, exec)
  const blocks = tool.output.render(args, value as never) as Array<{
    type: string
    text?: string
  }>
  return {
    tool,
    value,
    text: blocks.map(block => (block.type === 'text' ? block.text ?? '' : '')).join('\n'),
  }
}

/** 构造工具选项：真 client（但 fetch 是哨兵）+ 给定诊断依赖。 */
function optionsWith(
  deps: WorkerInfoDeps | undefined,
  fetchImpl: FetchLike = async () => new Response('{}'),
): EchuanToolOptions {
  const base = resolveConfig({ baseUrl: 'http://platform.test' })
  const options: EchuanToolOptions = {
    client: createClient(base, { fetchImpl }),
    identity: { workerId: undefined },
    ...(deps === undefined ? {} : { workerInfo: deps }),
  }
  return options
}

// ---------------------------------------------------------------------------
// 1. 输出内容
// ---------------------------------------------------------------------------

describe('echuan_worker_info：输出内容', () => {
  it('覆盖 poll / worker_id（打码）/ inflight / 会话三态 / preset / 工作区', async () => {
    const { text, value, tool } = await run(optionsWith(createDeps()), { project_id: 'APP-3' })

    expect(text).toContain('只读，零 HTTP')
    expect(text).toContain('poll.enable：true')
    expect(text).toContain(`worker_id：${maskWorkerId(WORKER_ID)}`)
    expect(text).not.toContain(WORKER_ID)

    // inflight：任务 / 项目 / 租约 / 剩余秒数；过期条目单独标注。
    expect(text).toContain('inflight：2 条')
    expect(text).toContain('任务 APP-1-T0001')
    expect(text).toContain('项目 APP-1')
    expect(text).toContain('剩余 ')
    expect(text).toContain('任务 APP-2-T0003')
    expect(text).toContain('已过期')

    // 会话三态：live / persisted / missing。
    expect(text).toContain('APP-1：live（running）')
    expect(text).toContain('APP-2：persisted（cwd=/ws/APP-2, preset=coder）')
    expect(text).toContain('APP-3：missing')

    // preset 与工作区。
    expect(text).toContain('默认 preset：default')
    expect(text).toContain('工作区根目录：/ws（worker.workspaceRoot）')
    expect(text).toContain('- /ws')
    expect(text).toContain('- /ws/APP-2')

    // 规范值就是那段文本，且能过注册表的 output.schema。
    expect(validateJsonSchemaValue(tool.output.schema, value)).toEqual([])
    expect((value as { text: string }).text).toBe(text)
  })

  it('worker 未注册、未配置 workspaceRoot 时给出可读状态', async () => {
    const { text } = await run(optionsWith(createDeps({
      pollEnabled: false,
      workspaceRoot: undefined,
      workerId: () => undefined,
      inflight: () => [],
    })))

    expect(text).toContain('poll.enable：false')
    expect(text).toContain('worker_id：未注册')
    expect(text).toContain('inflight：（无）')
    expect(text).toContain('无 inflight 项目')
    expect(text).toContain('工作区根目录：未配置（优先链')
  })

  it('传 project_id 时只去重展示，不重复 inflight 里已有的项目', async () => {
    const { text } = await run(optionsWith(createDeps()), { project_id: 'APP-1' })
    const occurrences = text.split('\n').filter(line => line.startsWith('  - APP-1：'))
    expect(occurrences).toHaveLength(1)
  })
})

// ---------------------------------------------------------------------------
// 2. 服务缺失只降级
// ---------------------------------------------------------------------------

describe('echuan_worker_info：服务缺失降级', () => {
  it('sessionPersistence / agentPresets / workspaceRegistry / agents 全缺失：不抛错', async () => {
    const deps: WorkerInfoDeps = {
      pollEnabled: true,
      workerId: () => WORKER_ID,
      inflight: () => [
        { taskId: 'APP-1-T0001', projectId: 'APP-1', leaseUntil: 'not-a-date' },
      ],
      agents: undefined,
    }

    const { text } = await run(optionsWith(deps), { project_id: 'APP-9' })

    expect(text).toContain('APP-1：无法判定（agents 服务缺失）')
    expect(text).toContain('APP-9：无法判定（agents 服务缺失）')
    expect(text).toContain('默认 preset：不可用（agentPresets 服务缺失）')
    expect(text).toContain('（workspaceRegistry 服务缺失，无法列出）')
    expect(text).toContain('剩余时间未知')
  })

  it('agents 可用但 persistence 缺失：live 正常，其余标注无法判定', async () => {
    const { text } = await run(optionsWith(createDeps({
      sessionPersistence: undefined,
    })), {})

    expect(text).toContain('APP-1：live（running）')
    expect(text).toContain('APP-2：无法判定（sessionPersistence 服务缺失）')
  })

  it('stat / resolve / list 抛错：逐项降级，不整体失败', async () => {
    const { text } = await run(optionsWith(createDeps({
      sessionPersistence: {
        stat: async () => {
          throw new Error('stat boom')
        },
      },
      agentPresets: {
        resolve: async () => {
          throw new Error('preset boom')
        },
      },
      workspaceRegistry: {
        list: () => {
          throw new Error('list boom')
        },
      },
    })), { project_id: 'APP-7' })

    expect(text).toContain('APP-7：无法判定（sessionPersistence.stat 失败）')
    expect(text).toContain('默认 preset：不可用（解析失败：preset boom）')
    expect(text).toContain('读取工作区列表失败')
  })

  it('未注入 workerInfo 依赖：返回可读提示而不是抛错', async () => {
    const { text } = await run(optionsWith(undefined))
    expect(text).toContain('worker 诊断不可用')
    expect(text).toContain('没有发出任何请求')
  })
})

// ---------------------------------------------------------------------------
// 3. 零 HTTP / 零副作用
// ---------------------------------------------------------------------------

describe('echuan_worker_info：零 HTTP 与零副作用', () => {
  it('执行全程 fetch 0 次', async () => {
    const spy = createFetchSpy()
    const { text } = await run(optionsWith(createDeps(), spy.fetchImpl), { project_id: 'APP-1' })

    expect(text).toContain('APP-1：live')
    expect(spy.calls).toBe(0)
  })

  it('未注入依赖时同样零请求', async () => {
    const spy = createFetchSpy()
    await run(optionsWith(undefined, spy.fetchImpl))
    expect(spy.calls).toBe(0)
  })

  it('执行不改动入参 inflight、不注册工具、不建定时器', async () => {
    const snapshot = [
      { taskId: 'APP-1-T0001', projectId: 'APP-1', leaseUntil: '2099-01-01T00:00:00.000Z' },
    ]
    const frozen = Object.freeze(snapshot)
    const setTimeoutSpy = vi.spyOn(globalThis, 'setTimeout')

    const namesBefore = [...TOOL_NAMES]
    const fetchSpy = createFetchSpy()
    const options = optionsWith(createDeps({ inflight: () => frozen }), fetchSpy.fetchImpl)

    // 只「构造」工具集 + 执行诊断工具：不应产生注册表 / 网络 / 定时器副作用。
    const all = createAllEchuanTools(options)
    expect(all.map(tool => tool.name)).toEqual(namesBefore)

    await run(options, {})
    expect(snapshot).toEqual([
      { taskId: 'APP-1-T0001', projectId: 'APP-1', leaseUntil: '2099-01-01T00:00:00.000Z' },
    ])
    expect(fetchSpy.calls).toBe(0)
    expect(setTimeoutSpy).not.toHaveBeenCalled()

    setTimeoutSpy.mockRestore()
  })
})
