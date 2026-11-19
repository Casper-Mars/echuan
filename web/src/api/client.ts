import type {
  AttachmentRef,
  ListResponse,
  Product,
  Project,
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
  listProducts: () => request<ListResponse<Product>>('/v1/products'),
  getProduct: (id: string) => request<Product>(`/v1/products${q({ id })}`),
  createProduct: (body: { product_id: string; name: string; git_url: string }) =>
    request<Product>('/v1/products', { method: 'POST', body: JSON.stringify(body) }),

  listAllProjects: (status?: 'open' | 'closed') => request<ListResponse<Project>>(`/v1/projects${q({ status })}`),
  listProjects: (productId: string, status?: 'open' | 'closed') =>
    request<ListResponse<Project>>(`/v1/projects${q({ status })}`).then(result => ({
      ...result,
      items: result.items.filter(project => project.product_id === productId),
    })),
  getProject: (id: string) => request<Project>(`/v1/projects${q({ id })}`),
  createProject: (body: { product_id: string; name: string; context_md?: string }) =>
    request<Project>('/v1/projects', { method: 'POST', body: JSON.stringify(body) }),
  closeProject: (id: string, force = false) =>
    request<void>(`/v1/projects/close${q({ id, force: force ? '1' : undefined })}`, { method: 'POST' }),
  reopenProject: (id: string) =>
    request<void>(`/v1/projects/reopen${q({ id })}`, { method: 'POST' }),

  listTasks: (query: { project_id?: string; kind?: TaskKind; status?: TaskStatus; limit?: number }) =>
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
