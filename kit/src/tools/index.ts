/**
 * 工具层入口：装配并注册 23 个工具（15 个写：项目创建 / 需求写入 / 提交评审 /
 * 方案创建 / 方案修订 / 批量建卡 / 领卡 / 续租 / 交报告 / 单卡增改删 /
 * 项目改标题 / 建产品 / 资料包下载；8 个读：待办 / 产品列表 / 项目列表 /
 * 任务列表 / 项目全景 / 任务详情 / 资料正文 / worker 诊断）。
 *
 * 写工具只覆盖「起草 → 提交评审 → 建卡」、任务执行（领卡/续租/交报告）与
 * 单卡增改删等辅助动作，**不提供任何审批 / 驳回 / 取消动作**。
 *
 * 裁剪：`EchuanToolOptions.exclude` 里的工具名不会进入本进程的注册表。过滤只发生在
 * 「构造 → 注册」这一层，工具定义、参数与平台 HTTP 契约完全不变；不传 `exclude`
 * 时行为与升级前一致（全量 23 个）。工具名清单的唯一真源是 `../tool-names.js`
 * （纯常量、不依赖 dsh-tools），因此入口可以在同步阶段就校验配置。
 *
 * 加载时机：本模块会静态引入 `@deepseek-ai/dsh-tools`（较重的模块图），
 * 因此**只能**由 `index.ts` 在 `apply` 内惰性 `import('./tools/index.js')`，
 * 不能在插件入口静态引入，否则会拖慢 `dsh --profile echuan --help` 这类快路径。
 *
 * 依赖方向：工具 → `client.ts`（唯一 HTTP 出口）→ 平台契约；
 * 身份 → `WorkerIdentity`（心跳循环的 `workerId`），工具层不自留副本。
 * 只读工具不使用身份：它们只发 GET，不带 worker 相关字段。
 */
import type { Context } from '@deepseek-ai/cordis'
import type { ToolDefinition } from '@deepseek-ai/dsh-tools'
import { validateExclude } from '../tool-names.js'
import { createEchuanReadTools } from './read.js'
import {
  createProductCreateTool,
  createProjectUpdateTool,
  createTaskCreateTool,
  createTaskDeleteTool,
  createTaskUpdateTool,
} from './maintenance.js'
import { createProjectPackageTool } from './package.js'
import {
  createPlanSubmitTool,
  createProjectCreateTool,
  createProjectSubmitTool,
  createRequirementWriteTool,
  createSpecCreateTool,
  createSpecReviseTool,
} from './lifecycle.js'
import {
  createClaimTool,
  createRenewTool,
  createReportTool,
  type EchuanToolOptions,
} from './write.js'

export type { ClaimValue, EchuanToolOptions, WorkerIdentity } from './write.js'
export type { WorkerInfoDeps } from './worker-info.js'
export { createWorkerInfoTool } from './worker-info.js'
export { REPORT_STRUCTURE_HINT } from './write.js'
/** 工具名清单唯一真源在 `../tool-names.js`，这里原样再导出（保持旧导入路径可用）。 */
export { READ_TOOL_NAMES, TOOL_NAMES, WRITE_TOOL_NAMES, validateExclude } from '../tool-names.js'
export {
  LIST_TEXT_MAX_CHARS,
  MATERIAL_MAX_CHARS,
  TASK_BODY_MAX_CHARS,
  formatDuration,
  formatInbox,
  formatMaterial,
  formatProductList,
  formatProject,
  formatProjectList,
  formatTask,
  formatTaskList,
  truncateText,
} from './render.js'
export { NO_WORK_TEXT } from './render.js'

/** 构造 15 个写工具定义（历史名称，等价于 {@link createEchuanWriteTools}）。 */
export function createEchuanTools(options: EchuanToolOptions): ToolDefinition[] {
  return createEchuanWriteTools(options)
}

/** 构造 15 个写工具定义（生命周期 6 个 + 任务执行 3 个 + 辅助 6 个）。 */
export function createEchuanWriteTools(options: EchuanToolOptions): ToolDefinition[] {
  return [
    createClaimTool(options),
    createRenewTool(options),
    createReportTool(options),
    createProjectCreateTool(options),
    createRequirementWriteTool(options),
    createProjectSubmitTool(options),
    createSpecCreateTool(options),
    createSpecReviseTool(options),
    createPlanSubmitTool(options),
    createTaskCreateTool(options),
    createTaskUpdateTool(options),
    createTaskDeleteTool(options),
    createProjectUpdateTool(options),
    createProductCreateTool(options),
    createProjectPackageTool(options),
  ]
}

/** 构造 8 个只读工具定义（含零 HTTP 的 `echuan_worker_info`）。 */
export { createEchuanReadTools }

/**
 * 构造**过滤后**的 23 个工具定义（先写后读，与注册顺序一致）。
 *
 * - 不传 / 传空 `exclude`：返回全部 23 个，顺序与 {@link TOOL_NAMES} 完全一致；
 * - 传了 `exclude`：先按 {@link validateExclude} 归一化（trim / 去重 / 未知名称抛错），
 *   再按名字过滤。未知名称在这里也会抛 `TypeError`，作为入口校验之外的第二道防线。
 */
export function createAllEchuanTools(options: EchuanToolOptions): ToolDefinition[] {
  const excluded = new Set(validateExclude(options.exclude))
  const all = [...createEchuanWriteTools(options), ...createEchuanReadTools(options)]
  if (excluded.size === 0) return all
  return all.filter(tool => !excluded.has(tool.name))
}

/**
 * 在上下文中注册**过滤后**的工具，返回注销器（按注册逆序注销）。
 *
 * @param ctx 插件上下文（`inject` 已包含 `tools`）
 * @param options 客户端 + 共享身份 + 可选的 `exclude` 裁剪列表
 */
export function registerEchuanTools(ctx: Context, options: EchuanToolOptions): () => void {
  const disposers = createAllEchuanTools(options).map(tool => ctx.tools.register(tool))
  return () => {
    for (let index = disposers.length - 1; index >= 0; index -= 1) disposers[index]?.()
  }
}
