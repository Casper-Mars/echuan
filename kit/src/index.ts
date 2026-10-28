/**
 * dsh-echuan 插件入口（Cordis 4 函数式插件）。
 *
 * 职责边界（技术方案 v0.6 §1）：
 * - 声明插件标识 `name` 与依赖服务 `inject`；
 * - 在 `apply(ctx, config)` 里解析 5 项配置、落加载日志、把运行期骨架挂到 `ctx.effect`；
 * - **本阶段不注册任何工具、不发任何 HTTP 请求**，只保证「装得上、加载得起来、停得干净」。
 *
 * 依赖方向：kit 只依赖平台 HTTP 契约，不 import 任何 Go 代码。
 *
 * 加载策略：入口模块只静态引入 `./config.js`（schemastery，Schema 必须同步可见）与
 * `./runtime.js`（零依赖）。后续任务-02 起的工具 / HTTP 实现模块按 dsh-tasks 的经验
 * 在 `apply` 内**惰性 import**，避免静态重依赖拖慢 `--help` 这类快速退出场景下的插件激活。
 */
import type { Context } from '@deepseek-ai/cordis'
import {
  Config,
  WORKER_METADATA_MAX_BYTES,
  metadataByteLength,
  resolveConfig,
  type ConfigInput,
  type ResolvedConfig,
} from './config.js'
import { createEchuanRuntime, type EchuanRuntime, type Logger } from './runtime.js'

export {
  Config,
  DEFAULT_ACTOR,
  DEFAULT_BASE_URL,
  DEFAULT_CONFIG,
  DEFAULT_REQUEST_TIMEOUT_MS,
  WORKER_METADATA_MAX_BYTES,
  metadataByteLength,
  resolveConfig,
} from './config.js'
export type { ConfigInput, ResolvedConfig } from './config.js'
export { DEFAULT_POLL_INTERVAL_MS, createEchuanRuntime } from './runtime.js'
export type { EchuanRuntime, EchuanRuntimeOptions, Logger, TimerApi } from './runtime.js'

/** Cordis 函数式插件标识（同时是 patch 行里的 `id`）。 */
export const name = 'echuan'

/**
 * 依赖服务。
 *
 * - `tools` / `skills`：后续阶段注册工具与技能指南；
 * - `agents`：**只**用于投递消息与读取 agent 状态（`idle` / `running`），
 *   不创建会话、不选模型。
 *
 * 三者由 `@deepseek-ai/dsh-base` 提供；缺任一则本插件保持未激活，而不是抛错。
 */
export const inject: string[] = ['tools', 'skills', 'agents']

/** 加载日志前缀，验收要求。 */
const TAG = '[dsh-echuan]'

/**
 * 安装驿传接入插件。
 *
 * @param ctx Cordis 上下文（fiber），所有资源都必须挂在它上面
 * @param config 宿主按 {@link Config} 校验后的配置；单测可传 `undefined`
 */
export function apply(ctx: Context, config?: ConfigInput): void {
  const resolved = resolveConfig(config)

  // 日志双写：`console.log` 保证在 `--help` 等日志服务尚未接管的场景也能被观测到，
  // `ctx.logger` 用于进入宿主日志管线（结构化、可落盘）。与 dsh-cron 的做法一致。
  const log = (message: string): void => {
    console.log(message)
    try {
      ctx.logger?.info(message)
    } catch {
      // 日志服务缺失/异常不影响插件装载：console 已输出，继续。
    }
  }

  log(
    `${TAG} 插件加载 name=${name} inject=[${inject.join(', ')}] `
      + `baseUrl=${resolved.baseUrl} actor=${resolved.actor} `
      + `timeout=${resolved.requestTimeoutMs}ms poll=${resolved.worker.poll.enable}`,
  )

  reportMetadataBudget(resolved, log)

  // 所有运行期资源（当前只有定时器骨架，后续为工具注册 + HTTP 客户端）都挂在
  // 同一个 effect 上：fiber dispose（含 HMR 重载、profile 停用）时按逆序清理。
  ctx.effect(() => {
    const runtime: EchuanRuntime = createEchuanRuntime(resolved, {
      logger: toRuntimeLogger(log),
    })
    runtime.start()
    return () => {
      runtime.dispose('plugin dispose')
    }
  }, 'echuan.runtime()')
}

/**
 * `worker.metadata` 超限只告警不截断：上报内容由调用方决定，
 * 插件不替调用方做有损处理（平台契约按 ≤8KiB 约定）。
 */
function reportMetadataBudget(config: ResolvedConfig, log: (message: string) => void): void {
  const bytes = metadataByteLength(config.worker.metadata)
  if (bytes > WORKER_METADATA_MAX_BYTES) {
    log(
      `${TAG} 警告：worker.metadata 序列化后 ${bytes}B，超过平台约定的 ${WORKER_METADATA_MAX_BYTES}B 上限`,
    )
  }
}

/** 把插件的日志出口适配成运行期骨架要求的 `Logger`（骨架当前不使用 debug）。 */
function toRuntimeLogger(log: (message: string) => void): Logger {
  return { info: log, warn: log }
}
