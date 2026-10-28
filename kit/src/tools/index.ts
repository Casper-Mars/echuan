/**
 * 工具层入口：装配并注册 3 个写工具（领卡 / 续租 / 交报告）。
 *
 * 加载时机：本模块会静态引入 `@deepseek-ai/dsh-tools`（较重的模块图），
 * 因此**只能**由 `index.ts` 在 `apply` 内惰性 `import('./tools/index.js')`，
 * 不能在插件入口静态引入，否则会拖慢 `dsh --profile echuan --help` 这类快路径。
 *
 * 依赖方向：工具 → `client.ts`（唯一 HTTP 出口）→ 平台契约；
 * 身份 → `WorkerIdentity`（心跳循环的 `workerId`），工具层不自留副本。
 */
import type { Context } from '@deepseek-ai/cordis'
import type { ToolDefinition } from '@deepseek-ai/dsh-tools'
import {
  createClaimTool,
  createRenewTool,
  createReportTool,
  type EchuanToolOptions,
} from './write.js'

export type { ClaimValue, EchuanToolOptions, WorkerIdentity } from './write.js'
export { REPORT_STRUCTURE_HINT } from './write.js'
export { NO_WORK_TEXT } from './render.js'

/** 三个写工具的名字（顺序即向模型暴露的顺序；不含任何门禁动作）。 */
export const WRITE_TOOL_NAMES = [
  'echuan_task_claim',
  'echuan_task_renew',
  'echuan_task_report',
] as const

/** 构造 3 个写工具定义。 */
export function createEchuanTools(options: EchuanToolOptions): ToolDefinition[] {
  return [
    createClaimTool(options),
    createRenewTool(options),
    createReportTool(options),
  ]
}

/**
 * 在上下文中注册 3 个写工具，返回注销器（按注册逆序注销）。
 *
 * @param ctx 插件上下文（`inject` 已包含 `tools`）
 * @param options 客户端 + 共享身份
 */
export function registerEchuanTools(ctx: Context, options: EchuanToolOptions): () => void {
  const disposers = createEchuanTools(options).map(tool => ctx.tools.register(tool))
  return () => {
    for (let index = disposers.length - 1; index >= 0; index -= 1) disposers[index]?.()
  }
}
