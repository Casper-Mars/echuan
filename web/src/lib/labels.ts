// lib/labels —— 状态文案与时间格式（徽标/类型文案已搬到 `lib/flow.ts`）
//
// 这里只留**一张状态表**：流程可配置（tech-echuan-180 §4 web 行）之后，类型的显示名由
// `GET /v1/tasks` 的 `kind_label` 给值、类型清单由 `GET /v1/flow` 给值，前端不再持有
// 「kind × status」二维表，也不再写死 KINDS / kindLabel：
//   * 旧二维表里「方案·待设计 / 开发·待开发」这类按环节定制的状态文案随类型维度一起取消，
//     7 个状态对所有环节使用同一套文案（需求行原值）；
//   * 新建环节（如 safety_review）不需要前端再补一行映射，也不会出现「未知 kind → 空白」。
import type { TaskStatus } from '../api/types'

/** 7 种任务状态的唯一文案表（看板详情 / 收件箱 / 队列共用这一份） */
export const STATUS_LABEL: Record<TaskStatus, string> = {
  pending: '等待前置',
  ready: '待处理',
  in_progress: '处理中',
  in_review: '待审核',
  done: '已完成',
  failed: '失败',
  cancelled: '已取消',
}

/**
 * 状态文案：单一状态表，不再按 kind 分叉。
 *
 * 入参保持 `string`：状态是后端枚举，但旧响应 / 脏数据可能带表外值，
 * 此时原样显示，不返回空白（与类型徽标同一口径）。
 */
export function statusLabel(status: TaskStatus | string): string {
  return STATUS_LABEL[status as TaskStatus] ?? status
}

export function formatTime(value?: string | null): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime())
    ? value
    : new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short', hour12: false }).format(date)
}
