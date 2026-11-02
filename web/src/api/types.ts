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
  reason: string | null
  /** 工作项批次最近一次驳回的原因；通过或改卡后后端清空。 */
  tasks_review_reason: string | null
  /** 上述驳回的落库时间。 */
  tasks_review_rejected_at: string | null
  last_actor: string | null
  version: number
  work_item_total: number
  work_item_done: number
  /** 按 work item 状态统计；无工作项时后端省略该字段。 */
  work_item_by_state?: Record<string, number>
  /** POST /v1/projects/decide 的响应携带：本次决策绑定的资料版本。 */
  material_id?: string | null
  create_time: string
  update_time: string
}

export interface ProjectList {
  items: Project[]
  next_cursor?: string
}

export interface ProjectCreateRequest {
  product_id: string
  title: string
  /** 需求正文，可选；缺失时后端创建为「需求待提交」。 */
  requirement_md?: string
}

export interface SpecDocument {
  spec_id: string
  project_id: string
  title: string
  version: number
  material: MaterialVersion
  body_md?: string
  versions?: MaterialVersion[]
}

/** GET /v1/projects?id= 的详情：在列表字段上追加当前版 Material 元信息。 */
export interface ProjectDetail extends Project {
  requirement: Material | null
  spec: Material | null
}

/** 工作项的四类 kind。 */
export type WorkItemKind = 'requirement' | 'design' | 'breakdown' | 'dev'

/** 工作项的七个文本状态。 */
export type WorkItemState =
  | 'blocked'
  | 'queued'
  | 'leased'
  | 'submitted'
  | 'accepted'
  | 'failed'
  | 'cancelled'

export interface WorkItem {
  work_id: string
  project_id: string
  parent_id: string | null
  kind: WorkItemKind
  seq_no: number
  title: string
  body_md: string
  priority: number
  state: WorkItemState
  /** 当前持有该卡的 worker；仅 leased 且领取时绑定了 worker 才有值。 */
  worker_id: string | null
  attempt: number
  max_attempts: number
  reason: string | null
  version: number
  depends_on: string[]
  result_material_id: string | null
  create_time: string
  update_time: string
  lease: WorkItemLease | null
}

export interface WorkItemList {
  items: WorkItem[]
  next_cursor?: string
}

/** 工作项进行中的租约（lease_id + 到期时间）。 */
export interface WorkItemLease {
  lease_id: string
  until: string
}

/** GET /v1/work-items?id= 的详情：在列表字段上追加结果资料。 */
export interface WorkItemDetail extends WorkItem {
  result: Material | null
  /** POST /v1/work-items/decide 的响应携带：本次决策绑定的结果资料。 */
  material_id?: string | null
}

/** POST /v1/work-items 的请求体。 */
export interface WorkItemCreateRequest {
  project_id: string
  kind: WorkItemKind
  title: string
  body_md: string
  priority?: number
  parent_id?: string
  depends_on?: string[]
}

/** PATCH /v1/work-items?work_id= 的请求体；只提交要修改的字段。 */
export interface WorkItemPatchRequest {
  title?: string
  body_md?: string
  priority?: number
}

/** POST /v1/work-items/review 的请求体；reject 时 reason 必填。 */
export interface WorkItemReviewRequest {
  project_id: string
  decision: 'approve' | 'reject'
  reason?: string
}

/** POST /v1/work-items/review 的 200 响应。 */
export interface WorkItemReviewResult {
  project_id: string
  status: number
  version: number
  ready_work_items: number
  reason: string | null
  rejected_at: string | null
}

/** 在线状态由服务端按 last_seen 与 worker_ttl 推导，不落库。 */
export type WorkerPresence = 'active' | 'offline'

/** worker 最近一次心跳声明的状态（声明值，不等于占用事实）。 */
export type WorkerState = 'idle' | 'busy'

/** GET /v1/workers 的单个 worker；字段恒定出现。 */
export interface Worker {
  worker_id: string
  presence: WorkerPresence
  state: WorkerState
  kinds: WorkItemKind[]
  current_work_item_id: string | null
  metadata: Record<string, unknown>
  first_seen: string
  last_seen: string
  heartbeat_interval_ms: number
  lease_ttl_seconds: number
}

/** GET /v1/workers（不带 id）的响应，按 last_seen 倒序。 */
export interface WorkerListResponse {
  items: Worker[]
  next_cursor?: string
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

/** 工作项裁决（approve 用于待验收，reject 用于待验收/已失败）。 */
export type WorkItemDecision = 'approve' | 'reject'

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
