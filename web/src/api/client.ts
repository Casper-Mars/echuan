import type {
  ErrorBody,
  Product,
  ProductList,
  Project,
  ProjectList,
  Task,
  TaskList,
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

export function listProjects(productId: string, signal?: AbortSignal): Promise<ProjectList> {
  return request<ProjectList>('/v1/projects', { query: { product_id: productId }, signal })
}

export function listTasks(projectId: string, signal?: AbortSignal): Promise<TaskList> {
  return request<TaskList>('/v1/tasks', { query: { project_id: projectId }, signal })
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
      case 401:
        return { title: '未授权（401）', hint: '身份信息缺失或无效，请确认 X-Actor 配置。' }
      case 404:
        return { title: '未找到（404）', hint: '资源不存在，可能已被删除。' }
      case 409:
        return { title: '冲突（409）', hint: '数据已被其他操作修改或当前状态不允许该操作，请刷新后重试。' }
      case 422:
        return { title: '参数不合法（422）', hint: '请检查提交内容后重试。' }
      case 429:
        return { title: '请求过于频繁（429）', hint: '已自动退避，稍后会继续自动刷新。' }
      default:
        if (error.status >= 500) {
          return { title: `服务异常（${error.status}）`, hint: '后端处理失败，页面会继续自动重试。' }
        }
        return { title: `请求失败（${error.status}）`, hint: '请稍后重试。' }
    }
  }
  return { title: '未知错误', hint: '发生未预期的问题，请稍后重试。' }
}
