// echuan 任务详情（features/task-detail）三视口 × 六态自检 + 截图
//
// 只做一件事：连真浏览器 → 用 CDP `Fetch` 拦截伪造 `/v1/*` → 六态各拍一张 → 量可量化指标 → PASS/FAIL + 退出码。
// 不引任何 npm 依赖（与 scripts/board-shots.mjs 同口径：原生 WebSocket + CDP），也不改后端。
//
// 用法（浏览器与静态托管由环境提供，见 web/README.md「详情自检」）：
//   ECHUAN_CDP_WS='ws://127.0.0.1:3010?token=<token>' \
//   ECHUAN_DETAIL_URL='http://127.0.0.1:4193/' \
//   ECHUAN_SHOT_DIR='.shots' \
//   node web/scripts/task-detail-shots.mjs
//
// 环境变量：
//   ECHUAN_CDP_WS     必需。浏览器级 CDP WebSocket 地址（带 token）
//   ECHUAN_DETAIL_URL 可选，默认 http://127.0.0.1:4193/ —— 指向 web/dist 的静态托管
//   ECHUAN_SHOT_DIR   可选，默认 <repo>/.shots（不入库，见 .git/info/exclude 口径）
//   ECHUAN_ONLY       可选，只跑指定视口/状态，逗号分隔，如 'mobile:default,desktop:error'
//
// 六态怎么造（全部走请求拦截，不改后端；状态矩阵见 design/screens/task-detail.md）：
//   default  in_review 任务：1 个 output + 2 个 input 附件、2 个前置依赖、无租约无驳回原因
//   loading  `GET /v1/tasks?id=` 挂到 20s（远长于观测窗口）→ 停在 SkeletonBody
//   error    任务主体 200、`GET /v1/tasks/attachments` 返回 503 → 错误条（HTTP 503 + 重试），
//            元信息/评审/描述等已渲染分区不消失
//   leased   任务带 lease_id / lease_until / worker_id → 租约条（mono）
//   failed   status=failed、attempt=3/3、带 reason → 末尾失败原因 + 底部「删除 / 重置为待处理」
//   rejected status=in_review 且带 reason → 顶部「上次驳回」条，评审区仍可再提交
//
// 另外四条 AC 的断言也在本脚本里（各自独立 tab）：
//   AC2 深链直达 + 关闭回看板（hash 断言）        —— closeProbe
//   AC3 评审区没有 material_id 手输入口 + 下拉 options/默认值 + 驳回必填拦截 —— reviewProbe
//   AC4 移动端底部操作条滚动 300 后仍在视口内、按钮命中区 ≥44×44 —— stickyProbe
//   AC5 评审冲突（POST 409 带服务端 message）→ 错误条 + 自动重拉详情（GET /v1/tasks?id= +1） —— conflictProbe
//
// 退出码：0 全部通过；1 有断言失败；2 环境/用法错误。
import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname, isAbsolute, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const WEB_DIR = dirname(dirname(fileURLToPath(import.meta.url)))
const REPO_DIR = dirname(WEB_DIR)

const VIEWPORTS = [
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'tablet', width: 1024, height: 768 },
  { name: 'mobile', width: 390, height: 844 },
]

const STATES = ['default', 'loading', 'error', 'leased', 'failed', 'rejected']

const PROJECT_ID = 'echuan-1'
const TASK_ID = 't_01M7TKBXJ4KAMEWN07J6QY18X8'
const DEPENDENCY_IDS = ['t_01M7SJ3KJ3JVCS9FWB218CP9MBB', 't_01M7SJ3KJ3JVCS9FWB218CP9MBC']
const LEASE_ID = 't_01M7TKK7TZ557VKBCF19Y5FXS2'
const WORKER_ID = 'w_d6f2a1'
const REJECT_REASON = '需要我定的问题都按照默认的来做'
const FAIL_REASON = '尝试 3/3 用尽：镜像构建阶段 pnpm --frozen-lockfile 失败。'
const CONFLICT_MESSAGE = '任务已被他人处理：lease 已转移给 w_9f3c21'

/** 设计稿里的 720 桌面抽屉（design/screens/task-detail.md「断点」）；值来自 theme/base.css 的 --ui-layout-sheet */
const DESKTOP_SHEET = 720

const OUTPUT_ATTACHMENTS = [{ material_id: 'm_out_tech_plan', title: 'Web UI 重做技术方案 v0.2', kind: '方案' }]
const INPUT_ATTACHMENTS = [
  { material_id: 'm_in_adr', title: 'ADR-0001 Web UI 技术底座', kind: '决策' },
  { material_id: 'm_in_issue', title: 'issue #76 原始诉求记录', kind: '需求' },
]

const DESCRIPTION_MD = [
  '针对 issue #76 完成 Web UI 重做的技术选型决策与技术方案，使后续 dev 任务可以照做。',
  '',
  '- 选哪套 UI 技术底座',
  '- 改动边界：不改 /v1 契约、不改数据库',
  '- 信息架构：看板为主路径',
].join('\n')

const CONTEXT_MD = [
  '## 约束',
  '',
  '- 只读消费既有接口，不新增端点',
  '- 附件正文由浏览器 HTTP 缓存直接复用，前端不自建缓存',
].join('\n')

const PROJECT = {
  project_id: PROJECT_ID,
  product_id: 'echuan',
  seq_no: 1,
  name: 'Web UI 重做（issue #76）',
  context_md: '',
  status: 'open',
  create_time: '2026-11-18T10:00:00Z',
  update_time: '2026-11-20T09:00:00Z',
}

/** 抽屉背后的看板上下文（design/refs 里详情打开时看板仍在背后） */
const BOARD_TASKS = [
  {
    task_id: 't_01M7TKBXJ4KAMEWN07J6QY18X8',
    project_id: PROJECT_ID,
    kind: 'dev',
    title: '体积门禁脚本与构建期打印',
    description_md: '',
    context_md: '',
    priority: 35,
    status: 'pending',
    attempt: 0,
    max_attempts: 3,
    create_time: '2026-11-19T08:00:00Z',
    update_time: '2026-11-19T10:12:00Z',
  },
  {
    task_id: 't_01M7TKBXJ4KAMEWN07J6QY18X9',
    project_id: PROJECT_ID,
    kind: 'dev',
    title: '评审动作：material_id 自动带入',
    description_md: '',
    context_md: '',
    priority: 30,
    status: 'ready',
    attempt: 0,
    max_attempts: 3,
    create_time: '2026-11-19T08:00:00Z',
    update_time: '2026-11-19T18:05:00Z',
  },
]

function taskFor(state) {
  const base = {
    task_id: TASK_ID,
    project_id: PROJECT_ID,
    parent_task_id: null,
    kind: 'design',
    title: 'Web UI 重做：技术选型决策 + 11 章技术方案',
    description_md: DESCRIPTION_MD,
    context_md: CONTEXT_MD,
    priority: 60,
    status: 'in_review',
    attempt: 2,
    max_attempts: 3,
    lease_id: null,
    lease_until: null,
    worker_id: null,
    reason: null,
    create_time: '2026-11-19T08:00:00Z',
    update_time: '2026-11-20T11:09:00Z',
  }
  if (state === 'leased') {
    return { ...base, lease_id: LEASE_ID, lease_until: '2026-11-20T19:13:36Z', worker_id: WORKER_ID }
  }
  if (state === 'failed') {
    return { ...base, status: 'failed', attempt: 3, reason: FAIL_REASON, update_time: '2026-11-20T11:41:00Z' }
  }
  if (state === 'rejected') {
    return { ...base, reason: REJECT_REASON, update_time: '2026-11-20T11:09:00Z' }
  }
  return base
}

/** ---------------- CDP 客户端（浏览器级 + 扁平会话） ---------------- */

class CDP {
  constructor(ws) {
    this.ws = ws
    this.seq = 0
    this.pending = new Map()
    this.listeners = new Map()
    ws.addEventListener('close', () => {
      for (const { reject } of this.pending.values()) reject(new Error('CDP 连接已关闭'))
      this.pending.clear()
    })
    ws.addEventListener('message', (event) => {
      const msg = JSON.parse(event.data)
      if (msg.id && this.pending.has(msg.id)) {
        const { resolve, reject } = this.pending.get(msg.id)
        this.pending.delete(msg.id)
        if (msg.error) reject(new Error(`${msg.error.message} (${JSON.stringify(msg.error)})`))
        else resolve(msg.result)
        return
      }
      if (!msg.method) return
      for (const fn of this.listeners.get(`${msg.sessionId ?? ''}:${msg.method}`) ?? []) fn(msg.params)
    })
  }

  static async connect(url) {
    if (typeof WebSocket === 'undefined') throw new Error('当前 Node 没有全局 WebSocket，请用 Node 22+')
    const ws = new WebSocket(url)
    await new Promise((res, rej) => {
      ws.addEventListener('open', res, { once: true })
      ws.addEventListener('error', () => rej(new Error(`CDP 连接失败：${url}`)), { once: true })
    })
    return new CDP(ws)
  }

  send(method, params = {}, sessionId) {
    const id = ++this.seq
    const payload = { id, method, params }
    if (sessionId) payload.sessionId = sessionId
    this.ws.send(JSON.stringify(payload))
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id)
        reject(new Error(`CDP 命令超时（20s）：${method}`))
      }, 20_000)
      const settle = (fn) => (value) => {
        clearTimeout(timer)
        fn(value)
      }
      this.pending.set(id, { resolve: settle(resolve), reject: settle(reject) })
    })
  }

  on(method, handler, sessionId) {
    const key = `${sessionId ?? ''}:${method}`
    if (!this.listeners.has(key)) this.listeners.set(key, [])
    this.listeners.get(key).push(handler)
  }

  clearListeners() {
    this.listeners.clear()
  }

  close() {
    try {
      this.ws.close()
    } catch {
      // 关闭失败不影响退出码
    }
  }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

/** 被 Fetch 拦下但还没响应的请求 id：loading 用例故意挂住它，收尾必须逐个收掉 */
const pausedRequestIds = new Set()

const results = []

/** 两个 CSS 颜色字符串（rgb()/rgba()）的 WCAG 对比度；解析失败返回 null */
function contrastRatio(foreground, background) {
  const parse = (value) => {
    if (!value) return null
    const m = /rgba?\(([^)]+)\)/.exec(value)
    if (!m) return null
    const [r, g, b] = m[1].split(',').map((part) => Number.parseFloat(part.trim()))
    if ([r, g, b].some((n) => !Number.isFinite(n))) return null
    const lum = [r, g, b].map((channel) => {
      const c = channel / 255
      return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
    })
    return 0.2126 * lum[0] + 0.7152 * lum[1] + 0.0722 * lum[2]
  }
  const a = parse(foreground)
  const b = parse(background)
  if (a === null || b === null) return null
  const [hi, lo] = a >= b ? [a, b] : [b, a]
  return (hi + 0.05) / (lo + 0.05)
}
function check(name, ok, detail = '') {
  results.push({ name, ok, detail })
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? ` — ${detail}` : ''}`)
}

async function createPage(cdp, attempts = 3) {
  let lastError = null
  for (let i = 0; i < attempts; i += 1) {
    try {
      const { targetId } = await cdp.send('Target.createTarget', { url: 'about:blank' })
      const { sessionId } = await cdp.send('Target.attachToTarget', { targetId, flatten: true })
      return { targetId, sessionId }
    } catch (err) {
      lastError = err
      await sleep(1000 * (i + 1))
    }
  }
  throw lastError ?? new Error('Target.createTarget 失败')
}

async function closePage(cdp, targetId) {
  try {
    await cdp.send('Target.closeTarget', { targetId })
  } catch {
    // 页面已关闭
  }
}

async function evaluate(cdp, sessionId, expression) {
  const result = await cdp.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }, sessionId)
  if (result.exceptionDetails) {
    throw new Error(`页面脚本异常：${result.exceptionDetails.text} ${result.exceptionDetails.exception?.description ?? ''}`)
  }
  return result.result.value
}

async function waitFor(cdp, sessionId, expression, timeoutMs, label) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    if (await evaluate(cdp, sessionId, expression)) return true
    await sleep(120)
  }
  throw new Error(`等待超时（${timeoutMs}ms）：${label}`)
}

/** 页面里跑的度量脚本：所有断言口径都在这里，返回纯数据 */
const MEASURE = `(() => {
  const root = document.querySelector('[data-ui="task-detail"]')
  const content = document.querySelector('.mantine-Drawer-content')
  const footer = document.querySelector('.ui-detail-sheet__footer')
  const scroller = document.querySelector('.ui-detail-sheet__body')
  const text = (sel) => document.querySelector(sel)?.textContent?.trim() ?? null
  const box = (el) => { const r = el.getBoundingClientRect(); return { top: Math.round(r.top), bottom: Math.round(r.bottom), left: Math.round(r.left), right: Math.round(r.right), width: Math.round(r.width), height: Math.round(r.height) } }
  const order = Array.from(document.querySelectorAll('.fx-detail__body [data-ui]')).map((el) => el.dataset.ui)
  const errorBar = document.querySelector('[data-ui="load-error"], [data-ui="review-error"]')
  const select = document.querySelector('#td-review-material')
  return {
    state: root?.dataset.state ?? null,
    taskId: root?.dataset.taskId ?? null,
    hash: window.location.hash,
    viewport: { width: window.innerWidth, height: window.innerHeight },
    drawer: content ? { ...box(content), position: getComputedStyle(content).position } : null,
    footer: footer ? { ...box(footer), position: getComputedStyle(footer).position } : null,
    scroller: scroller ? { scrollTop: Math.round(scroller.scrollTop), scrollHeight: scroller.scrollHeight, clientHeight: scroller.clientHeight } : null,
    doc: { scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth },
    order,
    meta: text('[data-ui="detail-meta"]'),
    lease: text('[data-ui="detail-lease"]'),
    banner: text('[data-ui="detail-reject-banner"]'),
    reason: text('[data-ui="detail-reason"]'),
    reviewLabel: text('[data-ui="review"]'),
    attachmentsHeader: text('[data-ui="detail-attachments"] .fx-detail__section-head'),
    attachmentRows: Array.from(document.querySelectorAll('.ui-attachment-row')).map((el) => ({
      label: el.getAttribute('aria-label'),
      role: el.dataset.role ?? null,
      text: el.textContent.trim(),
    })),
    dependencyChips: Array.from(document.querySelectorAll('[data-ui="detail-dependencies"] .ui-chip')).map((el) => el.textContent.trim()),
    descriptionText: text('[data-ui="detail-description"]'),
    errorBar: errorBar ? { key: errorBar.firstElementChild ? errorBar.firstElementChild.dataset.ui : null, text: errorBar.textContent.trim(), retry: !!errorBar.querySelector('.ui-button') } : null,
    skeleton: !!document.querySelector('[data-ui="detail-skeleton"]'),
    buttons: Array.from(document.querySelectorAll('.fx-detail__actions .ui-button')).map((el) => ({ text: el.textContent.trim(), ...box(el) })),
    select: select ? { tag: select.tagName, name: select.name, readOnly: select.readOnly, value: select.value, role: select.getAttribute('role') } : null,
    materialInputs: document.querySelectorAll('input[name*="material" i]').length,
    notice: text('[data-ui="detail-notice"]'),
    // 抽屉背后的看板上下文（AC：桌面/平板「保留看板上下文」）
    board: (() => {
      const el = document.querySelector('[data-ui="board"]')
      if (!el) return null
      const r = el.getBoundingClientRect()
      return {
        state: el.dataset.boardState ?? null,
        cards: el.querySelectorAll('.ui-task-card').length,
        left: Math.round(r.left),
        width: Math.round(r.width),
        marker: el.dataset.probeMarker ?? null,
      }
    })(),
    // 关闭键：前景色 / 头部底色 / 图标是否存在 / 命中区
    close: (() => {
      const el = document.querySelector('[aria-label="关闭详情"]')
      if (!el) return null
      const cs = getComputedStyle(el)
      const r = el.getBoundingClientRect()
      const header = document.querySelector('.mantine-Drawer-header')
      return {
        color: cs.color,
        headerBg: header ? getComputedStyle(header).backgroundColor : null,
        hasIcon: !!el.querySelector('svg path'),
        width: Math.round(r.width),
        height: Math.round(r.height),
      }
    })(),
  }
})()`

/** 一次「视口 × 状态」的完整观测 */
async function runCase(cdp, page, viewport, state, options) {
  const { sessionId } = page
  for (const requestId of [...pausedRequestIds]) {
    await cdp.send('Fetch.failRequest', { requestId, errorReason: 'Aborted' }, sessionId).catch(() => undefined)
    pausedRequestIds.delete(requestId)
  }
  await cdp.send('Page.navigate', { url: 'about:blank' }, sessionId)
  await cdp.send('Page.enable', {}, sessionId)
  await cdp.send('Runtime.enable', {}, sessionId)
  await cdp.send('Fetch.disable', {}, sessionId).catch(() => undefined)
  cdp.clearListeners()

  const fixture = { state, taskGets: 0, decidePosts: 0, decideFails: false, attachmentsFails: state === 'error' }

  await cdp.send(
    'Emulation.setDeviceMetricsOverride',
    { width: viewport.width, height: viewport.height, deviceScaleFactor: 1, mobile: viewport.name === 'mobile' },
    sessionId,
  )
  if (viewport.name === 'mobile') {
    await cdp.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 }, sessionId)
  }
  await cdp.send('Network.enable', {}, sessionId)
  await cdp.send('Network.setCacheDisabled', { cacheDisabled: true }, sessionId)
  await cdp.send('Fetch.enable', { patterns: [{ urlPattern: '*/v1/*', requestStage: 'Request' }] }, sessionId)

  const fulfill = (requestId, responseCode, body) =>
    cdp
      .send(
        'Fetch.fulfillRequest',
        {
          requestId,
          responseCode,
          responseHeaders: [{ name: 'content-type', value: 'application/json' }],
          body: Buffer.from(typeof body === 'string' ? body : JSON.stringify(body)).toString('base64'),
        },
        sessionId,
      )
      .catch(() => undefined)
      .then(() => pausedRequestIds.delete(requestId))

  cdp.on(
    'Fetch.requestPaused',
    async ({ requestId, request }) => {
      const url = request.url
      pausedRequestIds.add(requestId)
      if (url.includes('/v1/tasks/decide')) {
        fixture.decidePosts += 1
        await fulfill(requestId, 200, JSON.stringify(taskFor('default')))
        return
      }
      if (url.includes('/v1/tasks/dependencies')) {
        await fulfill(requestId, 200, { items: DEPENDENCY_IDS })
        return
      }
      if (url.includes('/v1/tasks/attachments')) {
        if (fixture.attachmentsFails) {
          await fulfill(requestId, 503, { message: '附件服务暂不可用，请稍后重试' })
          return
        }
        const items = [
          ...OUTPUT_ATTACHMENTS.map((item) => ({ ...item, role: 'output' })),
          ...INPUT_ATTACHMENTS.map((item) => ({ ...item, role: 'input' })),
        ]
        await fulfill(requestId, 200, { items })
        return
      }
      if (url.includes('/v1/tasks?id=')) {
        fixture.taskGets += 1
        // loading：把这一笔挂到 20s（远长于观测窗口）→ 页面停在 SkeletonBody；
        // 不能「永不响应」：永远挂着的 paused 请求会让后续 Fetch/Runtime 命令卡死。
        if (state === 'loading') {
          await sleep(20_000)
          await fulfill(requestId, 200, JSON.stringify(taskFor('default')))
          return
        }
        await fulfill(requestId, 200, JSON.stringify(taskFor(state)))
        return
      }
      if (url.includes('/v1/tasks')) {
        await fulfill(requestId, 200, { items: BOARD_TASKS })
        return
      }
      if (url.includes('/v1/projects')) {
        await fulfill(requestId, 200, JSON.stringify(PROJECT))
        return
      }
      if (url.includes('/v1/products')) {
        await fulfill(requestId, 200, { items: [{ product_id: 'echuan', name: '驿传', git_url: 'https://github.com/Casper-Mars/echuan.git' }] })
        return
      }
      await fulfill(requestId, 200, { items: [] })
    },
    sessionId,
  )

  await cdp.send('Page.navigate', { url: `${options.url}#/projects/${PROJECT_ID}/tasks/${TASK_ID}` }, sessionId)
  if (state === 'loading') {
    await waitFor(cdp, sessionId, `!!document.querySelector('[data-ui="detail-skeleton"]')`, 15_000, '详情骨架出现')
    await sleep(400)
  } else {
    await waitFor(cdp, sessionId, `!!document.querySelector('[data-ui="task-detail"]')`, 20_000, '详情挂载')
    await waitFor(cdp, sessionId, `document.querySelector('[data-ui="task-detail"]').dataset.state !== 'loading'`, 15_000, '详情离开 loading')
    await sleep(500)
  }
  // 抽屉背后的看板先出卡片，再截图（失败由断言兜住，不在这里中断用例）
  await waitFor(
    cdp,
    sessionId,
    `(document.querySelector('[data-ui="board"]')?.querySelectorAll('.ui-task-card').length ?? 0) > 0`,
    10_000,
    '背后看板卡片',
  ).catch(() => undefined)

  // 字体就绪后再截图，避免与 ref 对照时字宽漂移
  await evaluate(cdp, sessionId, 'document.fonts ? document.fonts.ready.then(() => true) : true')
  await sleep(200)

  const measure = await evaluate(cdp, sessionId, MEASURE)
  const shot = await cdp.send(
    'Page.captureScreenshot',
    { format: 'png', captureBeyondViewport: false },
    sessionId,
  )
  const shotPath = join(options.shotDir, `task-detail__${viewport.name}__dark__${state}.png`)
  writeFileSync(shotPath, Buffer.from(shot.data, 'base64'))
  return { viewport, state, fixture, measure, shotPath }
}

/** 分区分量表（用于交付报告与逐态对照） */
function orderOf(measure) {
  return measure.order.filter((key) =>
    ['detail-meta', 'detail-lease', 'detail-reject-banner', 'review', 'detail-attachments', 'detail-description', 'detail-context', 'detail-dependencies', 'detail-reason'].includes(
      key,
    ),
  )
}

const EXPECTED_ORDER = [
  'detail-meta',
  'detail-lease',
  'detail-reject-banner',
  'review',
  'detail-attachments',
  'detail-description',
  'detail-context',
  'detail-dependencies',
  'detail-reason',
]

function assertionsOf(record) {
  const key = `${record.viewport.name}:${record.state}`
  const m = record.measure
  const state = record.state

  check(`${key} 挂载与状态`, m.state === state, `data-state=${m.state}`)

  // 关闭键：图标存在 + 前景/头部底色对比度（非文本控件下限 3:1）
  const closeContrast = m.close ? contrastRatio(m.close.color, m.close.headerBg) : null
  check(
    `${key} 关闭键可见（对比度 ≥3:1）`,
    m.close !== null && m.close.hasIcon && closeContrast !== null && closeContrast >= 3,
    m.close ? `color=${m.close.color} on ${m.close.headerBg} → ${closeContrast?.toFixed(2)}:1，icon=${m.close.hasIcon}，命中区 ${m.close.width}×${m.close.height}` : '找不到关闭键',
  )

  // 抽屉背后的看板上下文（design/screens/task-detail.md「保留看板上下文」）
  check(
    `${key} 看板上下文挂载`,
    m.board !== null && m.board.state !== null,
    m.board ? `data-board-state=${m.board.state} 卡片=${m.board.cards} left=${m.board.left} width=${m.board.width}` : '背后无看板',
  )
  if (record.viewport.name !== 'mobile') {
    check(
      `${key} 抽屉左侧可见看板列与卡片`,
      m.board !== null && m.board.cards > 0 && m.board.width > 0,
      m.board ? `卡片=${m.board.cards}，看板宽=${m.board.width}` : '背后无看板',
    )
  }

  const drawerWidth = m.drawer?.width ?? -1
  if (state === 'loading' && m.state === null) return
  if (record.viewport.name === 'mobile') {
    check(
      `${key} 抽屉全屏`,
      drawerWidth === record.viewport.width,
      `drawer=${drawerWidth} / viewport=${record.viewport.width}`,
    )
  } else {
    check(
      `${key} 抽屉 720`,
      Math.abs(drawerWidth - DESKTOP_SHEET) <= 1,
      `drawer=${drawerWidth}（期望 ${DESKTOP_SHEET}）`,
    )
  }

  check(
    `${key} 底部操作条钉在抽屉底部`,
    m.footer !== null && Math.abs(m.footer.bottom - record.viewport.height) <= 1 && m.footer.top >= 0,
    m.footer ? `footer top=${m.footer.top} bottom=${m.footer.bottom} / viewportHeight=${record.viewport.height} position=${m.footer.position}` : 'footer 不存在',
  )

  if (record.viewport.name === 'mobile') {
    check(
      `${key} 移动端操作条 sticky/fixed`,
      m.footer !== null && ['sticky', 'fixed'].includes(m.footer.position),
      `position=${m.footer?.position}`,
    )
    const small = (m.buttons ?? []).filter((b) => b.width < 44 || b.height < 44)
    check(
      `${key} 操作条按钮命中区 ≥44`,
      (m.buttons ?? []).length > 0 && small.length === 0,
      (m.buttons ?? []).map((b) => `${b.text} ${b.width}×${b.height}`).join(' / '),
    )
  }

  check(`${key} 无横向溢出`, m.doc.scrollWidth <= m.doc.clientWidth, `scrollWidth=${m.doc.scrollWidth} clientWidth=${m.doc.clientWidth}`)

  // 分区顺序：按状态矩阵，缺的分区（如非 in_review 无评审区）跳过，但相对顺序必须一致
  const order = orderOf(m)
  const positions = order.map((name) => EXPECTED_ORDER.indexOf(name))
  const ascending = positions.every((value, index) => index === 0 || value > positions[index - 1])
  check(`${key} 分区顺序`, ascending, order.join(' → '))

  if (state === 'loading') {
    check(`${key} loading 骨架`, m.skeleton === true, `skeleton=${m.skeleton}`)
    check(`${key} loading 头部/操作条保持`, m.footer !== null && m.buttons.length > 0, m.buttons.map((b) => b.text).join('/'))
    return
  }

  if (state === 'error') {
    check(
      `${key} 错误条含 HTTP status 与重试`,
      m.errorBar !== null && /HTTP 503/.test(m.errorBar.text) && m.errorBar.retry,
      m.errorBar?.text?.slice(0, 120) ?? '无错误条',
    )
    check(`${key} 已有分区不消失`, m.meta !== null && m.reviewLabel !== null, `meta=${!!m.meta} review=${!!m.reviewLabel}`)
    return
  }

  check(`${key} 元信息四格`, /状态/.test(m.meta ?? '') && /类型/.test(m.meta ?? '') && /尝试/.test(m.meta ?? '') && /优先级/.test(m.meta ?? ''), (m.meta ?? '').slice(0, 60))

  if (state === 'leased') {
    check(
      `${key} 租约条 mono 三要素`,
      (m.lease ?? '').includes(LEASE_ID) && (m.lease ?? '').includes(WORKER_ID) && /到期/.test(m.lease ?? ''),
      (m.lease ?? '').slice(0, 120),
    )
  }

  if (state === 'rejected') {
    check(
      `${key} 顶部驳回原因条`,
      (m.banner ?? '').includes(REJECT_REASON) && m.order.indexOf('detail-reject-banner') < m.order.indexOf('review'),
      (m.banner ?? '').slice(0, 120),
    )
    check(`${key} 评审区仍可再次提交`, /评审/.test(m.reviewLabel ?? '') && m.reviewLabel !== null, '评审区存在')
  }

  if (state === 'failed') {
    check(`${key} 失败原因分区`, (m.reason ?? '').includes('尝试 3/3 用尽'), (m.reason ?? '').slice(0, 120))
    const titles = m.buttons.map((b) => b.text)
    check(
      `${key} 底部删除 + 重置为待处理`,
      titles.includes('删除') && titles.includes('重置为待处理'),
      titles.join(' / '),
    )
  }

  if (state === 'default') {
    check(
      `${key} 评审区（通过/驳回 + 自动带入 output）`,
      /通过/.test(m.reviewLabel ?? '') && /驳回/.test(m.reviewLabel ?? '') && /被评审附件/.test(m.reviewLabel ?? ''),
      (m.reviewLabel ?? '').slice(0, 80),
    )
    check(`${key} 附件 3 个且角色齐全`, m.attachmentRows.length === 3 && m.attachmentRows.some((r) => /输入/.test(r.label ?? '')), m.attachmentRows.map((r) => r.label).join(' / '))
    check(`${key} 依赖 chips`, m.dependencyChips.length === DEPENDENCY_IDS.length, m.dependencyChips.join(' / '))
    check(
      `${key} 底部三动作`,
      ['取消', '驳回', '通过并关闭'].every((title) => m.buttons.some((b) => b.text === title)),
      m.buttons.map((b) => b.text).join(' / '),
    )
  }
}

/**
 * AC3：评审区没有 material_id 手输入口 + 被评审附件是下拉且默认选第一个 output + 驳回必填拦截
 */
async function reviewProbe(cdp, page, options) {
  const { sessionId } = page
  const fixture = { decidePosts: 0 }
  await installFetcher(cdp, sessionId, () => 'default', fixture)
  await cdp.send('Page.navigate', { url: `${options.url}#/projects/${PROJECT_ID}/tasks/${TASK_ID}` }, sessionId)
  await waitFor(cdp, sessionId, `document.querySelector('[data-ui="review"]') !== null`, 20_000, '评审区出现')
  await sleep(400)

  const base = await evaluate(
    cdp,
    sessionId,
    `(() => {
      const inputs = Array.from(document.querySelectorAll('input, textarea')).map((el) => ({
        tag: el.tagName,
        id: el.id,
        name: el.name,
        type: el.getAttribute('type'),
        readOnly: el.readOnly,
        role: el.getAttribute('role'),
        placeholder: el.getAttribute('placeholder'),
      }))
      return {
        materialNamedInputs: document.querySelectorAll('input[name*="material" i], textarea[name*="material" i]').length,
        inputs,
        selectReadOnly: document.querySelector('#td-review-material')?.readOnly ?? null,
        selectName: document.querySelector('#td-review-material')?.name ?? null,
        selectValue: document.querySelector('#td-review-material')?.value ?? null,
      }
    })()`,
  )
  check('AC3 无 material_id 手输入口', base.materialNamedInputs === 0, `input/textarea[name*=material] = ${base.materialNamedInputs}`)
  check(
    'AC3 被评审附件是只读下拉（不可自由输入）',
    base.selectReadOnly === true && base.selectName === '',
    `#td-review-material readOnly=${base.selectReadOnly} name="${base.selectName}" value="${base.selectValue}"`,
  )

  // 打开下拉，读 options 与选中项
  await evaluate(cdp, sessionId, `document.querySelector('#td-review-material').click()`)
  await waitFor(cdp, sessionId, `document.querySelectorAll('[role="option"]').length > 0`, 5000, '下拉选项渲染')
  const dropdown = await evaluate(
    cdp,
    sessionId,
    `(() => {
      // 只看**可见**的 option：看板筛选（kind/status）的下拉在背后也渲染了 role=option，
      // 全量 querySelectorAll 会把它们算进来（AC3 的 options 必须只来自本次打开的评审下拉）
      const visible = Array.from(document.querySelectorAll('[role="option"]')).filter(
        (el) => el.getClientRects().length > 0 && el.closest('[aria-hidden="true"]') === null,
      )
      return {
        options: visible.map((el) => el.textContent.trim()),
        selected: document.querySelector('#td-review-material')?.value ?? null,
        selectedOptions: visible.filter((el) => el.getAttribute('aria-selected') === 'true').map((el) => el.textContent.trim()),
      }
    })()`,
  )
  const outputTitles = OUTPUT_ATTACHMENTS.map((item) => item.title)
  check(
    'AC3 下拉 options 只来自 output 附件',
    dropdown.options.length === outputTitles.length && outputTitles.every((title) => dropdown.options.includes(title)),
    `options=[${dropdown.options.join(', ')}] 期望=[${outputTitles.join(', ')}]`,
  )
  check(
    'AC3 默认已选第一个 output',
    dropdown.selected === outputTitles[0],
    `selected="${dropdown.selected}" 期望="${outputTitles[0]}"`,
  )
  // 关掉下拉：点抽屉标题处（**不能按 Escape**——Esc 是 ui/DetailSheet 的关闭手势，会把详情关掉回看板）
  await evaluate(
    cdp,
    sessionId,
    `(() => { const title = document.querySelector('.mantine-Drawer-title'); if (title) title.click(); return true })()`,
  )
  await sleep(300)

  // 驳回必填：意见为空 → 就地标红 + 不发请求
  await waitFor(cdp, sessionId, `document.querySelectorAll('.fx-detail__actions .ui-button').length > 0`, 5000, '底部操作条按钮')
  const rejectBox = await evaluate(
    cdp,
    sessionId,
    `(() => {
      const btn = Array.from(document.querySelectorAll('.fx-detail__actions .ui-button')).find((el) => el.textContent.trim() === '驳回')
      if (!btn) return null
      const r = btn.getBoundingClientRect()
      return { x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2) }
    })()`,
  )
  if (rejectBox === null) {
    check('AC3 驳回必填拦截', false, '找不到底部「驳回」按钮')
    return
  }
  await clickAt(cdp, sessionId, rejectBox.x, rejectBox.y)
  await sleep(500)
  const rejectState = await evaluate(
    cdp,
    sessionId,
    `(() => ({
      errorText: document.querySelector('#td-review-reason')?.closest('.ui-text-area')?.textContent ?? '',
      dialogOpen: !!document.querySelector('.ui-confirm-modal [role="dialog"], .ui-confirm-modal'),
      value: document.querySelector('#td-review-reason')?.value ?? null,
    }))()`,
  )
  check(
    'AC3 驳回必填拦截（拦下且不发请求）',
    /驳回必须填写意见/.test(rejectState.errorText) && fixture.decidePosts === 0,
    `错误文案命中=${/驳回必须填写意见/.test(rejectState.errorText)} POST /v1/tasks/decide 次数=${fixture.decidePosts}`,
  )
}

/**
 * AC5：评审冲突（POST 非 2xx）→ 错误条含服务端 message + 自动重拉详情（GET /v1/tasks?id= +1）
 */
async function conflictProbe(cdp, page, options) {
  const { sessionId } = page
  const fixture = { decidePosts: 0, conflict: true, taskGets: 0 }
  await installFetcher(cdp, sessionId, () => 'default', fixture)
  await cdp.send('Page.navigate', { url: `${options.url}#/projects/${PROJECT_ID}/tasks/${TASK_ID}` }, sessionId)
  await waitFor(cdp, sessionId, `document.querySelector('[data-ui="review"]') !== null`, 20_000, '评审区出现')
  await sleep(500)
  const before = fixture.taskGets
  await evaluate(cdp, sessionId, `(() => { const b = document.querySelector('[data-ui="board"]'); if (b) b.dataset.probeMarker = 'conflict'; return true })()`)

  const submitBox = await evaluate(
    cdp,
    sessionId,
    `(() => {
      const btn = Array.from(document.querySelectorAll('.fx-detail__actions .ui-button')).find((el) => el.textContent.trim() === '通过并关闭')
      if (!btn) return null
      const r = btn.getBoundingClientRect()
      return { x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2) }
    })()`,
  )
  check('AC5 找到「通过并关闭」', submitBox !== null, JSON.stringify(submitBox))
  if (!submitBox) return
  await clickAt(cdp, sessionId, submitBox.x, submitBox.y)
  // 等弹窗里的确认键真的挂上（Modal 根节点先出现、内容随后挂载，只等根会拿到空列表）
  await waitFor(
    cdp,
    sessionId,
    `Array.from(document.querySelectorAll('.ui-confirm-modal .ui-button')).some((el) => /确认通过/.test(el.textContent))`,
    8000,
    '二次确认弹窗',
  )
  const confirmBox = await evaluate(
    cdp,
    sessionId,
    `(() => {
      const btn = Array.from(document.querySelectorAll('.ui-confirm-modal .ui-button')).find((el) => /确认通过/.test(el.textContent))
      if (!btn) return null
      const r = btn.getBoundingClientRect()
      return { x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2) }
    })()`,
  )
  check('AC5 二次确认弹窗可确认', confirmBox !== null, '')
  if (!confirmBox) return
  await clickAt(cdp, sessionId, confirmBox.x, confirmBox.y)
  await waitFor(cdp, sessionId, `!!document.querySelector('[data-ui="review-error"]')`, 10_000, '评审错误条')
  await sleep(600)
  const after = await evaluate(
    cdp,
    sessionId,
    `(() => {
      const bar = document.querySelector('[data-ui="review-error"]')
      return {
        errorText: bar ? bar.textContent.trim() : null,
        metaPresent: !!document.querySelector('[data-ui="detail-meta"]'),
        hash: window.location.hash,
        dialogOpen: !!document.querySelector('.ui-confirm-modal'),
        boardMarker: document.querySelector('[data-ui="board"]')?.dataset.probeMarker ?? null,
      }
    })()`,
  )
  check('AC5 错误条含服务端 message', (after.errorText ?? '').includes(CONFLICT_MESSAGE), (after.errorText ?? '').slice(0, 160))
  check(
    'AC5 冲突后自动重拉详情（GET /v1/tasks?id= +1）',
    fixture.taskGets === before + 1,
    `POST /v1/tasks/decide=${fixture.decidePosts} 次；GET /v1/tasks?id= ${before} → ${fixture.taskGets}`,
  )
  check('AC5 重拉后不整屏转空', after.metaPresent === true, `meta 分区存在=${after.metaPresent}`)
  check(
    'AC5 冲突后看板未被重挂',
    after.boardMarker === 'conflict',
    `board marker=${after.boardMarker}`,
  )
}

/** AC2：深链直达 + 关闭回看板 */
async function closeProbe(cdp, page, options) {
  const { sessionId } = page
  const deepUrl = `${options.url}#/projects/${PROJECT_ID}/tasks/${TASK_ID}`
  const boardUrl = `#/projects/${PROJECT_ID}/tasks`
  await installFetcher(cdp, sessionId, () => 'default', {})
  await cdp.send('Page.navigate', { url: deepUrl }, sessionId)
  await waitFor(cdp, sessionId, `document.querySelector('[data-ui="detail-meta"]') !== null`, 20_000, '深链直达详情')
  await waitFor(cdp, sessionId, `(document.querySelector('[data-ui="board"]')?.querySelectorAll('.ui-task-card').length ?? 0) > 0`, 10_000, '背后看板卡片')

  const deep = await evaluate(
    cdp,
    sessionId,
    `(() => {
      const board = document.querySelector('[data-ui="board"]')
      board.dataset.probeMarker = 'deep-link'
      return {
        hash: window.location.hash,
        title: document.querySelector('.mantine-Drawer-title')?.textContent ?? null,
        meta: !!document.querySelector('[data-ui="detail-meta"]'),
        boardState: board.dataset.boardState ?? null,
        boardCards: board.querySelectorAll('.ui-task-card').length,
      }
    })()`,
  )
  check(
    'AC2 深链直接打开详情',
    deep.hash === `#/projects/${PROJECT_ID}/tasks/${TASK_ID}` && deep.meta,
    `hash=${deep.hash} title="${deep.title}"`,
  )
  check(
    'AC2 深链打开时背后看板已挂载',
    deep.boardState !== null && deep.boardCards > 0,
    `data-board-state=${deep.boardState} 卡片=${deep.boardCards}`,
  )

  // 关闭路径 1：点头部 ×
  await evaluate(cdp, sessionId, `document.querySelector('[aria-label="关闭详情"]').click()`)
  await waitFor(cdp, sessionId, `!document.querySelector('.mantine-Drawer-content')`, 8000, '抽屉关闭（×）')
  await sleep(400)
  const closed = await evaluate(
    cdp,
    sessionId,
    `(() => {
      const board = document.querySelector('[data-ui="board"]')
      return {
        hash: window.location.hash,
        hasBoard: !!board,
        marker: board?.dataset.probeMarker ?? null,
        cards: board ? board.querySelectorAll('.ui-task-card').length : 0,
      }
    })()`,
  )
  check('AC2 点 × 关闭回看板（hash）', closed.hash === boardUrl, `hash=${closed.hash}`)
  check(
    'AC2 关闭后看板未重挂（同一 DOM 节点，不白屏）',
    closed.marker === 'deep-link' && closed.cards > 0,
    `marker=${closed.marker} 卡片=${closed.cards}`,
  )

  // 关闭路径 2：Esc（ui/DetailSheet 的键盘关闭手势）
  await evaluate(cdp, sessionId, `(() => { window.location.hash = '#/projects/${PROJECT_ID}/tasks/${TASK_ID}'; return true })()`)
  await waitFor(cdp, sessionId, `document.querySelector('[data-ui="detail-meta"]') !== null`, 10_000, '再次打开详情')
  await evaluate(cdp, sessionId, `(() => { document.querySelector('[data-ui="board"]').dataset.probeMarker = 'esc-path'; return true })()`)
  await cdp.send('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 }, sessionId)
  await cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 }, sessionId)
  await waitFor(cdp, sessionId, `!document.querySelector('.mantine-Drawer-content')`, 8000, '抽屉关闭（Esc）')
  await sleep(400)
  const escaped = await evaluate(
    cdp,
    sessionId,
    `(() => {
      const board = document.querySelector('[data-ui="board"]')
      return { hash: window.location.hash, marker: board?.dataset.probeMarker ?? null, cards: board ? board.querySelectorAll('.ui-task-card').length : 0 }
    })()`,
  )
  check('AC2 按 Esc 关闭回看板（hash）', escaped.hash === boardUrl, `hash=${escaped.hash}`)
  check(
    'AC2 Esc 关闭后看板未重挂',
    escaped.marker === 'esc-path' && escaped.cards > 0,
    `marker=${escaped.marker} 卡片=${escaped.cards}`,
  )
}

/** AC4：移动端底部操作条滚动 300 后仍在视口内 */
async function stickyProbe(cdp, page, options, shotDir) {
  const { sessionId } = page
  await installFetcher(cdp, sessionId, () => 'rejected', {})
  await cdp.send(
    'Emulation.setDeviceMetricsOverride',
    { width: 390, height: 844, deviceScaleFactor: 1, mobile: true },
    sessionId,
  )
  await cdp.send('Emulation.setTouchEmulationEnabled', { enabled: true, maxTouchPoints: 1 }, sessionId)
  await cdp.send('Page.navigate', { url: `${options.url}#/projects/${PROJECT_ID}/tasks/${TASK_ID}` }, sessionId)
  await waitFor(cdp, sessionId, `document.querySelector('[data-ui="detail-meta"]') !== null`, 20_000, '移动端详情挂载')
  await sleep(500)
  const before = await evaluate(cdp, sessionId, `(() => {
    const f = document.querySelector('.ui-detail-sheet__footer').getBoundingClientRect()
    return { top: Math.round(f.top), bottom: Math.round(f.bottom), vh: window.innerHeight, scrollTop: Math.round(document.querySelector('.ui-detail-sheet__body').scrollTop) }
  })()`)
  await evaluate(cdp, sessionId, `(() => { const el = document.querySelector('.ui-detail-sheet__body'); el.scrollTop = el.scrollTop + 300; return el.scrollTop })()`)
  await sleep(400)
  const after = await evaluate(cdp, sessionId, `(() => {
    const f = document.querySelector('.ui-detail-sheet__footer')
    const r = f.getBoundingClientRect()
    const buttons = Array.from(document.querySelectorAll('.fx-detail__actions .ui-button')).map((el) => { const b = el.getBoundingClientRect(); return { text: el.textContent.trim(), w: Math.round(b.width), h: Math.round(b.height) } })
    return {
      top: Math.round(r.top), bottom: Math.round(r.bottom), vh: window.innerHeight,
      position: getComputedStyle(f).position,
      scrollTop: Math.round(document.querySelector('.ui-detail-sheet__body').scrollTop),
      buttons,
      inViewport: r.top >= 0 && r.bottom <= window.innerHeight + 1,
    }
  })()`)
  check('AC4 滚动后底部操作条仍在视口内', after.inViewport && after.scrollTop >= 300, `scrollTop ${before.scrollTop} → ${after.scrollTop}；footer ${after.top}..${after.bottom} / vh ${after.vh}`)
  check('AC4 操作条 position sticky/fixed', ['sticky', 'fixed'].includes(after.position), `position=${after.position}`)
  check('AC4 按钮命中区 ≥44×44', after.buttons.every((b) => b.w >= 44 && b.h >= 44), after.buttons.map((b) => `${b.text} ${b.w}×${b.h}`).join(' / '))
  const shot = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false }, sessionId)
  writeFileSync(join(shotDir, 'task-detail__mobile__dark__sticky-scrolled.png'), Buffer.from(shot.data, 'base64'))
}

/** 公共：装设备指标 + Fetch 拦截（probe 用，任务固定按 state 取值） */
async function installFetcher(cdp, sessionId, stateOf, fixture) {
  for (const requestId of [...pausedRequestIds]) {
    await cdp.send('Fetch.failRequest', { requestId, errorReason: 'Aborted' }, sessionId).catch(() => undefined)
    pausedRequestIds.delete(requestId)
  }
  await cdp.send('Page.navigate', { url: 'about:blank' }, sessionId)
  await cdp.send('Page.enable', {}, sessionId)
  await cdp.send('Runtime.enable', {}, sessionId)
  await cdp.send('Fetch.disable', {}, sessionId).catch(() => undefined)
  cdp.clearListeners()
  await cdp.send('Network.enable', {}, sessionId)
  await cdp.send('Network.setCacheDisabled', { cacheDisabled: true }, sessionId)
  await cdp.send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false }, sessionId)
  await cdp.send('Fetch.enable', { patterns: [{ urlPattern: '*/v1/*', requestStage: 'Request' }] }, sessionId)

  const fulfill = (requestId, responseCode, body) =>
    cdp
      .send(
        'Fetch.fulfillRequest',
        {
          requestId,
          responseCode,
          responseHeaders: [{ name: 'content-type', value: 'application/json' }],
          body: Buffer.from(typeof body === 'string' ? body : JSON.stringify(body)).toString('base64'),
        },
        sessionId,
      )
      .catch(() => undefined)
      .then(() => pausedRequestIds.delete(requestId))

  cdp.on(
    'Fetch.requestPaused',
    async ({ requestId, request }) => {
      const url = request.url
      pausedRequestIds.add(requestId)
      const state = stateOf()
      if (url.includes('/v1/tasks/decide')) {
        fixture.decidePosts = (fixture.decidePosts ?? 0) + 1
        if (fixture.conflict) {
          await fulfill(requestId, 409, { message: CONFLICT_MESSAGE })
          return
        }
        await fulfill(requestId, 200, JSON.stringify(taskFor('default')))
        return
      }
      if (url.includes('/v1/tasks/dependencies')) {
        await fulfill(requestId, 200, { items: DEPENDENCY_IDS })
        return
      }
      if (url.includes('/v1/tasks/attachments')) {
        const items = [
          ...OUTPUT_ATTACHMENTS.map((item) => ({ ...item, role: 'output' })),
          ...INPUT_ATTACHMENTS.map((item) => ({ ...item, role: 'input' })),
        ]
        await fulfill(requestId, 200, { items })
        return
      }
      if (url.includes('/v1/tasks?id=')) {
        fixture.taskGets = (fixture.taskGets ?? 0) + 1
        await fulfill(requestId, 200, JSON.stringify(taskFor(state)))
        return
      }
      if (url.includes('/v1/tasks')) {
        await fulfill(requestId, 200, { items: BOARD_TASKS })
        return
      }
      if (url.includes('/v1/projects')) {
        await fulfill(requestId, 200, JSON.stringify(PROJECT))
        return
      }
      await fulfill(requestId, 200, { items: [] })
    },
    sessionId,
  )
}

async function clickAt(cdp, sessionId, x, y) {
  await cdp.send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', clickCount: 1 }, sessionId)
  await cdp.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', clickCount: 1 }, sessionId)
}

async function main() {
  const wsUrl = process.env.ECHUAN_CDP_WS
  if (!wsUrl) {
    console.error('[detail] 错误：需要 ECHUAN_CDP_WS（浏览器级 CDP WebSocket 地址，含 token）')
    process.exit(2)
  }
  const url = process.env.ECHUAN_DETAIL_URL ?? 'http://127.0.0.1:4193/'
  const shotDirRaw = process.env.ECHUAN_SHOT_DIR ?? join(REPO_DIR, '.shots')
  const shotDir = isAbsolute(shotDirRaw) ? shotDirRaw : resolve(REPO_DIR, shotDirRaw)
  mkdirSync(shotDir, { recursive: true })

  const only = (process.env.ECHUAN_ONLY ?? '')
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
  const wants = (key) => only.length === 0 || only.includes(key) || only.includes('probes')

  const cdp = await CDP.connect(wsUrl)
  const records = []
  try {
    for (const viewport of VIEWPORTS) {
      for (const state of STATES) {
        const key = `${viewport.name}:${state}`
        if (only.length > 0 && !only.includes(key) && !only.includes(viewport.name) && !only.includes(state)) continue
        console.log(`\n[detail] ${key} —— ${viewport.width}×${viewport.height}`)
        let record = null
        let page = await createPage(cdp)
        try {
          record = await runCase(cdp, page, viewport, state, { url, shotDir })
        } catch (err) {
          await closePage(cdp, page.targetId).catch(() => undefined)
          page = await createPage(cdp)
          try {
            record = await runCase(cdp, page, viewport, state, { url, shotDir })
          } catch (err2) {
            check(`${key} 用例执行`, false, `${err2 instanceof Error ? err2.message : String(err2)}（首次：${err instanceof Error ? err.message : String(err)}）`)
            await closePage(cdp, page.targetId).catch(() => undefined)
            continue
          }
        }
        await closePage(cdp, page.targetId).catch(() => undefined)
        records.push(record)
        console.log(`[detail] 截图 ${record.shotPath}`)
        console.log(
          `[detail] 度量 state=${record.measure.state} drawer=${record.measure.drawer?.width} footer=${record.measure.footer?.top}..${record.measure.footer?.bottom} order=${orderOf(record.measure).join('>')} buttons=${record.measure.buttons.map((b) => `${b.text}:${b.width}x${b.height}`).join(',')}`,
        )
        assertionsOf(record)
      }
    }

    if (only.length === 0 || wants('probes')) {
      const probes = [
        { name: 'AC2 深链与关闭', run: (page) => closeProbe(cdp, page, { url }) },
        { name: 'AC3 评审区', run: (page) => reviewProbe(cdp, page, { url }) },
        { name: 'AC4 移动操作条', run: (page) => stickyProbe(cdp, page, { url }, shotDir) },
        { name: 'AC5 评审冲突', run: (page) => conflictProbe(cdp, page, { url }) },
      ]
      for (const probe of probes) {
        console.log(`\n[detail] ${probe.name}`)
        const page = await createPage(cdp)
        try {
          await probe.run(page)
        } catch (err) {
          check(`${probe.name} 执行`, false, err instanceof Error ? err.message : String(err))
        }
        await closePage(cdp, page.targetId).catch(() => undefined)
      }
    }
  } finally {
    cdp.close()
  }

  // 量化表：六态 × 三视口（交付报告直接引用）
  const table = records.map((record) => ({
    viewport: record.viewport.name,
    state: record.state,
    dataState: record.measure.state,
    drawerWidth: record.measure.drawer?.width ?? null,
    footerTop: record.measure.footer?.top ?? null,
    footerBottom: record.measure.footer?.bottom ?? null,
    footerPosition: record.measure.footer?.position ?? null,
    order: orderOf(record.measure),
    buttons: record.measure.buttons.map((b) => `${b.text} ${b.width}×${b.height}`),
    shot: record.shotPath,
  }))
  writeFileSync(join(shotDir, 'task-detail-shots.json'), JSON.stringify(table, null, 2))
  console.log('\n[detail] 量化表：')
  for (const row of table) {
    console.log(
      `[detail] ${row.viewport}:${row.state} state=${row.dataState} drawer=${row.drawerWidth} footer=${row.footerTop}..${row.footerBottom}(${row.footerPosition}) order=${row.order.join('>')} buttons=${row.buttons.join(',')}`,
    )
  }

  const failed = results.filter((item) => !item.ok)
  console.log(`\n[detail] 合计 ${results.length} 条断言，失败 ${failed.length} 条`)
  if (failed.length > 0) {
    for (const item of failed) console.log(`[detail] FAIL ${item.name} — ${item.detail}`)
    process.exit(1)
  }
  console.log(`[detail] 通过；截图目录 ${shotDir}`)
  process.exit(0)
}

await main()
