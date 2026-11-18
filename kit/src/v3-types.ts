/** V3 平台的 JSON 形状（只声明工具包读取的字段）。 */
import type { ApiResult } from './client.js'

export type V3TaskKind = 'requirement' | 'design' | 'breakdown' | 'dev'

export type V3TaskStatus =
  | 'pending'
  | 'ready'
  | 'in_progress'
  | 'in_review'
  | 'done'
  | 'failed'
  | 'cancelled'

export interface V3Project {
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

export interface V3Task {
  task_id: string
  project_id: string
  parent_task_id?: string | null
  kind: V3TaskKind
  title: string
  description_md: string
  context_md: string
  priority: number
  status: V3TaskStatus
  attempt: number
  max_attempts: number
  lease_id?: string | null
  lease_until?: string | null
  worker_id?: string | null
  reason?: string | null
  create_time: string
  update_time: string
}

export interface V3Attachment {
  id: number
  project_id?: number | null
  task_id?: number | null
  material_id: number
  role: 'input' | 'output'
  sort_no: number
  create_time: string
}

export interface V3Event {
  id: number
  event: string
  actor?: string | null
  detail_json: string
  create_time: string
}

export interface V3Decision {
  id: number
  task_id: string
  gate: V3TaskKind
  material_id?: number | null
  decision: 'approve' | 'reject'
  decided_by: string
  reason?: string | null
  create_time: string
}

export interface V3Material {
  material_id: string
  kind: string
  version: number
  title: string
}

export interface V3Worker {
  worker_id: string
  state: 'idle' | 'busy'
  current_task_id?: number | null
  current_lease_id?: string | null
  last_seen: string
}

export interface V3List<T> {
  items: T[]
}

export interface V3ProjectCreateInput {
  product_id: string
  name: string
  context_md?: string
}

export interface V3TaskCreateInput {
  project_id: string
  task_id?: string
  parent_task_id?: string
  kind: V3TaskKind
  title: string
  description_md?: string
  context_md?: string
  priority?: number
  max_attempts?: number
  depends_on?: string[]
}

export interface V3TaskUpdateInput {
  title: string
  description_md?: string
  context_md?: string
  priority?: number
}

/** V3 客户端扩展：工具层只依赖这些方法。 */
export interface V3Client {
  createV3Project(input: V3ProjectCreateInput): Promise<ApiResult<V3Project>>
  closeV3Project(input: { id: string; force?: boolean }): Promise<ApiResult<unknown>>
  reopenV3Project(input: { id: string }): Promise<ApiResult<unknown>>
  getV3Project(input: { id: string }): Promise<ApiResult<V3Project>>
  listV3Projects(query?: { status?: 'open' | 'closed'; after_id?: number; limit?: number }): Promise<ApiResult<V3List<V3Project>>>
  createV3Task(input: V3TaskCreateInput): Promise<ApiResult<V3Task>>
  updateV3Task(input: { id: string; body: V3TaskUpdateInput }): Promise<ApiResult<V3Task>>
  getV3Task(input: { id: string }): Promise<ApiResult<V3Task>>
  listV3Tasks(query?: {
    project_id?: string
    kind?: V3TaskKind
    status?: V3TaskStatus
    limit?: number
  }): Promise<ApiResult<V3List<V3Task>>>
  claimV3Task(input: { worker_id: string; kind: V3TaskKind }): Promise<ApiResult<V3Task>>
  renewV3Task(input: { task_id: string; lease_id: string }): Promise<ApiResult<{ task_id: string; lease_id: string; lease_until: string }>>
  reportV3Task(input: { task_id: string; lease_id: string; material_id: string }): Promise<ApiResult<V3Task>>
  createV3Material(input: { kind: string; title: string; body_md: string }): Promise<ApiResult<V3Material>>
  attachV3(input: {
    project_id?: string
    task_id?: string
    material_id: string
    role: 'input' | 'output'
    sort_no?: number
  }): Promise<ApiResult<V3Attachment>>
  heartbeatV3(input: { worker_id: string; state: "idle" | "busy"; kinds: readonly V3TaskKind[]; metadata: Record<string, unknown> }): Promise<ApiResult<unknown>>
  getV3Inbox(): Promise<ApiResult<V3List<V3Task>>>
  listV3Events(query: { task_id: string; limit?: number }): Promise<ApiResult<V3List<V3Event>>>
  listV3Decisions(query: { task_id: string; limit?: number }): Promise<ApiResult<V3List<V3Decision>>>
  listV3Workers(query?: { limit?: number }): Promise<ApiResult<V3List<V3Worker>>>
}
