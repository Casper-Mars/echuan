// 一次性静态托管 + `/v1` mock：本资产独占（web-preview 的 stub 只支持 default/empty 两态，造不出
// 「长标题 / 长串 / 长 task_id / in_review 首卡」这组矩阵数据，故在此补齐；断言与截图仍由同一脚本驱动）。
//
// 行为：
//   ① `/v1/*` 回 fixtures 里的确定性数据（GET only；写请求回 204 并记账）；
//   ② 其余路径当 SPA 静态根，未命中回落 index.html；
//   ③ `/__delay?ms=N` 让后续 `/v1/*` 延迟 N 毫秒返回——用于抓 loading（骨架）态截图。
import { createReadStream, existsSync, statSync } from 'node:fs'
import http from 'node:http'
import path from 'node:path'

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.woff2': 'font/woff2',
  '.ico': 'image/x-icon',
}

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms))

function products() {
  return [
    {
      product_id: 'echuan',
      name: '驿传 Echuan',
      git_url: 'https://example.com/echuan.git',
      description: '',
      license: 'MIT',
      project_count: 2,
    },
  ]
}

function projects() {
  return [
    {
      project_id: 'echuan-6',
      product_id: 'echuan',
      seq_no: 6,
      name: '任务卡片内距与断行修复（issue #122）',
      context_md: '',
      status: 'open',
      create_time: '2026-11-22T02:00:00Z',
      update_time: '2026-11-22T12:00:00Z',
      closed_at: null,
    },
    {
      project_id: 'echuan-5',
      product_id: 'echuan',
      seq_no: 5,
      name: '队列分组用项目',
      context_md: '',
      status: 'open',
      create_time: '2026-11-21T02:00:00Z',
      update_time: '2026-11-21T12:00:00Z',
      closed_at: null,
    },
  ]
}

/**
 * @param {{dist: string, fixtures: {tasks: object[]}, port: number, log?: (entry: object) => void}} opts
 */
export function createStubServer(opts) {
  const dist = path.resolve(opts.dist)
  if (!existsSync(dist)) throw new Error(`dist 不存在：${dist}`)
  const tasks = opts.fixtures.tasks
  const state = { delayMs: 0 }

  function jsonOf(url) {
    const id = url.searchParams.get('id')
    const pathname = url.pathname
    if (pathname === '/v1/products') {
      const list = products()
      if (id === null) return { status: 200, body: { items: list } }
      const hit = list.find(item => item.product_id === id)
      return hit === undefined ? { status: 404, body: { message: 'product not found' } } : { status: 200, body: hit }
    }
    if (pathname === '/v1/projects') {
      const list = projects()
      if (id === null) return { status: 200, body: { items: list } }
      const hit = list.find(item => item.project_id === id)
      return hit === undefined ? { status: 404, body: { message: 'project not found' } } : { status: 200, body: hit }
    }
    if (pathname === '/v1/tasks') {
      const projectId = url.searchParams.get('project_id')
      const status = url.searchParams.get('status')
      const kind = url.searchParams.get('kind')
      const limit = Number(url.searchParams.get('limit') ?? 300)
      const items = tasks
        .filter(task => (projectId === null ? true : task.project_id === projectId))
        .filter(task => (status === null ? true : task.status === status))
        .filter(task => (kind === null ? true : task.kind === kind))
        .slice(0, Number.isInteger(limit) ? limit : 300)
      return { status: 200, body: { items } }
    }
    if (pathname === '/v1/inbox') {
      const items = tasks.filter(task => task.status === 'in_review' || task.status === 'pending')
      return { status: 200, body: { items } }
    }
    if (pathname === '/v1/workers') return { status: 200, body: { items: [] } }
    if (pathname.startsWith('/v1/')) return { status: 200, body: { items: [] } }
    return null
  }

  function sendJSON(response, status, body) {
    const payload = JSON.stringify(body)
    response.writeHead(status, {
      'content-type': 'application/json; charset=utf-8',
      'content-length': Buffer.byteLength(payload),
      'cache-control': 'no-store',
    })
    response.end(payload)
  }

  function sendFile(response, file) {
    response.writeHead(200, {
      'content-type': MIME[path.extname(file).toLowerCase()] ?? 'application/octet-stream',
      'cache-control': 'no-store',
    })
    createReadStream(file).pipe(response)
  }

  const server = http.createServer(async (request, response) => {
    const url = new URL(request.url ?? '/', `http://${request.headers.host ?? 'stub'}`)
    if (url.pathname === '/__delay') {
      state.delayMs = Number(url.searchParams.get('ms') ?? 0)
      sendJSON(response, 200, { delayMs: state.delayMs })
      return
    }
    if (opts.log !== undefined) opts.log({ method: request.method, url: url.pathname + url.search })
    if (request.method !== 'GET' && request.method !== 'HEAD') {
      response.writeHead(204, { 'cache-control': 'no-store' })
      response.end()
      return
    }
    const mocked = jsonOf(url)
    if (mocked !== null) {
      if (state.delayMs > 0) await sleep(state.delayMs)
      sendJSON(response, mocked.status, mocked.body)
      return
    }
    const relative = decodeURIComponent(url.pathname).replace(/^\/+/, '')
    const candidate = path.resolve(dist, relative)
    if (candidate.startsWith(dist) && relative !== '' && existsSync(candidate) && statSync(candidate).isFile()) {
      sendFile(response, candidate)
      return
    }
    sendFile(response, path.join(dist, 'index.html'))
  })

  return {
    server,
    setDelay: ms => {
      state.delayMs = ms
    },
    start: () =>
      new Promise(resolve => {
        server.listen(opts.port, '0.0.0.0', () => resolve(opts.port))
      }),
    close: () =>
      new Promise(resolve => {
        server.close(() => resolve(true))
      }),
  }
}
