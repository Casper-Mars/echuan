/**
 * 工具层入口：装配 V3 的 18 个工具（10 写 + 8 读）。
 *
 * 写工具只覆盖「建项目 / 建任务 / 领卡 / 续租 / 交报告 / 挂附件 / 关项目」，
 * **不提供任何审批 / 驳回 / 取消动作**。
 *
 * 裁剪：`EchuanToolOptions.exclude` 里的工具名不会进入本进程的注册表；名称校验
 * 在同步阶段完成，未知名称让插件激活失败。工具名清单的唯一真源是 `../tool-names.js`。
 */
import type { Context } from '@deepseek-ai/cordis'
import type { ToolDefinition } from '@deepseek-ai/dsh-tools'
import { TOOL_NAMES, validateExclude } from '../tool-names.js'
import { createV3ReadTools, createV3WriteTools } from './v3.js'
import type { EchuanToolOptions } from './write.js'

export type { EchuanToolOptions, WorkerIdentity } from './write.js'
export type { WorkerInfoDeps } from './worker-info.js'
export { createWorkerInfoTool } from './worker-info.js'
export { READ_TOOL_NAMES, TOOL_NAMES, WRITE_TOOL_NAMES, validateExclude } from '../tool-names.js'

/** 构造 10 个写工具定义。 */
export function createEchuanTools(options: EchuanToolOptions): ToolDefinition[] {
  return createV3WriteTools(options)
}

/** 构造 10 个写工具定义（claim / renew / report / create / update / attach / project ×3 / product）。 */
export function createEchuanWriteTools(options: EchuanToolOptions): ToolDefinition[] {
  return createV3WriteTools(options)
}

/** 构造 8 个只读工具定义。 */
export const createEchuanReadTools = createV3ReadTools

/** 构造过滤后的 18 个工具定义（先写后读）。 */
export function createAllEchuanTools(options: EchuanToolOptions): ToolDefinition[] {
  const excluded = new Set(validateExclude(options.exclude))
  const all = [...createV3WriteTools(options), ...createV3ReadTools(options)]
  if (excluded.size === 0) return all
  return all.filter(tool => !excluded.has(tool.name))
}

/** 在上下文中注册过滤后的工具，返回注销器（按注册逆序注销）。 */
export function registerEchuanTools(ctx: Context, options: EchuanToolOptions): () => void {
  return registerEchuanToolNames(ctx, options, TOOL_NAMES)
}

/** 按白名单注册工具；preset 子路径用这个入口。 */
export function registerEchuanToolNames(ctx: Context, options: EchuanToolOptions, names: readonly string[]): () => void {
  const wanted = new Set(names)
  const disposers = createAllEchuanTools(options)
    .filter(tool => wanted.has(tool.name))
    .map(tool => ctx.tools.register(tool))
  return () => {
    for (let index = disposers.length - 1; index >= 0; index -= 1) disposers[index]?.()
  }
}
