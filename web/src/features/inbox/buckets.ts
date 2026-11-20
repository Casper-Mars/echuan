// features/inbox/buckets —— 收件箱三区的归桶与卡片元信息（纯函数，无 React）
//
// 规格对照：design/screens/inbox.md
//   * 数据来源：`GET /v1/inbox`（待审核 / 待领取）、`GET /v1/tasks?status=ready`（派单队列区）；
//   * 待审核 = `in_review`，待领取 = `ready`；其余状态不属于三区任何一区，**不进任何区的计数**
//     （与 T5 看板同一口径：页头与各列计数之和恒等，异常数据不会造成对不上的账）。
//   * 区内顺序：priority 降序 → update_time 降序（与看板列内排序同一口径）。
import type { Task } from '../../api/types'

export interface InboxBuckets {
  /** 待审核（in_review） */
  review: Task[]
  /** 待领取（ready） */
  ready: Task[]
  /** 状态不在 `in_review` / `ready` 内的异常条目数（不静默丢弃：页面用 info 提示条说明） */
  dropped: number
}

/** priority 降序、update_time 降序（稳定：同优先级同时间按原顺序） */
export function sortByPriority(tasks: Task[]): Task[] {
  return [...tasks].sort((a, b) => b.priority - a.priority || b.update_time.localeCompare(a.update_time))
}

export function bucketInbox(tasks: Task[]): InboxBuckets {
  const review: Task[] = []
  const ready: Task[] = []
  let dropped = 0

  for (const task of tasks) {
    if (task.status === 'in_review') review.push(task)
    else if (task.status === 'ready') ready.push(task)
    else dropped += 1
  }

  return { review: sortByPriority(review), ready: sortByPriority(ready), dropped }
}

/** 卡片的更新时间只显示时分（design/refs 卡片元信息行末尾是 18:31 这样的时间） */
const TIME_FORMAT = new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })

export function timeOf(value?: string | null): string | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : TIME_FORMAT.format(date)
}

/** 详情路由（flows F1：点卡片进同一详情路由，收件箱 / 队列与看板共用一条） */
export function detailPath(task: Pick<Task, 'project_id' | 'task_id'>): string {
  return `/projects/${task.project_id}/tasks/${task.task_id}`
}
