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

export interface Worker {
  worker_id: string
  state: 'idle' | 'busy'
  current_task_id?: number | null
  current_lease_id?: string | null
  last_seen: string
}

export interface ListResponse<T> {
  items: T[]
}
