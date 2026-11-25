/**
 * 任务类型 = 流程配置里某个环节的 `stage.name`（唯一对外标识）。
 *
 * 词汇由 `GET /v1/flow` 的 `stages[].name` 给出，前端不再写死联合类型：
 * 配置里加一个环节（如 `safety_review`），只要重启后端就能出现新类型，
 * 不需要改前端代码与类型。显示名一律取 DTO 的 `kind_label`（缺省 = name）。
 */
export type TaskKind = string
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
  /** 环节 name（例：`design`；配置里新加的环节也是原值），不是显示名 */
  kind: TaskKind
  /** 环节 id（= 库里的 `tasks.kind` 整数），前端不使用，留着便于排查 */
  kind_id?: number
  /** 显示名：配置的 `label`，未配置时后端回退成 name；徽标用这个 */
  kind_label?: string
  /** 交付物素材类别（`requirement|design|plan|report|context|other`） */
  material_kind?: string
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
  /**
   * 简报指向它解释的那一版正文。接口本来就在返回（`v3ClaimInput`），这里补声明：
   * `lib/current-versions` 的「简报跟随正文」（R-e）需要它。
   */
  source_material_id?: string
}

/**
 * `GET /v1/projects/materials` 的一行：材料按任务归属，项目视图是派生结果。
 * source_material_id 把简报指向它解释的那一版正文；普通产物为空。
 *
 * `task_id` 是产出这条材料的任务业务号（`internal/api/v3_handlers.go` 的 `v3ProjectMaterialItem`
 * 一直在返回它），资料区的 (任务, kind) 去重靠它。声明为可选：响应缺该字段（旧后端窗口）时
 * `lib/current-versions` 按 R-g 当作「每条各成一版」不隐藏，而不是静默藏件。
 */
export interface ProjectMaterial extends AttachmentRef {
  source_material_id?: string
  task_id?: string
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
  /**
   * 服务端按 `last_seen + worker_ttl` 推导的在线态（`domain.Presence`），只有 `active` / `offline`。
   *
   * 过期判定**只**认这个字段：`worker_ttl` 只有服务端知道，前端不再用 `last_seen` + 本地阈值兜底。
   * 类型上放宽为可选字符串：响应缺字段或取值非法时按「不是 offline」处理（不淡底、不计数），
   * 而不是把缺字段误判成过期。
   */
  presence?: string
}

export interface ListResponse<T> {
  items: T[]
}

/**
 * `GET /v1/flow` 的一个环节（响应契约见 tech-echuan-180 §7.2）。
 *
 * 只有 `name` / `label` 对界面有意义：下拉的 value 用 `name`（提交给 `POST /v1/tasks` 的也是它），
 * 显示用 `label`。`id` 落库不外显，其余字段（material/derive/...）留给运维排查。
 */
export interface FlowStage {
  id: number
  name: string
  /** 显示名；后端保证「未配置 label 时 = name」，所以界面可以无条件用 */
  label: string
  material: string
  open_cards?: string
  closing?: boolean
  title?: string
  /**
   * 阶段描述（人读文本）：v2 起这个字段只描述环节本身，**不再**当派生卡的任务说明。
   *
   * 后端投影恒定给值（未配置为 `""`）；声明为可选与 `Flow` 其它字段同口径，
   * 兼容「前端先上、后端未升」的短窗口。
   */
  description?: string
  /**
   * 派生的下游卡用的任务说明模板（v2 新增，见 echuan-188 §5 D9 / §7.1）。
   *
   * 看板不消费它（选环节只用 `name` / `label`）；声明为可选是因为升级顺序上
   * 前端可能先于后端上线，缺这个键不能影响渲染。
   */
  task?: string
  derive?: {
    mode?: string
    to?: string
    source?: string
    format?: string
    items?: unknown[]
  }
}

/**
 * `GET /v1/flow` 的快照（v2 投影见 echuan-188 §5 D9 / §7.1）。
 *
 * v2 删掉了顶层 `auto`：自动派生改由每个环节的 `stages[].derive` 内 `auto: true` 表达
 *（配置侧开关），而看板本来就不读它——界面只取 `stages[].name` / `label`，删字段无行为影响。
 */
export interface Flow {
  default: boolean
  source: string
  fingerprint: string
  stages: FlowStage[]
}
