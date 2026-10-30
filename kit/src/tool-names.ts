/**
 * 工具名清单与 `exclude` 校验（**唯一真源**）。
 *
 * 本模块只含常量与纯函数，**不依赖** `@deepseek-ai/dsh-tools`，因此可以被
 * 插件入口 `index.ts` 静态引入：配置里的工具名拼错时，`apply` 能在**同步阶段**
 * 直接抛 `TypeError` 让插件激活失败，而不是等到惰性 import 工具层后才变成一条 warning。
 *
 * 语义约定：
 * - `TOOL_NAMES` 始终表示**全集**（23 个 = 15 写 + 8 读），不随 `exclude` 变化；
 * - `exclude` 只是「本进程不注册哪些工具」，不影响工具定义与平台 HTTP 契约。
 */

/** 十五个写工具的名字（顺序即向模型暴露的顺序；不含任何审批动作）。 */
export const WRITE_TOOL_NAMES = [
  'echuan_task_claim',
  'echuan_task_renew',
  'echuan_task_report',
  'echuan_project_create',
  'echuan_project_requirement_write',
  'echuan_project_submit',
  'echuan_spec_create',
  'echuan_spec_revise',
  'echuan_plan_submit',
  'echuan_task_create',
  'echuan_task_update',
  'echuan_task_delete',
  'echuan_project_update',
  'echuan_product_create',
  'echuan_project_package',
] as const

/** 八个只读工具的名字（顺序即向模型暴露的顺序）。 */
export const READ_TOOL_NAMES = [
  'echuan_inbox',
  'echuan_product_list',
  'echuan_project_list',
  'echuan_task_list',
  'echuan_project_get',
  'echuan_task_get',
  'echuan_material_get',
  'echuan_worker_info',
] as const

/** 全部 23 个工具名（15 写 + 8 读）。始终是全集，不受 `exclude` 影响。 */
export const TOOL_NAMES = [...WRITE_TOOL_NAMES, ...READ_TOOL_NAMES] as const

/** 合法工具名的查找表（用于 O(1) 判断与错误信息）。 */
const TOOL_NAME_SET: ReadonlySet<string> = new Set(TOOL_NAMES)

/**
 * 归一化并校验 `exclude` 列表。
 *
 * 规则：
 * - 每项做 `trim`；去重（保留首次出现的顺序）；
 * - 空白项（`''` / 纯空格）直接抛 `TypeError`——静默丢弃会让「以为排除了」变成
 *   「其实没排除」，属于配置错误，必须让激活失败；
 * - 未知工具名抛 `TypeError`，错误信息同时列出**未知项**与**合法名单**；
 * - `undefined` / `null` / 空数组都返回 `[]`（等价于不排除）。
 *
 * @throws {TypeError} 含空白项或未知工具名时
 */
export function validateExclude(exclude?: readonly string[] | null): string[] {
  if (exclude === undefined || exclude === null || exclude.length === 0) return []

  const normalized: string[] = []
  const seen = new Set<string>()
  const unknown: string[] = []

  for (const item of exclude) {
    const name = item.trim()
    if (name === '') {
      throw new TypeError(
        'exclude 不能包含空白工具名。请填写完整工具名，或直接删掉该条目（exclude: [] 表示不排除）。',
      )
    }
    if (seen.has(name)) continue
    seen.add(name)
    if (!TOOL_NAME_SET.has(name)) {
      unknown.push(name)
      continue
    }
    normalized.push(name)
  }

  if (unknown.length > 0) {
    throw new TypeError(
      `exclude 含未知工具名：${unknown.join(', ')}。合法工具名（${TOOL_NAMES.length} 个）：`
        + `${TOOL_NAMES.join(', ')}。`,
    )
  }

  return normalized
}
