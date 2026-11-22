/**
 * 工具名清单与 `exclude` 校验（**唯一真源**）。
 *
 * V3 工具面：18 个 = 10 写 + 8 读。
 * - 写：任务执行 6 个（claim/renew/report/create/update/attach）
 *       项目 4 个（create/close/reopen/product_create）
 * - 读：inbox / product_list / project_list / project_get /
 *       task_list / task_get / material_get / worker_info
 *
 * 已删除的 v2 工具：work_*（改名为 task_*）、project_requirement_write、
 * project_submit、spec_create、spec_revise、plan_submit、project_package。
 */

export const WRITE_TOOL_NAMES = [
  'echuan_task_claim',
  'echuan_task_renew',
  'echuan_task_report',
  'echuan_task_create',
  'echuan_task_update',
  'echuan_task_attach',
  'echuan_project_create',
  'echuan_project_close',
  'echuan_project_reopen',
  'echuan_product_create',
] as const

export const READ_TOOL_NAMES = [
  'echuan_inbox',
  'echuan_product_list',
  'echuan_project_list',
  'echuan_project_get',
  'echuan_task_list',
  'echuan_task_get',
  'echuan_material_get',
  'echuan_worker_info',
] as const

export const REQUESTER_WRITE_TOOL_NAMES = [
  'echuan_project_create',
  'echuan_project_close',
  'echuan_project_reopen',
  'echuan_task_create',
  'echuan_task_update',
  'echuan_task_report',
  'echuan_task_attach',
] as const

export const REQUESTER_TOOL_NAMES = [...READ_TOOL_NAMES, ...REQUESTER_WRITE_TOOL_NAMES] as const

export const TOOL_NAMES = [...WRITE_TOOL_NAMES, ...READ_TOOL_NAMES] as const

const TOOL_NAME_SET: ReadonlySet<string> = new Set(TOOL_NAMES)

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
