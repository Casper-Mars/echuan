/** 时间与状态展示的统一格式化。 */

const PROJECT_STATUS: Record<number, string> = {
  0: '草稿',
  1: '规格评审',
  2: '规划中',
  3: '方案评审',
  4: '开发中',
  5: '交付评审',
  6: '已验收',
  7: '已取消',
}

const TASK_STATUS: Record<number, string> = {
  0: '待办',
  1: '可领取',
  2: '进行中',
  3: '待验收',
  4: '已完成',
  5: '已失败',
  6: '阻塞',
  7: '已取消',
}

/** 任务看板列顺序（与 domain/task.go 的整数状态一致）。 */
export const TASK_COLUMNS: { status: number; label: string }[] = [
  { status: 0, label: '待办' },
  { status: 1, label: '可领取' },
  { status: 2, label: '进行中' },
  { status: 3, label: '待验收' },
  { status: 4, label: '已完成' },
  { status: 5, label: '已失败' },
  { status: 6, label: '阻塞' },
  { status: 7, label: '已取消' },
]

export function projectStatusLabel(status: number): string {
  return PROJECT_STATUS[status] ?? `未知(${status})`
}

export function taskStatusLabel(status: number): string {
  return TASK_STATUS[status] ?? `未知(${status})`
}

/** 相对当前时间的“停留时长”（基于 update_time）。 */
export function formatSince(iso: string | undefined, now = Date.now()): string {
  if (!iso) return '—'
  const started = Date.parse(iso)
  if (Number.isNaN(started)) return '—'
  return formatDuration(now - started)
}

export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return '—'
  const seconds = Math.floor(ms / 1000)
  if (seconds < 60) return `${seconds} 秒`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes} 分钟`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时`
  return `${Math.floor(hours / 24)} 天`
}

export function formatClock(date: Date | null): string {
  if (!date) return '—'
  return date.toLocaleTimeString('zh-CN', { hour12: false })
}
