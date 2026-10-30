import type {
  ErrorBody,
  MaterialChain,
  Product,
  ProductCreateRequest,
  ProductList,
  ProductPatchRequest,
  Project,
  ProjectCreateRequest,
  ProjectDecision,
  ProjectDetail,
  ProjectList,
  SpecDocument,
  Task,
  TaskCreateRequest,
  TaskDecision,
  TaskDetail,
  TaskList,
  TaskPatchRequest,
  TaskReviewRequest,
  TaskReviewResult,
  Worker,
  WorkerListResponse,
} from './types'

/**
 * 同源请求层。
 *
 * - baseURL 为空：生产环境由 Go 二进制同源提供，开发环境走 Vite 代理（见 vite.config.ts）。
 * - 标识一律走 query（技术方案 §4.1），例如 /v1/products?id=APP。
 * - 每个请求带 X-Actor: human:web，可用 VITE_ACTOR 覆盖。
 * - 写请求通过 options.version 生成 If-Match（后端接受裸整数）。
 */

const ACTOR = (import.meta.env.VITE_ACTOR as string | undefined)?.trim() || 'human:web'

export type QueryValue = string | number | boolean | null | undefined

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'DELETE'
  query?: Record<string, QueryValue>
  body?: unknown
  /** 乐观并发版本号，写入 If-Match。 */
  version?: number
  signal?: AbortSignal
}

/** 后端统一错误体对应的前端异常。 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly details: unknown[]
  readonly requestId: string

  constructor(status: number, body: Partial<ErrorBody>, fallbackMessage: string) {
    super(body.message?.trim() || fallbackMessage)
    this.name = 'ApiError'
    this.status = status
    this.code = body.code?.trim() || 'unknown'
    this.details = Array.isArray(body.details) ? body.details : []
    this.requestId = body.request_id?.trim() || ''
  }
}

/** 网络层失败（后端不可达、DNS、连接被重置等）。 */
export class NetworkError extends Error {
  readonly requestId = ''
  readonly code = 'network_error'

  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options)
    this.name = 'NetworkError'
  }
}

export function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === 'AbortError'
}

function buildPath(path: string, query?: Record<string, QueryValue>): string {
  if (!query) return path
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === null || value === undefined || value === '') continue
    params.set(key, String(value))
  }
  const suffix = params.toString()
  return suffix ? `${path}?${suffix}` : path
}

async function parseBody(response: Response): Promise<unknown> {
  if (response.status === 204) return undefined
  const text = await response.text()
  if (!text) return undefined
  try {
    return JSON.parse(text)
  } catch {
    return undefined
  }
}

export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = 'GET', query, body, version, signal } = options
  const headers: Record<string, string> = {
    Accept: 'application/json',
    'X-Actor': ACTOR,
  }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (version !== undefined && version !== null) headers['If-Match'] = String(version)

  let response: Response
  try {
    response = await fetch(buildPath(path, query), {
      method,
      headers,
      signal,
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch (error) {
    if (isAbortError(error)) throw error
    throw new NetworkError('无法连接服务，请检查网络或后端进程是否已启动。', { cause: error })
  }

  const payload = await parseBody(response)

  if (!response.ok) {
    const body = (payload ?? {}) as Partial<ErrorBody>
    // 统一错误体缺失时，至少保留 HTTP 状态与 request id。
    if (!body.request_id) {
      const headerId = response.headers.get('X-Request-Id')
      if (headerId) body.request_id = headerId
    }
    throw new ApiError(response.status, body, `请求失败（HTTP ${response.status}）`)
  }

  return payload as T
}

export function listProducts(signal?: AbortSignal): Promise<ProductList> {
  return request<ProductList>('/v1/products', { signal })
}

/** 产品详情：?id= 返回 Product，缺失时后端可能回列表，调用方需判定。 */
export function getProduct(id: string, signal?: AbortSignal): Promise<Product | ProductList> {
  return request<Product | ProductList>('/v1/products', { query: { id }, signal })
}

/** 产品详情（?id= 固定返回 Product，而非列表）。 */
export function getProductDetail(id: string, signal?: AbortSignal): Promise<Product> {
  return request<Product>('/v1/products', { query: { id }, signal })
}

export function createProduct(body: ProductCreateRequest): Promise<Product> {
  return request<Product>('/v1/products', { method: 'POST', body })
}

export function updateProduct(id: string, body: ProductPatchRequest): Promise<Product> {
  return request<Product>('/v1/products', { method: 'PATCH', query: { id }, body })
}

export interface ListProjectsOptions {
  /** 按项目状态过滤（九态），不传表示全部。 */
  status?: number
  /** 上一页最后一条的 id；不传表示第一页。 */
  cursor?: string
  /** 每页条数，默认 20。 */
  limit?: number
  signal?: AbortSignal
}

/**
 * 项目列表。过滤与分页都在服务端完成：status 单值过滤，cursor 向后翻页，
 * 后端按 id 倒序返回（最新在前）。默认每页 20 条。
 */
export function listProjects(
  productId: string,
  { status, cursor, limit = 20, signal }: ListProjectsOptions = {},
): Promise<ProjectList> {
  return request<ProjectList>('/v1/projects', {
    query: { product_id: productId, status, cursor, limit },
    signal,
  })
}

export function getProject(id: string, signal?: AbortSignal): Promise<ProjectDetail> {
  return request<ProjectDetail>('/v1/projects', { query: { id }, signal })
}

export function listSpecs(projectId: string, signal?: AbortSignal): Promise<{ items: SpecDocument[] }> {
  return request('/v1/specs', { query: { project_id: projectId }, signal })
}

export function getSpec(id: string, materialId?: string, signal?: AbortSignal): Promise<SpecDocument> {
  return request('/v1/specs', { query: { id, material_id: materialId }, signal })
}

export function saveSpec(projectId: string, title: string, body: string, spec?: SpecDocument): Promise<SpecDocument> {
  return request('/v1/specs', spec
    ? { method: 'PATCH', query: { id: spec.spec_id }, version: spec.version, body: { title, body_md: body } }
    : { method: 'POST', body: { project_id: projectId, title, body_md: body } })
}

export function createProject(body: ProjectCreateRequest): Promise<ProjectDetail> {
  return request<ProjectDetail>('/v1/projects', { method: 'POST', body })
}

export function updateProjectTitle(id: string, title: string): Promise<Project> {
  return request<Project>('/v1/projects', { method: 'PATCH', query: { id }, body: { title } })
}

export function updateProjectRequirement(id: string, bodyMd: string): Promise<ProjectDetail> {
  return request<ProjectDetail>('/v1/projects/requirement', {
    method: 'POST', query: { id }, body: { body_md: bodyMd },
  })
}

/** kind=1 需求 / kind=2 方案，返回该物料的完整版本链。 */
export function listProjectMaterials(
  projectId: string,
  kind: 1 | 2,
  signal?: AbortSignal,
): Promise<MaterialChain> {
  return request<MaterialChain>('/v1/projects/materials', {
    query: { id: projectId, kind },
    signal,
  })
}

/** 提交评审：requirement_draft → requirement_review 或 spec_draft → spec_review，无 If-Match。 */
export function submitProject(id: string): Promise<Project> {
  return request<Project>('/v1/projects/submit', { method: 'POST', query: { id } })
}

/** 评审门裁决；必须带 If-Match（version）。 */
export function decideProject(
  id: string,
  decision: ProjectDecision,
  reason: string,
  version: number,
): Promise<Project> {
  return request<Project>('/v1/projects/decide', {
    method: 'POST',
    query: { id },
    body: { decision, reason },
    version,
  })
}

/** 取消项目；无 If-Match，终态由后端 409 invalid_state 兜底。 */
export function cancelProject(id: string): Promise<Project> {
  return request<Project>('/v1/projects/cancel', { method: 'POST', query: { id } })
}

export function listTasks(projectId: string, signal?: AbortSignal): Promise<TaskList> {
  return request<TaskList>('/v1/tasks', {
    query: { project_id: projectId, limit: 200 },
    signal,
  })
}

export function getTask(id: string, signal?: AbortSignal): Promise<TaskDetail> {
  return request<TaskDetail>('/v1/tasks', { query: { id }, signal })
}

/** 在任务草稿/任务评审/待完成阶段新增一张卡。 */
export function createTask(projectId: string, body: TaskCreateRequest): Promise<TaskDetail> {
  return request<TaskDetail>('/v1/tasks', {
    method: 'POST',
    query: { project_id: projectId },
    body,
  })
}

/** 修改卡片的标题/正文/优先级；后端不校验 If-Match。 */
export function updateTask(taskId: string, body: TaskPatchRequest): Promise<TaskDetail> {
  return request<TaskDetail>('/v1/tasks', { method: 'PATCH', query: { id: taskId }, body })
}

/** 删除未领取的待办卡（204 无响应体）。 */
export function deleteTask(taskId: string): Promise<void> {
  return request<void>('/v1/tasks', { method: 'DELETE', query: { id: taskId } })
}

/**
 * 任务批次评审：approve 把 backlog 卡置为 ready 并推进项目；
 * reject 只记录驳回原因（必填），卡片与项目状态不变。
 */
export function reviewTasks(
  projectId: string,
  decision: 'approve' | 'reject',
  reason?: string,
): Promise<TaskReviewResult> {
  const body: TaskReviewRequest = reason === undefined ? {} : { reason }
  return request<TaskReviewResult>('/v1/tasks/review', {
    method: 'POST',
    query: { project_id: projectId, decision },
    body,
  })
}

/** 任务裁决；必须带 If-Match（version）。 */
export function decideTask(
  id: string,
  decision: TaskDecision,
  reason: string,
  version: number,
): Promise<TaskDetail> {
  return request<TaskDetail>('/v1/tasks/decide', {
    method: 'POST',
    query: { id },
    body: { decision, reason },
    version,
  })
}

export interface WorkerListQuery {
  /** 分页大小，缺省 200（接口上限）。 */
  limit?: number
  cursor?: string
  signal?: AbortSignal
}

/**
 * Worker 列表（按 last_seen 倒序）。
 *
 * 缺省取满单页上限 200 条；调用方需要全量时按 next_cursor 继续翻页。
 */
export function listWorkers(options: WorkerListQuery = {}): Promise<WorkerListResponse> {
  const { limit = 200, cursor, signal } = options
  return request<WorkerListResponse>('/v1/workers', { query: { limit, cursor }, signal })
}

/** Worker 详情（?id= 返回裸 Worker；未知 id 由后端 404 表达）。 */
export function getWorker(id: string, signal?: AbortSignal): Promise<Worker> {
  return request<Worker>('/v1/workers', { query: { id }, signal })
}

/** 物料下载地址（同源字节流，支持 HEAD/Range）。 */
export function materialDownloadUrl(materialId: string): string {
  return `/v1/materials?id=${encodeURIComponent(materialId)}`
}

/**
 * 取物料正文（渐进式加载：详情只给元信息，正文按 material_id 单独取）。
 * 非 2xx 沿用统一错误体；成功返回 UTF-8 文本。
 */
export async function fetchMaterialText(materialId: string, signal?: AbortSignal): Promise<string> {
  let response: Response
  try {
    response = await fetch(materialDownloadUrl(materialId), {
      headers: { 'X-Actor': ACTOR },
      signal,
    })
  } catch (error) {
    if (isAbortError(error)) throw error
    throw new NetworkError('无法连接服务，请检查网络或后端进程是否已启动。', { cause: error })
  }

  if (!response.ok) {
    const payload = await parseBody(response)
    const body = (payload ?? {}) as Partial<ErrorBody>
    if (!body.request_id) {
      const headerId = response.headers.get('X-Request-Id')
      if (headerId) body.request_id = headerId
    }
    throw new ApiError(response.status, body, `读取正文失败（HTTP ${response.status}）`)
  }

  return response.text()
}

const ERROR_LABELS: Record<string, string> = {
  invalid_argument: '输入内容不符合要求，请检查后重试。',
  invalid_state: '当前状态不允许该操作，请刷新后核对。',
  invalid_version: '数据已被更新，请刷新后重新提交。',
  not_found: '未找到所需内容，可能已被删除或编号有误。',
  conflict: '数据冲突，编号可能重复或数据已发生变化，请核对后重试。',
  material_required: '缺少所需资料，请补充后再提交。',
  too_large: '提交内容超过大小限制，请缩小后重试。',
  lease_lost: '任务租约已失效，请重新确认任务归属。',
  worker_unknown: '执行节点尚未注册或已失效，请重新连接。',
  worker_busy: '执行节点正在处理其他任务，请稍后重试。',
  method_not_allowed: '当前请求方式不受支持，请刷新页面后重试。',
  internal: '服务暂时异常，请稍后重试。',
  unauthorized: '身份信息无效，请检查身份配置。',
  forbidden: '当前身份无法执行该操作。',
  timeout: '请求超时，请稍后重试。',
  rate_limited: '操作过于频繁，请稍后重试。',
}

export function apiErrorSummary(error: ApiError): string {
  return ERROR_LABELS[error.code] ?? describeError(error).hint
}

export function errorDetailMessage(detail: unknown): string {
  const fields: Record<string, string> = {
    product_id: '产品编号', project_id: '项目编号', task_id: '任务编号', spec_id: '方案编号',
    id: '编号', title: '标题', name: '名称', git_url: '代码仓库地址',
    description: '描述', license: '许可证', requirement_md: '需求正文',
    body_md: '正文', reason: '原因', decision: '处理方式', status: '状态',
    version: '版本', material_id: '资料编号', priority: '优先级',
  }
  if (!detail || typeof detail !== 'object') return '请检查提交内容。'
  const item = detail as { field?: unknown; reason?: unknown }
  const field = typeof item.field === 'string' ? fields[item.field] ?? '相关输入' : '提交内容'
  const reasons: Record<string, string> = {
    'is required': '不能为空',
    'must not be blank': '不能为空',
    'must not be empty': '不能为空',
    'is not allowed': '不允许修改',
    'is immutable': '不允许修改',
  }
  const reason = typeof item.reason === 'string' ? reasons[item.reason] : undefined
  return `${field}${reason ?? '不符合要求，请检查后重试'}。`
}

/** User-facing messages leave protocol diagnostics on the original error. */
export function errorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    const parts = [apiErrorSummary(error)]
    parts.push(...error.details.slice(0, 5).map(errorDetailMessage))
    if (error.requestId) parts.push(`请求编号：${error.requestId}`)
    return parts.join(' · ')
  }
  if (error instanceof NetworkError) return error.message
  // 前端前置校验（如“请先上传需求文档”）直接抛出 Error，原样展示。
  if (error instanceof Error && error.message.trim()) return error.message
  return '操作失败，请稍后重试。'
}

export function isProduct(value: Product | ProductList): value is Product {
  return typeof (value as Product).product_id === 'string' && !Array.isArray((value as ProductList).items)
}

export function isProject(value: Project | { items: Project[] }): value is Project {
  return typeof (value as Project).project_id === 'string'
}

export function isTask(value: Task | { items: Task[] }): value is Task {
  return typeof (value as Task).task_id === 'string'
}

/** 状态分档文案（401/409/422/429 单独成档，网络失败给可读提示）。 */
export function describeError(error: unknown): { title: string; hint: string } {
  if (error instanceof NetworkError) {
    return {
      title: '网络错误',
      hint: '无法连接服务。页面会按退避策略自动重试，也可手动刷新。',
    }
  }
  if (error instanceof ApiError) {
    switch (error.status) {
      case 400:
        return { title: '输入内容有误', hint: '请检查必填项和输入格式后重试。' }
      case 401:
        return { title: '身份信息无效', hint: '身份信息缺失或无效，请检查身份配置。' }
      case 403:
        return { title: '无法执行该操作', hint: '当前身份无法执行该操作。' }
      case 404:
        return { title: '未找到所需内容', hint: '资源不存在，可能已被删除。' }
      case 409:
        return { title: '操作发生冲突', hint: '数据已被其他操作修改或当前状态不允许该操作，请刷新后重试。' }
      case 413:
        return { title: '内容过大', hint: '提交内容超过大小限制，请缩小后重试。' }
      case 422:
        return { title: '提交内容不符合要求', hint: '请检查提交内容后重试。' }
      case 429:
        return { title: '操作过于频繁', hint: '稍后会继续自动刷新，也可稍等后重试。' }
      default:
        if (error.status >= 500) {
          return { title: '服务异常', hint: '服务处理失败，页面会继续自动重试。' }
        }
        return { title: '请求失败', hint: '请稍后重试。' }
    }
  }
  return { title: '未知错误', hint: '发生未预期的问题，请稍后重试。' }
}
