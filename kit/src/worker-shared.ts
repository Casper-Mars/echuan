/**
 * worker 路径共用的小件：常量、打码、inflight 类型。
 *
 * v2 心跳派单循环（`worker.ts`）已删除；这里保留的是两条路径与工具层都需要的
 * 定义，避免工具层为了一个打码函数去依赖某个具体循环实现。
 */

/**
 * 首轮心跳相对 `start()` 的延迟（毫秒）。
 *
 * 插件拿不到 dsh 的启动参数，无法区分「长驻进程」与 `dsh --help` 这类几秒内就退出
 * 的短命令；若启动即注册，`--help`（`worker.poll.enable=true` 时）也会打一次真实
 * POST。用 5s 的短延迟规避这个副作用。
 */
export const FIRST_HEARTBEAT_DELAY_MS = 5_000

/** 回包没给合法 `heartbeat_interval_ms` 时的兜底间隔（毫秒）。 */
export const DEFAULT_HEARTBEAT_INTERVAL_MS = 15_000

/**
 * 已投递、尚未报告的派单（内存态，不落盘）。
 *
 * `workId` 是平台业务编号；`state` 只按「非空即持卡」判定。
 */
export interface Inflight {
  workId: string
  kind: string
  leaseUntil: string
}

/** worker id 打码：日志里只留前 4 位。 */
export function maskWorkerId(id: string): string {
  if (id.length <= 4) return '****'
  return `${id.slice(0, 4)}****`
}

/**
 * 本插件投递消息的生产者 kind（dsh v4：`plugin:<name>`，不再带 `plugin` 字段）。
 *
 * dsh 会话格式准入会拒收 `kind === 'plugin'`，第三方插件必须使用自有 kind。
 */
export const WORKER_PLUGIN_SOURCE = 'echuan'

/** 派单消息的 `source.kind`。 */
export const WORKER_PLUGIN_SOURCE_KIND = `plugin:${WORKER_PLUGIN_SOURCE}` as const

/**
 * 把本插件的生产者 kind 合并进 dsh-llm 的 `MessageSourceMap`。
 *
 * `MessageSourceMap` 是 dsh-llm 公开的合并扩展点：声明之后 `createUserMessage`
 * 的 `source` 仍走完整类型检查，无需把字面量断言成 `any`。
 *
 * 注意：这是模块级声明，必须留在编译单元内；原先挂在 v2 循环文件里，
 * 删除该文件时一并搬到这里。
 */
declare module '@deepseek-ai/dsh-llm/message' {
  interface MessageSourceMap {
    /** 驿传（echuan）插件投递的派单消息。 */
    'plugin:echuan': { kind: 'plugin:echuan' }
  }
}
