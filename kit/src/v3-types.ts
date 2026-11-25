/** V3 平台的 JSON 形状（只声明工具包读取的字段）。 */
import type { ApiResult } from './client.js'

/**
 * 环节 name：平台流程里的**对外标识**（ADR-0003 D2‴），不是 kit 的枚举。
 *
 * kit **不维护词汇**：能接哪些环节由平台配置决定（`GET /v1/flow` 的
 * `stages[].name`），加环节不需要升包。拼错的名字由平台 400 裁决并回可用清单，
 * 本地只校验形态（非空、≤32 字符），见 `tools/v3.ts` 的 `requireKindName`。
 */
export type V3TaskKind = string

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
  /** 环节 id（`tasks.kind` 落库的整数）；旧平台不带。 */
  kind_id?: number
  /** 环节显示名（label，缺省等于 name）；旧平台不带。 */
  kind_label?: string
  /**
   * 本环节交付物的素材类别（`requirement` / `design` / `plan` / `report` /
   * `context` / `other`），由平台按流程配置裁定。旧平台不带，历史卡也可能是空串
   * （环节已从配置里删掉）——两种情况都由调用方回落 `report`。
   */
  material_kind?: string
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

/** 领卡回包里的输入物料（`claim` 的 `inputs[]`）。 */
export interface V3ClaimInput {
  material_id: string
  title?: string
  kind?: string
  role?: string
}

/**
 * 领卡回包：平台返回的是 envelope（`{ task, project, product, inputs, upstream, … }`），
 * 不是扁平的任务对象。工具层必须从 `task` 里取 task_id / lease_id。
 */
export interface V3ClaimEnvelope {
  task: V3Task
  inputs?: V3ClaimInput[]
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

/**
 * 流程环节（`GET /v1/flow` 的 `stages[]`）。
 *
 * **只声明工具渲染用到的三个字段**：`id`（= `tasks.kind`）、`name`（对外标识，建卡时
 * 填的 kind）与 `description`（阶段描述，人读文本）。平台的 `label` / `material` /
 * `derive` / `source` / `fingerprint` 等字段不进这个口径（方案 N7）——这里不声明，
 * 渲染层也就没有顺手多给的入口。
 */
export interface V3FlowStage {
  /** 环节 id（平台落库的整数）；异常回包里可能是字符串，渲染层两种都认。 */
  id: number | string
  /** 环节 name（对外标识，agent 建卡 / 认领时按它填 `kind`）。 */
  name: string
  /** 阶段描述；未配置时是空串。 */
  description: string
}

/**
 * `GET /v1/flow` 的回包。
 *
 * 只消费 `stages`；`source` / `fingerprint` / `default` 等运维字段不进口径。
 * `stages` 声明为可选：形状不对时由渲染层按空清单处理，不在这里抛错。
 */
export interface V3Flow {
  stages?: V3FlowStage[]
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
  listV3Projects(query?: {
    product_id?: string
    status?: 'open' | 'closed'
    after_id?: number
    limit?: number
  }): Promise<ApiResult<V3List<V3Project>>>
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
  createV3Material(input: { kind: string; title: string; body_md: string; source_material_id?: string }): Promise<ApiResult<V3Material>>
  attachV3(input: {
    project_id?: string
    task_id?: string
    material_id: string
    role: 'input' | 'output'
    sort_no?: number
  }): Promise<ApiResult<V3Attachment>>
  heartbeatV3(input: { worker_id: string; state: "idle" | "busy"; kinds: readonly V3TaskKind[]; metadata: Record<string, unknown> }): Promise<ApiResult<unknown>>
  getV3Inbox(): Promise<ApiResult<V3List<V3Task>>>
  /** `GET /v1/flow`：当前流程的环节表（渲染只取 id / name / 阶段描述）。 */
  getV3Flow(): Promise<ApiResult<V3Flow>>
  listV3Events(query: { task_id: string; limit?: number }): Promise<ApiResult<V3List<V3Event>>>
  listV3Decisions(query: { task_id: string; limit?: number }): Promise<ApiResult<V3List<V3Decision>>>
  listV3Workers(query?: { limit?: number }): Promise<ApiResult<V3List<V3Worker>>>
}
