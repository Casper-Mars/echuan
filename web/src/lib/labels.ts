import type { TaskKind, TaskStatus } from '../api/types'

const labels: Record<TaskKind, Partial<Record<TaskStatus, string>>> = {
  requirement: {
    pending: '等待前置', ready: '待处理', in_progress: '处理中', in_review: '待审核', done: '已完成', failed: '失败', cancelled: '已取消',
  },
  design: {
    pending: '等待前置', ready: '待设计', in_progress: '设计中', in_review: '待评审', done: '已完成', failed: '失败', cancelled: '已取消',
  },
  breakdown: {
    pending: '等待前置', ready: '待拆分', in_progress: '拆分中', in_review: '待评审', done: '已完成', failed: '失败', cancelled: '已取消',
  },
  dev: {
    pending: '等待前置', ready: '待开发', in_progress: '开发中', in_review: '待验收', done: '已完成', failed: '失败', cancelled: '已取消',
  },
}

export const KINDS: TaskKind[] = ['requirement', 'design', 'breakdown', 'dev']
export const STATUSES: TaskStatus[] = ['pending', 'ready', 'in_progress', 'in_review', 'done', 'failed', 'cancelled']

export function statusLabel(kind: TaskKind, status: TaskStatus): string {
  return labels[kind][status] ?? status
}

export function kindLabel(kind: TaskKind): string {
  return { requirement: '需求', design: '方案', breakdown: '拆分', dev: '开发' }[kind]
}

export function formatTime(value?: string | null): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime())
    ? value
    : new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short', hour12: false }).format(date)
}
