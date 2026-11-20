// features/queue/group —— 派单队列按项目分组（纯函数，无 React）
//
// 规格对照：design/screens/queue.md
//   * 数据来源：`GET /v1/tasks?status=ready&limit=300`，**按 `project_id` 分组**；
//     分组轴就是这批 ready 任务本身，不额外拉项目列表（接口契约与屏规格同源）。
//   * 分组顺序：project_id 升序（字符串序），且**只出现有任务的项目组**——
//     屏规格的数据源只给了任务接口，凭空补一个「0 个任务」的项目组需要第二个接口，
//     不在本卡契约内（design/refs 里的 `echuan-2 0 个任务` 是原型写死的分组，见报告「未覆盖项」）。
//   * 组内顺序：priority 降序 → update_time 降序（与看板列内排序同一口径）。
import type { Task } from '../../api/types'

/** project_id 为空/缺失时的兜底组名（异常数据不静默丢弃，卡片仍然可见、总数仍然对得上） */
export const UNKNOWN_PROJECT = '未标记项目'

export interface QueueGroup {
  projectId: string
  tasks: Task[]
}

export function groupByProject(tasks: Task[]): QueueGroup[] {
  const map = new Map<string, Task[]>()

  for (const task of tasks) {
    const key = task.project_id === '' || task.project_id === undefined ? UNKNOWN_PROJECT : task.project_id
    const bucket = map.get(key)
    if (bucket) bucket.push(task)
    else map.set(key, [task])
  }

  return [...map.entries()]
    .map(([projectId, list]) => ({
      projectId,
      tasks: [...list].sort((a, b) => b.priority - a.priority || b.update_time.localeCompare(a.update_time)),
    }))
    .sort((a, b) => a.projectId.localeCompare(b.projectId))
}

/** 卡片的更新时间只显示时分（design/refs 卡片元信息行末尾是 18:31 这样的时间） */
const TIME_FORMAT = new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })

export function timeOf(value?: string | null): string | undefined {
  if (!value) return undefined
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? undefined : TIME_FORMAT.format(date)
}

/** 详情路由（flows F1：点卡片进同一详情路由，队列与看板 / 收件箱共用一条） */
export function detailPath(task: Pick<Task, 'project_id' | 'task_id'>): string {
  return `/projects/${task.project_id}/tasks/${task.task_id}`
}
