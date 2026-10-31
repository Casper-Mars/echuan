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
 * 5. 回包 `commands` 里的 `run_task` 按 `task.project_id` 走 `ensureSession` 取到
 *    项目会话的 agent 后投递（项目号即会话号，不再要求「根 agent 唯一」）；
 *    ensure 失败只 warn 一条、本轮不投递、不写 inflight、不重试本轮；S-01：按
 *    `task.id + lease_id` 在内存里记住最近投递过的派单（LRU，容量
 *    {@link MAX_REMEMBERED_DELIVERIES}），重复下发只投一次，不落盘；
 * 6. inflight 记录「已投递但未报告」的派单（`taskId` / `projectId` / `leaseUntil`）：
 *    投递成功写入；`notifyReported` / 租约过期 / 同任务新 lease 覆盖时清除；
 *    `state` 只看「手里有没有卡」，与 `agent.status` 无关：inflight 非空 → `busy`；
 *    inflight 为空但上一轮回包 `worker.current_task_id` 非空（手动 `echuan_task_claim`
 *    领的卡）→ 同样 `busy`；两者都没有才是 `idle`；
 * 7. 网络失败 / 500 / 超时只 warn，保留 id，按当前间隔继续；不做退避状态机；
 * 8. `poll.enable=false` 时零请求、零定时器；`dispose()` 后定时器立刻清空，
 *    之后不再产生任何请求与日志（包括还没发出的首轮心跳）；
 * 9. worker_id 只在内存，日志里一律打码；
 * 10. R-02：`worker.metadata` 序列化后超过 {@link WORKER_METADATA_MAX_BYTES} 时
 *    **不启动循环**（一条明确 warn 后返回），避免每轮 413 永久刷屏。
 *
 * 所有外部依赖（客户端 / 定时器 / 日志 / 会话访问 / ensure / 投递 / 时钟）都可注入：
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

/**
 * 单条派单 `ensureSession` 的超时（毫秒）。
 *
 * ensure 永不 resolve 时会把整轮心跳卡死：`runRound` 里 `await deliverCommands()`
 * 之后才 `schedule(next)`，后续心跳、state 与 inflight 刷新全部停摆。默认 10s，
 * 超时按「ensure 失败」处理（只 warn、不投递、不写 inflight、不记去重）。
 */
export const DEFAULT_ENSURE_TIMEOUT_MS = 10_000

/**
 * 插件标识：派单消息的 `source.kind` 由它派生（`plugin:<name>`）。
 *
 * dsh v4 取消了共享的 `plugin` kind，改为「生产者自有 kind」：第三方插件的
 * `source.kind` 形如 `plugin:<name>`，并且**不再带** `plugin` 字段。会话格式准入
 * 直接拒收 `kind === 'plugin'`（`dsh-session-format-v3-to-v4` 的
 * `src/message-sources.ts` → `assertV4MessageSources`），V3 → V4 迁移也按
 * `plugin:<name>` 重写（同包 `src/sources.ts` → `producerKind` / `rewritePluginSource`）。
 */
export const WORKER_PLUGIN_SOURCE = 'echuan'

/** 派单消息的 `source.kind`：`plugin:<插件标识>`，见 {@link WORKER_PLUGIN_SOURCE}。 */
export const WORKER_PLUGIN_SOURCE_KIND = `plugin:${WORKER_PLUGIN_SOURCE}` as const

/**
 * 把本插件的生产者 kind 合并进 dsh-llm 的 `MessageSourceMap`。
 *
 * `MessageSourceMap` 是 dsh-llm 公开的合并扩展点（`packages/llm/llm/src/message.ts`，
 * 已无共享 catch-all `plugin` kind）：声明之后 `createUserMessage` 的 `source` 仍走
 * 完整类型检查，无需把字面量断言成 `any`。
 */
declare module '@deepseek-ai/dsh-llm/message' {
  interface MessageSourceMap {
    /** 驿传（echuan）插件投递的派单消息。 */
    'plugin:echuan': { kind: 'plugin:echuan' }
  }
}

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

/**
 * 心跳状态：`busy` = 本 worker 手里有一张未报告的卡。
 *
 * 它是「占用」而不是「此刻 agent 是否在跑」：平台据此决定是否派新卡，所以只要
 * 持卡就必须报 `busy`，否则回合间隙的瞬时 idle 会让同一 worker 领到第二张卡。
 * 见 {@link WorkerLoopOptions.sessions} 与 `computeState()`。
 */
export type WorkerState = 'idle' | 'busy'

/**
 * 已投递、尚未报告的派单（内存态，不落盘）。
 *
 * `projectId` 同时是会话 id（项目号即会话号）；`state` 只按「非空即持卡」判定，
 * 诊断工具（`echuan_worker_info`）展示明细。
 */
export interface Inflight {
  taskId: string
  projectId: string
  leaseUntil: string
}

/** 按会话 id 读取活跃 agent（生产环境就是 `ctx.agents`）。 */
export interface WorkerSessionRegistry {
  get(id: string): Agent | undefined
}

/** 投递出口：默认用 `createUserMessage` + `agent.followup`，单测可替换。 */
export type WorkerDeliver = (agent: Agent, text: string) => void

export interface WorkerLoopOptions {
  /** 已归一化的配置（读取 `worker.metadata` / `worker.poll.enable`）。 */
  config: ResolvedConfig
  /** worker 心跳客户端（`createWorkerClient` 的返回值）。 */
  client: WorkerClient
  /**
   * 按会话 id 取 agent；生产环境传 `ctx.agents`。
   *
   * 仅供诊断工具使用（`agent.status` 这类瞬时值不参与 `state` 判定）。
   */
  sessions: WorkerSessionRegistry
  /**
   * 确保项目会话就绪（项目号即会话号）；生产环境用任务-03 的
   * `createSessionEnsurer`。返回 `undefined` 表示放弃本轮投递（只 warn）。
   */
  ensureSession: (projectId: string) => Promise<{ agent: Agent } | undefined>
  /** 日志出口。 */
  logger: Logger
  /** 定时器实现，默认 `globalThis`；单测注入假时钟。 */
  timer?: WorkerTimerApi
  /** 投递实现，默认 `createUserMessage` + `agent.followup`。 */
  deliver?: WorkerDeliver
  /** 回包间隔缺失/非法时的兜底值，默认 {@link DEFAULT_HEARTBEAT_INTERVAL_MS}。 */
  defaultIntervalMs?: number
  /** 当前时间（毫秒），用于租约过期判定；默认 `Date.now`。 */
  now?: () => number
  /**
   * 单条派单 `ensureSession` 的超时（毫秒），默认
   * {@link DEFAULT_ENSURE_TIMEOUT_MS}；非法值回退默认。
   */
  ensureTimeoutMs?: number
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
  /** 已投递、尚未报告的派单快照（只读，诊断工具用）。 */
  readonly inflight: readonly Inflight[]
  /**
   * 启动：{@link FIRST_HEARTBEAT_DELAY_MS} 后发出首轮心跳，之后按回包间隔用
   * setTimeout 链重排。
   */
  start(): void
  /** 幂等卸载：清定时器，之后不再产生请求与日志。 */
  dispose(reason?: string): void
  /** 报告成功回调：清除该任务的 inflight（不存在的 taskId 静默忽略）。 */
  notifyReported(taskId: string): void
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
  const now = options.now ?? Date.now
  const fallbackIntervalMs =
    options.defaultIntervalMs !== undefined && Number.isFinite(options.defaultIntervalMs)
      && options.defaultIntervalMs > 0
      ? options.defaultIntervalMs
      : DEFAULT_HEARTBEAT_INTERVAL_MS
  const ensureTimeoutMs =
    options.ensureTimeoutMs !== undefined && Number.isFinite(options.ensureTimeoutMs)
      && options.ensureTimeoutMs > 0
      ? options.ensureTimeoutMs
      : DEFAULT_ENSURE_TIMEOUT_MS

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
  /** 已投递未报告的派单（按 taskId 唯一）。 */
  const inflight: Inflight[] = []
  /**
   * 上一轮心跳回包里的 `worker.current_task_id`（`null` = 平台认为本 worker 空闲）。
   *
   * 手动 `echuan_task_claim` 领的卡不会进本地 inflight，只能靠这个快照上报 busy。
   */
  let serverHeldTaskId: string | null = null
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
    get inflight() {
      return inflight.map(item => ({ ...item }))
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
      inflight.length = 0
      serverHeldTaskId = null
      logger.info(
        `[dsh-echuan] worker 心跳已停止（${reason}）：定时器已清除，不再产生请求与日志`,
      )
    },

    notifyReported(taskId: string) {
      // 手动领的卡本地没有 inflight，但回包快照可能记着它：一起清掉，
      // 下一轮就能如实上报 idle，不用多等一轮。
      if (serverHeldTaskId === taskId) serverHeldTaskId = null
      const index = inflight.findIndex(item => item.taskId === taskId)
      if (index < 0) return
      inflight.splice(index, 1)
      logger.info(`[dsh-echuan] 任务 ${taskId} 报告已提交，已从 inflight 清除`)
    },
  }

  // -------------------------------------------------------------------------
  // 内部实现
  // -------------------------------------------------------------------------

  /** 一轮心跳：清过期 inflight → 算 state → 发心跳 → 处理派单 → 排下一轮。 */
  async function runRound(): Promise<void> {
    if (disposed || !started) return

    // 先清过期再算 state：保证上报的 state 与 inflight 快照一致。
    expireInflight()
    const state = computeState()

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
        await deliverCommands(commands)
        if (disposed) return
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

    // 快照里的 current_task_id 是「平台认为本 worker 持有的卡」：手动 claim 的卡
    // 本地没有 inflight，只靠它才能在下一轮如实上报 busy。
    serverHeldTaskId = readHeldTaskId(snapshot)

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

  /**
   * 把本轮的 `run_task` 指令投给对应项目会话的 agent。
   *
   * 每条派单：去重（`task.id:lease_id`）→ `ensureSession(task.project_id)` →
   * `deliver`；ensure 返回空或抛错只 warn 一条，不投递、不写 inflight、不重试本轮。
   */
  async function deliverCommands(commands: readonly WorkerCommand[]): Promise<void> {
    const runTasks = commands.filter(isRunTaskCommand)
    if (runTasks.length === 0) return

    for (const command of runTasks) {
      const key = deliveryKey(command)
      if (deliveredKeys.has(key)) {
        // S-01：平台重复下发同一 (task.id, lease_id) 时只投一次，避免重复派单。
        logger.debug?.(
          `[dsh-echuan] 跳过重复派单 ${key}（已投递过，按 task.id+lease_id 去重）`,
        )
        continue
      }

      const task = command.task
      const projectId = task.project_id
      let ensured: { agent: Agent } | undefined
      const outcome = await settleWithTimeout(
        options.ensureSession(projectId),
        ensureTimeoutMs,
        timer,
      )
      if (outcome.status === 'timeout') {
        // ensure 挂起不能拖死整轮心跳：按 ensure 失败处理，继续下一条。
        logger.warn(
          `[dsh-echuan] 任务 ${task.id} 的项目会话 ${projectId} ensure 超时`
            + `（${ensureTimeoutMs}ms），本轮跳过投递；worker id 保持不变`,
        )
        continue
      }
      if (outcome.status === 'error') {
        logger.warn(
          `[dsh-echuan] 任务 ${task.id} 的项目会话 ${projectId} ensure 失败`
            + `（${describeError(outcome.error)}），本轮跳过投递；worker id 保持不变`,
        )
        continue
      }
      ensured = outcome.value
      if (disposed) return
      if (ensured === undefined || ensured === null || ensured.agent === undefined
        || ensured.agent === null) {
        logger.warn(
          `[dsh-echuan] 任务 ${task.id} 的项目会话 ${projectId} 未能就绪`
            + '（ensureSession 返回空），本轮跳过投递；worker id 保持不变',
        )
        continue
      }

      const agent = ensured.agent
      try {
        deliver(agent, renderTaskMessage(command))
        rememberDelivery(key)
        setInflight(command)
        logger.info(
          `[dsh-echuan] 已投递任务 ${task.id} 给会话 ${projectId} 的 agent`
            + `${agent.id === undefined ? '' : ` ${agent.id}`}`,
        )
      } catch (err) {
        logger.warn(
          `[dsh-echuan] 任务 ${task.id} 投递失败（${describeError(err)}）；`
            + '本轮不再重试，等下一轮心跳',
        )
      }
    }
  }

  /** 投递成功：同一 taskId 的新 lease 覆盖旧记录，其余任务保留。 */
  function setInflight(command: RunTaskCommand): void {
    const entry: Inflight = {
      taskId: command.task.id,
      projectId: command.task.project_id,
      leaseUntil: command.lease.until,
    }
    const index = inflight.findIndex(item => item.taskId === entry.taskId)
    if (index >= 0) {
      inflight[index] = entry
    } else {
      inflight.push(entry)
    }
  }

  /**
   * 清除租约已过期的 inflight（发送心跳前执行，保证 `state` 与 `inflight` 一致）。
   *
   * `leaseUntil` 无法解析时不做清理（不把契约异常当成过期，避免误清）。
   */
  function expireInflight(): void {
    if (inflight.length === 0) return
    let current: number
    try {
      current = now()
    } catch {
      return
    }
    for (let index = inflight.length - 1; index >= 0; index -= 1) {
      const item = inflight[index]
      if (item === undefined) continue
      const until = Date.parse(item.leaseUntil)
      if (Number.isNaN(until) || until > current) continue
      inflight.splice(index, 1)
      logger.info(
        `[dsh-echuan] 任务 ${item.taskId} 的租约已过期（${item.leaseUntil}），`
          + '已从 inflight 清除',
      )
    }
  }

  /**
   * `state` 只看「手里有没有卡」，与 `agent.status` 无关。
   *
   * `agent.status` 是瞬时值：回合间隙、等工具、刚投递完都会是 `idle`。用它判 busy
   * 会让持卡中的 worker 在下一轮报 idle，平台据此再派一张卡（P0：同一 worker 同时
   * 持有两张卡）。两个来源任一成立即 busy：
   *
   * 1. 本地 inflight 非空（心跳派单的常规路径）；
   * 2. 上一轮回包 `worker.current_task_id` 非空 —— 覆盖手动 `echuan_task_claim`
   *    领卡等本地没有 inflight 记录的持卡情况。
   */
  function computeState(): WorkerState {
    if (inflight.length > 0) return 'busy'
    if (serverHeldTaskId !== null) return 'busy'
    return 'idle'
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
 * 只放任务事实：任务编号、标题、正文、租约到期、项目、产品、仓库。
 * 不放操作指示——「怎么开工、怎么汇报」由 `echuan-task-execution` 技能负责。
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
    '【驿传派单】',
    '',
    `任务编号：${task.id}`,
    `标题：${task.title}`,
    `项目：${projectLabel}`,
    `产品：${context.product.name}（${context.product.id}）`,
    `仓库：${context.product.git_url}`,
    `租约到期：${lease.until}`,
  ]
  lines.push(
    '',
    '任务正文：',
    task.body_md,
  )
  return lines.join('\n')
}

/** 默认投递：插件来源的用户消息，走 `agent.followup` 入队。 */
function defaultDeliver(agent: Agent, text: string): void {
  const message = createUserMessage({
    content: [{ type: 'text', text }],
    // dsh v4：producer-owned kind（`plugin:<name>`），不再带 V3 的 `plugin` 字段。
    source: { kind: WORKER_PLUGIN_SOURCE_KIND },
  })
  agent.followup(message)
}

/** 回包快照里的 `current_task_id` → `string | null`（空串同样视为未持卡）。 */
function readHeldTaskId(snapshot: WorkerHeartbeatPayload['worker'] | undefined): string | null {
  const value = snapshot?.current_task_id
  return typeof value === 'string' && value !== '' ? value : null
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

type SettledWithTimeout<T> =
  | { status: 'ok'; value: T }
  | { status: 'error'; error: unknown }
  | { status: 'timeout' }

/**
 * 给一段 Promise 套超时：超时后立即 settle 为 `timeout`，并清掉定时器。
 *
 * 超时后底层 Promise 仍可能 later resolve/reject：`then` 的两个回调在创建时就
 * 已挂上，late reject 会被吞掉（不产生 unhandledRejection），也不会再触发投递。
 */
function settleWithTimeout<T>(
  promise: Promise<T>,
  ms: number,
  timer: WorkerTimerApi,
): Promise<SettledWithTimeout<T>> {
  return new Promise<SettledWithTimeout<T>>(resolve => {
    let settled = false
    let handle: unknown
    const finish = (result: SettledWithTimeout<T>): void => {
      if (settled) return
      settled = true
      timer.clearTimeout(handle)
      handle = undefined
      resolve(result)
    }
    handle = timer.setTimeout(() => finish({ status: 'timeout' }), ms)
    promise.then(
      value => finish({ status: 'ok', value }),
      error => finish({ status: 'error', error: error as unknown }),
    )
  })
}
