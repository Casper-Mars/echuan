/**
 * 驿传平台 HTTP 客户端 —— 工具层与平台交互的**唯一出口**。
 *
 * 职责：
 * 1. 拼接 `baseUrl + path`，query 参数统一编码；
 * 2. 每个请求带 `X-Actor`，有 body 时才带 `content-type: application/json`；
 * 3. 按 `config.requestTimeoutMs` 超时，超时/网络失败都转成可读中文错误；
 * 4. 解析成功体与错误体，把平台错误码翻译成「下一步该做什么」；
 * 5. 204 = 「无活」，返回明确结果而不是抛异常。
 *
 * 设计约束：
 * - 本文件是 `kit/src` 里**唯一**调用 `fetch(` 的地方；
 * - `fetchImpl` 可注入（默认 `globalThis.fetch`），单测不连真实平台；
 * - 不读环境变量、不写磁盘、不缓存响应、不存业务状态；
 * - 只暴露工具包需要的 20 个客户端方法（{@link TOOL_METHODS}），**没有任何门禁动作**
 *   （accept / decide / cancel / rework / approve / reject 一律不提供）；
 * - 与 `api/openapi.yaml` 一一对应：{@link TOOL_OPERATIONS} 记录每个方法对应的
 *   `METHOD /path` 与 `operationId`，单测会解析 yaml 断言这 14 条确实存在。
 *
 * 任务-03 的 worker 注册/心跳方法由 {@link createWorkerClient} 提供，走
 * **独立命名空间**，不进入 {@link TOOL_METHODS}，因此不会破坏「恰好 20 个」的断言；
 * 两者共用 `requestRaw` / `requestJson`，仍然是全插件唯一的 fetch 出口。
 */
import {
  WORKER_METADATA_MAX_BYTES,
  metadataByteLength,
  type ResolvedConfig,
} from './config.js'
import {
  DEFAULT_RETRY_AFTER_MS,
  EchuanApiError,
  EchuanClientError,
  EchuanNetworkError,
  EchuanTimeoutError,
  EchuanValidationError,
  composeApiMessage,
  fallbackCodeForStatus,
  hintForCode,
  parseRetryAfter,
  type ErrorDetail,
} from './errors.js'

/** `fetch` 的最小签名；注入假实现时无需依赖 DOM 类型。 */
export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>

/** query 参数值；数组按 `key=v1&key=v2` 展开（openapi `explode: true`）。 */
export type QueryValue =
  | string
  | number
  | boolean
  | null
  | undefined
  | readonly (string | number)[]

export type QueryParams = Record<string, QueryValue>

/** 核心请求层依赖，单独创建便于测试与任务-03 复用。 */
export interface RequestContext {
  config: ResolvedConfig
  fetchImpl: FetchLike
}

/** 客户端构造项。 */
export interface ClientOptions {
  /** 默认 `globalThis.fetch`；单测注入假实现。 */
  fetchImpl?: FetchLike
}

/** 一次请求的描述。 */
export interface RequestSpec {
  method: 'GET' | 'POST' | 'PATCH' | 'DELETE'
  /** 必须以 `/` 开头（契约里都是 `/v1/...`）。 */
  path: string
  query?: QueryParams
  /** 有值时序列化为 JSON 并带 `content-type`；GET 不传。 */
  body?: unknown
  /** 额外请求头（如 `If-Match`）；与 `X-Actor` / `content-type` 合并。 */
  headers?: Record<string, string>
}

/** 核心请求层的原始响应（未解析业务语义）。 */
export interface RawResponse {
  status: number
  text: string
  headers: Headers
  requestId: string | undefined
}

/** 二进制响应（下载资料包用）：`bytes` 原样返回，不做文本解码。 */
export interface BytesResponse {
  status: number
  bytes: Uint8Array
  headers: Headers
  requestId: string | undefined
}

/** 成功结果：`data` 已按方法声明解析。 */
export interface OkResult<T> {
  kind: 'ok'
  status: number
  data: T
  requestId?: string
}

/** 204「无活」结果：不是错误，任何 204 都走这条路径。 */
export interface NoWorkResult {
  kind: 'empty'
  status: 204
  message: string
  requestId?: string
}

export type ApiResult<T> = OkResult<T> | NoWorkResult

/** 204 的统一文案。 */
export const NO_WORK_MESSAGE =
  '无活：平台当前没有可领取的任务（HTTP 204）。'
  + '下一步：按退避策略稍后再试，不要把 204 当错误重试。'

// ---------------------------------------------------------------------------
// 平台数据结构（只声明本工具包实际读取的字段，其余透传）
// ---------------------------------------------------------------------------

export interface ClaimedTask {
  id: string
  project_id: string
  title: string
  body_md: string
  attempt: number
  priority: number
}

export interface ClaimLease {
  lease_id: string
  until: string
  ttl_seconds: number
}

export interface ClaimContext {
  spec_version: number
  spec_digest: string
  product: { id: string; name: string; git_url: string }
}

export interface ClaimResponse {
  task: ClaimedTask
  lease: ClaimLease
  context: ClaimContext
}

export interface HeartbeatResponse {
  lease_id: string
  until: string
  ttl_seconds: number
}

export interface TaskDetail {
  id?: string
  task_id?: string
  project_id?: string
  status?: number
  status_name?: string
  [key: string]: unknown
}

export interface Task {
  id?: string
  [key: string]: unknown
}

export interface TaskList {
  items: Task[]
  next_cursor?: string
}

export interface Project {
  id?: string
  [key: string]: unknown
}

export interface ProjectList {
  items: Project[]
  next_cursor?: string
}

export interface Product {
  id?: string
  product_id?: string
  name?: string
  git_url?: string
  description?: string
  license?: string
  project_count?: number
  create_time?: string
  update_time?: string
  [key: string]: unknown
}

export interface ProductList {
  items: Product[]
  next_cursor?: string
}

export interface InboxItem {
  kind?: string
  id?: string
  state?: number
  state_name?: string
  title?: string
  update_time?: string
  age_seconds?: number
  stale?: boolean
  reason?: string | null
  [key: string]: unknown
}

export interface InboxResponse {
  items: InboxItem[]
  count: number
  review_stale_hours: number
  generated_at: string
}

export interface MaterialBody {
  /** 物料字节流（按文本读取；物料契约是 markdown）。 */
  content: string
  contentType: string
  etag: string | undefined
  materialId: string | undefined
  materialVersion: number | undefined
}

/** 项目详情（`ProjectDetail`）：在列表项之上带需求/方案物料元信息。 */
export interface ProjectDetail extends Project {
  requirement?: MaterialMeta | null
  spec?: MaterialMeta | null
}

/** 物料元信息（`Material` / `MaterialVersion`）里本工具包读取的字段。 */
export interface MaterialMeta {
  material_id?: string
  kind?: number
  version?: number
  title?: string
  sha256?: string
  size?: number
  previous_material_id?: string
  uploaded_by?: string
  create_time?: string
  [key: string]: unknown
}

/** 独立技术方案（`SpecDocument`）。 */
export interface SpecDocument {
  spec_id: string
  project_id: string
  title: string
  version: number
  material: MaterialMeta
  body_md?: string
  versions?: MaterialMeta[]
  [key: string]: unknown
}

/** `POST /v1/projects/plans` 的单张任务卡入参（`depends_on` 为 1-based 批内序号）。 */
export interface PlanTaskInput {
  title: string
  body_md: string
  priority?: number
  depends_on?: number[]
}

/** `PlanResponse.tasks` 里的一行。 */
export interface PlanTaskResult {
  id: string
  seq_no: number
  title: string
  priority: number
  status: number
  status_name: string
  /** 平台返回的是任务 id（不是批内序号）。 */
  depends_on: string[]
  [key: string]: unknown
}

/** 批量建卡回包（`PlanResponse`）。 */
export interface PlanResponse {
  project_id: string
  status: number
  status_name: string
  project_version: number
  tasks: PlanTaskResult[]
  [key: string]: unknown
}

// ---------------------------------------------------------------------------
// 方法入参
// ---------------------------------------------------------------------------

export interface ClaimTaskInput {
  product: string
  task_id?: string
  worker_id?: string
}

export interface RenewLeaseInput {
  /** 任务 id，走 query `?id=`。 */
  id: string
  lease_id: string
}

export interface ReportTaskInput {
  id: string
  lease_id: string
  summary_md: string
}

export interface CreateProjectInput {
  product_id: string
  title: string
  /** 首版需求正文（可选；缺失/空白时项目建为 requirement_draft）。 */
  requirement_md?: string
}

export interface WriteRequirementInput {
  /** 项目编号，走 query `?id=`。 */
  id: string
  body_md: string
}

export interface SubmitProjectInput {
  /** 项目编号，走 query `?id=`。 */
  id: string
}

export interface CreateSpecInput {
  project_id: string
  /** 方案标题，契约必填（POST /v1/specs 的 required 含 title）。 */
  title: string
  body_md: string
}

export interface ReviseSpecInput {
  /** 方案编号，走 query `?id=`。 */
  id: string
  /** 当前方案版本，走 `If-Match` 头（不放进 body）。 */
  version: number
  /** 方案标题，契约必填（PATCH /v1/specs 的 required 含 title）。 */
  title: string
  body_md: string
}

export interface SubmitPlanInput {
  /** 项目编号，走 query `?id=`。 */
  id: string
  tasks: PlanTaskInput[]
}

export interface CreateTaskInput {
  /** 项目编号，走 query `?project_id=`。 */
  project_id: string
  title: string
  body_md: string
  priority?: number
  /** 依赖的任务 id（平台返回的是任务 id，不是批内序号）。 */
  depends_on?: string[]
}

export interface UpdateTaskInput {
  /** 任务编号，走 query `?id=`。 */
  id: string
  title?: string
  body_md?: string
  priority?: number
}

export interface DeleteTaskInput {
  /** 任务编号，走 query `?id=`。 */
  id: string
}

export interface UpdateProjectInput {
  /** 项目编号，走 query `?id=`。 */
  id: string
  title: string
}

export interface CreateProductInput {
  product_id: string
  name: string
  git_url: string
  description?: string
  license?: string
}

export interface DownloadProjectPackageInput {
  /** 项目编号，走 query `?id=`。 */
  id: string
}

export interface InboxQuery {
  limit?: number
}

export interface ProductsQuery {
  /** 契约允许 `?id=` 取详情；列表工具只用 `limit` / `cursor`。 */
  id?: string
  limit?: number
  cursor?: string
}

export interface ProjectsQuery {
  id?: string
  product_id?: string
  /** 契约里 projects.status 是**单个 integer**，不是数组。 */
  status?: number
  limit?: number
  cursor?: string
}

export interface TasksQuery {
  id?: string
  project_id?: string
  status?: readonly number[]
  limit?: number
  cursor?: string
}

export interface MaterialsQuery {
  id: string
}

/** 20 个客户端方法的集合（顺序即客户端对象属性顺序）。 */
export const TOOL_METHODS = [
  'claimTask',
  'renewLease',
  'reportTask',
  'createProject',
  'writeRequirement',
  'submitProject',
  'createSpec',
  'reviseSpec',
  'submitPlan',
  'createTask',
  'updateTask',
  'deleteTask',
  'updateProject',
  'createProduct',
  'downloadProjectPackage',
  'getInbox',
  'listProducts',
  'listProjects',
  'listTasks',
  'getMaterials',
] as const

export type ToolMethodName = (typeof TOOL_METHODS)[number]

/**
 * 20 个方法 ↔ openapi operation 的对应表。
 *
 * `tool` 是客户端方法名；`method`/`path`/`operationId` 必须能在
 * `api/openapi.yaml` 的 `paths` 下原样找到（单测解析 yaml 校验）。
 *
 * 注意：22 个工具共用这 20 个 operation——`listProjects` / `listTasks`
 * 各由一个 GET 同时承担「列表」与「按 id 取详情」两种工具语义。
 */
export const TOOL_OPERATIONS = [
  { tool: 'claimTask', method: 'POST', path: '/v1/tasks/claim', operationId: 'claimTask' },
  { tool: 'renewLease', method: 'POST', path: '/v1/tasks/heartbeat', operationId: 'heartbeatTask' },
  { tool: 'reportTask', method: 'POST', path: '/v1/tasks/report', operationId: 'reportTask' },
  { tool: 'createProject', method: 'POST', path: '/v1/projects', operationId: 'createProject' },
  { tool: 'writeRequirement', method: 'POST', path: '/v1/projects/requirement', operationId: 'writeProjectRequirement' },
  { tool: 'submitProject', method: 'POST', path: '/v1/projects/submit', operationId: 'submitProject' },
  { tool: 'createSpec', method: 'POST', path: '/v1/specs', operationId: 'createSpecDocument' },
  { tool: 'reviseSpec', method: 'PATCH', path: '/v1/specs', operationId: 'reviseSpecDocument' },
  { tool: 'submitPlan', method: 'POST', path: '/v1/projects/plans', operationId: 'submitProjectPlan' },
  { tool: 'createTask', method: 'POST', path: '/v1/tasks', operationId: 'createTask' },
  { tool: 'updateTask', method: 'PATCH', path: '/v1/tasks', operationId: 'updateTask' },
  { tool: 'deleteTask', method: 'DELETE', path: '/v1/tasks', operationId: 'deleteTask' },
  { tool: 'updateProject', method: 'PATCH', path: '/v1/projects', operationId: 'updateProject' },
  { tool: 'createProduct', method: 'POST', path: '/v1/products', operationId: 'createProduct' },
  {
    tool: 'downloadProjectPackage',
    method: 'GET',
    path: '/v1/projects/package',
    operationId: 'downloadProjectPackage',
  },
  { tool: 'getInbox', method: 'GET', path: '/v1/inbox', operationId: 'listInbox' },
  { tool: 'listProducts', method: 'GET', path: '/v1/products', operationId: 'listOrGetProducts' },
  { tool: 'listProjects', method: 'GET', path: '/v1/projects', operationId: 'listOrGetProjects' },
  { tool: 'listTasks', method: 'GET', path: '/v1/tasks', operationId: 'listOrGetTasks' },
  { tool: 'getMaterials', method: 'GET', path: '/v1/materials', operationId: 'downloadMaterial' },
] as const

/** 客户端对外的 20 个方法。 */
export interface EchuanClient {
  /**
   * `POST /v1/tasks/claim`（operationId `claimTask`）。
   * 无卡可领时平台返回 204，本方法返回 {@link NoWorkResult}，不抛错。
   */
  claimTask(input: ClaimTaskInput): Promise<ApiResult<ClaimResponse>>
  /** `POST /v1/tasks/heartbeat`（operationId `heartbeatTask`）。 */
  renewLease(input: RenewLeaseInput): Promise<ApiResult<HeartbeatResponse>>
  /** `POST /v1/tasks/report`（operationId `reportTask`）。 */
  reportTask(input: ReportTaskInput): Promise<ApiResult<TaskDetail>>
  /** `POST /v1/projects`（operationId `createProject`）；返回 `ProjectDetail`。 */
  createProject(input: CreateProjectInput): Promise<ApiResult<ProjectDetail>>
  /** `POST /v1/projects/requirement?id=`（operationId `writeProjectRequirement`）。 */
  writeRequirement(input: WriteRequirementInput): Promise<ApiResult<ProjectDetail>>
  /** `POST /v1/projects/submit?id=`（operationId `submitProject`）；无 body。 */
  submitProject(input: SubmitProjectInput): Promise<ApiResult<Project>>
  /** `POST /v1/specs`（operationId `createSpecDocument`）。 */
  createSpec(input: CreateSpecInput): Promise<ApiResult<SpecDocument>>
  /** `PATCH /v1/specs?id=`（operationId `reviseSpecDocument`）；`If-Match: <version>`。 */
  reviseSpec(input: ReviseSpecInput): Promise<ApiResult<SpecDocument>>
  /** `POST /v1/projects/plans?id=`（operationId `submitProjectPlan`）。 */
  submitPlan(input: SubmitPlanInput): Promise<ApiResult<PlanResponse>>
  /** `POST /v1/tasks?project_id=`（operationId `createTask`）。 */
  createTask(input: CreateTaskInput): Promise<ApiResult<TaskDetail>>
  /** `PATCH /v1/tasks?id=`（operationId `updateTask`）；无 If-Match。 */
  updateTask(input: UpdateTaskInput): Promise<ApiResult<TaskDetail>>
  /** `DELETE /v1/tasks?id=`（operationId `deleteTask`）；204 表示已删除。 */
  deleteTask(input: DeleteTaskInput): Promise<ApiResult<TaskDetail>>
  /** `PATCH /v1/projects?id=`（operationId `updateProject`）；只发 title。 */
  updateProject(input: UpdateProjectInput): Promise<ApiResult<Project>>
  /** `POST /v1/products`（operationId `createProduct`）。 */
  createProduct(input: CreateProductInput): Promise<ApiResult<Product>>
  /** `GET /v1/projects/package?id=`（operationId `downloadProjectPackage`）；返回 zip 字节。 */
  downloadProjectPackage(input: DownloadProjectPackageInput): Promise<BytesResponse>
  /** `GET /v1/inbox`（operationId `listInbox`）。 */
  getInbox(query?: InboxQuery): Promise<ApiResult<InboxResponse>>
  /** `GET /v1/products`（operationId `listOrGetProducts`）；带 `id` 时为详情。 */
  listProducts(query?: ProductsQuery): Promise<ApiResult<Product | ProductList>>
  /** `GET /v1/projects`（operationId `listOrGetProjects`）；带 `id` 时为详情。 */
  listProjects(query?: ProjectsQuery): Promise<ApiResult<Project | ProjectList>>
  /** `GET /v1/tasks`（operationId `listOrGetTasks`）；带 `id` 时为详情。 */
  listTasks(query?: TasksQuery): Promise<ApiResult<TaskDetail | TaskList>>
  /** `GET /v1/materials`（operationId `downloadMaterial`）；`?id=` 必填。 */
  getMaterials(query: MaterialsQuery): Promise<ApiResult<MaterialBody>>
}

// ---------------------------------------------------------------------------
// Worker 心跳（独立命名空间，不计入 TOOL_METHODS）
// ---------------------------------------------------------------------------

export type WorkerState = 'idle' | 'busy'

/** `POST /v1/workers/heartbeat` 的请求体；首次注册时 `worker_id` 字段**完全不出现**。 */
export interface WorkerHeartbeatInput {
  worker_id?: string
  state: WorkerState
  metadata: Record<string, unknown>
}

/** 心跳回包里的 worker 快照（只声明本工具包读取的字段）。 */
export interface WorkerSnapshot {
  worker_id: string
  presence?: string
  state?: WorkerState
  current_task_id?: string | null
  metadata?: Record<string, unknown>
  first_seen?: string
  last_seen?: string
  heartbeat_interval_ms?: number
  lease_ttl_seconds?: number
  [key: string]: unknown
}

/** 指令公共形状；当前枚举只有 `run_task`。 */
export interface WorkerCommandBase {
  kind: string
}

export interface WorkerRunTaskCommand extends WorkerCommandBase {
  kind: 'run_task'
  task: ClaimedTask
  lease: ClaimLease
  context: ClaimContext
}

/** 心跳返回的一条指令；未识别的 kind 由调用方忽略。 */
export type WorkerCommand = WorkerRunTaskCommand | WorkerCommandBase

/** `POST /v1/workers/heartbeat` 的 200 回包。 */
export interface WorkerHeartbeatPayload {
  worker: WorkerSnapshot
  /** 无任务时恒为 `[]`（不会是 null 或省略）。 */
  commands: WorkerCommand[]
  [key: string]: unknown
}

/** worker 心跳客户端；与 20 个客户端方法分开，避免污染 `TOOL_METHODS`。 */
export interface WorkerClient {
  /** `POST /v1/workers/heartbeat`（operationId `heartbeatWorker`）。 */
  heartbeat(input: WorkerHeartbeatInput): Promise<ApiResult<WorkerHeartbeatPayload>>
}

// ---------------------------------------------------------------------------
// 核心请求层
// ---------------------------------------------------------------------------

/** 校验并创建请求上下文；`baseUrl` 非法时在发请求前直接报错。 */
export function createRequestContext(
  config: ResolvedConfig,
  options: ClientOptions = {},
): RequestContext {
  assertUsableBaseUrl(config.baseUrl)
  return {
    config,
    fetchImpl: options.fetchImpl ?? ((input, init) => fetch(input, init)),
  }
}

/**
 * `worker.metadata` 超过 {@link WORKER_METADATA_MAX_BYTES} 时在本地直接拒绝，
 * 不等服务端 413。任务-03 的 worker 心跳发送 `{ metadata }` 时会自动经过这条校验。
 */
export function assertWorkerMetadataSize(metadata: Record<string, unknown>): void {
  let bytes: number
  try {
    bytes = metadataByteLength(metadata)
  } catch (err) {
    throw new EchuanValidationError(messageOf(err), 'invalid_argument', 'metadata')
  }
  if (bytes > WORKER_METADATA_MAX_BYTES) {
    throw new EchuanValidationError(
      `worker.metadata 序列化后 ${bytes}B，超过平台上限 ${WORKER_METADATA_MAX_BYTES}B。`
        + '下一步：缩小 metadata（删减字段或缩短取值）后再发送，不要原样重试。',
      'too_large',
      'metadata',
    )
  }
}

/**
 * 发起一次请求并返回原始响应；非 2xx 抛 {@link EchuanApiError}。
 *
 * 这是插件里唯一接触 `fetch` 的地方。请求发出前会做序列化与 metadata 预算校验，
 * 因此 D-01（metadata > 8KiB）不会消耗一次网络往返。
 */
export async function requestRaw(
  ctx: RequestContext,
  spec: RequestSpec,
): Promise<RawResponse> {
  if (!spec.path.startsWith('/')) {
    throw new EchuanValidationError(
      `请求路径必须以 / 开头，收到 ${JSON.stringify(spec.path)}。`
        + '下一步：修正客户端实现（契约路径都形如 /v1/...）。',
      'invalid_argument',
      'path',
    )
  }

  const bodyText = serializeBody(spec.body)
  const url = buildUrl(ctx.config.baseUrl, spec.path, spec.query)
  const headers: Record<string, string> = { 'X-Actor': ctx.config.actor }
  if (bodyText !== undefined) headers['content-type'] = 'application/json'
  if (spec.headers !== undefined) Object.assign(headers, spec.headers)

  const timeoutMs = ctx.config.requestTimeoutMs
  const controller = new AbortController()
  let timedOut = false
  const timer = setTimeout(() => {
    timedOut = true
    controller.abort()
  }, timeoutMs)

  const startedAt = Date.now()
  let response: Response
  try {
    response = await ctx.fetchImpl(url, {
      method: spec.method,
      headers,
      ...(bodyText === undefined ? {} : { body: bodyText }),
      signal: controller.signal,
    })
  } catch (err) {
    // fetch 失败时立刻释放定时器；响应体读取阶段仍复用同一超时。
    clearTimeout(timer)
    const elapsedMs = Date.now() - startedAt
    if (timedOut || isAbortError(err)) {
      throw new EchuanTimeoutError(timeoutMs, elapsedMs)
    }
    throw new EchuanNetworkError(elapsedMs)
  }

  let text: string
  try {
    text = await response.text()
  } catch (err) {
    const elapsedMs = Date.now() - startedAt
    if (timedOut || isAbortError(err)) {
      throw new EchuanTimeoutError(timeoutMs, elapsedMs)
    }
    throw new EchuanNetworkError(elapsedMs)
  } finally {
    clearTimeout(timer)
  }

  if (!response.ok) {
    throw toApiError(response, text)
  }

  const requestId = response.headers.get('x-request-id') ?? undefined
  return { status: response.status, text, headers: response.headers, requestId }
}

/**
 * 二进制响应：返回 `Uint8Array` + headers + requestId，不做文本解码。
 *
 * 与 {@link requestRaw} 共用同一套超时 / 网络错误处理；非 2xx 时仍把响应体
 * 按 UTF-8 解码后交给 `toApiError`（平台的错误体是 JSON），保证错误码/提示一致。
 */
export async function requestBytes(
  ctx: RequestContext,
  spec: RequestSpec,
): Promise<BytesResponse> {
  if (!spec.path.startsWith('/')) {
    throw new EchuanValidationError(
      `请求路径必须以 / 开头，收到 ${JSON.stringify(spec.path)}。`
        + '下一步：修正客户端实现（契约路径都形如 /v1/...）。',
      'invalid_argument',
      'path',
    )
  }

  const bodyText = serializeBody(spec.body)
  const url = buildUrl(ctx.config.baseUrl, spec.path, spec.query)
  const headers: Record<string, string> = { 'X-Actor': ctx.config.actor }
  if (bodyText !== undefined) headers['content-type'] = 'application/json'
  if (spec.headers !== undefined) Object.assign(headers, spec.headers)

  const timeoutMs = ctx.config.requestTimeoutMs
  const controller = new AbortController()
  let timedOut = false
  const timer = setTimeout(() => {
    timedOut = true
    controller.abort()
  }, timeoutMs)

  const startedAt = Date.now()
  let response: Response
  try {
    response = await ctx.fetchImpl(url, {
      method: spec.method,
      headers,
      ...(bodyText === undefined ? {} : { body: bodyText }),
      signal: controller.signal,
    })
  } catch (err) {
    clearTimeout(timer)
    const elapsedMs = Date.now() - startedAt
    if (timedOut || isAbortError(err)) {
      throw new EchuanTimeoutError(timeoutMs, elapsedMs)
    }
    throw new EchuanNetworkError(elapsedMs)
  }

  let buffer: ArrayBuffer
  try {
    buffer = await response.arrayBuffer()
  } catch (err) {
    const elapsedMs = Date.now() - startedAt
    if (timedOut || isAbortError(err)) {
      throw new EchuanTimeoutError(timeoutMs, elapsedMs)
    }
    throw new EchuanNetworkError(elapsedMs)
  } finally {
    clearTimeout(timer)
  }

  if (!response.ok) {
    const text = new TextDecoder().decode(buffer)
    throw toApiError(response, text)
  }

  const requestId = response.headers.get('x-request-id') ?? undefined
  return { status: response.status, bytes: new Uint8Array(buffer), headers: response.headers, requestId }
}

/** JSON 响应包装：204 统一转成「无活」，2xx 解析 JSON。 */
export async function requestJson<T>(
  ctx: RequestContext,
  spec: RequestSpec,
): Promise<ApiResult<T>> {
  const raw = await requestRaw(ctx, spec)
  if (raw.status === 204) return emptyResult(raw.requestId)
  return okResult(raw.status, parseSuccessJson<T>(raw), raw.requestId)
}

// ---------------------------------------------------------------------------
// 客户端工厂
// ---------------------------------------------------------------------------

/**
 * 创建客户端。返回对象**恰好**有 {@link TOOL_METHODS} 里的 20 个方法。
 *
 * @param config 已归一化的配置（见 `resolveConfig`）
 * @param options `fetchImpl` 可注入，单测用假 fetch
 */
export function createClient(
  config: ResolvedConfig,
  options: ClientOptions = {},
): EchuanClient {
  const ctx = createRequestContext(config, options)

  return {
    async claimTask(input) {
      const product = requireNonEmpty(input?.product, 'product')
      const body: Record<string, string> = { product }
      if (input?.task_id !== undefined) body.task_id = requireNonEmpty(input.task_id, 'task_id')
      if (input?.worker_id !== undefined) body.worker_id = requireNonEmpty(input.worker_id, 'worker_id')
      return requestJson<ClaimResponse>(ctx, {
        method: 'POST',
        path: '/v1/tasks/claim',
        body,
      })
    },

    async renewLease(input) {
      const id = requireNonEmpty(input?.id, 'id')
      const lease_id = requireNonEmpty(input?.lease_id, 'lease_id')
      return requestJson<HeartbeatResponse>(ctx, {
        method: 'POST',
        path: '/v1/tasks/heartbeat',
        query: { id },
        body: { lease_id },
      })
    },

    async reportTask(input) {
      const id = requireNonEmpty(input?.id, 'id')
      const lease_id = requireNonEmpty(input?.lease_id, 'lease_id')
      const summary_md = requireString(input?.summary_md, 'summary_md')
      return requestJson<TaskDetail>(ctx, {
        method: 'POST',
        path: '/v1/tasks/report',
        query: { id },
        body: { lease_id, summary_md },
      })
    },

    async createProject(input) {
      const product_id = requireNonEmpty(input?.product_id, 'product_id')
      const title = requireNonEmpty(input?.title, 'title')
      const body: Record<string, unknown> = { product_id, title }
      if (input?.requirement_md !== undefined && input.requirement_md.trim() !== '') {
        body.requirement_md = input.requirement_md
      }
      return requestJson<ProjectDetail>(ctx, {
        method: 'POST',
        path: '/v1/projects',
        body,
      })
    },

    async writeRequirement(input) {
      const id = requireNonEmpty(input?.id, 'id')
      const body_md = requireNonBlank(input?.body_md, 'body_md')
      return requestJson<ProjectDetail>(ctx, {
        method: 'POST',
        path: '/v1/projects/requirement',
        query: { id },
        body: { body_md },
      })
    },

    async submitProject(input) {
      const id = requireNonEmpty(input?.id, 'id')
      return requestJson<Project>(ctx, {
        method: 'POST',
        path: '/v1/projects/submit',
        query: { id },
      })
    },

    async createSpec(input) {
      const project_id = requireNonEmpty(input?.project_id, 'project_id')
      const title = requireNonEmpty(input?.title, 'title')
      const body_md = requireNonBlank(input?.body_md, 'body_md')
      // 契约要求 title 必填：始终发送，不再按“可选”省略。
      const body: Record<string, unknown> = { project_id, title, body_md }
      return requestJson<SpecDocument>(ctx, {
        method: 'POST',
        path: '/v1/specs',
        body,
      })
    },

    async reviseSpec(input) {
      const id = requireNonEmpty(input?.id, 'id')
      const version = requirePositiveInt(input?.version, 'version')
      const title = requireNonEmpty(input?.title, 'title')
      const body_md = requireNonBlank(input?.body_md, 'body_md')
      // 契约要求 title 必填：始终发送；version 仍只走 If-Match，不进 body。
      const body: Record<string, unknown> = { title, body_md }
      return requestJson<SpecDocument>(ctx, {
        method: 'PATCH',
        path: '/v1/specs',
        query: { id },
        headers: { 'If-Match': String(version) },
        body,
      })
    },

    async submitPlan(input) {
      const id = requireNonEmpty(input?.id, 'id')
      const tasks = requirePlanTasks(input?.tasks)
      return requestJson<PlanResponse>(ctx, {
        method: 'POST',
        path: '/v1/projects/plans',
        query: { id },
        body: { tasks },
      })
    },

    async createTask(input) {
      const project_id = requireNonEmpty(input?.project_id, 'project_id')
      const title = requireNonEmpty(input?.title, 'title')
      const body_md = requireNonBlank(input?.body_md, 'body_md')
      const body: Record<string, unknown> = { title, body_md }
      if (input?.priority !== undefined) body.priority = requireInteger(input.priority, 'priority')
      if (input?.depends_on !== undefined) body.depends_on = requireIdList(input.depends_on, 'depends_on')
      return requestJson<TaskDetail>(ctx, {
        method: 'POST',
        path: '/v1/tasks',
        query: { project_id },
        body,
      })
    },

    async updateTask(input) {
      const id = requireNonEmpty(input?.id, 'id')
      const body: Record<string, unknown> = {}
      if (input?.title !== undefined) body.title = requireString(input.title, 'title')
      if (input?.body_md !== undefined) body.body_md = requireString(input.body_md, 'body_md')
      if (input?.priority !== undefined) body.priority = requireInteger(input.priority, 'priority')
      // 空 patch 不本地拦截：交给平台按契约返回 400（错误原样透出）。
      return requestJson<TaskDetail>(ctx, {
        method: 'PATCH',
        path: '/v1/tasks',
        query: { id },
        body,
      })
    },

    async deleteTask(input) {
      const id = requireNonEmpty(input?.id, 'id')
      return requestJson<TaskDetail>(ctx, {
        method: 'DELETE',
        path: '/v1/tasks',
        query: { id },
      })
    },

    async updateProject(input) {
      const id = requireNonEmpty(input?.id, 'id')
      const title = requireNonEmpty(input?.title, 'title')
      return requestJson<Project>(ctx, {
        method: 'PATCH',
        path: '/v1/projects',
        query: { id },
        body: { title },
      })
    },

    async createProduct(input) {
      const product_id = requireNonEmpty(input?.product_id, 'product_id')
      const name = requireNonEmpty(input?.name, 'name')
      const git_url = requireNonEmpty(input?.git_url, 'git_url')
      const body: Record<string, unknown> = { product_id, name, git_url }
      if (input?.description !== undefined) body.description = requireString(input.description, 'description')
      if (input?.license !== undefined) body.license = requireString(input.license, 'license')
      return requestJson<Product>(ctx, {
        method: 'POST',
        path: '/v1/products',
        body,
      })
    },

    async downloadProjectPackage(input) {
      const id = requireNonEmpty(input?.id, 'id')
      return requestBytes(ctx, {
        method: 'GET',
        path: '/v1/projects/package',
        query: { id },
      })
    },

    async getInbox(query) {
      return requestJson<InboxResponse>(ctx, {
        method: 'GET',
        path: '/v1/inbox',
        ...(query === undefined ? {} : { query: toQuery(query) }),
      })
    },

    async listProducts(query) {
      return requestJson<Product | ProductList>(ctx, {
        method: 'GET',
        path: '/v1/products',
        ...(query === undefined ? {} : { query: toQuery(query) }),
      })
    },

    async listProjects(query) {
      return requestJson<Project | ProjectList>(ctx, {
        method: 'GET',
        path: '/v1/projects',
        ...(query === undefined ? {} : { query: toQuery(query) }),
      })
    },

    async listTasks(query) {
      return requestJson<TaskDetail | TaskList>(ctx, {
        method: 'GET',
        path: '/v1/tasks',
        ...(query === undefined ? {} : { query: toQuery(query) }),
      })
    },

    async getMaterials(query) {
      const id = requireNonEmpty(query?.id, 'id')
      const raw = await requestRaw(ctx, {
        method: 'GET',
        path: '/v1/materials',
        query: { id },
      })
      if (raw.status === 204) return emptyResult(raw.requestId)

      const versionHeader = raw.headers.get('x-material-version')
      const version = versionHeader === null ? undefined : Number.parseInt(versionHeader, 10)
      const material: MaterialBody = {
        content: raw.text,
        contentType: raw.headers.get('content-type') ?? '',
        etag: raw.headers.get('etag') ?? undefined,
        materialId: raw.headers.get('x-material-id') ?? undefined,
        materialVersion: version !== undefined && Number.isFinite(version) ? version : undefined,
      }
      return okResult(raw.status, material, raw.requestId)
    },
  }
}

/**
 * 创建 worker 心跳客户端。
 *
 * 与 {@link createClient} 分开：20 个客户端方法的对象键必须**恰好**是
 * {@link TOOL_METHODS}（任务-02 有断言），worker 心跳走独立命名空间。
 *
 * 首次注册时请求体里**没有** `worker_id` 键（不是空串、不是 null）；
 * `metadata` 原样上报，仍会经过 {@link assertWorkerMetadataSize} 的 8KiB 前置校验。
 */
export function createWorkerClient(
  config: ResolvedConfig,
  options: ClientOptions = {},
): WorkerClient {
  const ctx = createRequestContext(config, options)

  return {
    async heartbeat(input) {
      const state: WorkerState = input?.state === 'busy' ? 'busy' : 'idle'
      const body: Record<string, unknown> = {}
      if (input?.worker_id !== undefined && input.worker_id !== '') {
        body.worker_id = input.worker_id
      }
      body.state = state
      body.metadata = input?.metadata ?? {}

      return requestJson<WorkerHeartbeatPayload>(ctx, {
        method: 'POST',
        path: '/v1/workers/heartbeat',
        body,
      })
    },
  }
}

// ---------------------------------------------------------------------------
// 内部工具
// ---------------------------------------------------------------------------

function assertUsableBaseUrl(baseUrl: string): void {
  let parsed: URL
  try {
    parsed = new URL(baseUrl)
  } catch {
    throw new EchuanValidationError(
      `baseUrl 不是合法的 URL：${baseUrl}。下一步：改成 http://host:port 形式。`,
      'invalid_argument',
      'baseUrl',
    )
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new EchuanValidationError(
      `baseUrl 只支持 http/https，收到 ${parsed.protocol}//。下一步：检查配置项 baseUrl。`,
      'invalid_argument',
      'baseUrl',
    )
  }
}

function buildUrl(baseUrl: string, path: string, query?: QueryParams): string {
  const url = new URL(`${baseUrl}${path}`)
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value === undefined || value === null) continue
    if (Array.isArray(value)) {
      for (const item of value) url.searchParams.append(key, String(item))
    } else {
      url.searchParams.set(key, String(value))
    }
  }
  return url.toString()
}

/** 序列化 body，并在发出前执行 metadata 预算校验（D-01）。 */
function serializeBody(body: unknown): string | undefined {
  if (body === undefined) return undefined

  if (body !== null && typeof body === 'object' && 'metadata' in body) {
    const metadata = (body as { metadata?: unknown }).metadata
    if (metadata !== null && typeof metadata === 'object') {
      assertWorkerMetadataSize(metadata as Record<string, unknown>)
    }
  }

  let text: string | undefined
  try {
    text = JSON.stringify(body)
  } catch (err) {
    throw new EchuanValidationError(
      `请求体无法序列化为 JSON（${messageOf(err)}）。`
        + '下一步：移除循环引用或 BigInt 等不可序列化值后再试。',
      'invalid_argument',
      'body',
    )
  }
  if (text === undefined) {
    throw new EchuanValidationError(
      '请求体无法序列化为 JSON。下一步：传入普通对象，而不是函数或 Symbol。',
      'invalid_argument',
      'body',
    )
  }
  return text
}

function toApiError(response: Response, text: string): EchuanApiError {
  const payload = parseJsonObject(text)
  const headerRequestId = response.headers.get('x-request-id') ?? undefined

  const rawCode = payload?.code
  const code = typeof rawCode === 'string' && rawCode !== ''
    ? rawCode
    : fallbackCodeForStatus(response.status)

  const rawMessage = payload?.message
  const platformMessage = typeof rawMessage === 'string' && rawMessage !== ''
    ? rawMessage
    : `${response.status} ${response.statusText}`.trim()

  const details = toDetails(payload?.details)

  const rawRequestId = payload?.request_id
  const requestId = typeof rawRequestId === 'string' && rawRequestId !== ''
    ? rawRequestId
    : headerRequestId

  const hint = hintForCode(code, response.status)
  let retryAfterMs: number | undefined
  let retryNote: string | undefined
  if (response.status === 429 || code === 'rate_limited') {
    const parsed = parseRetryAfter(response.headers.get('retry-after'))
    if (parsed === undefined) {
      retryAfterMs = DEFAULT_RETRY_AFTER_MS
      retryNote = ` 平台未提供 Retry-After，按保守默认 ${DEFAULT_RETRY_AFTER_MS}ms 退避后再试，本客户端不会自动重试。`
    } else {
      retryAfterMs = parsed
      retryNote = ` 平台建议等待 ${parsed}ms 后再试（按 Retry-After 计算），本客户端不会自动重试。`
    }
  }

  return new EchuanApiError({
    code,
    status: response.status,
    message: composeApiMessage(response.status, code, platformMessage, hint, retryNote, details),
    platformMessage,
    hint,
    details,
    ...(requestId === undefined ? {} : { requestId }),
    ...(retryAfterMs === undefined ? {} : { retryAfterMs }),
  })
}

function parseSuccessJson<T>(raw: RawResponse): T {
  const text = raw.text.trim()
  if (text === '') return undefined as T
  try {
    return JSON.parse(text) as T
  } catch {
    const requestId = raw.requestId === undefined ? '' : `，requestId=${raw.requestId}`
    throw new EchuanClientError(
      'invalid_response',
      `平台返回 ${raw.status}，但响应体不是合法 JSON（${requestId || '无 requestId'}）。`
        + '下一步：重试；若持续出现，把该信息反馈给平台维护者。',
    )
  }
}

function parseJsonObject(text: string): Record<string, unknown> | undefined {
  if (text.trim() === '') return undefined
  try {
    const value: unknown = JSON.parse(text)
    return value !== null && typeof value === 'object' && !Array.isArray(value)
      ? (value as Record<string, unknown>)
      : undefined
  } catch {
    return undefined
  }
}

function toDetails(value: unknown): ErrorDetail[] {
  if (!Array.isArray(value)) return []
  return value
    .filter(item => item !== null && typeof item === 'object')
    .map(item => item as ErrorDetail)
}

function emptyResult(requestId: string | undefined): NoWorkResult {
  return requestId === undefined
    ? { kind: 'empty', status: 204, message: NO_WORK_MESSAGE }
    : { kind: 'empty', status: 204, message: NO_WORK_MESSAGE, requestId }
}

function okResult<T>(status: number, data: T, requestId: string | undefined): OkResult<T> {
  return requestId === undefined
    ? { kind: 'ok', status, data }
    : { kind: 'ok', status, data, requestId }
}

function toQuery(input: object): QueryParams {
  return input as unknown as QueryParams
}

function requireNonEmpty(value: unknown, field: string): string {
  if (typeof value !== 'string' || value.trim() === '') {
    throw new EchuanValidationError(
      `${field} 不能为空。下一步：检查调用参数，按 api/openapi.yaml 传入非空字符串。`,
      'invalid_argument',
      field,
    )
  }
  return value.trim()
}

/** 必填正文：保留首尾空白，只拒绝全空白（Markdown 原样保存）。 */
function requireNonBlank(value: unknown, field: string): string {
  if (typeof value !== 'string' || value.trim() === '') {
    throw new EchuanValidationError(
      `${field} 不能为空。下一步：检查调用参数，按 api/openapi.yaml 传入非空 Markdown 正文。`,
      'invalid_argument',
      field,
    )
  }
  return value
}

/** 正整数（版本号 / 批内序号）；缺失或 <=0 在本地拒绝。 */
function requirePositiveInt(value: unknown, field: string): number {
  if (typeof value !== 'number' || !Number.isInteger(value) || value <= 0) {
    throw new EchuanValidationError(
      `${field} 必须是大于 0 的整数，收到 ${String(value)}。`
        + '下一步：传入当前版本号（例如项目/方案详情里的 version），不要用字符串或小数。',
      'invalid_argument',
      field,
    )
  }
  return value
}

/** 校验批量建卡入参：非空数组，逐项校验 title/body_md/priority/depends_on。 */
function requirePlanTasks(value: unknown): PlanTaskInput[] {
  if (!Array.isArray(value) || value.length === 0) {
    throw new EchuanValidationError(
      'tasks 不能为空。下一步：至少给一张卡（title + body_md），不要发空批次。',
      'invalid_argument',
      'tasks',
    )
  }

  return value.map((item, index) => {
    if (item === null || typeof item !== 'object' || Array.isArray(item)) {
      throw new EchuanValidationError(
        `tasks[${index}] 必须是对象。下一步：按 api/openapi.yaml 传 { title, body_md }。`,
        'invalid_argument',
        'tasks',
      )
    }
    const record = item as Record<string, unknown>
    const task: PlanTaskInput = {
      title: requireNonEmpty(record['title'], `tasks[${index}].title`),
      body_md: requireNonEmpty(record['body_md'], `tasks[${index}].body_md`),
    }
    if (record['priority'] !== undefined) {
      const priority = record['priority']
      if (typeof priority !== 'number' || !Number.isInteger(priority)) {
        throw new EchuanValidationError(
          `tasks[${index}].priority 必须是整数，收到 ${String(priority)}。`,
          'invalid_argument',
          'tasks',
        )
      }
      task.priority = priority
    }
    if (record['depends_on'] !== undefined) {
      const dependsOn = record['depends_on']
      if (!Array.isArray(dependsOn)) {
        throw new EchuanValidationError(
          `tasks[${index}].depends_on 必须是数组（1-based 批内序号）。`,
          'invalid_argument',
          'tasks',
        )
      }
      task.depends_on = dependsOn.map((dep, depIndex) => {
        if (typeof dep !== 'number' || !Number.isInteger(dep) || dep <= 0) {
          throw new EchuanValidationError(
            `tasks[${index}].depends_on[${depIndex}] 必须是大于 0 的整数（1-based 批内序号）。`,
            'invalid_argument',
            'tasks',
          )
        }
        return dep
      })
    }
    return task
  })
}

function requireInteger(value: unknown, field: string): number {
  if (typeof value !== 'number' || !Number.isInteger(value)) {
    throw new EchuanValidationError(
      `${field} 必须是整数，收到 ${String(value)}。下一步：传入整数值后重试。`,
      'invalid_argument',
      field,
    )
  }
  return value
}

function requireIdList(value: unknown, field: string): string[] {
  if (!Array.isArray(value)) {
    throw new EchuanValidationError(
      `${field} 必须是字符串数组。下一步：传入任务 id 数组（不是批内序号）。`,
      'invalid_argument',
      field,
    )
  }
  return value.map((item, index) => requireNonEmpty(item, `${field}[${index}]`))
}

function requireString(value: unknown, field: string): string {
  if (typeof value !== 'string') {
    throw new EchuanValidationError(
      `${field} 必须是字符串。下一步：检查调用参数，按 api/openapi.yaml 传入正确的字段类型。`,
      'invalid_argument',
      field,
    )
  }
  return value
}

function isAbortError(err: unknown): boolean {
  return err instanceof Error && (err.name === 'AbortError' || err.name === 'TimeoutError')
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}
