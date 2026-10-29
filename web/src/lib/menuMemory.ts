/**
 * 侧边栏菜单的“最后停留路径”记忆（技术方案 §3.1）。
 *
 * - 以一级入口（section）为键，记住该入口下用户最后停留的完整路径；
 * - 存 sessionStorage：刷新保留，新标签页 / 新窗口各自独立，不污染长期状态；
 * - 读写全部静默降级：隐私模式、禁用存储、配额满、脏数据都不应影响导航。
 */

const STORAGE_KEY = 'echuan:menu-path'

/** 需要记忆的一级入口。执行节点（/workers）没有下钻层级，无需记忆。 */
const SECTIONS = ['/products'] as const

type Section = (typeof SECTIONS)[number]

function isSection(value: string): value is Section {
  return (SECTIONS as readonly string[]).includes(value)
}

/** 路径所属的一级入口；不在任何已知入口下时返回 null。 */
function sectionOf(pathname: string): Section | null {
  for (const section of SECTIONS) {
    if (pathname === section || pathname.startsWith(`${section}/`)) return section
  }
  return null
}

/** 读取整张映射表；任何异常或结构不符都按空表处理。 */
function readStore(): Record<string, string> {
  try {
    if (typeof window === 'undefined') return {}
    const raw = window.sessionStorage.getItem(STORAGE_KEY)
    if (!raw) return {}
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return {}
    const source = parsed as Record<string, unknown>
    const store: Record<string, string> = {}
    for (const key of Object.keys(source)) {
      const value = source[key]
      // 只接受已知 section + 字符串值，其余脏数据直接丢弃。
      if (isSection(key) && typeof value === 'string') store[key] = value
    }
    return store
  } catch {
    return {}
  }
}

/** 记录当前路径。路径不属于任何已知入口时不做任何事。 */
export function rememberMenuPath(pathname: string): void {
  const section = sectionOf(pathname)
  if (!section) return
  try {
    const store = readStore()
    if (store[section] === pathname) return
    store[section] = pathname
    window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify(store))
  } catch {
    // 存储不可用：静默降级为不记忆。
  }
}

/**
 * 取回某个入口最后停留的路径。
 * 记忆缺失、被清空、或读回值不在该入口下（脏数据 / 手工篡改）时返回 fallback。
 */
export function recallMenuPath(section: string, fallback: string): string {
  if (!isSection(section)) return fallback
  const remembered = readStore()[section]
  if (typeof remembered !== 'string') return fallback
  if (remembered !== section && !remembered.startsWith(`${section}/`)) return fallback
  return remembered
}
