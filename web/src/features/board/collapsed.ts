// features/board/collapsed —— 看板列折叠偏好（design/screens/board.md「列头折叠按钮切换折叠态并入 localStorage」）
//
// 存储键：localStorage['echuan:board:collapsed']，值是折叠状态枚举的 JSON 数组，例如
//   ["done","cancelled"]   ← 默认值：done / cancelled 默认折成 56 竖条（flows F5「列折叠偏好存 localStorage」）
// 为什么存枚举数组而不是「7 个布尔」：列顺序固定、新增状态时旧值仍然可解析，脏值只需按枚举过滤。
//
// 读写全部静默降级：隐私模式、禁用存储、配额满、脏数据都不应让看板崩掉（沿用 features/home/last-project 的口径）。
import type { TaskStatus } from '../../api/types'

/** 折叠偏好的存储键（web/README.md 与验收报告引用同一个值） */
export const COLLAPSED_STORAGE_KEY = 'echuan:board:collapsed'

/** 7 列固定枚举（顺序即看板列顺序：design/screens/board.md「结构」） */
export const BOARD_COLUMNS: readonly TaskStatus[] = [
  'pending',
  'ready',
  'in_progress',
  'in_review',
  'done',
  'failed',
  'cancelled',
]

/** 默认折叠：done / cancelled（design/screens/board.md collapsed 态；U-02 已按「默认折叠」定案） */
export const DEFAULT_COLLAPSED: readonly TaskStatus[] = ['done', 'cancelled']

const KNOWN = new Set<string>(BOARD_COLUMNS)

/** 按列顺序归一：只保留已知状态、去重、顺序固定，避免脏值把列序搞乱 */
function normalize(list: readonly string[]): TaskStatus[] {
  const wanted = new Set(list.filter((item) => KNOWN.has(item)))
  return BOARD_COLUMNS.filter((status) => wanted.has(status))
}

/** 当前折叠列；无记录 / 脏数据 / 存储不可用 → 默认值（done + cancelled） */
export function readCollapsed(): TaskStatus[] {
  try {
    if (typeof window === 'undefined') return normalize(DEFAULT_COLLAPSED)
    const raw = window.localStorage.getItem(COLLAPSED_STORAGE_KEY)
    if (raw === null) return normalize(DEFAULT_COLLAPSED)
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return normalize(DEFAULT_COLLAPSED)
    return normalize(parsed.filter((item): item is string => typeof item === 'string'))
  } catch {
    return normalize(DEFAULT_COLLAPSED)
  }
}

/** 写入折叠列；存储不可用时静默降级为仅本次会话生效 */
export function writeCollapsed(list: readonly TaskStatus[]): void {
  try {
    if (typeof window === 'undefined') return
    window.localStorage.setItem(COLLAPSED_STORAGE_KEY, JSON.stringify(normalize(list)))
  } catch {
    // 存储不可用：静默降级，不做任何 UI 提示（折叠本身仍然生效）
  }
}

/** 切换一列的折叠态，返回新列表（不改入参） */
export function toggleCollapsed(list: readonly TaskStatus[], status: TaskStatus): TaskStatus[] {
  return list.includes(status) ? list.filter((item) => item !== status) : [...list, status]
}
