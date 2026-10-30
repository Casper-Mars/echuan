/**
 * dsh-echuan 插件入口（Cordis 4 函数式插件）。
 *
 * 职责边界（技术方案 v0.6 §1）：
 * - 声明插件标识 `name` 与依赖服务 `inject`；
 * - 在 `apply(ctx, config)` 里解析 5 项配置、落加载日志；
 * - 注册 16 个工具：9 个写（项目创建 / 需求写入 / 提交评审 / 方案创建 /
 *   方案修订 / 批量建卡 / 领卡 / 续租 / 交报告）+ 7 个只读
 *   （待办 / 产品 / 项目 / 任务列表 / 项目全景 / 任务详情 / 资料），
 *   只调平台契约、不提供审批/驳回/取消动作；
 * - 静态注册 1 个技能 `echuan-task-execution`（派单开工 / 读上下文 / 汇报 / 续租 / 门禁禁区），
 *   随插件 fiber 一并注销；
 * - `worker.poll.enable=false`（默认）时**零请求、零定时器**，心跳循环完全不创建；
 * - `worker.metadata` 序列化后超过 8KiB 时只 warn 一条并**不启动心跳循环**（R-02），
 *   避免每轮都被平台拒（413）而永久刷屏；
 * - 启用时把 worker 心跳循环挂到 `ctx.effect`，由它定时心跳、上报状态并投递派单。
 *
 * worker_id 只有一个内存来源：心跳循环的 `workerId`。工具层通过 getter 读同一个值，
 * 不缓存副本、不读磁盘；循环不存在（poll=false）时工具发请求不带该字段。
 *
 * 依赖方向：kit 只依赖平台 HTTP 契约，不 import 任何 Go 代码。
 *
 * 加载策略：入口模块只静态引入 `./config.js`（schemastery，Schema 必须同步可见）与
 * `./runtime.js`（零依赖的日志/定时器类型）。`./client.js`、`./worker.js`
 * （后者带 `@deepseek-ai/dsh-llm`）与 `./tools/index.js`（带 `@deepseek-ai/dsh-tools`）
 * 都在 `apply` 内**惰性 import**，避免拖慢 `--help` 这类快速退出场景下的插件激活。
 */
import type { Context } from '@deepseek-ai/cordis'
import {
  Config,
  WORKER_METADATA_MAX_BYTES,
  metadataByteLength,
  resolveConfig,
  type ConfigInput,
} from './config.js'
import type { WorkerClient } from './client.js'
import type { Logger } from './runtime.js'
import { ECHUAN_SKILL_NAME, registerEchuanSkill } from './skill.js'
import type { WorkerLoop } from './worker.js'

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
export type { WorkerLoop, WorkerLoopOptions } from './worker.js'
export {
  ECHUAN_SKILL_DESCRIPTION,
  ECHUAN_SKILL_NAME,
  ECHUAN_SKILL_WHEN_TO_USE,
  createEchuanSkillRegistration,
  loadEchuanSkillGuide,
  registerEchuanSkill,
} from './skill.js'

/** Cordis 函数式插件标识（同时是 patch 行里的 `id`）。 */
export const name = 'echuan'

/**
 * 依赖服务。
 *
 * - `tools` / `skills`：后续阶段注册工具与技能指南；
 * - `agents`：**只**用于读取根 agent 状态（`idle` / `running`）与投递任务消息，
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

  // 告警单独出口：保留 warn 级别给宿主，同时保证测试/终端可见。
  const warn = (message: string): void => {
    console.warn(message)
    try {
      ctx.logger?.warn?.(message)
    } catch {
      // 同上。
    }
  }

  log(
    `${TAG} 插件加载 name=${name} inject=[${inject.join(', ')}] `
      + `baseUrl=${resolved.baseUrl} actor=${resolved.actor} `
      + `timeout=${resolved.requestTimeoutMs}ms poll=${resolved.worker.poll.enable}`,
  )

  // R-02：超限在这里判一次，超限就完全不启动循环（零请求、零定时器）。
  let metadataOverLimit = false
  try {
    const metadataBytes = metadataByteLength(resolved.worker.metadata)
    metadataOverLimit = metadataBytes > WORKER_METADATA_MAX_BYTES
    if (metadataOverLimit) {
      warn(
        `${TAG} 警告：worker.metadata 序列化后 ${metadataBytes}B，`
          + `超过平台约定的 ${WORKER_METADATA_MAX_BYTES}B 上限，不启动心跳循环`
          + '（否则每轮注册都会被拒）；请缩小 metadata 或保持 worker.poll.enable=false',
      )
    }
  } catch (err) {
    // S-04：metadata 不可序列化（循环引用 / BigInt）时只告警，不让插件装载失败。
    warn(`${TAG} 警告：${messageOf(err)}`)
  }

  // worker_id 的**唯一内存来源**：心跳循环。工具层通过这个 getter 读同一个值，
  // 不自留副本、不读磁盘；循环尚未创建时（poll=false）自然是 undefined。
  let loop: WorkerLoop | undefined

  // 工具注册：与心跳开关无关（poll=false 也能手动领卡/续租/交报告）。
  // 惰性 import 保证 `--help` 快路径不加载 `@deepseek-ai/dsh-tools`。
  ctx.effect(() => {
    let disposed = false
    let disposeTools: (() => void) | undefined

    void (async () => {
      try {
        const [toolsModule, clientModule] = await Promise.all([
          import('./tools/index.js'),
          import('./client.js'),
        ])
        if (disposed) return

        disposeTools = toolsModule.registerEchuanTools(ctx, {
          client: clientModule.createClient(resolved),
          identity: {
            get workerId() {
              return loop?.workerId
            },
          },
        })
        log(`${TAG} 已注册 ${toolsModule.TOOL_NAMES.length} 个工具`
          + `（${toolsModule.WRITE_TOOL_NAMES.length} 写 + ${toolsModule.READ_TOOL_NAMES.length} 读）：`
          + toolsModule.TOOL_NAMES.join(', '))
      } catch (err) {
        warn(`${TAG} 工具注册失败：${messageOf(err)}`)
      }
    })()

    return () => {
      disposed = true
      const active = disposeTools
      disposeTools = undefined
      active?.()
    }
  }, 'echuan.tools()')

  // 技能注册：正文是随构建产物发布的同步小文件（`lib/assets/*.md`），
  // 不需要惰性 import，也不会拖慢 `--help` 快路径。注册与注销都在同一个 effect 内，
  // fiber 卸载（含 HMR 重载、profile 停用）时随插件一并注销。
  ctx.effect(() => {
    let disposeSkill: (() => void) | undefined
    try {
      disposeSkill = registerEchuanSkill(ctx)
      log(`${TAG} 已注册技能 ${ECHUAN_SKILL_NAME}（source=runtime）`)
    } catch (err) {
      warn(`${TAG} 技能注册失败：${messageOf(err)}`)
      return () => {}
    }

    return () => {
      const off = disposeSkill
      disposeSkill = undefined
      if (!off) return
      off()
      log(`${TAG} 已注销技能 ${ECHUAN_SKILL_NAME}`)
    }
  }, 'echuan.skill()')

  // 所有运行期资源（worker 心跳定时器）都挂在同一个 effect 上：
  // fiber dispose（含 HMR 重载、profile 停用）时按逆序清理。
  ctx.effect(() => {
    let disposed = false

    if (!resolved.worker.poll.enable) {
      // 默认关闭：不加载客户端、不建定时器、不发请求。
      log(`${TAG} worker.poll.enable=false，未创建心跳定时器`)
      return () => {}
    }

    if (metadataOverLimit) {
      // R-02：metadata 超限时不加载客户端、不建定时器、不发请求（warn 已在 apply 打过）。
      log(`${TAG} worker.metadata 超过 ${WORKER_METADATA_MAX_BYTES}B，未创建心跳定时器`)
      return () => {}
    }

    // 惰性加载：只有真正启用轮询时才引入 fetch 客户端与 worker 循环，
    // 保证 `--help` 快路径不加载 HTTP / dsh-llm 依赖。
    void (async () => {
      try {
        const [clientModule, workerModule] = await Promise.all([
          import('./client.js'),
          import('./worker.js'),
        ])
        if (disposed) return

        const client: WorkerClient = clientModule.createWorkerClient(resolved)
        const nextLoop = workerModule.createWorkerLoop({
          config: resolved,
          client,
          agents: () => ctx.agents.roots(),
          logger: toRuntimeLogger(log, warn),
        })
        loop = nextLoop

        if (disposed) {
          nextLoop.dispose('plugin dispose')
          return
        }
        nextLoop.start()
      } catch (err) {
        warn(`${TAG} 心跳启动失败：${messageOf(err)}`)
      }
    })()

    return () => {
      disposed = true
      const active = loop
      loop = undefined
      active?.dispose('plugin dispose')
    }
  }, 'echuan.runtime()')
}

/** 把插件的两个日志出口适配成 worker 循环要求的 `Logger`。 */
function toRuntimeLogger(
  info: (message: string) => void,
  warn: (message: string) => void,
): Logger {
  return { info, warn }
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}
