// features/home/last-project —— 「上次访问的项目看板」记忆（design/flows.md F1 第 1 步）
//
// 存储键：localStorage['echuan:last-project']，值是 project_id（如 echuan-1），不是完整路径。
// 为什么存 id 不存路径：看板路由形态可能随 T5 调整（列折叠 / 筛选进 query），
// 存 id 让 `#/` 永远收敛到「该项目看板」，不会被旧 query 带偏。
//
// 读写全部静默降级：隐私模式、禁用存储、脏数据都不应影响导航（沿用 lib/menuMemory 的口径）。
const STORAGE_KEY = 'echuan:last-project'

/** 只接受看起来像 id 的字符串：非空、无斜杠与空白，避免脏数据拼出跨路由路径 */
function isProjectId(value: string): boolean {
  return value.length > 0 && !/[/?#\s]/.test(value)
}

/** 上次访问的项目 id；无记录 / 脏数据 / 存储不可用 → null */
export function readLastProject(): string | null {
  try {
    if (typeof window === 'undefined') return null
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return null
    return isProjectId(raw) ? raw : null
  } catch {
    return null
  }
}

/** 记录本次访问的项目 id；非法值不写入 */
export function rememberLastProject(projectId: string): void {
  if (!isProjectId(projectId)) return
  try {
    if (typeof window === 'undefined') return
    if (window.localStorage.getItem(STORAGE_KEY) === projectId) return
    window.localStorage.setItem(STORAGE_KEY, projectId)
  } catch {
    // 存储不可用：静默降级为不记忆
  }
}

/** 「看板」入口的目标路由：有记忆 → 该项目看板；无记忆 → 产品页（flows F1 第 1 步） */
export function boardPath(projectId?: string | null): string {
  const id = projectId ?? readLastProject()
  return id && isProjectId(id) ? `/projects/${id}/tasks` : '/products'
}
