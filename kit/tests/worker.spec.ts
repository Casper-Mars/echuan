/**
 * Worker 心跳循环单测（任务-03）。
 *
 * 全部使用假平台（注入 fetch）+ 假时钟（注入 setTimeout 链）：
 * 不连真实平台、不做真实 sleep。
 *
 * 覆盖验收标准的 1–10 条：首轮心跳延迟 5s（而非启动即发）、首次注册字段、
 * 间隔重排、派单投递文案（多会话 / ensure 路由）、state 与 inflight、409 worker_unknown、
 * 网络失败、poll=false、dispose（含「延迟内 dispose 连首轮也不发」）、
 * 以及「源码不写盘」。
 */
import { readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import type { Agent } from '@deepseek-ai/dsh-agent'
import { describe, expect, it, vi } from 'vitest'
import {
  TOOL_METHODS,
  createWorkerClient,
  type FetchLike,
  type WorkerHeartbeatPayload,
} from '../src/client.js'
import { WORKER_METADATA_MAX_BYTES, metadataByteLength, resolveConfig } from '../src/config.js'
import type { Logger } from '../src/runtime.js'
import {
  DEFAULT_HEARTBEAT_INTERVAL_MS,
  FIRST_HEARTBEAT_DELAY_MS,
  MAX_HEARTBEAT_INTERVAL_MS,
  MAX_REMEMBERED_DELIVERIES,
  MIN_HEARTBEAT_INTERVAL_MS,
  createWorkerLoop,
  maskWorkerId,
  renderTaskMessage,
  type WorkerLoop,
  type WorkerLoopOptions,
} from '../src/worker.js'

const here = path.dirname(fileURLToPath(import.meta.url))

// ---------------------------------------------------------------------------
// 假平台与假时钟
// ---------------------------------------------------------------------------

type Handler = (body: Record<string, unknown>, call: number) => Response | Promise<Response>

interface RecordedCall {
  url: string
  body: Record<string, unknown>
  init: RequestInit
}

function createPlatform(handler: Handler): { calls: RecordedCall[]; fetchImpl: FetchLike } {
  const calls: RecordedCall[] = []
  const fetchImpl: FetchLike = async (url, init = {}) => {
    const body: Record<string, unknown> = init.body === undefined
      ? {}
      : (JSON.parse(String(init.body)) as Record<string, unknown>)
    calls.push({ url, body, init })
    return handler(body, calls.length - 1)
  }
  return { calls, fetchImpl }
}

function okResponse(payload: unknown): Response {
  return new Response(JSON.stringify(payload), {
    status: 200,
    headers: { 'content-type': 'application/json' },
  })
}

function errorResponse(status: number, code: string): Response {
  return new Response(
    JSON.stringify({ code, message: code, details: [], request_id: `req-${code}` }),
    { status, headers: { 'content-type': 'application/json' } },
  )
}

function heartbeatPayload(
  workerId: string,
  commands: unknown[] = [],
  heartbeatIntervalMs?: number,
): WorkerHeartbeatPayload {
  return {
    worker: {
      worker_id: workerId,
      ...(heartbeatIntervalMs === undefined ? {} : { heartbeat_interval_ms: heartbeatIntervalMs }),
    },
    commands: commands as WorkerHeartbeatPayload['commands'],
  }
}

interface ScheduledTask {
  at: number
  fn: () => void
}

/** 假时钟：只接管心跳循环的 setTimeout 链，可手动推进。 */
function createScheduler(): {
  api: NonNullable<WorkerLoopOptions['timer']>
  pending: ScheduledTask[]
  count: number
  now: number
  advance(ms: number): Promise<void>
} {
  let now = 0
  let seq = 0
  const tasks = new Map<number, ScheduledTask>()

  return {
    api: {
      setTimeout(handler, ms) {
        const id = ++seq
        tasks.set(id, { at: now + ms, fn: handler })
        return id
      },
      clearTimeout(handle) {
        tasks.delete(Number(handle))
      },
    },
    get pending() {
      return [...tasks.values()].sort((left, right) => left.at - right.at)
    },
    get count() {
      return tasks.size
    },
    get now() {
      return now
    },
    async advance(ms) {
      now += ms
      // 只触发推进前已到期的任务；回调里新排的任务留到下一次 advance，
      // 这样 409 的「立即重排」也能被逐步断言。
      const due = [...tasks.entries()]
        .filter(([, task]) => task.at <= now)
        .sort((left, right) => left[1].at - right[1].at)
      for (const [id, task] of due) {
        if (!tasks.has(id)) continue
        tasks.delete(id)
        task.fn()
      }
      await drain()
    },
  }
}

/** 排空微任务队列，等一轮异步心跳结束。 */
async function drain(times = 3): Promise<void> {
  for (let index = 0; index < times; index += 1) {
    await new Promise<void>(resolve => {
      setImmediate(resolve)
    })
  }
}

/**
 * 启动循环并推进过首轮心跳延迟。
 *
 * `start()` 不再立即发请求：首轮由 `FIRST_HEARTBEAT_DELAY_MS` 的定时器驱动，
 * 因此断言「发出了首轮」前必须先推进假时钟。
 */
async function startAndBeat(
  loop: WorkerLoop,
  scheduler: ReturnType<typeof createScheduler>,
): Promise<void> {
  loop.start()
  await drain()
  await scheduler.advance(FIRST_HEARTBEAT_DELAY_MS)
}

function buildAgent(status: 'idle' | 'running' = 'idle', id = 'APP-1'): {
  agent: Agent
  followup: ReturnType<typeof vi.fn>
  setStatus(next: 'idle' | 'running'): void
} {
  const followup = vi.fn()
  const agent = { id, status, followup } as unknown as Agent
  return {
    agent,
    followup,
    setStatus(next) {
      (agent as unknown as { status: string }).status = next
    },
  }
}

interface LoopHarness {
  config: ReturnType<typeof resolveConfig>
  platform: ReturnType<typeof createPlatform>
  scheduler: ReturnType<typeof createScheduler>
  lines: string[]
  loop: WorkerLoop
}

function setup(
  handler: Handler,
  options: {
    enable?: boolean
    metadata?: Record<string, unknown>
    /** 活跃会话（按 `agent.id` 建索引，`id` 就是项目号）。 */
    sessions?: readonly Agent[]
    /** 覆盖默认 ensure：默认按 project_id 直接从 `sessions` 里取。 */
    ensureSession?: WorkerLoopOptions['ensureSession']
    now?: () => number
    deliver?: WorkerLoopOptions['deliver']
    ensureTimeoutMs?: number
  } = {},
): LoopHarness {
  const metadata = options.metadata ?? { host: 'build-01', os: 'linux' }
  const config = resolveConfig({
    worker: { poll: { enable: options.enable ?? true }, metadata },
  })
  const platform = createPlatform(handler)
  const client = createWorkerClient(config, { fetchImpl: platform.fetchImpl })
  const scheduler = createScheduler()
  const lines: string[] = []
  const logger: Logger = { info: message => lines.push(message), warn: message => lines.push(message) }
  const agentsById = new Map<string, Agent>()
  for (const agent of options.sessions ?? []) agentsById.set(agent.id, agent)
  const ensureSession: WorkerLoopOptions['ensureSession'] = options.ensureSession
    ?? (async (projectId: string) => {
      const agent = agentsById.get(projectId)
      return agent === undefined ? undefined : { agent }
    })
  const loop = createWorkerLoop({
    config,
    client,
    sessions: { get: (id: string) => agentsById.get(id) },
    ensureSession,
    logger,
    timer: scheduler.api,
    ...(options.now === undefined ? {} : { now: options.now }),
    ...(options.deliver === undefined ? {} : { deliver: options.deliver }),
    ...(options.ensureTimeoutMs === undefined ? {} : { ensureTimeoutMs: options.ensureTimeoutMs }),
  })
  return { config, platform, scheduler, lines, loop }
}

// ---------------------------------------------------------------------------
// 测试数据
// ---------------------------------------------------------------------------

const WORKER_ID = 'w_TEST01ABCDEFGHIJKLMN'
const RUN_TASK = {
  kind: 'run_task',
  task: {
    id: 'APP-1-T0001',
    project_id: 'APP-1',
    title: '实现登录接口',
    body_md: '# 任务正文\n请实现登录接口。',
    attempt: 1,
    priority: 10,
  },
  lease: {
    lease_id: 'l_9f8b7b09ff971dd1',
    until: '2026-10-27T16:49:25.157Z',
    ttl_seconds: 90,
  },
  context: {
    spec_version: 1,
    spec_digest: 'sha256:bb23da360a3b21e827138bad027ef3f30a29699e84014bc520e4f40131ea2d76',
    product: { id: 'APP', name: '驿传示例产品', git_url: 'git@example.com:APP.git' },
  },
}

// ---------------------------------------------------------------------------
// 1. 注册与心跳字段
// ---------------------------------------------------------------------------

describe('首次注册与后续心跳', () => {
  it('常量 FIRST_HEARTBEAT_DELAY_MS 钉死为 5000ms', () => {
    // 5s 是与用户约定的产品契约（几秒内就退出的进程不该打请求），
    // 改这个值必须同步改 README 与验收标准。
    expect(FIRST_HEARTBEAT_DELAY_MS).toBe(5_000)
  })

  it('字面时序契约：4999ms 零请求、第 5000ms 恰好发出 1 次', async () => {
    const { loop, platform, scheduler } = setup(() => okResponse(heartbeatPayload(WORKER_ID, [], 5_000)))

    loop.start()
    await drain()

    // 刻意只写字面值、不引用 FIRST_HEARTBEAT_DELAY_MS：
    // 若常量被改成 0 之类，「5 秒」这个契约仍能被这条用例独立钉住。
    await scheduler.advance(4_999)
    expect(platform.calls).toHaveLength(0)

    await scheduler.advance(1) // 累计 5_000ms
    expect(platform.calls).toHaveLength(1)
  })

  it('首轮延迟 5000ms：4999ms 无请求、5000ms 才发出；首次请求体完全没有 worker_id；回包 id 只存内存；第二次带同一 id', async () => {
    const { loop, platform, scheduler } = setup(() => okResponse(heartbeatPayload(WORKER_ID, [], 5_000)))

    loop.start()
    await drain()

    // start() 不再立即发请求：延迟满 4999ms 仍然零请求。
    expect(scheduler.pending.map(task => task.at)).toEqual([FIRST_HEARTBEAT_DELAY_MS])
    await scheduler.advance(FIRST_HEARTBEAT_DELAY_MS - 1)
    expect(platform.calls).toHaveLength(0)

    // 第 5000ms 首轮心跳发出并完成注册。
    await scheduler.advance(1)
    expect(platform.calls).toHaveLength(1)
    expect(platform.calls[0]?.url).toBe('http://127.0.0.1:8720/v1/workers/heartbeat')
    const firstBody = platform.calls[0]?.body ?? {}
    expect('worker_id' in firstBody).toBe(false)
    expect(firstBody.state).toBe('idle')
    expect(firstBody.metadata).toEqual({ host: 'build-01', os: 'linux' })
    expect(loop.workerId).toBe(WORKER_ID)

    await scheduler.advance(5_000)

    expect(platform.calls).toHaveLength(2)
    expect(platform.calls[1]?.body.worker_id).toBe(WORKER_ID)
  })

  it('metadata 原样上报（深等于配置）', async () => {
    const metadata = { host: 'build-01', nested: { zone: 'cn', tags: ['a', 'b'] } }
    const { loop, platform, scheduler } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [], 15_000)),
      { metadata },
    )

    await startAndBeat(loop, scheduler)

    expect(platform.calls[0]?.body.metadata).toEqual(metadata)
  })


  it('日志里的 worker id 打码，不出现明文', async () => {
    const { loop, scheduler, lines } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [], 5_000)),
    )

    await startAndBeat(loop, scheduler)

    const joined = lines.join('\n')
    expect(joined).not.toContain(WORKER_ID)
    expect(joined).toContain(maskWorkerId(WORKER_ID))
  })
})

// ---------------------------------------------------------------------------
// 2. 间隔：只认回包，缺失回落 15s；setTimeout 链重排
// ---------------------------------------------------------------------------

describe('心跳间隔', () => {
  it('回包 heartbeat_interval_ms=5000 → 下一轮在 5000ms 后', async () => {
    const { loop, scheduler } = setup(() => okResponse(heartbeatPayload(WORKER_ID, [], 5_000)))

    await startAndBeat(loop, scheduler)

    expect(scheduler.pending.map(task => task.at)).toEqual([
      FIRST_HEARTBEAT_DELAY_MS + 5_000,
    ])
    expect(loop.intervalMs).toBe(5_000)
  })

  it('回包缺失 heartbeat_interval_ms → 用 15000ms', async () => {
    const { loop, scheduler } = setup(() => okResponse(heartbeatPayload(WORKER_ID)))

    await startAndBeat(loop, scheduler)

    expect(scheduler.pending.map(task => task.at)).toEqual([
      FIRST_HEARTBEAT_DELAY_MS + DEFAULT_HEARTBEAT_INTERVAL_MS,
    ])
    expect(loop.intervalMs).toBe(DEFAULT_HEARTBEAT_INTERVAL_MS)
  })

  it('回包间隔非法（0 / 负数 / 非数字）同样回落 15000ms', async () => {
    const values: unknown[] = [0, -1, 'soon', Number.NaN]
    for (const value of values) {
      const payload = heartbeatPayload(WORKER_ID) as unknown as {
        worker: Record<string, unknown>
        commands: unknown[]
      }
      payload.worker.heartbeat_interval_ms = value
      const { loop, scheduler } = setup(() => okResponse(payload))

      await startAndBeat(loop, scheduler)

      expect(scheduler.pending.map(task => task.at)).toEqual([
        FIRST_HEARTBEAT_DELAY_MS + DEFAULT_HEARTBEAT_INTERVAL_MS,
      ])
    }
  })
})

// ---------------------------------------------------------------------------
// 3. run_task 投递
// ---------------------------------------------------------------------------

describe('run_task 投递', () => {
  it('派单投递文案只含任务事实（编号/标题/租约/项目/产品/仓库），不含操作指示', async () => {
    const { agent, followup } = buildAgent('idle')
    const { loop, scheduler } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [RUN_TASK], 5_000)),
      { sessions: [agent] },
    )

    await startAndBeat(loop, scheduler)

    expect(followup).toHaveBeenCalledTimes(1)
    const message = followup.mock.calls[0]?.[0] as {
      role: string
      content: Array<{ type: string; text: string }>
      source: { kind: string; plugin: string }
    }
    expect(message.role).toBe('user')
    expect(message.source).toEqual({ kind: 'plugin', plugin: 'echuan' })
    const text = message.content.map(block => block.text).join('\n')

    expect(text).toContain('APP-1-T0001')
    expect(text).toContain('实现登录接口')
    expect(text).toContain('2026-10-27T16:49:25.157Z')
    expect(text).toContain('APP-1')
    expect(text).toContain('驿传示例产品')
    expect(text).toContain('仓库：git@example.com:APP.git')
    expect(text).toContain('# 任务正文')
    expect(text).not.toContain('echuan_task_report')
    expect(text).not.toContain('汇报')
  })

  it('renderTaskMessage 输出「仓库：」一行，且等于派单里的 git_url', () => {
    const text = renderTaskMessage(RUN_TASK as never)
    const line = text.split('\n').find(item => item.startsWith('仓库：'))
    expect(line).toBe(`仓库：${RUN_TASK.context.product.git_url}`)
    expect(line).toBe('仓库：git@example.com:APP.git')
  })

  it('renderTaskMessage 首行严格是【驿传派单】，且不含操作指示与工作目录字段', () => {
    const text = renderTaskMessage(RUN_TASK as never)
    expect(text.split('\n')[0]).toBe('【驿传派单】')
    expect(text).not.toContain('echuan_task_report')
    expect(text).not.toContain('汇报')
    expect(text).not.toContain('请立即执行')
    expect(text).not.toContain('工作目录：')
  })

  it('多会话（6 个 root）：sessions.get 只命中 APP-1，run_task(APP-1) 仍 ensure + 投递 1 次', async () => {
    const agents = ['APP-1', 'APP-2', 'APP-3', 'APP-4', 'APP-5', 'APP-6']
      .map(id => buildAgent('idle', id))
    const ensure = vi.fn(async (projectId: string) => {
      const hit = agents.find(item => item.agent.id === projectId)
      return hit === undefined ? undefined : { agent: hit.agent }
    })
    const { loop, scheduler, platform } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [RUN_TASK], 5_000)),
      { sessions: agents.map(item => item.agent), ensureSession: ensure },
    )

    await startAndBeat(loop, scheduler)

    expect(ensure).toHaveBeenCalledTimes(1)
    expect(ensure).toHaveBeenCalledWith('APP-1')
    const targeted = agents.find(item => item.agent.id === 'APP-1')
    expect(targeted?.followup).toHaveBeenCalledTimes(1)
    for (const other of agents.filter(item => item.agent.id !== 'APP-1')) {
      expect(other.followup).not.toHaveBeenCalled()
    }
    expect(platform.calls[0]?.body.worker_id).toBeUndefined()
    expect(loop.inflight).toEqual([
      { taskId: 'APP-1-T0001', projectId: 'APP-1', leaseUntil: RUN_TASK.lease.until },
    ])
  })

  it('ensure 返回 undefined：warn + 不投递 + 无 inflight，心跳继续', async () => {
    const deliver = vi.fn()
    const { loop, platform, scheduler, lines } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [RUN_TASK], 5_000)),
      { sessions: [], deliver, ensureSession: async () => undefined },
    )

    await startAndBeat(loop, scheduler)

    expect(deliver).not.toHaveBeenCalled()
    expect(loop.inflight).toEqual([])
    const joined = lines.join('\n')
    expect(joined).toContain('APP-1-T0001')
    expect(joined).toContain('APP-1')
    expect(joined).toContain('未能就绪')
    expect(loop.workerId).toBe(WORKER_ID)

    // 心跳不中断：下一轮照常带同一个 id。
    await scheduler.advance(5_000)
    expect(platform.calls[1]?.body.worker_id).toBe(WORKER_ID)
  })

  it('ensure 抛错：warn + 不投递 + 无 inflight，心跳继续', async () => {
    const deliver = vi.fn()
    const { loop, platform, scheduler, lines } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [RUN_TASK], 5_000)),
      {
        sessions: [],
        deliver,
        ensureSession: async () => {
          throw new Error('resume boom')
        },
      },
    )

    await startAndBeat(loop, scheduler)

    expect(deliver).not.toHaveBeenCalled()
    expect(loop.inflight).toEqual([])
    expect(lines.join('\n')).toContain('resume boom')
    expect(loop.workerId).toBe(WORKER_ID)

    await scheduler.advance(5_000)
    expect(platform.calls[1]?.body.worker_id).toBe(WORKER_ID)
  })

  it('ensure 永不 resolve：超时后 warn 1 条、0 投递、0 inflight，下一轮心跳照常发出', async () => {
    const deliver = vi.fn()
    const never = new Promise<{ agent: Agent }>(() => {})
    const { loop, platform, scheduler, lines } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [RUN_TASK], 5_000)),
      { sessions: [], deliver, ensureSession: () => never, ensureTimeoutMs: 300 },
    )

    await startAndBeat(loop, scheduler)
    expect(platform.calls).toHaveLength(1)
    expect(deliver).not.toHaveBeenCalled()
    expect(loop.inflight).toEqual([])

    // ensure 挂起中：本轮尚未排下一轮心跳，只剩 ensure 超时定时器。
    expect(scheduler.pending.map(task => task.at)).toEqual([FIRST_HEARTBEAT_DELAY_MS + 300])

    await scheduler.advance(300)
    expect(deliver).not.toHaveBeenCalled()
    expect(loop.inflight).toEqual([])
    const timeoutWarns = lines.filter(line => line.includes('ensure 超时'))
    expect(timeoutWarns).toHaveLength(1)
    expect(timeoutWarns[0]).toContain('APP-1-T0001')
    expect(timeoutWarns[0]).toContain('300ms')

    // 超时后本轮继续并重排：下一轮心跳按 interval 计划（不再被 ensure 拖死）。
    expect(scheduler.pending.map(task => task.at)).toEqual([
      FIRST_HEARTBEAT_DELAY_MS + 300 + 5_000,
    ])

    await scheduler.advance(5_000)
    expect(platform.calls).toHaveLength(2)
  })

  it('ensure 超时后原 Promise 再 resolve：仍不投递、不写 inflight', async () => {
    const { agent } = buildAgent('idle')
    const deliver = vi.fn()
    let resolveEnsure: ((value: { agent: Agent } | undefined) => void) | undefined
    const pending = new Promise<{ agent: Agent } | undefined>(resolve => {
      resolveEnsure = resolve
    })
    const { loop, platform, scheduler } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [RUN_TASK], 5_000)),
      { sessions: [agent], deliver, ensureSession: () => pending, ensureTimeoutMs: 300 },
    )

    await startAndBeat(loop, scheduler)
    await scheduler.advance(300)
    expect(deliver).not.toHaveBeenCalled()
    expect(loop.inflight).toEqual([])

    // 超时后底层 Promise 才 resolve：不得补投递、不得补写 inflight。
    resolveEnsure?.({ agent })
    await drain(6)
    expect(deliver).not.toHaveBeenCalled()
    expect(loop.inflight).toEqual([])
    expect(platform.calls).toHaveLength(1)
  })

  it('ensure 超时后原 Promise 再 reject：不产生 unhandledRejection', async () => {
    const deliver = vi.fn()
    let rejectEnsure: ((reason: unknown) => void) | undefined
    const pending = new Promise<{ agent: Agent } | undefined>((_, reject) => {
      rejectEnsure = reject
    })
    const { loop, platform, scheduler } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [RUN_TASK], 5_000)),
      { sessions: [], deliver, ensureSession: () => pending, ensureTimeoutMs: 300 },
    )

    const rejections: unknown[] = []
    const onUnhandled = (reason: unknown): void => {
      rejections.push(reason)
    }
    process.on('unhandledRejection', onUnhandled)
    try {
      await startAndBeat(loop, scheduler)
      await scheduler.advance(300)
      rejectEnsure?.(new Error('late ensure boom'))
      await drain(6)
      expect(rejections).toEqual([])
      expect(deliver).not.toHaveBeenCalled()
      expect(loop.inflight).toEqual([])
      expect(platform.calls).toHaveLength(1)
    } finally {
      process.off('unhandledRejection', onUnhandled)
    }
  })

  it('投递失败（deliver 抛错）：warn + 不写 inflight + 不记去重（下一轮重投）', async () => {
    const { agent } = buildAgent('idle')
    let attempts = 0
    const deliver = vi.fn(() => {
      attempts += 1
      if (attempts === 1) throw new Error('followup boom')
      agent.followup('ok' as never)
    })
    const { loop, scheduler, lines } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [RUN_TASK], 5_000)),
      { sessions: [agent], deliver },
    )

    await startAndBeat(loop, scheduler)

    expect(deliver).toHaveBeenCalledTimes(1)
    expect(loop.inflight).toEqual([])
    expect(lines.join('\n')).toContain('followup boom')

    await scheduler.advance(5_000)
    expect(deliver).toHaveBeenCalledTimes(2)
  })
})

// ---------------------------------------------------------------------------
// 4. state 只看 inflight
// ---------------------------------------------------------------------------

describe('state 只看 inflight', () => {
  it('无 inflight → idle（即使存在 running 会话）', async () => {
    const { agent } = buildAgent('running')
    const { loop, platform, scheduler } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [], 5_000)),
      { sessions: [agent] },
    )

    await startAndBeat(loop, scheduler)
    expect(platform.calls[0]?.body.state).toBe('idle')

    await scheduler.advance(5_000)
    expect(platform.calls[1]?.body.state).toBe('idle')
  })

  it('投递后 inflight 会话 running → 下一轮 busy；平台不再派卡', async () => {
    const until = Date.parse(RUN_TASK.lease.until)
    const { agent, followup } = buildAgent('running')
    const { loop, platform, scheduler } = setup(
      (body, call) => {
        if (call === 0) {
          expect(body.state).toBe('idle')
          return okResponse(heartbeatPayload(WORKER_ID, [RUN_TASK], 5_000))
        }
        expect(body.state).toBe('busy')
        return okResponse(heartbeatPayload(WORKER_ID, [], 5_000))
      },
      { sessions: [agent], now: () => until - 60_000 },
    )

    await startAndBeat(loop, scheduler)
    expect(followup).toHaveBeenCalledTimes(1)
    expect(loop.inflight).toHaveLength(1)

    await scheduler.advance(5_000)
    expect(platform.calls[1]?.body.state).toBe('busy')
    expect(followup).toHaveBeenCalledTimes(1)
  })

  it('投递后 inflight 会话 idle → 下一轮 idle', async () => {
    const until = Date.parse(RUN_TASK.lease.until)
    const { agent } = buildAgent('idle')
    const { loop, platform, scheduler } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [RUN_TASK], 5_000)),
      { sessions: [agent], now: () => until - 60_000 },
    )

    await startAndBeat(loop, scheduler)
    expect(platform.calls[0]?.body.state).toBe('idle')

    await scheduler.advance(5_000)
    expect(platform.calls[1]?.body.state).toBe('idle')
  })

  it('notifyReported 之后回到 idle（即使会话仍 running）', async () => {
    const until = Date.parse(RUN_TASK.lease.until)
    const { agent } = buildAgent('running')
    const { loop, platform, scheduler } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [RUN_TASK], 5_000)),
      { sessions: [agent], now: () => until - 60_000 },
    )

    await startAndBeat(loop, scheduler)
    await scheduler.advance(5_000)
    expect(platform.calls[1]?.body.state).toBe('busy')

    loop.notifyReported('APP-1-T0001')
    await scheduler.advance(5_000)
    expect(platform.calls[2]?.body.state).toBe('idle')
  })
})

// ---------------------------------------------------------------------------
// 4b. inflight 生命周期
// ---------------------------------------------------------------------------

describe('inflight 生命周期', () => {
  it('投递成功写入 taskId / projectId / leaseUntil', async () => {
    const { agent } = buildAgent('idle')
    const { loop, scheduler } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [RUN_TASK], 5_000)),
      { sessions: [agent] },
    )

    expect(loop.inflight).toEqual([])
    await startAndBeat(loop, scheduler)
    expect(loop.inflight).toEqual([
      { taskId: RUN_TASK.task.id, projectId: 'APP-1', leaseUntil: RUN_TASK.lease.until },
    ])
  })

  it('notifyReported 清除对应任务；未知 taskId 静默忽略', async () => {
    const { agent } = buildAgent('idle')
    const { loop, scheduler, lines } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [RUN_TASK], 5_000)),
      { sessions: [agent] },
    )

    await startAndBeat(loop, scheduler)
    loop.notifyReported('not-a-task')
    expect(loop.inflight).toHaveLength(1)

    loop.notifyReported(RUN_TASK.task.id)
    expect(loop.inflight).toEqual([])
    expect(lines.join('\n')).toContain('已从 inflight 清除')
  })

  it('租约过期：下一轮开始前清除并记一条日志', async () => {
    const until = Date.parse(RUN_TASK.lease.until)
    let clock = until - 60_000
    const { agent } = buildAgent('idle')
    const { loop, scheduler, lines } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [RUN_TASK], 5_000)),
      { sessions: [agent], now: () => clock },
    )

    await startAndBeat(loop, scheduler)
    expect(loop.inflight).toHaveLength(1)

    clock = until + 1
    await scheduler.advance(5_000)
    expect(loop.inflight).toEqual([])
    expect(lines.join('\n')).toContain('租约已过期')
  })

  it('同 task 新 lease：覆盖旧 leaseUntil，不重复计数', async () => {
    const until = Date.parse(RUN_TASK.lease.until)
    const renewed = {
      ...RUN_TASK,
      lease: { ...RUN_TASK.lease, lease_id: 'l_renewed', until: '2026-10-27T17:00:00.000Z' },
    }
    const { agent } = buildAgent('idle')
    const { loop, scheduler } = setup(
      (body, call) => okResponse(
        heartbeatPayload(WORKER_ID, [call === 0 ? RUN_TASK : renewed], 5_000),
      ),
      { sessions: [agent], now: () => until - 60_000 },
    )

    await startAndBeat(loop, scheduler)
    expect(loop.inflight[0]?.leaseUntil).toBe(RUN_TASK.lease.until)

    await scheduler.advance(5_000)
    expect(loop.inflight).toEqual([
      { taskId: RUN_TASK.task.id, projectId: 'APP-1', leaseUntil: '2026-10-27T17:00:00.000Z' },
    ])
  })
})

// ---------------------------------------------------------------------------
// 5. 409 worker_unknown
// ---------------------------------------------------------------------------

describe('409 worker_unknown', () => {
  it('清空 id → 立即重排 → 下一轮无 worker_id → 再下一轮带新 id', async () => {
    const oldId = 'w_OLD000000000000000000'
    const newId = 'w_NEW111111111111111111'
    let recovered = false
    const { loop, platform, scheduler, lines } = setup((body) => {
      if (!('worker_id' in body)) {
        // 首次注册：如果已经恢复过就用新 id。
        return okResponse(heartbeatPayload(recovered ? newId : oldId, [], 5_000))
      }
      if (body.worker_id === oldId && !recovered) {
        recovered = true
        return errorResponse(409, 'worker_unknown')
      }
      return okResponse(heartbeatPayload(newId, [], 5_000))
    })

    await startAndBeat(loop, scheduler)
    expect(platform.calls[0] && 'worker_id' in platform.calls[0].body).toBe(false)
    expect(loop.workerId).toBe(oldId)

    // 第二轮带旧 id → 409，清空并排一轮「立即」重注册（0ms）。
    await scheduler.advance(5_000)
    expect(platform.calls[1]?.body.worker_id).toBe(oldId)
    expect(loop.workerId).toBeUndefined()
    expect(lines.join('\n')).toContain('worker_unknown')
    expect(scheduler.pending.map(task => task.at)).toEqual([scheduler.now])

    // 立即重注册：body 里完全没有 worker_id。
    await scheduler.advance(0)
    expect(platform.calls[2] && 'worker_id' in platform.calls[2].body).toBe(false)
    expect(loop.workerId).toBe(newId)

    // 再下一轮带新 id。
    await scheduler.advance(5_000)
    expect(platform.calls[3]?.body.worker_id).toBe(newId)
  })
})

// ---------------------------------------------------------------------------
// 6. 500 / 断网 / 超时
// ---------------------------------------------------------------------------

describe('单轮失败不崩溃', () => {
  const failures: Array<{ name: string; make: () => Response | Promise<Response>; expectText: string }> = [
    {
      name: '500 internal',
      make: () => errorResponse(500, 'internal'),
      expectText: 'code=internal',
    },
    {
      name: '断网（fetch reject）',
      make: () => Promise.reject(new Error('connect ECONNREFUSED')),
      expectText: 'code=network_error',
    },
    {
      name: '超时（AbortError）',
      make: () => {
        const error = new Error('aborted')
        error.name = 'AbortError'
        return Promise.reject(error)
      },
      expectText: 'code=timeout',
    },
  ]

  for (const failure of failures) {
    it(`${failure.name}：warn、不清 id、下一轮继续`, async () => {
      const { loop, platform, scheduler, lines } = setup((body, call) => {
        if (call === 0) return okResponse(heartbeatPayload(WORKER_ID, [], 5_000))
        if (call === 1) return failure.make()
        return okResponse(heartbeatPayload(WORKER_ID, [], 5_000))
      })

      await startAndBeat(loop, scheduler)
      expect(loop.workerId).toBe(WORKER_ID)

      await scheduler.advance(5_000)
      expect(platform.calls).toHaveLength(2)
      expect(platform.calls[1]?.body.worker_id).toBe(WORKER_ID)
      expect(loop.workerId).toBe(WORKER_ID)
      expect(lines.join('\n')).toContain(failure.expectText)

      await scheduler.advance(5_000)
      expect(platform.calls).toHaveLength(3)
      expect(platform.calls[2]?.body.worker_id).toBe(WORKER_ID)
    })
  }
})

// ---------------------------------------------------------------------------
// 7. poll.enable=false / dispose
// ---------------------------------------------------------------------------

describe('开关与卸载', () => {
  it('poll.enable=false：零请求、零定时器、零 ensure（构造也不建）', async () => {
    const ensure = vi.fn(async () => undefined)
    const { loop, platform, scheduler, lines } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID)),
      { enable: false, ensureSession: ensure },
    )

    expect(scheduler.count).toBe(0)
    await startAndBeat(loop, scheduler)

    expect(platform.calls).toHaveLength(0)
    expect(scheduler.count).toBe(0)
    expect(ensure).not.toHaveBeenCalled()
    expect(loop.inflight).toEqual([])
    expect(loop.started).toBe(false)
    expect(lines.join('\n')).toContain('worker.poll.enable=false')

    loop.dispose()
    expect(scheduler.count).toBe(0)
  })

  it('运行中 dispose：定时器立即清除，推进假时钟不再有请求与日志', async () => {
    const { loop, platform, scheduler, lines } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [], 5_000)),
    )

    await startAndBeat(loop, scheduler)
    expect(platform.calls).toHaveLength(1)
    expect(scheduler.count).toBe(1)

    loop.dispose('plugin dispose')
    expect(scheduler.count).toBe(0)
    expect(loop.disposed).toBe(true)

    const logCountAfterDispose = lines.length
    await scheduler.advance(60_000)

    expect(platform.calls).toHaveLength(1)
    expect(lines).toHaveLength(logCountAfterDispose)
  })

  it('首轮延迟内 dispose：定时器立即清除，连首轮心跳也不发（零请求）', async () => {
    const { loop, platform, scheduler, lines } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [], 5_000)),
    )

    loop.start()
    await drain()
    // 首轮心跳还排在 5000ms 之后，此时卸载：不准补发首轮。
    expect(scheduler.count).toBe(1)
    loop.dispose('plugin dispose')
    expect(scheduler.count).toBe(0)

    const logCountAfterDispose = lines.length
    await scheduler.advance(FIRST_HEARTBEAT_DELAY_MS)
    await scheduler.advance(60_000)

    expect(platform.calls).toHaveLength(0)
    expect(loop.workerId).toBeUndefined()
    expect(lines).toHaveLength(logCountAfterDispose)
  })

  it('dispose 幂等，重复调用不再记日志', async () => {
    const { loop, scheduler, lines } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [], 5_000)),
    )

    await startAndBeat(loop, scheduler)
    loop.dispose()
    const count = lines.length
    loop.dispose()
    loop.dispose()

    expect(lines).toHaveLength(count)
    expect(scheduler.count).toBe(0)
  })

  it('dispose 之后 start() 不再建定时器', async () => {
    const { loop, scheduler, platform } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [], 5_000)),
    )

    loop.dispose()
    await startAndBeat(loop, scheduler)

    expect(scheduler.count).toBe(0)
    expect(platform.calls).toHaveLength(0)
  })
})

// ---------------------------------------------------------------------------
// 8. 客户端命名空间与源码约束
// ---------------------------------------------------------------------------

describe('客户端与源码约束', () => {
  it('worker 心跳走独立客户端，TOOL_METHODS 恰好 20 个', () => {
    expect(TOOL_METHODS).toHaveLength(20)
    const config = resolveConfig({ worker: { poll: { enable: true } } })
    const client = createWorkerClient(config, {
      fetchImpl: async () => okResponse(heartbeatPayload(WORKER_ID)),
    })
    expect(Object.keys(client)).toEqual(['heartbeat'])
    expect(TOOL_METHODS).not.toContain('heartbeat')
  })

  it('kit/src 不含任何磁盘写入调用', () => {
    const srcDir = path.resolve(here, '../src')
    for (const file of readdirSync(srcDir).filter(name => name.endsWith('.ts'))) {
      const text = readFileSync(path.join(srcDir, file), 'utf8')
      expect(text, `${file} 不应写盘`).not.toMatch(/writeFile|appendFile|createWriteStream/)
    }
  })
})

// ---------------------------------------------------------------------------
// 9. R-01：持续 409 worker_unknown 必须退避，不能忙等风暴
// ---------------------------------------------------------------------------

describe('R-01 持续 409 worker_unknown 退避', () => {
  /** 平台永远 409 worker_unknown（例如平台库被清空后仍能连通）。 */
  const alwaysUnknown: Handler = () => errorResponse(409, 'worker_unknown')

  it('首轮延迟 5s 后 409 立即重试一次，之后 1s→2s→5s→15s：20s 内请求 ≤ 6、warn ≤ 3', async () => {
    const times: number[] = []
    let clock: ReturnType<typeof createScheduler> | undefined
    const { loop, platform, scheduler, lines } = setup((body, call) => {
      times.push(clock?.now ?? -1)
      return alwaysUnknown(body, call)
    })
    clock = scheduler

    await startAndBeat(loop, scheduler)
    // 首轮在 5000ms 发出（延迟一个 FIRST_HEARTBEAT_DELAY_MS，不是立即）。
    expect(times).toEqual([FIRST_HEARTBEAT_DELAY_MS])
    // 第 1 次 409 → 唯一允许的「立即重注册」（0ms；轮内重排，不受启动延迟影响）。
    expect(platform.calls).toHaveLength(1)
    expect(scheduler.pending.map(task => task.at)).toEqual([scheduler.now])

    // 先走一次 0ms（立即重试），再按 1s 粒度推进到 20s。
    await scheduler.advance(0)
    expect(platform.calls).toHaveLength(2)
    for (let index = 0; index < 20; index += 1) await scheduler.advance(1_000)

    expect(scheduler.now).toBe(FIRST_HEARTBEAT_DELAY_MS + 20_000)
    // 请求时刻 = +5s（首轮）/ +5s（立即）/ +6s / +8s / +13s 的退避阶梯（修前是 0ms 无限重排）。
    expect(times).toEqual([
      FIRST_HEARTBEAT_DELAY_MS,
      FIRST_HEARTBEAT_DELAY_MS,
      FIRST_HEARTBEAT_DELAY_MS + 1_000,
      FIRST_HEARTBEAT_DELAY_MS + 3_000,
      FIRST_HEARTBEAT_DELAY_MS + 8_000,
    ])
    expect(platform.calls.length).toBeLessThanOrEqual(6)

    const unknownLogs = lines.filter(line => line.includes('worker_unknown'))
    expect(unknownLogs.length).toBeLessThanOrEqual(3)
    expect(unknownLogs).toHaveLength(1)

    // 下一轮要等到 15s 封顶之后（+13s + 15000）。
    expect(scheduler.pending.map(task => task.at)).toEqual([
      FIRST_HEARTBEAT_DELAY_MS + 23_000,
    ])
  })

  it('长时间持续 409：稳态速率 ≤ 0.2 请求/秒（120s 内 13 次）', async () => {
    const { loop, platform, scheduler } = setup(alwaysUnknown)

    await startAndBeat(loop, scheduler)
    await scheduler.advance(0)
    for (let index = 0; index < 120; index += 1) await scheduler.advance(1_000)

    expect(scheduler.now).toBe(FIRST_HEARTBEAT_DELAY_MS + 120_000)
    // 稳态每 15s 一次：120s 内共 12 次 → 0.1 请求/秒（修前 593/s）。
    expect(platform.calls).toHaveLength(12)
    expect(platform.calls.length / (scheduler.now / 1_000)).toBeLessThanOrEqual(0.2)
  })

  it('warn 节流：首次与每 10 次各一条，并带累计次数（20 次 409 → 3 条）', async () => {
    const { loop, platform, scheduler, lines } = setup(alwaysUnknown)

    await startAndBeat(loop, scheduler)
    for (let guard = 0; guard < 60 && platform.calls.length < 20; guard += 1) {
      await scheduler.advance(15_000)
    }

    expect(platform.calls).toHaveLength(20)
    const unknownLogs = lines.filter(line => line.includes('worker_unknown'))
    expect(unknownLogs).toHaveLength(3)
    expect(unknownLogs[0]).toContain('立即重新注册')
    expect(unknownLogs[1]).toContain('连续 10 次')
    expect(unknownLogs[2]).toContain('连续 20 次')
  })

  it('成功注册后计数归零：之后再遇到 409 仍可「立即」重试一次', async () => {
    const idA = 'w_AAA000000000000000000'
    const idB = 'w_BBB111111111111111111'
    const { loop, platform, scheduler } = setup((body, call) => {
      if (call === 0) return okResponse(heartbeatPayload(idA, [], 5_000))
      if (call === 1) return errorResponse(409, 'worker_unknown')
      if (call === 2) return okResponse(heartbeatPayload(idB, [], 5_000))
      return errorResponse(409, 'worker_unknown')
    })

    await startAndBeat(loop, scheduler)
    expect(loop.workerId).toBe(idA)

    await scheduler.advance(5_000) // 409 → 计数 1 → 立即重排
    expect(scheduler.pending.map(task => task.at)).toEqual([scheduler.now])

    await scheduler.advance(0) // 重新注册成功 → 计数归零
    expect(loop.workerId).toBe(idB)

    await scheduler.advance(5_000) // 再 409 → 又享受一次「立即」
    expect(platform.calls).toHaveLength(4)
    expect(scheduler.pending.map(task => task.at)).toEqual([scheduler.now])
  })

  it('网络失败仍按固定间隔重试，不套用 409 的退避（回归）', async () => {
    let call = 0
    const times: number[] = []
    const { loop, scheduler } = setup(() => {
      const current = call
      call += 1
      times.push(scheduler.now)
      if (current === 0) return okResponse(heartbeatPayload(WORKER_ID, [], 5_000))
      return Promise.reject(new Error('connect ECONNREFUSED'))
    })

    await startAndBeat(loop, scheduler)
    await scheduler.advance(5_000)
    expect(times).toEqual([FIRST_HEARTBEAT_DELAY_MS, FIRST_HEARTBEAT_DELAY_MS + 5_000])
    // 失败后仍按当前 5000ms 间隔（不是 409 的 1s/2s/5s 退避）。
    expect(scheduler.pending.map(task => task.at)).toEqual([
      FIRST_HEARTBEAT_DELAY_MS + 10_000,
    ])
  })
})

// ---------------------------------------------------------------------------
// 10. R-02：metadata 超 8KiB 不启动循环
// ---------------------------------------------------------------------------

describe('R-02 metadata 超限不启动循环', () => {
  it('metadata 超 8KiB：零请求、零定时器，warn 含实际字节数与上限', async () => {
    const metadata = { blob: 'x'.repeat(WORKER_METADATA_MAX_BYTES) }
    const bytes = metadataByteLength(metadata)
    expect(bytes).toBeGreaterThan(WORKER_METADATA_MAX_BYTES)

    const { loop, platform, scheduler, lines } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [], 5_000)),
      { metadata },
    )
    expect(loop.metadataOverLimit).toBe(true)

    await startAndBeat(loop, scheduler)

    expect(loop.started).toBe(false)
    expect(loop.workerId).toBeUndefined()
    expect(platform.calls).toHaveLength(0)
    expect(scheduler.count).toBe(0)

    const joined = lines.join('\n')
    expect(joined).toContain(`${bytes}B`)
    expect(joined).toContain(`${WORKER_METADATA_MAX_BYTES}B`)
    expect(joined).toContain('不启动心跳循环')

    // 推进假时钟也不会凭空冒出请求（没有任何定时器）。
    await scheduler.advance(120_000)
    expect(platform.calls).toHaveLength(0)
    expect(scheduler.count).toBe(0)
  })

  it('metadata 恰好等于上限（8192B）时仍正常启动（边界不误伤）', async () => {
    // `{"k":""}` 的固定开销是 8 字节 → 8184 个 a 恰好 8192 字节。
    const metadata = { k: 'a'.repeat(WORKER_METADATA_MAX_BYTES - 8) }
    expect(metadataByteLength(metadata)).toBe(WORKER_METADATA_MAX_BYTES)

    const { loop, platform, scheduler } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [], 5_000)),
      { metadata },
    )
    await startAndBeat(loop, scheduler)

    expect(loop.metadataOverLimit).toBe(false)
    expect(loop.started).toBe(true)
    expect(platform.calls).toHaveLength(1)
  })

  it('poll.enable=false 且 metadata 超限：行为不变（只提示 poll 关闭，零定时器）', async () => {
    const { loop, platform, scheduler } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID)),
      { metadata: { blob: 'x'.repeat(WORKER_METADATA_MAX_BYTES) }, enable: false },
    )

    await startAndBeat(loop, scheduler)

    expect(platform.calls).toHaveLength(0)
    expect(scheduler.count).toBe(0)
    expect(loop.started).toBe(false)
  })
})

// ---------------------------------------------------------------------------
// 11. R-03：heartbeat_interval_ms 夹紧到 [500ms, 24h]
// ---------------------------------------------------------------------------

describe('R-03 心跳间隔上下限夹紧', () => {
  it('回 1ms → 夹紧到 500ms（不再照单全收）', async () => {
    const { loop, scheduler, lines } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [], 1)),
    )

    await startAndBeat(loop, scheduler)

    expect(loop.intervalMs).toBe(MIN_HEARTBEAT_INTERVAL_MS)
    expect(MIN_HEARTBEAT_INTERVAL_MS).toBe(500)
    expect(scheduler.pending.map(task => task.at)).toEqual([
      FIRST_HEARTBEAT_DELAY_MS + MIN_HEARTBEAT_INTERVAL_MS,
    ])
    expect(lines.join('\n')).toContain('heartbeat_interval_ms=1')
  })

  it('回 999999999ms → 夹紧到 24h（86400000ms）', async () => {
    const { loop, scheduler } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [], 999_999_999)),
    )

    await startAndBeat(loop, scheduler)

    expect(MAX_HEARTBEAT_INTERVAL_MS).toBe(86_400_000)
    expect(loop.intervalMs).toBe(MAX_HEARTBEAT_INTERVAL_MS)
    expect(scheduler.pending.map(task => task.at)).toEqual([
      FIRST_HEARTBEAT_DELAY_MS + MAX_HEARTBEAT_INTERVAL_MS,
    ])
  })

  it('缺失 / "abc" / 0 / -1 → 回落 15000ms（不属于越界夹紧）', async () => {
    const variants: Array<{ name: string; mutate: (payload: { worker: Record<string, unknown> }) => void }> = [
      { name: '缺失', mutate: () => {} },
      { name: '"abc"', mutate: payload => { payload.worker.heartbeat_interval_ms = 'abc' } },
      { name: '0', mutate: payload => { payload.worker.heartbeat_interval_ms = 0 } },
      { name: '-1', mutate: payload => { payload.worker.heartbeat_interval_ms = -1 } },
    ]

    for (const variant of variants) {
      const payload = heartbeatPayload(WORKER_ID) as unknown as { worker: Record<string, unknown> }
      variant.mutate(payload)
      const { loop, scheduler, lines } = setup(() => okResponse(payload))

      await startAndBeat(loop, scheduler)

      expect(loop.intervalMs, variant.name).toBe(DEFAULT_HEARTBEAT_INTERVAL_MS)
      expect(scheduler.pending.map(task => task.at), variant.name).toEqual([
        FIRST_HEARTBEAT_DELAY_MS + DEFAULT_HEARTBEAT_INTERVAL_MS,
      ])
      expect(lines.join('\n'), variant.name).not.toContain('已夹紧为 500ms')
    }
  })

  it('夹紧提示只打一次，不每轮刷', async () => {
    const { loop, scheduler, lines } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [], 1)),
    )

    await startAndBeat(loop, scheduler)
    await scheduler.advance(500)
    await scheduler.advance(500)
    await scheduler.advance(500)

    const notices = lines.filter(line => line.includes('已夹紧为 500ms'))
    expect(notices).toHaveLength(1)
    expect(loop.intervalMs).toBe(500)
  })
})

// ---------------------------------------------------------------------------
// 12. S-01：重复派单去重（task.id + lease_id）
// ---------------------------------------------------------------------------

describe('S-01 重复派单去重', () => {
  it('同一 (task.id, lease_id) 连续多轮下发 → followup 只 1 次', async () => {
    const { agent, followup } = buildAgent('idle')
    const { loop, scheduler, lines } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [RUN_TASK], 5_000)),
      { sessions: [agent] },
    )

    await startAndBeat(loop, scheduler)
    expect(followup).toHaveBeenCalledTimes(1)

    await scheduler.advance(5_000)
    await scheduler.advance(5_000)
    expect(followup).toHaveBeenCalledTimes(1)
    // 只投递过 1 次，且后续轮次没有再打「已投递」日志（无重复投递）。
    expect(lines.filter(line => line.includes('已投递任务'))).toHaveLength(1)
  })

  it('同一轮里重复下发同一条 → 只投 1 次', async () => {
    const { agent, followup } = buildAgent('idle')
    const deliver = vi.fn((target: Agent, text: string) => { target.followup(text as never) })
    const { loop, scheduler } = setup(
      () => okResponse(heartbeatPayload(WORKER_ID, [RUN_TASK, RUN_TASK], 5_000)),
      { sessions: [agent], deliver },
    )

    await startAndBeat(loop, scheduler)

    expect(deliver).toHaveBeenCalledTimes(1)
    expect(followup).toHaveBeenCalledTimes(1)
  })

  it('同 task.id 不同 lease_id → 两次都投递', async () => {
    const { agent, followup } = buildAgent('idle')
    const first = RUN_TASK
    const second = { ...RUN_TASK, lease: { ...RUN_TASK.lease, lease_id: 'l_other_lease' } }
    const { loop, scheduler } = setup(
      (body, call) => okResponse(heartbeatPayload(WORKER_ID, [call === 0 ? first : second], 5_000)),
      { sessions: [agent] },
    )

    await startAndBeat(loop, scheduler)
    await scheduler.advance(5_000)

    expect(followup).toHaveBeenCalledTimes(2)
  })

  it('去重表是有界 LRU（容量 200）：超出后最早的键被淘汰可重投', async () => {
    const { agent, followup } = buildAgent('idle')
    expect(MAX_REMEMBERED_DELIVERIES).toBe(200)
    const many = Array.from({ length: MAX_REMEMBERED_DELIVERIES + 1 }, (_, index) => ({
      ...RUN_TASK,
      task: { ...RUN_TASK.task, id: `APP-1-T${String(index).padStart(4, '0')}` },
    }))
    let round = 0
    const { loop, scheduler } = setup(
      () => {
        round += 1
        return okResponse(
          heartbeatPayload(WORKER_ID, round === 1 ? many : [many[0]], 5_000),
        )
      },
      { sessions: [agent] },
    )

    await startAndBeat(loop, scheduler)
    expect(followup).toHaveBeenCalledTimes(MAX_REMEMBERED_DELIVERIES + 1)

    // 最早的键（第 1 条）已被淘汰 → 再次下发会重新投递。
    await scheduler.advance(5_000)
    expect(followup).toHaveBeenCalledTimes(MAX_REMEMBERED_DELIVERIES + 2)
  })
})
