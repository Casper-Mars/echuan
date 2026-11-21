/**
 * V3 worker 循环：心跳只负责注册，任务通过 claim 主动拉取。
 *
 * 与 v2 的 worker.ts 的区别：v2 等心跳回包里的 `commands.dispatch`；V3 的
 * `/v1/workers/heartbeat` 只登记 worker，任务队列走 `/v1/tasks/claim`。本文件
 * 因此不再解析 dispatch 指令，而是按 `worker.kinds` 逐个 kind 领卡。
 *
 * 一个 worker 同时只持一张卡：拿到即写 inflight，报告成功（工具层回调
 * `notifyReported`）或租约过期后清空，下一轮才继续领。
 */
import { randomUUID } from 'node:crypto'
import type { Agent } from '@deepseek-ai/dsh-agent'
import { createUserMessage } from '@deepseek-ai/dsh-llm/message'
import type { ResolvedConfig } from './config.js'
import type { V3Task, V3TaskKind } from './v3-types.js'
import { renderDispatchFacts } from './dispatch-format.js'
import {
  DEFAULT_HEARTBEAT_INTERVAL_MS,
  FIRST_HEARTBEAT_DELAY_MS,
  WORKER_PLUGIN_SOURCE_KIND,
} from './worker-shared.js'

const TAG = '[dsh-echuan]'

export interface PullWorkerLogger {
  info(message: string): void
  warn(message: string): void
  debug?(message: string): void
}

export interface PullWorkerInflight {
  readonly workId: string
  readonly kind: V3TaskKind
  readonly leaseId: string
  readonly leaseUntil: string
}

export interface PullWorkerClient {
  heartbeatV3(input: {
    worker_id: string
    state: 'idle' | 'busy'
    kinds: readonly V3TaskKind[]
    metadata: Record<string, unknown>
  }): Promise<{ kind: string }>
  claimV3Task(input: { worker_id: string; kind: V3TaskKind }): Promise<{ kind: string; data?: any }>
  renewV3Task(input: { task_id: string; lease_id: string }): Promise<{
    kind: string
    data?: { task_id: string; lease_id: string; lease_until: string }
  }>
}

export interface PullClaimEnvelope {
  task: V3Task
  project: { project_id: string; name: string; context_md: string }
  product: { product_id: string; name: string; git_url: string }
  inputs: Array<{ material_id: string; title: string; kind: string; role: string }>
  upstream: Array<{ task_id: string; material_id?: string | null; title?: string | null }>
  has_downstream_dev: boolean
}

export interface PullWorkerOptions {
  config: ResolvedConfig
  client: PullWorkerClient
  ensureSession?: (projectId: string, presetId?: string) => Promise<Agent | undefined>
  agents?: { roots(): readonly Agent[] }
  deliver?: (agent: Agent, text: string) => void
  logger: PullWorkerLogger
  /** 测试用：替换定时器。 */
  schedule?: (fn: () => void, ms: number) => unknown
  cancel?: (handle: unknown) => void
  /** 测试用：单轮拉取（不启动定时器）。 */
  intervalMs?: number
}

export interface PullWorkerLoop {
  readonly workerId: string
  readonly inflight: readonly PullWorkerInflight[]
  start(): void
  dispose(reason?: string): void
  notifyReported(taskId: string): void
  /** 只跑一轮心跳 + 拉取，供测试与排障。 */
  pollOnce(): Promise<void>
}

function defaultDeliver(agent: Agent, text: string): void {
  const message = createUserMessage({
    content: [{ type: 'text', text }],
    source: { kind: WORKER_PLUGIN_SOURCE_KIND },
  })
  agent.followup(message)
}

export function renderPullDispatch(envelope: PullClaimEnvelope): string {
  const { task, project, product } = envelope
  return renderDispatchFacts({
    workId: task.task_id,
    kind: task.kind,
    title: task.title,
    projectId: project.project_id,
    projectName: project.name,
    productId: product.product_id,
    productName: product.name,
    gitUrl: product.git_url,
    status: task.status,
    attempt: task.attempt,
    maxAttempts: task.max_attempts,
    priority: task.priority,
    leaseId: task.lease_id,
    leaseUntil: task.lease_until,
    reason: task.reason,
    descriptionMd: task.description_md,
    contextMd: task.context_md,
    upstream: envelope.upstream.map(item => ({
      taskId: item.task_id,
      materialId: item.material_id,
      title: item.title,
    })),
    inputs: envelope.inputs.map(input => ({
      materialId: input.material_id,
      kind: input.kind,
      role: input.role,
      title: input.title,
    })),
  })
}

export function createPullWorkerLoop(options: PullWorkerOptions): PullWorkerLoop {
  const { config, client, logger } = options
  const deliver = options.deliver ?? defaultDeliver
  const interval = options.intervalMs ?? DEFAULT_HEARTBEAT_INTERVAL_MS
  const schedule = options.schedule ?? ((fn, ms) => setTimeout(fn, ms))
  const cancel = options.cancel ?? ((handle) => clearTimeout(handle as ReturnType<typeof setTimeout>))

  const workerId = `w_${randomUUID()}`
  let inflight: PullWorkerInflight | undefined
  let timer: unknown
  let disposed = false
  let started = false

  async function heartbeat(): Promise<void> {
    await client.heartbeatV3({
      worker_id: workerId,
      state: inflight === undefined ? 'idle' : 'busy',
      kinds: config.worker.kinds as V3TaskKind[],
      metadata: config.worker.metadata,
    })
  }

  async function pickAgent(projectId: string, kind: V3TaskKind): Promise<Agent | undefined> {
    const presetId = config.worker.presetByKind[kind]
    if (options.ensureSession !== undefined) {
      return options.ensureSession(projectId, presetId)
    }
    return options.agents?.roots()[0]
  }

  async function claimOnce(): Promise<void> {
    for (const kind of config.worker.kinds as V3TaskKind[]) {
      const result = await client.claimV3Task({ worker_id: workerId, kind })
      if (result.kind === 'empty' || result.data === undefined) continue

      const envelope = result.data as PullClaimEnvelope
      const projectId = envelope.project.project_id
      const agent = await pickAgent(projectId, kind)
      if (agent === undefined) {
        logger.warn(`${TAG} 领到任务 ${envelope.task.task_id} 但项目 ${projectId} 的会话未就绪；`
          + '本轮跳过投递，租约到期后平台会自动回收')
        return
      }

      deliver(agent, renderPullDispatch(envelope))
      inflight = {
        workId: envelope.task.task_id,
        kind,
        leaseId: envelope.task.lease_id ?? "",
        leaseUntil: envelope.task.lease_until ?? "",
      }
      logger.info(`${TAG} 已投递任务 ${envelope.task.task_id}（kind=${kind}）给项目 ${projectId}`)
      return
    }
  }

  async function pollOnce(): Promise<void> {
    if (disposed) return
    try {
      await heartbeat()
      if (inflight !== undefined) {
        if (inflight.leaseUntil !== '' && Date.parse(inflight.leaseUntil) <= Date.now()) {
          logger.warn(`${TAG} 任务 ${inflight.workId} 的租约已过期，清除 inflight 并等待平台回收`)
          inflight = undefined
        } else if (inflight.leaseId !== '') {
          const current = inflight
          try {
            const renewed = await client.renewV3Task({
              task_id: current.workId,
              lease_id: current.leaseId,
            })
            if (renewed.kind === 'empty' || renewed.data?.lease_until === undefined) {
              logger.warn(`${TAG} 任务 ${current.workId} 续租无回包，清除 inflight`)
              inflight = undefined
            } else {
              inflight = { ...current, leaseUntil: renewed.data.lease_until }
            }
          } catch (err) {
            logger.warn(`${TAG} 任务 ${current.workId} 续租失败，清除 inflight：${err instanceof Error ? err.message : String(err)}`)
            inflight = undefined
          }
        }
      }
      if (inflight === undefined) await claimOnce()
    } catch (err) {
      logger.warn(`${TAG} 心跳/领卡失败：${err instanceof Error ? err.message : String(err)}`)
    }
  }

  function arm(delay: number): void {
    if (disposed) return
    timer = schedule(() => {
      void (async () => {
        await pollOnce()
        arm(interval)
      })()
    }, delay)
  }

  return {
    workerId,
    get inflight() {
      return inflight === undefined ? [] : [inflight]
    },
    start() {
      if (started || disposed) return
      started = true
      arm(FIRST_HEARTBEAT_DELAY_MS)
    },
    dispose(reason?: string) {
      if (disposed) return
      disposed = true
      if (timer !== undefined) cancel(timer)
      timer = undefined
      logger.info(`${TAG} 心跳已停止${reason ? `：${reason}` : ''}`)
    },
    notifyReported(taskId: string) {
      if (inflight?.workId === taskId) inflight = undefined
    },
    pollOnce,
  }
}
