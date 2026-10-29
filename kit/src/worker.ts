/**
 * Worker 心跳循环：把「定时心跳 + 派单投递」收敛成**唯一**一套定时器。
 *
 * 行为契约（任务-03）：
 * 1. `POST /v1/workers/heartbeat`，body `{ worker_id?, state, metadata }`；
 * 2. 首次注册**不带** `worker_id` 字段，回包里的 `w_` id 只存内存；
 * 3. 首轮心跳在 `start()` 后延迟 {@link FIRST_HEARTBEAT_DELAY_MS} 发出（见该常量
 *    注释），之后每轮都带同一个 id；平台说 id 不存在（409 `worker_unknown`）就清空并
 *    重排一轮重新注册——「立即」只给第 1 次（R-01）；连续第 2 次起按
 *    {@link WORKER_UNKNOWN_BACKOFF_MS} 退避（1s → 2s → 5s → 15s 封顶），成功注册后
 *    计数归零；同一原因的 warn 节流，首次与每 {@link WORKER_UNKNOWN_LOG_EVERY} 次
 *    各打一条，并带累计次数；
 * 4. 间隔只认回包 `heartbeat_interval_ms`，缺失/非法回落 15s；间隔会变，因此用
 *    `setTimeout` 链（每轮结束再排下一轮），不用 `setInterval`；R-03：合法值夹紧到
 *    [{@link MIN_HEARTBEAT_INTERVAL_MS}, {@link MAX_HEARTBEAT_INTERVAL_MS}]；
 * 5. 回包 `commands` 里的 `run_task` 投给唯一的根 agent；根 agent 不是恰好 1 个时
 *    只 warn 并跳过本轮投递（不清 id、不重试本轮）；S-01：按 `task.id + lease_id`
 *    在内存里记住最近投递过的派单（LRU，容量 {@link MAX_REMEMBERED_DELIVERIES}），
 *    重复下发只投一次，不落盘；
 * 6. 网络失败 / 500 / 超时只 warn，保留 id，按当前间隔继续；不做退避状态机；
 * 7. `poll.enable=false` 时零请求、零定时器；`dispose()` 后定时器立刻清空，
 *    之后不再产生任何请求与日志（包括还没发出的首轮心跳）；
 * 8. worker_id 只在内存，日志里一律打码；
 * 9. R-02：`worker.metadata` 序列化后超过 {@link WORKER_METADATA_MAX_BYTES} 时
 *    **不启动循环**（一条明确 warn 后返回），避免每轮 413 永久刷屏。
 *
 * 所有外部依赖（客户端 / 定时器 / 日志 / agent 访问 / 投递）都可注入：
 * 单测用假平台与假时钟，不连真实网络、不做真实 sleep。
 */
import type { Agent } from '@deepseek-ai/dsh-agent'
import { createUserMessage } from '@deepseek-ai/dsh-llm/message'
import type {
  ClaimContext,
  ClaimLease,
  ClaimedTask,
  WorkerClient,
  WorkerCommand as ClientWorkerCommand,
  WorkerHeartbeatPayload,
} from './client.js'
import type { ResolvedConfig } from './config.js'
import { WORKER_METADATA_MAX_BYTES, metadataByteLength } from './config.js'
import { EchuanApiError, EchuanClientError } from './errors.js'
import type { Logger } from './runtime.js'

/** 回包没给合法 `heartbeat_interval_ms` 时的兜底间隔（毫秒）。 */
export const DEFAULT_HEARTBEAT_INTERVAL_MS = 15_000

/**
 * 首轮心跳相对 `start()` 的延迟（毫秒）。
 *
 * 插件拿不到 dsh 的启动参数，无法区分「长驻进程」与 `dsh --help` 这类几秒内就退出
 * 的短命令；若启动即注册，`--help`（`worker.poll.enable=true` 时）也会打一次真实
 * POST。用 5s 的短延迟规避这个副作用：正常长驻进程 5s 后照常注册（平台侧只多等
 * 5s），几秒内退出的命令则不会产生任何请求。
 *
 * 注意：它只作用于「启动」，与 409 `worker_unknown` 的「首轮立即重试」无关——
 * 后者是同一轮内的重排，延迟 0 的语义保持不变。
 */
export const FIRST_HEARTBEAT_DELAY_MS = 5_000

/** R-03：平台下发间隔的下限（毫秒），低于它按下限。 */
export const MIN_HEARTBEAT_INTERVAL_MS = 500

/** R-03：平台下发间隔的上限（毫秒，24h），高于它按上限。 */
export const MAX_HEARTBEAT_INTERVAL_MS = 86_400_000

/**
 * R-01：连续 `worker_unknown` 第 2 次起的退避序列（毫秒）。
 *
 * 第 1 次仍是「立即重注册」（延迟 0）：第 2 次 1s、第 3 次 2s、第 4 次 5s、
 * 第 5 次及以后 15s 封顶。只作用于 `worker_unknown` 分支；网络错误 / 500
 * 保持固定间隔重试，不引入通用退避状态机。
 */
export const WORKER_UNKNOWN_BACKOFF_MS: readonly number[] = [1_000, 2_000, 5_000, 15_000]

/** R-01：同一原因连续失败时，每多少次才补一条 warn（首次必打）。 */
export const WORKER_UNKNOWN_LOG_EVERY = 10

/** S-01：内存里最多记住多少条已投递派单（`task.id:lease_id`）。 */
export const MAX_REMEMBERED_DELIVERIES = 200

/** 插件投递消息时写入 `source.plugin` 的标识。 */
export const WORKER_PLUGIN_SOURCE = 'echuan'

/** 只声明循环用到的定时器能力。 */
export interface WorkerTimerApi {
  setTimeout(handler: () => void, ms: number): unknown
  clearTimeout(handle: unknown): void
}

/** 一条 `kind=run_task` 的派单指令（与 claim 200 的结构同构）。 */
export interface RunTaskCommand {
  kind: 'run_task'
  task: ClaimedTask
  lease: ClaimLease
  context: ClaimContext
}

/** 心跳回包里的指令；当前只有 `run_task`，其余 kind 原样忽略。 */
export type WorkerCommand = ClientWorkerCommand

/** 心跳状态：`busy` 只表示意图，平台据此决定是否派新卡。 */
export type WorkerState = 'idle' | 'busy'

/** 投递出口：默认用 `createUserMessage` + `agent.followup`，单测可替换。 */
export type WorkerDeliver = (agent: Agent, text: string) => void

export interface WorkerLoopOptions {
  /** 已归一化的配置（读取 `worker.metadata` / `worker.poll.enable`）。 */
  config: ResolvedConfig
  /** worker 心跳客户端（`createWorkerClient` 的返回值）。 */
  client: WorkerClient
  /** 读取当前根 agent 列表；生产环境传 `() => ctx.agents.roots()`。 */
  agents: () => readonly Agent[]
  /** 日志出口。 */
  logger: Logger
  /** 定时器实现，默认 `globalThis`；单测注入假时钟。 */
  timer?: WorkerTimerApi
  /** 投递实现，默认 `createUserMessage` + `agent.followup`。 */
  deliver?: WorkerDeliver
  /** 回包间隔缺失/非法时的兜底值，默认 {@link DEFAULT_HEARTBEAT_INTERVAL_MS}。 */
  defaultIntervalMs?: number
}

export interface WorkerLoop {
  /** 是否已启动（`poll.enable=false` 时为 `false`）。 */
  readonly started: boolean
  /** 是否已卸载。 */
  readonly disposed: boolean
  /** 当前持有的 worker_id（仅内存；测试断言用，不要把值写日志）。 */
  readonly workerId: string | undefined
  /** 当前使用的轮询间隔（毫秒）。 */
  readonly intervalMs: number
  /** R-02：`worker.metadata` 是否超过平台上限（超限时永不启动循环）。 */
  readonly metadataOverLimit: boolean
  /**
   * 启动：{@link FIRST_HEARTBEAT_DELAY_MS} 后发出首轮心跳，之后按回包间隔用
   * setTimeout 链重排。
   */
  start(): void
  /** 幂等卸载：清定时器，之后不再产生请求与日志。 */
  dispose(reason?: string): void
}

const consoleLogger: Logger = {
  info: message => console.log(message),
  warn: message => console.warn(message),
  debug: message => console.debug(message),
}

const defaultTimer: WorkerTimerApi = {
  setTimeout: (handler, ms) => globalThis.setTimeout(handler, ms),
  clearTimeout: handle => globalThis.clearTimeout(handle as ReturnType<typeof setTimeout>),
}

/**
 * 创建 worker 心跳循环。
 *
 * 构造本身无副作用：不建定时器、不发请求；只有 `start()` 才进入循环。
 */
export function createWorkerLoop(options: WorkerLoopOptions): WorkerLoop {
  const { config, client } = options
  const logger = options.logger ?? consoleLogger
  const timer = options.timer ?? defaultTimer
  const deliver = options.deliver ?? defaultDeliver
  const fallbackIntervalMs =
    options.defaultIntervalMs !== undefined && Number.isFinite(options.defaultIntervalMs)
      && options.defaultIntervalMs > 0
      ? options.defaultIntervalMs
      : DEFAULT_HEARTBEAT_INTERVAL_MS

  let workerId: string | undefined
  let intervalMs = clampInterval(fallbackIntervalMs)
  let timerHandle: unknown
  let started = false
  let disposed = false
  /** R-01：连续 `worker_unknown` 次数；成功注册后归零。 */
  let workerUnknownStreak = 0
  /** R-03：间隔被夹紧的提示只打一次，不每轮刷。 */
  let intervalClampNoticed = false
  /** S-01：最近投递过的 `task.id:lease_id`（内存 LRU，不落盘）。 */
  const deliveredKeys = new Set<string>()
  const deliveredQueue: string[] = []
  /** R-02：metadata 序列化后的字节数；不可序列化时为 `undefined`。 */
  const metadataBytes = readMetadataBytes()
  const metadataOverLimit =
    metadataBytes !== undefined && metadataBytes > WORKER_METADATA_MAX_BYTES

  return {
    get started() {
      return started
    },
    get disposed() {
      return disposed
    },
    get workerId() {
      return workerId
    },
    get intervalMs() {
      return intervalMs
    },
    get metadataOverLimit() {
      return metadataOverLimit
    },

    start() {
      if (disposed || started) return

      if (!config.worker.poll.enable) {
        // 默认关闭：连第一个定时器都不创建，也不发任何请求。
        logger.info('[dsh-echuan] worker.poll.enable=false，未创建心跳定时器')
        return
      }

      if (metadataOverLimit) {
        // R-02：超限时每轮都会被平台拒（413），启动循环只会永久刷屏；
        // 这里直接不启动：零请求、零定时器。
        logger.warn(
          `[dsh-echuan] worker.metadata 序列化后 ${String(metadataBytes)}B，`
            + `超过平台上限 ${WORKER_METADATA_MAX_BYTES}B，不启动心跳循环；`
            + '请缩小 metadata，或保持 worker.poll.enable=false',
        )
        return
      }

      started = true
      logger.info(
        `[dsh-echuan] worker 心跳已启动（首轮心跳将在 ${FIRST_HEARTBEAT_DELAY_MS}ms 后发出，`
          + `兜底间隔 ${intervalMs}ms）`,
      )
      // 首轮也走定时器：延迟 FIRST_HEARTBEAT_DELAY_MS 再发，避免 --help 这类
      // 短命令产生真实请求；dispose() 在这之前调用会连首轮一起清掉。
      schedule(FIRST_HEARTBEAT_DELAY_MS)
    },

    dispose(reason = 'plugin dispose') {
      if (disposed) return
      disposed = true
      started = false
      clearTimer()
      logger.info(
        `[dsh-echuan] worker 心跳已停止（${reason}）：定时器已清除，不再产生请求与日志`,
      )
    },
  }

  // -------------------------------------------------------------------------
  // 内部实现
  // -------------------------------------------------------------------------

  /** 一轮心跳：读 agent 状态 → 发心跳 → 处理派单 → 排下一轮。 */
  async function runRound(): Promise<void> {
    if (disposed || !started) return

    const roots = readRoots()
    const state: WorkerState =
      roots.length === 1 && roots[0]?.status === 'running' ? 'busy' : 'idle'

    const sentWorkerId = workerId
    const input = {
      state,
      metadata: config.worker.metadata,
      ...(workerId === undefined ? {} : { worker_id: workerId }),
    }

    let nextDelayMs = intervalMs

    try {
      const result = await client.heartbeat(input)
      if (disposed) return

      if (result.kind !== 'ok') {
        // 心跳端点约定恒为 200；出现 204 视为异常回包，不清 id。
        logger.warn(
          '[dsh-echuan] 心跳回包为空（HTTP 204），保留当前 worker id，按当前间隔继续',
        )
      } else {
        if (workerUnknownStreak > 0) {
          // 注册重新成功：连续失败计数归零，下一次 409 又能享受一次「立即重试」。
          workerUnknownStreak = 0
        }
        nextDelayMs = applyHeartbeat(result.data)
        const commands = Array.isArray(result.data.commands) ? result.data.commands : []
        deliverCommands(roots, commands)
      }
    } catch (err) {
      if (disposed) return

      if (isWorkerUnknown(err)) {
        // 平台已经不认识这个 id（行被保留期回收、平台库被清空等）：清空后重排重新注册；
        // 下一轮 body 里完全不会出现 worker_id 字段。
        // R-01：「立即」重试只允许一次；从第 2 次起必须退避，否则持续 409 会退化成
        // 0ms 忙等风暴（实测 593 请求/秒 + 593 条 warn/秒）。
        if (sentWorkerId !== undefined) workerId = undefined
        workerUnknownStreak += 1
        const immediate = workerUnknownStreak === 1
        nextDelayMs = immediate ? 0 : workerUnknownBackoffMs(workerUnknownStreak)
        if (immediate) {
          logger.warn(
            '[dsh-echuan] 心跳返回 worker_unknown，已清空本地 worker id，立即重新注册',
          )
        } else if (workerUnknownStreak % WORKER_UNKNOWN_LOG_EVERY === 0) {
          logger.warn(
            `[dsh-echuan] 心跳返回 worker_unknown 已连续 ${workerUnknownStreak} 次，`
              + `按退避策略 ${nextDelayMs}ms 后重试注册（同一原因每 `
              + `${WORKER_UNKNOWN_LOG_EVERY} 次汇总一条，避免刷屏）`,
          )
        }
      } else {
        // 网络失败 / 500 / 超时：不崩、不清 id，按当前间隔继续，不做退避状态机。
        logger.warn(
          `[dsh-echuan] 心跳失败（${describeError(err)}），保留当前 worker id，`
            + `${intervalMs}ms 后继续`,
        )
      }
    }

    if (disposed || !started) return
    schedule(nextDelayMs)
  }

  /** 应用成功回包：更新 id 与间隔，返回下一轮延迟。 */
  function applyHeartbeat(payload: WorkerHeartbeatPayload): number {
    const snapshot = payload?.worker
    const nextId = snapshot?.worker_id
    if (typeof nextId === 'string' && nextId.startsWith('w_')) {
      if (workerId === undefined) {
        logger.info(`[dsh-echuan] worker 已注册（id=${maskWorkerId(nextId)}）`)
      } else if (workerId !== nextId) {
        logger.info(`[dsh-echuan] worker id 已更新（id=${maskWorkerId(nextId)}）`)
      }
      workerId = nextId
    } else {
      logger.warn(
        '[dsh-echuan] 心跳回包缺少合法的 w_ 前缀 worker_id，保留当前 id；'
          + '请检查平台契约',
      )
    }

    const fromServer = snapshot?.heartbeat_interval_ms
    if (typeof fromServer === 'number' && Number.isFinite(fromServer) && fromServer > 0) {
      // R-03：平台回 1 这类越界值会被照单全收 → 自身高频心跳，必须夹紧。
      const clamped = clampInterval(fromServer)
      if (clamped !== fromServer && !intervalClampNoticed) {
        intervalClampNoticed = true
        const notice = logger.debug ?? logger.info
        notice(
          `[dsh-echuan] 平台下发的 heartbeat_interval_ms=${fromServer} 超出允许范围 `
            + `[${MIN_HEARTBEAT_INTERVAL_MS}, ${MAX_HEARTBEAT_INTERVAL_MS}]，`
            + `已夹紧为 ${clamped}ms（仅提示一次）`,
        )
      }
      intervalMs = clamped
    } else {
      intervalMs = clampInterval(fallbackIntervalMs)
    }
    return intervalMs
  }

  /** 把本轮的 `run_task` 指令投给唯一的根 agent。 */
  function deliverCommands(roots: readonly Agent[], commands: readonly WorkerCommand[]): void {
    const runTasks = commands.filter(isRunTaskCommand)
    if (runTasks.length === 0) return

    if (roots.length !== 1) {
      logger.warn(
        `[dsh-echuan] 收到 ${runTasks.length} 条派单，但当前根 agent 数量为 ${roots.length}`
          + '（需恰好 1 个），本轮跳过投递；worker id 保持不变，不重试本轮',
      )
      return
    }

    const agent = roots[0]
    if (agent === undefined) return

    for (const command of runTasks) {
      const key = deliveryKey(command)
      if (deliveredKeys.has(key)) {
        // S-01：平台重复下发同一 (task.id, lease_id) 时只投一次，避免重复派单。
        logger.debug?.(
          `[dsh-echuan] 跳过重复派单 ${key}（已投递过，按 task.id+lease_id 去重）`,
        )
        continue
      }
      try {
        deliver(agent, renderTaskMessage(command))
        rememberDelivery(key)
        logger.info(
          `[dsh-echuan] 已投递任务 ${command.task.id} 给根 agent`
            + `${agent.id === undefined ? '' : ` ${agent.id}`}`,
        )
      } catch (err) {
        logger.warn(
          `[dsh-echuan] 任务 ${command.task.id} 投递失败（${describeError(err)}）；`
            + '本轮不再重试，等下一轮心跳',
        )
      }
    }
  }

  /** R-02：装载期只算一次 metadata 字节数；不可序列化时交给客户端逐轮报错。 */
  function readMetadataBytes(): number | undefined {
    try {
      return metadataByteLength(config.worker.metadata)
    } catch {
      return undefined
    }
  }

  /** S-01：把一次成功投递记进内存 LRU，超出容量淘汰最旧的一条。 */
  function rememberDelivery(key: string): void {
    if (deliveredKeys.has(key)) return
    deliveredKeys.add(key)
    deliveredQueue.push(key)
    while (deliveredQueue.length > MAX_REMEMBERED_DELIVERIES) {
      const evicted = deliveredQueue.shift()
      if (evicted !== undefined) deliveredKeys.delete(evicted)
    }
  }

  /** 读取根 agent；读不到时按 0 个处理，保证循环不因单轮异常中断。 */
  function readRoots(): Agent[] {
    try {
      return [...options.agents()]
    } catch (err) {
      logger.warn(
        `[dsh-echuan] 读取根 agent 失败（${describeError(err)}），本轮按 idle / 不投递处理`,
      )
      return []
    }
  }

  function schedule(ms: number): void {
    if (disposed || !started) return
    clearTimer()
    timerHandle = timer.setTimeout(() => {
      timerHandle = undefined
      void runRound()
    }, ms)
  }

  function clearTimer(): void {
    if (timerHandle === undefined) return
    timer.clearTimeout(timerHandle)
    timerHandle = undefined
  }
}

/**
 * 渲染派单消息。
 *
 * 必须包含：任务编号、标题、正文、租约到期、项目、产品、工作目录（有则带），
 * 以及结尾的行动指令「干完用 `echuan_task_report` 汇报」。
 */
export function renderTaskMessage(command: RunTaskCommand): string {
  const { task, lease, context } = command
  // 契约里的 context 只有 product，没有项目名；若平台后续补了 project 字段则顺手带上。
  const project = (context as ClaimContext & { project?: { id?: string; name?: string } }).project
  const projectLabel =
    project?.name !== undefined && project.name !== ''
      ? `${project.name}（${project.id ?? task.project_id}）`
      : task.project_id

  const lines: string[] = [
    '【驿传派单】收到新任务，请立即执行。',
    '',
    `任务编号：${task.id}`,
    `标题：${task.title}`,
    `项目：${projectLabel}`,
    `产品：${context.product.name}（${context.product.id}）`,
    `仓库：${context.product.git_url}`,
    `租约到期：${lease.until}`,
  ]
  if (context.workdir !== undefined && context.workdir !== '') {
    lines.push(`工作目录：${context.workdir}`)
  }
  lines.push(
    '',
    '任务正文：',
    task.body_md,
    '',
    '干完用 `echuan_task_report` 汇报。',
  )
  return lines.join('\n')
}

/** 默认投递：插件来源的用户消息，走 `agent.followup` 入队。 */
function defaultDeliver(agent: Agent, text: string): void {
  const message = createUserMessage({
    content: [{ type: 'text', text }],
    source: { kind: 'plugin', plugin: WORKER_PLUGIN_SOURCE },
  })
  agent.followup(message)
}

/** 日志里的 worker id 打码：只保留前 4 个字符。 */
export function maskWorkerId(id: string): string {
  if (id.length <= 4) return '****'
  return `${id.slice(0, 4)}****`
}

/** R-03：把间隔夹紧到 [MIN, MAX]；非数字 / 非正数由调用方先回落兜底值。 */
function clampInterval(ms: number): number {
  if (ms < MIN_HEARTBEAT_INTERVAL_MS) return MIN_HEARTBEAT_INTERVAL_MS
  if (ms > MAX_HEARTBEAT_INTERVAL_MS) return MAX_HEARTBEAT_INTERVAL_MS
  return ms
}

/** R-01：连续第 `streak`（≥2）次 `worker_unknown` 的退避延迟，末项封顶。 */
function workerUnknownBackoffMs(streak: number): number {
  const index = Math.min(streak - 2, WORKER_UNKNOWN_BACKOFF_MS.length - 1)
  return WORKER_UNKNOWN_BACKOFF_MS[Math.max(0, index)] ?? DEFAULT_HEARTBEAT_INTERVAL_MS
}

/** S-01：派单去重键 = `task.id:lease_id`（同一任务换租约算新派单）。 */
function deliveryKey(command: RunTaskCommand): string {
  return `${command.task.id}:${String(command.lease?.lease_id ?? '')}`
}

function isRunTaskCommand(command: WorkerCommand): command is RunTaskCommand {
  return command.kind === 'run_task'
    && (command as RunTaskCommand).task !== undefined
    && (command as RunTaskCommand).lease !== undefined
    && (command as RunTaskCommand).context !== undefined
}

function isWorkerUnknown(err: unknown): boolean {
  return err instanceof EchuanApiError && err.code === 'worker_unknown'
}

function describeError(err: unknown): string {
  if (err instanceof EchuanClientError) {
    return `code=${err.code}：${err.message}`
  }
  return err instanceof Error ? err.message : String(err)
}
