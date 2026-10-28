/**
 * 插件运行期骨架：定时器与清理路径。
 *
 * @deprecated 任务-03 起，真实心跳循环由 `worker.ts` 的 `createWorkerLoop` 承担
 * （`setTimeout` 链 + 平台下发间隔）。本模块仅作为任务-01 的**兼容导出**保留，
 * 插件入口不再使用它；它本身不发请求，也不会与 worker 循环形成第二套定时器。
 *
 * 本阶段（任务-01）**不注册工具、不发任何 HTTP 请求**，只把「生命周期」立起来：
 *
 * - `worker.poll.enable = false`（默认）时 `start()` 直接返回，**不创建任何定时器**；
 * - 定时器句柄只在 `start()` 内创建、只在 `dispose()` 内清除，句柄不逃逸；
 * - `dispose()` 幂等：重复调用不会重复清理，也不会再产生日志；
 * - 心跳 tick 的实际动作由 `onPoll` 注入，本阶段为 `undefined`（空转），
 *   任务-02 接线后再补 HTTP 拉取。
 *
 * `timerApi` 可注入，便于单测用假定时器断言「零定时器」而不依赖真实时钟。
 */
import type { ResolvedConfig } from './config.js'

/** 仅声明用到的定时器能力，避免把整个 `globalThis` 当成依赖注入。 */
export interface TimerApi {
  setInterval(handler: () => void, ms: number): unknown
  clearInterval(handle: unknown): void
}

/** 日志出口；默认走 `console`。 */
export interface Logger {
  info(message: string): void
  warn(message: string): void
  debug?(message: string): void
}

/**
 * 心跳间隔（毫秒）。本阶段的内部常量，不属 5 项配置；
 * 任务-02 若平台契约要求可配置，再提升到 `Config.worker.poll` 下。
 */
export const DEFAULT_POLL_INTERVAL_MS = 60_000

export interface EchuanRuntimeOptions {
  /** 日志出口，默认 `console`。 */
  logger?: Logger
  /** 定时器实现，默认 `globalThis`。 */
  timerApi?: TimerApi
  /** 心跳 tick 的实现；本阶段缺省（无网络调用）。 */
  onPoll?: (config: ResolvedConfig) => void | Promise<void>
  /** 心跳间隔，默认 {@link DEFAULT_POLL_INTERVAL_MS}。 */
  pollIntervalMs?: number
}

export interface EchuanRuntime {
  /** 是否已创建心跳定时器（单测断言「零定时器」用）。 */
  readonly polling: boolean
  /** 是否已卸载。 */
  readonly disposed: boolean
  /** 按配置启动；`worker.poll.enable=false` 时什么都不做。 */
  start(): void
  /** 幂等卸载：清定时器、落一条日志，之后不再产生任何活动。 */
  dispose(reason?: string): void
}

const consoleLogger: Logger = {
  info: message => console.log(message),
  warn: message => console.warn(message),
  debug: message => console.debug(message),
}

/**
 * 创建运行期骨架。
 *
 * @param config 已归一化的配置（见 `resolveConfig`）
 * @param options 可注入的日志 / 定时器 / tick 实现
 */
export function createEchuanRuntime(
  config: ResolvedConfig,
  options: EchuanRuntimeOptions = {},
): EchuanRuntime {
  const logger = options.logger ?? consoleLogger
  const timers: TimerApi = options.timerApi ?? {
    setInterval: (handler, ms) => globalThis.setInterval(handler, ms),
    clearInterval: handle => globalThis.clearInterval(handle as ReturnType<typeof setInterval>),
  }
  const intervalMs = options.pollIntervalMs ?? DEFAULT_POLL_INTERVAL_MS

  let timer: unknown
  let started = false
  let disposed = false

  return {
    get polling() {
      return timer !== undefined
    },

    get disposed() {
      return disposed
    },

    start() {
      if (disposed || started) return
      started = true

      if (!config.worker.poll.enable) {
        logger.info('[dsh-echuan] worker.poll.enable=false，未创建心跳定时器')
        return
      }

      timer = timers.setInterval(() => {
        // 定时器可能在 dispose 之后才触发（事件循环里已排队的回调）：
        // 这里再判一次，保证卸载后不再产生任何活动。
        if (disposed) return
        void options.onPoll?.(config)
      }, intervalMs)
      logger.info(`[dsh-echuan] 心跳定时器已启动，间隔 ${intervalMs}ms`)
    },

    dispose(reason = 'plugin dispose') {
      if (disposed) return
      disposed = true
      started = false
      if (timer !== undefined) {
        timers.clearInterval(timer)
        timer = undefined
      }
      logger.info(`[dsh-echuan] 已卸载（${reason}）：定时器已清除，不再产生请求与日志`)
    },
  }
}
