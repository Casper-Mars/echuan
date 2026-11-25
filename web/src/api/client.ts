import type {
  AttachmentRef,
  Flow,
  ListResponse,
  Product,
  Project,
  ProjectMaterial,
  Task,
  TaskKind,
  TaskStatus,
  Worker,
} from './types'

const BASE = ''

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${BASE}${path}`, {
    ...init,
    headers: {
      ...(init?.body === undefined ? {} : { 'content-type': 'application/json' }),
      ...(init?.headers ?? {}),
    },
  })
  if (response.status === 204) return undefined as T
  const text = await response.text()
  const body = text === '' ? undefined : JSON.parse(text)
  if (!response.ok) {
    throw new ApiError(response.status, body?.message ?? `HTTP ${response.status}`)
  }
  return body as T
}

const q = (params: Record<string, string | number | undefined>) => {
  const search = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== '') search.set(key, String(value))
  }
  const text = search.toString()
  return text === '' ? '' : `?${text}`
}

export const api = {
  /**
   * `GET /v1/flow`：当前进程实际加载的环节表（配置里加了环节并重启，这里就多一段）。
   *
   * 类型下拉与筛选的唯一来源；失败时界面回退本地 4 个默认 name→中文映射（见 `lib/flow`），
   * 不影响已有卡片渲染（卡片显示名来自任务 DTO 的 `kind_label`）。
   */
  getFlow: () => request<Flow>('/v1/flow'),

  listProducts: () => request<ListResponse<Product>>('/v1/products'),
  getProduct: (id: string) => request<Product>(`/v1/products${q({ id })}`),
  createProduct: (body: { product_id: string; name: string; git_url: string }) =>
    request<Product>('/v1/products', { method: 'POST', body: JSON.stringify(body) }),

  listAllProjects: (status?: 'open' | 'closed') => request<ListResponse<Project>>(`/v1/projects${q({ status })}`),
  // 顺序与筛选只有一个真相：服务端（`id DESC` + 可选 `product_id`/`status`，ADR-0002 §2）。
  // 前端不排序、不按状态过滤，只把两个参数原样传下去（空串由 `q` 视同不传，与后端 `TrimSpace` 口径一致）。
  listProjects: (query: { productId: string; status?: 'open' | 'closed' }) =>
    request<ListResponse<Project>>(`/v1/projects${q({ product_id: query.productId, status: query.status })}`).then(
      result => ({
        ...result,
        // ADR-0002 §4 C3（方案 §8.2「旧后端窗口」）：1 行防御性再过滤，兜住「新前端 + 未升级后端」——
        // 旧后端忽略 `product_id` 会返回他产品的项目，不兜就会串产品显示。这是部署防线，不是筛选机制。
        items: result.items.filter(project => project.product_id === query.productId),
      }),
    ),
  getProject: (id: string) => request<Project>(`/v1/projects${q({ id })}`),
  getProjectMaterials: (id: string) => request<ListResponse<ProjectMaterial>>(`/v1/projects/materials${q({ id })}`),
  createProject: (body: { product_id: string; name: string; context_md?: string }) =>
    request<Project>('/v1/projects', { method: 'POST', body: JSON.stringify(body) }),
  closeProject: (id: string, force = false) =>
    request<void>(`/v1/projects/close${q({ id, force: force ? '1' : undefined })}`, { method: 'POST' }),
  reopenProject: (id: string) =>
    request<void>(`/v1/projects/reopen${q({ id })}`, { method: 'POST' }),

  listTasks: (query: { project_id?: string; kind?: TaskKind | ''; status?: TaskStatus; limit?: number }) =>
    request<ListResponse<Task>>(`/v1/tasks${q(query)}`),
  getTask: (id: string) => request<Task>(`/v1/tasks${q({ id })}`),
  createTask: (body: {
    project_id: string
    kind: TaskKind
    title: string
    description_md?: string
    context_md?: string
    depends_on?: string[]
    priority?: number
  }) => request<Task>('/v1/tasks', { method: 'POST', body: JSON.stringify(body) }),
  updateTask: (id: string, body: { title: string; description_md?: string; context_md?: string; priority?: number }) =>
    request<Task>(`/v1/tasks${q({ id })}`, { method: 'PATCH', body: JSON.stringify(body) }),
  deleteTask: (id: string) => request<void>(`/v1/tasks${q({ id })}`, { method: 'DELETE' }),
  decideTask: (body: { task_id: string; decision: 'approve' | 'reject'; material_id?: string; decided_by: string; reason?: string }) =>
    request<Task>('/v1/tasks/decide', { method: 'POST', body: JSON.stringify(body) }),
  resetTask: (task_id: string) =>
    request<Task>('/v1/tasks/reset', { method: 'POST', body: JSON.stringify({ task_id }) }),

  dependencies: (taskId: string) =>
    request<ListResponse<string>>(`/v1/tasks/dependencies${q({ task_id: taskId })}`),
  attachments: (taskId: string) =>
    request<ListResponse<AttachmentRef>>(`/v1/tasks/attachments${q({ task_id: taskId })}`),
  uploadMaterial: (body: { kind: string; title: string; body_md: string }) =>
    request<{ material_id: string }>('/v1/materials', { method: 'POST', body: JSON.stringify(body) }),

  inbox: () => request<ListResponse<Task>>('/v1/inbox'),
  listWorkers: () => request<ListResponse<Worker>>('/v1/workers'),
}

export const materialURL = (materialId: string) => `/v1/materials${q({ id: materialId })}`
