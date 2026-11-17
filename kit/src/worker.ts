/**
 * Worker 心跳循环：把「定时心跳 + 派单投递」收敛成**唯一**一套定时器。
 *
 * 行为契约（v2 派单协议）：
 * 1. `POST /v1/workers/heartbeat`，body `{ worker_id?, state, kinds, metadata }`；
 *    `kinds` 来自配置 `worker.kinds`，每次心跳覆盖平台上的声明；
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
 * 5. 回包 `commands` 里的 `dispatch` 指令先按 `project_id` ensureSession：会话 id 即
 *    项目号，cwd 取会话数最多的 workspace；拿到该会话的 agent 再投递。会话未就绪时
 *    warn 一条、本轮跳过、下一轮重试（不写 inflight）。S-01：按
 *    `work_id + lease_id` 在内存里记住最近投递过的派单（LRU，容量
 *    {@link MAX_REMEMBERED_DELIVERIES}），重复下发只投一次，不落盘；
 * 6. inflight 记录「已投递但未报告」的派单（`workId` / `kind` / `leaseUntil`）：
 *    投递成功写入；`notifyReported` / 租约过期 / 同一 work_id 新 lease 覆盖时清除；
 *    `state` 只看「手里有没有活」，与 `agent.status` 无关：inflight 非空 → `busy`；
 *    inflight 为空但上一轮回包 `worker.current_work_item_id` 非空（手动
 *    `echuan_work_claim` 领的卡）→ 同样 `busy`；两者都没有才是 `idle`；
 * 7. 网络失败 / 500 / 超时只 warn，保留 id，按当前间隔继续；不做退避状态机；
 * 8. `poll.enable=false` 时零请求、零定时器；`dispose()` 后定时器立刻清空，
 *    之后不再产生任何请求与日志（包括还没发出的首轮心跳）；
 * 9. worker_id 只在内存，日志里一律打码；
 * 10. R-02：`worker.metadata` 序列化后超过 {@link WORKER_METADATA_MAX_BYTES} 时
 *    **不启动循环**（一条明确 warn 后返回），避免每轮 413 永久刷屏；
 * 11. `worker.kinds` 为空时同样**不启动循环**（平台心跳要求 kinds 非空，发出去只会
 *    每轮 400）。
 *
 * 所有外部依赖（客户端 / 定时器 / 日志 / agent 注册表 / 投递 / 时钟）都可注入：
 * 单测用假平台与假时钟，不连真实网络、不做真实 sleep。
 */
import type { Agent } from '@deepseek-ai/dsh-agent'
import { createUserMessage } from '@deepseek-ai/dsh-llm/message'
import type {
  Dispatch,
  WorkerClient,
  WorkerCommand as ClientWorkerCommand,
  WorkerHeartbeatPayload,
} from './client.js'
import type { ResolvedConfig, WorkerKind } from './config.js'
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

/** S-01：内存里最多记住多少条已投递派单（`work_id:lease_id`）。 */
export const MAX_REMEMBERED_DELIVERIES = 200

/** 单条派单 `ensureSession` 的超时（毫秒）；超时按「本轮未就绪」处理。 */
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

/** 一条 `kind=dispatch` 的派单指令（与 claim 200 的结构同构）。 */
export interface DispatchCommand {
  kind: 'dispatch'
  dispatch: Dispatch
}

/** 心跳回包里的指令；当前只有 `dispatch`，其余 kind 原样忽略。 */
export type WorkerCommand = ClientWorkerCommand

/**
 * 心跳状态：`busy` = 本 worker 手里有一张未报告的卡。
 *
 * 它是「占用」而不是「此刻 agent 是否在跑」：平台据此决定是否派新卡，所以只要
 * 持卡就必须报 `busy`，否则回合间隙的瞬时 idle 会让同一 worker 领到第二张卡。
 */
export type WorkerState = 'idle' | 'busy'

/**
 * 已投递、尚未报告的派单（内存态，不落盘）。
 *
 * `workId` 是平台业务编号（`w_` 前缀）；`state` 只按「非空即持卡」判定，
 * 诊断工具（`echuan_worker_info`）展示明细。
 */
export interface Inflight {
  workId: string
  kind: string
  leaseUntil: string
}

/**
 * 本进程的 agent 注册表（生产环境就是 `ctx.agents`）。
 *
 * 循环只读 `roots()` 并取第一项：一个 worker 只有一个 agent，平台不感知会话与角色。
 */
export interface WorkerAgentRegistry {
  roots(): Agent[]
}

/** 投递出口：默认用 `createUserMessage` + `agent.followup`，单测可替换。 */
export type WorkerDeliver = (agent: Agent, text: string) => void

/**
 * 项目会话 ensure：输入项目号，返回该会话的 agent；无法就绪时返回 `undefined`。
 *
 * 生产环境接 `createSessionEnsurer(ctx)`（sessionId = 项目号，cwd 取会话数最多
 * 的 workspace，agentOptions 取 agentDefaultModel）。
 */
export type WorkerEnsureSession = (projectId: string, presetId?: string) => Promise<Agent | undefined>

export interface WorkerLoopOptions {
  /** 已归一化的配置（读取 `worker.kinds` / `worker.metadata` / `worker.poll.enable`）。 */
  config: ResolvedConfig
  /** worker 心跳客户端（`createWorkerClient` 的返回值）。 */
  client: WorkerClient
  /** 本进程 agent 注册表；仅在未提供 `ensureSession` 时作为兼容回退。 */
  agents: WorkerAgentRegistry
  /** 项目会话 ensure；生产环境必须提供。缺省时回退到 `agents.roots()[0]`。 */
  ensureSession?: WorkerEnsureSession
  /** 单条 `ensureSession` 的超时（毫秒），默认 {@link DEFAULT_ENSURE_TIMEOUT_MS}。 */
  ensureTimeoutMs?: number
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
}

export interface WorkerLoop {
  /** 是否已启动（`poll.enable=false` / kinds 为空 / metadata 超限时为 `false`）。 */
  readonly started: boolean
  /** 是否已卸载。 */
  readonly disposed: boolean
  /** 当前持有的 worker_id（仅内存；测试断言用，不要把值写日志）。 */
  readonly workerId: string | undefined
  /** 当前声明的 kinds（来自配置，与心跳 body 一致）。 */
  readonly kinds: readonly WorkerKind[]
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
  /** 报告成功回调：清除该 work_id 的 inflight（不存在的 id 静默忽略）。 */
  notifyReported(workId: string): void
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
  const { config } = options
  const { client } = options
  const logger = options.logger ?? consoleLogger
  const timer = options.timer ?? defaultTimer
  const deliver = options.deliver ?? defaultDeliver
  const ensureSession = options.ensureSession
  const ensureTimeoutMs = options.ensureTimeoutMs !== undefined
    && Number.isFinite(options.ensureTimeoutMs) && options.ensureTimeoutMs > 0
    ? options.ensureTimeoutMs
    : DEFAULT_ENSURE_TIMEOUT_MS
  const now = options.now ?? Date.now
  const kinds: readonly WorkerKind[] = config.worker.kinds
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
  /** S-01：最近投递过的 `work_id:lease_id`（内存 LRU，不落盘）。 */
  const deliveredKeys = new Set<string>()
  const deliveredQueue: string[] = []
  /** 已投递未报告的派单（按 workId 唯一）。 */
  const inflight: Inflight[] = []
  /**
   * 上一轮心跳回包里的 `worker.current_work_item_id`（`null` = 平台认为本 worker 空闲）。
   *
   * 手动 `echuan_work_claim` 领的卡不会进本地 inflight，只能靠这个快照上报 busy。
   */
  let serverHeldWorkId: string | null = null
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
    get kinds() {
      return kinds
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

      if (kinds.length === 0) {
        // 平台心跳要求 kinds 非空：空声明发出去只会每轮 400，启动循环毫无意义。
        logger.warn(
          '[dsh-echuan] worker.kinds 为空，不启动心跳循环；'
            + '请在配置里声明至少一个 kind（requirement / design / breakdown / dev），'
            + '或保持 worker.poll.enable=false',
        )
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
        `[dsh-echuan] worker 心跳已启动（kinds=${kinds.join(',')}；`
          + `首轮心跳将在 ${FIRST_HEARTBEAT_DELAY_MS}ms 后发出，兜底间隔 ${intervalMs}ms）`,
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
      serverHeldWorkId = null
      logger.info(
        `[dsh-echuan] worker 心跳已停止（${reason}）：定时器已清除，不再产生请求与日志`,
      )
    },

    notifyReported(workId: string) {
      // 手动领的卡本地没有 inflight，但回包快照可能记着它：一起清掉，
      // 下一轮就能如实上报 idle，不用多等一轮。
      if (serverHeldWorkId === workId) serverHeldWorkId = null
      const index = inflight.findIndex(item => item.workId === workId)
      if (index < 0) return
      inflight.splice(index, 1)
      logger.info(`[dsh-echuan] 工作项 ${workId} 报告已提交，已从 inflight 清除`)
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
      kinds,
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

    // 快照里的 current_work_item_id 是「平台认为本 worker 持有的卡」：手动 claim 的卡
    // 本地没有 inflight，只靠它才能在下一轮如实上报 busy。
    serverHeldWorkId = readHeldWorkItemId(snapshot)

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
   * 把本轮的 `dispatch` 指令投给本 worker 唯一的 agent。
   *
   * 目标固定为 `agents.roots()[0]`：只取 `roots()[0]`、不因存在多个 root 而丢弃，
   * 也不按项目号建会话。没有可用 agent 时只 warn 一条并跳过本轮（下一轮重试）。
   * 每条派单按 `work_id + lease_id` 去重，投递成功才写 inflight。
   */
  async function deliverCommands(commands: readonly WorkerCommand[]): Promise<void> {
    const dispatches = commands.filter(isDispatchCommand)
    if (dispatches.length === 0) return

    for (const command of dispatches) {
      const key = deliveryKey(command)
      if (deliveredKeys.has(key)) {
        // S-01：平台重复下发同一 (work_id, lease_id) 时只投一次，避免重复派单。
        logger.debug?.(
          `[dsh-echuan] 跳过重复派单 ${key}（已投递过，按 work_id+lease_id 去重）`,
        )
        continue
      }

      const dispatch = command.dispatch
      const projectId = dispatch.work_item.project_id
      let agent: Agent | undefined

      if (ensureSession !== undefined) {
        const outcome = await settleWithTimeout(
          ensureSession(projectId, dispatch.agent_preset),
          ensureTimeoutMs,
          timer,
        )
        if (outcome.status === 'timeout') {
          logger.warn(
            `[dsh-echuan] 工作项 ${dispatch.work_id} 的项目会话 ${projectId} ensure 超时`
              + `（${ensureTimeoutMs}ms），本轮跳过投递；下一轮心跳重试`,
          )
          continue
        }
        if (outcome.status === 'error') {
          logger.warn(
            `[dsh-echuan] 工作项 ${dispatch.work_id} 的项目会话 ${projectId} ensure 失败`
              + `（${describeError(outcome.error)}），本轮跳过投递；下一轮心跳重试`,
          )
          continue
        }
        agent = outcome.value
        if (agent === undefined || agent === null) {
          logger.warn(
            `[dsh-echuan] 工作项 ${dispatch.work_id} 的项目会话 ${projectId} 未能就绪`
              + '（ensureSession 返回空），本轮跳过投递；下一轮心跳重试',
          )
          continue
        }
      } else {
        // 兼容回退：未接项目会话时仍投本进程第一个 root agent。
        agent = pickAgent()
        if (agent === undefined) {
          logger.warn(
            `[dsh-echuan] 本进程没有可用 agent（agents.roots() 为空），`
              + `跳过本轮 ${dispatches.length} 条派单；下一轮心跳重试`,
          )
          return
        }
      }

      if (disposed) return
      try {
        deliver(agent, renderDispatchMessage(dispatch))
        rememberDelivery(key)
        setInflight(dispatch)
        logger.info(
          `[dsh-echuan] 已投递工作项 ${dispatch.work_id}（kind=${dispatch.kind}）`
            + `给项目 ${projectId} 的 agent${agent.id === undefined ? '' : ` ${agent.id}`}`,
        )
      } catch (err) {
        logger.warn(
          `[dsh-echuan] 工作项 ${dispatch.work_id} 投递失败（${describeError(err)}）；`
            + '本轮不再重试，等下一轮心跳',
        )
      }
    }
  }

  /** 取第一个可用 agent：只看 `roots()`，不判断数量。 */
  function pickAgent(): Agent | undefined {
    let roots: readonly (Agent | undefined)[]
    try {
      roots = options.agents.roots()
    } catch (err) {
      logger.warn(`[dsh-echuan] 读取 agents.roots() 失败（${describeError(err)}），本轮跳过派单`)
      return undefined
    }
    if (!Array.isArray(roots)) return undefined
    for (const agent of roots) {
      // 注册表理论上只放 live agent；这里只挡 undefined/null，不改变「取第一个」的语义。
      if (agent !== undefined && agent !== null) return agent
    }
    return undefined
  }

  /** 投递成功：同一 workId 的新 lease 覆盖旧记录，其余工作项保留。 */
  function setInflight(dispatch: Dispatch): void {
    const entry: Inflight = {
      workId: dispatch.work_id,
      kind: dispatch.kind,
      leaseUntil: dispatch.lease.until,
    }
    const index = inflight.findIndex(item => item.workId === entry.workId)
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
        `[dsh-echuan] 工作项 ${item.workId} 的租约已过期（${item.leaseUntil}），`
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
   * 2. 上一轮回包 `worker.current_work_item_id` 非空 —— 覆盖手动
   *    `echuan_work_claim` 领卡等本地没有 inflight 记录的持卡情况。
   */
  function computeState(): WorkerState {
    if (inflight.length > 0) return 'busy'
    if (serverHeldWorkId !== null) return 'busy'
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

/** kind 的中文名（只用于派单消息，不参与任何判断）。 */
const KIND_LABELS: Record<string, string> = {
  requirement: '需求',
  design: '方案',
  breakdown: '拆解',
  dev: '开发',
}

/**
 * 渲染派单消息（v2 dispatch 协议）。
 *
 * 只放事实：工作项编号、类别、标题、项目、产品、仓库、租约、输入物料、正文。
 * 不放操作指示——「怎么开工、怎么汇报」由 `echuan-task-execution` 技能负责。
 *
 * 唯一的结构性例外是返工块：平台把「这是驳回后重派的」与驳回原因作为字段
 * 下发（`rework` / `reason`），这里原样前置渲染。否则 agent 只能看到
 * 「尝试次数：2」这种数字，既不知道是被驳回，也不知道要改什么。
 */
export function renderDispatchMessage(dispatch: Dispatch): string {
  const { work_item: item, lease, context } = dispatch
  const kindLabel = KIND_LABELS[dispatch.kind] ?? dispatch.kind
  const lines: string[] = [
    '【驿传派单】',
    '',
    `工作项编号：${dispatch.work_id}`,
    `类别：${dispatch.kind}（${kindLabel}）`,
    `标题：${item.title}`,
    `项目：${item.project_id}`,
    `产品：${context.product.name}（${context.product.id}）`,
    `仓库：${context.product.git_url}`,
    ...(typeof context.dev_branch === 'string' && context.dev_branch.trim() !== ''
      ? [`开发分支：${context.dev_branch.trim()}`]
      : []),
    `租约到期：${lease.until}（TTL ${lease.ttl_seconds}s，lease_id=${lease.lease_id}）`,
    `尝试次数：${item.attempt}${item.max_attempts === undefined ? '' : `/${item.max_attempts}`}，优先级：${item.priority}`,
  ]

  const reason = typeof item.reason === 'string' ? item.reason.trim() : ''
  if (item.rework === true || reason !== '') {
    lines.push(
      '',
      '【返工】本卡是上一版结果被驳回后重新派单的，不是首次执行。',
      `驳回原因：${reason === '' ? '（平台未给出原因，按项目最近一次评审意见判断）' : reason}`,
      '按上面的原因修改，不要从头重做；报告里写明针对该原因改了什么。',
    )
  }

  lines.push('', '输入物料：')
  if (dispatch.inputs.length === 0) {
    lines.push('- 无')
  } else {
    for (const input of dispatch.inputs) {
      lines.push(`- material_id=${input.material_id}，kind=${input.kind}，版本 v${input.version}`)
    }
  }

  lines.push('', '工作项正文：', item.body_md)
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

/** 回包快照里的 `current_work_item_id` → `string | null`（空串同样视为未持卡）。 */
function readHeldWorkItemId(
  snapshot: WorkerHeartbeatPayload['worker'] | undefined,
): string | null {
  const value = snapshot?.current_work_item_id
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

/** S-01：派单去重键 = `work_id:lease_id`（同一工作项换租约算新派单）。 */
function deliveryKey(command: DispatchCommand): string {
  const { dispatch } = command
  return `${dispatch.work_id}:${String(dispatch.lease?.lease_id ?? '')}`
}

function isDispatchCommand(command: WorkerCommand): command is DispatchCommand {
  if (command.kind !== 'dispatch') return false
  const dispatch = (command as DispatchCommand).dispatch
  return dispatch !== undefined
    && dispatch !== null
    && typeof dispatch.work_id === 'string'
    && dispatch.lease !== undefined
    && dispatch.lease !== null
}

function isWorkerUnknown(err: unknown): boolean {
  return err instanceof EchuanApiError && err.code === 'worker_unknown'
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

function describeError(err: unknown): string {
  if (err instanceof EchuanClientError) {
    return `code=${err.code}：${err.message}`
  }
  return err instanceof Error ? err.message : String(err)
}
