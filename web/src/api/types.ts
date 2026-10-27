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

/** 一个物料版本（需求 / 方案 / 报告共用）。 */
export interface Material {
  material_id: string
  kind: number
  version: number
  title: string
  sha256: string
  size: number
  uploaded_by: string
  create_time: string
}

/** 版本链上的一环；首版 previous_material_id 为 null。 */
export interface MaterialVersion extends Material {
  previous_material_id: string | null
}

/** GET /v1/projects/materials 的响应。 */
export interface MaterialChain {
  items: MaterialVersion[]
  current_material_id?: string
  count: number
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

/** GET /v1/projects?id= 的详情：在列表字段上追加正文与当前版元信息。 */
export interface ProjectDetail extends Project {
  requirement: Material | null
  requirement_md: string
  spec: Material | null
  spec_excerpt: string | null
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

/** 任务进行中的租约（lease_id + 到期时间）。 */
export interface TaskLease {
  lease_id: string
  until: string
}

/** GET /v1/tasks?id= 的详情：在列表字段上追加报告与租约。 */
export interface TaskDetail extends Task {
  report: Material | null
  lease: TaskLease | null
}

/** POST /v1/products 的请求体。 */
export interface ProductCreateRequest {
  product_id: string
  name: string
  git_url: string
  description?: string
  license?: string
}

/** PATCH /v1/products?id= 的请求体；product_id 不可修改。 */
export interface ProductPatchRequest {
  name?: string
  git_url?: string
  description?: string
  license?: string
}

/** 项目评审门的裁决（approve/reject，reject 必带 reason）。 */
export type ProjectDecision = 'approve' | 'reject'

/** 任务裁决（accept/rework 作用于待验收，retry/abandon 作用于已失败）。 */
export type TaskDecision = 'accept' | 'rework' | 'retry' | 'abandon'

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
