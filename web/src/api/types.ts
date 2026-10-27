// API 契约类型（与 api/openapi.yaml 保持一致，仅取前端用到的字段）。
// 后端契约变更时同步修改这里，不在页面里散落 any。

export interface Product {
  id: string
  product_id: string
  name: string
  git_url: string
  description: string
  license: string
  project_count: number
  create_time: string
  update_time: string
}

export interface ProductList {
  items: Product[]
  next_cursor?: string
}

export interface TaskStats {
  total: number
  done: number
  by_status?: Record<string, number>
}

export interface Project {
  id: string
  project_id: string
  product_id: string
  seq_no: number
  title: string
  status: number
  status_name: string
  reason: string | null
  last_actor: string | null
  version: number
  task_stats: TaskStats
  create_time: string
  update_time: string
}

export interface ProjectList {
  items: Project[]
  next_cursor?: string
}

export interface Task {
  id: string
  task_id: string
  project_id: string
  seq_no: number
  title: string
  body_md: string
  priority: number
  status: number
  status_name: string
  attempt_count: number
  max_attempts: number
  reason: string | null
  last_actor: string | null
  version: number
  depends_on: string[]
  create_time: string
  update_time: string
}

export interface TaskList {
  items: Task[]
  next_cursor?: string
}

/** 统一错误体 {code,message,details,request_id}。 */
export interface ErrorBody {
  code: string
  message: string
  details: unknown[]
  request_id: string
}

/** 列表响应可能是列表本身，也可能是详情对象（?id= 语义）。 */
export function itemsOf<T>(value: { items?: T[] } | null | undefined): T[] {
  return Array.isArray(value?.items) ? value.items : []
}
