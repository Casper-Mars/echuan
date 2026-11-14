/** 时间与状态展示的统一格式化。 */

const PROJECT_STATUS: Record<number, string> = {
  0: '需求草稿',
  1: '需求评审',
  2: '方案草稿',
  3: '方案评审',
  4: '开发任务草稿',
  5: '开发任务评审',
  6: '待完成',
  7: '已完成',
  8: '已取消',
}

/** 工作项 kind 的中文标签。 */
export const WORK_ITEM_KINDS = ['requirement', 'design', 'breakdown', 'dev'] as const

const WORK_ITEM_KIND_LABELS: Record<(typeof WORK_ITEM_KINDS)[number], string> = {
  requirement: '需求',
  design: '方案',
  breakdown: '拆解',
  dev: '开发',
}

/**
 * 工作项 state 的中文标签（与 api/openapi.yaml 的文本状态一一对应）。
 *
 * accepted 固定叫「已验收」：项目状态 7 已经占用了「已完成」，
 * 卡片验收与项目人工完成是两件事，三个词互不覆盖（待完成 / 已完成 / 已验收）。
 */
const WORK_ITEM_STATE_LABELS: Record<string, string> = {
  blocked: '待评审',
  queued: '待开发',
  leased: '开发中',
  submitted: '待验收',
  accepted: '已验收',
  failed: '已失败',
  cancelled: '已取消',
}

export type WorkItemKindValue = (typeof WORK_ITEM_KINDS)[number]

export const WORK_ITEM_STATE_OPTIONS = Object.entries(WORK_ITEM_STATE_LABELS).map(
  ([state, label]) => ({ state, label }),
)

/** 全部工作项状态（按看板列顺序）。 */
export const WORK_ITEM_STATE_COLUMNS = WORK_ITEM_STATE_OPTIONS

/**
 * 工作项看板主列：blocked 仅在非空时出现，
 * queued/leased/submitted/accepted 常驻（数量为 0 也展示列头与计数）。
 */
export const WORK_ITEM_BOARD_COLUMNS: { state: string; label: string }[] = [
  { state: 'blocked', label: '待评审' },
  { state: 'queued', label: '待开发' },
  { state: 'leased', label: '开发中' },
  { state: 'submitted', label: '待验收' },
  { state: 'accepted', label: '已验收' },
]

/** 附加列（已失败/已取消）：仅非空时显示。 */
export const WORK_ITEM_TRAILING_COLUMNS: { state: string; label: string }[] = [
  { state: 'failed', label: '已失败' },
  { state: 'cancelled', label: '已取消' },
]

/**
 * 未结清的工作项状态：既非 accepted 也非 cancelled。
 *
 * 与后端 store.CountUnsettledWorkItemsByProject 的判定一致
 * （state NOT IN (accepted, cancelled)），是「完成项目」的前置条件。
 */
export const WORK_ITEM_UNSETTLED_STATES = [
  'blocked',
  'queued',
  'leased',
  'submitted',
  'failed',
] as const

/** 按 work_item_by_state 统计未结清卡片数（字段缺失按 0）。 */
export function unsettledWorkItemCount(byState: Record<string, number> | undefined): number {
  if (!byState) return 0
  return WORK_ITEM_UNSETTLED_STATES.reduce((sum, state) => sum + (byState[state] ?? 0), 0)
}

export function workItemKindLabel(kind: string): string {
  return WORK_ITEM_KIND_LABELS[kind as WorkItemKindValue] ?? `未知(${kind})`
}

export function workItemStateLabel(state: string): string {
  return WORK_ITEM_STATE_LABELS[state] ?? `未知(${state})`
}

export function projectStatusLabel(status: number): string {
  return PROJECT_STATUS[status] ?? `未知(${status})`
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

/** 相对时间（中文口径）：刚刚 / N 分钟前 / N 小时前 / N 天前。 */
export function formatRelative(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return '—'
  const time = Date.parse(iso)
  if (Number.isNaN(time)) return '—'
  const diff = Math.max(0, now - time)
  if (diff < 60_000) return '刚刚'
  const minutes = Math.floor(diff / 60_000)
  if (minutes < 60) return `${minutes} 分钟前`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours} 小时前`
  return `${Math.floor(hours / 24)} 天前`
}

export function formatClock(date: Date | null): string {
  if (!date) return '—'
  return date.toLocaleTimeString('zh-CN', { hour12: false })
}

/** 绝对时间（详情里展示 create_time / update_time）。 */
export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return '—'
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString('zh-CN', { hour12: false })
}

/** 字节数人性化：报告/物料大小。 */
export function formatBytes(size: number): string {
  if (!Number.isFinite(size) || size < 0) return '—'
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / (1024 * 1024)).toFixed(1)} MB`
}

/** 全部项目状态（按整数升序，用于筛选下拉/按钮）。 */
export const PROJECT_STATUS_OPTIONS: { status: number; label: string }[] = Object.keys(PROJECT_STATUS)
  .map(Number)
  .sort((a, b) => a - b)
  .map((status) => ({ status, label: projectStatusLabel(status) }))

/**
 * 需要人工决策的项目状态：需求评审 / 方案评审 / 开发任务评审。
 *
 * 用于「待决策 / 超时滞留」判定：开发任务评审也在等人工处理，收件箱与
 * 列表标红都要把它算进来，因此这里保持 [1, 3, 5]。
 */
export const PROJECT_PENDING_STATUSES = [1, 3, 5] as const

/**
 * 项目级「批准 / 驳回」可用的状态：需求评审 / 方案评审。
 *
 * 与 PROJECT_PENDING_STATUSES 的区别：开发任务评审（5）虽然同样待人工处理，
 * 但它的决策对象是工作项批次，入口在工作项看板（全部通过 / 全部驳回），
 * 调项目级 POST /v1/projects/decide 只会得到 409 invalid_state。
 */
export const PROJECT_DECIDABLE_STATUSES = [1, 3] as const

/** 待决策超过 72 小时视为滞留（阈值可在此统一调整）。 */
export const PENDING_STALE_MS = 72 * 60 * 60 * 1000

export function isPendingProjectStatus(status: number): boolean {
  return (PROJECT_PENDING_STATUSES as readonly number[]).includes(status)
}

/** 项目级「批准 / 驳回」是否可用（不含开发任务评审的批次决策）。 */
export function isDecidableProjectStatus(status: number): boolean {
  return (PROJECT_DECIDABLE_STATUSES as readonly number[]).includes(status)
}

/** 基于 update_time 判断待决策是否已滞留超阈值。 */
export function isPendingStale(
  updateTime: string | undefined,
  now = Date.now(),
  threshold = PENDING_STALE_MS,
): boolean {
  if (!updateTime) return false
  const started = Date.parse(updateTime)
  if (Number.isNaN(started)) return false
  return now - started > threshold
}
