export type TaskKind = 'requirement' | 'design' | 'breakdown' | 'dev'
export type TaskStatus =
  | 'pending'
  | 'ready'
  | 'in_progress'
  | 'in_review'
  | 'done'
  | 'failed'
  | 'cancelled'

export interface Product {
  product_id: string
  name: string
  git_url: string
  description?: string | null
  license?: string | null
  /** `GET /v1/products?id=` 已返回；列表响应可能缺省，缺省时回退调用方的本地计数。 */
  project_count?: number
}

export interface Project {
  project_id: string
  product_id: string
  seq_no: number
  name: string
  context_md: string
  status: 'open' | 'closed'
  create_time: string
  update_time: string
  closed_at?: string | null
}

export interface Task {
  task_id: string
  project_id: string
  parent_task_id?: string | null
  kind: TaskKind
  title: string
  description_md: string
  context_md: string
  priority: number
  status: TaskStatus
  attempt: number
  max_attempts: number
  lease_id?: string | null
  lease_until?: string | null
  worker_id?: string | null
  reason?: string | null
  create_time: string
  update_time: string
}

export interface AttachmentRef {
  material_id: string
  title: string
  kind: string
  role: 'input' | 'output'
}

/**
 * `GET /v1/workers` 的一行（字段见 `internal/api/v3_handlers.go` 的 `v3WorkerResponse`；
 * 真正挂载的是 V3 路由，见 `cmd/echuan/main.go` 的 `NewRouterV3`）。
 *
 * `current_work_item_id` 是**任务业务标识**（`t_...` 字符串），不是自增 id；
 * 无当前任务时后端返回显式 null。此处曾按 V3 行 id 口径写成数字型任务 id 字段，
 * 与实际响应不一致，导致忙碌节点的「当前任务」取不到值（issue #89 C1 / E4）。
 * V3 响应里的自增行 id 字段（旧字段）仍保留给潜在消费者，前端不使用。
 */
export interface Worker {
  worker_id: string
  state: 'idle' | 'busy'
  current_work_item_id: string | null
  last_seen: string
}

export interface ListResponse<T> {
  items: T[]
}
