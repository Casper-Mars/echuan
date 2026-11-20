// echuan 看板（features/board）三视口 × 六态自检 + 截图
//
// 只做一件事：连真浏览器 → 用 CDP `Fetch` 拦截伪造 `/v1/*` → 六态各拍一张 → 量可量化指标 → PASS/FAIL + 退出码。
// 不引任何 npm 依赖（与 scripts/ui-verify.mjs 同口径：原生 WebSocket + CDP），也不改后端。
//
// 用法（浏览器与静态托管由环境提供，见 web/README.md「看板自检」）：
//   ECHUAN_CDP_WS='ws://127.0.0.1:3010?token=<token>' \
//   ECHUAN_BOARD_URL='http://127.0.0.1:4191/' \
//   ECHUAN_SHOT_DIR='.shots' \
//   node web/scripts/board-shots.mjs
//
// 环境变量：
//   ECHUAN_CDP_WS    必需。浏览器级 CDP WebSocket 地址（带 token）
//   ECHUAN_BOARD_URL 可选，默认 http://127.0.0.1:4191/ —— 指向 web/dist 的静态托管
//   ECHUAN_SHOT_DIR  可选，默认 <repo>/.shots（不入库，见 .gitignore 口径「产物目录」）
//   ECHUAN_ONLY      可选，只跑指定视口/状态，逗号分隔，如 'mobile:default,desktop:error'
//
// 六态怎么造（全部走请求拦截，不改后端）：
//   default    12 个任务（pending/ready/in_progress/in_review/done 各 2、failed/cancelled 各 1）
//   empty      /v1/tasks 返回 items: []
//   loading    /v1/tasks 永不响应 → 停在 SkeletonCard
//   error      先成功一次，再让第二笔（15s 自动刷新）返回 500 → 保留上次数据 + 错误条
//   collapsed  初始 localStorage 写入 ["done","cancelled"] 后刷新 → 验证折叠粘性
//   long-list  每列 9 张卡 → 列内滚动 + 列头 sticky
//
// 退出码：0 全部通过；1 有断言失败；2 环境/用法错误。
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, isAbsolute, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const WEB_DIR = dirname(dirname(fileURLToPath(import.meta.url)))
const REPO_DIR = dirname(WEB_DIR)

const VIEWPORTS = [
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'tablet', width: 1024, height: 768 },
  { name: 'mobile', width: 390, height: 844 },
]

const STATES = ['default', 'empty', 'loading', 'error', 'collapsed', 'long-list']

/** 7 列固定枚举（顺序 = 看板列顺序，design/screens/board.md「结构」） */
const COLUMNS = ['pending', 'ready', 'in_progress', 'in_review', 'done', 'failed', 'cancelled']

/** 折叠偏好的存储键（features/board/collapsed.ts 的 COLLAPSED_STORAGE_KEY） */
const COLLAPSED_KEY = 'echuan:board:collapsed'

const AUTO_REFRESH_MS = 15_000

/** design/refs 里移动端 TabBar 高度（设计口径 57；实现侧的 45 是已知遗留 D4，与看板几何无关） */
const REF_MOBILE_TABBAR = 57

const PROJECT = {
  project_id: 'echuan-1',
  product_id: 'echuan',
  seq_no: 1,
  name: 'Web UI 重做（issue #76）',
  context_md: '',
  status: 'open',
  create_time: '2026-11-18T10:00:00Z',
  update_time: '2026-11-20T09:00:00Z',
}

const TITLES = [
  '体积门禁脚本与构建期打印',
  '评审动作：material_id 自动带入',
  '附件 markdown 应用内预览与降级',
  '样式债出清：单一令牌源 + 死类清理',
  '看板列折叠与横向切列（移动端）',
  '详情路由化：可分享 URL + 抽屉承载',
  'Web UI 重做：技术选型决策 + 11 章技术方案',
  '收件箱合并派单队列入口',
  '旧看板 .board-column 样式块删除',
]
const KINDS = ['dev', 'dev', 'design', 'breakdown', 'dev', 'design', 'requirement', 'dev', 'dev']

/** 各列卡片数：default 对齐 design/refs 看板条的「7 列 · 12 个任务」 */
const DEFAULT_SHAPE = { pending: 2, ready: 2, in_progress: 2, in_review: 2, done: 2, failed: 1, cancelled: 1 }
const LONG_SHAPE = { pending: 9, ready: 9, in_progress: 9, in_review: 9, done: 9, failed: 9, cancelled: 9 }

function makeTasks(shape) {
  const items = []
  let n = 0
  for (const status of COLUMNS) {
    for (let i = 0; i < shape[status]; i += 1) {
      n += 1
      items.push({
        task_id: `t_01M7TKBXJ4KAMEWN07J6QY18X${String.fromCharCode(65 + (n % 26))}${COLUMNS.indexOf(status)}${i}`,
        project_id: 'echuan-1',
        kind: KINDS[n % KINDS.length],
        title: TITLES[n % TITLES.length],
        description_md: '',
        context_md: '',
        // in_review 首卡给最高优先级：design/screens/board.md default 态「P60 待审卡片高亮描边」
        priority: status === 'in_review' && i === 0 ? 60 : 30 + ((n * 5) % 30),
        status,
        attempt: n % 3,
        max_attempts: 3,
        create_time: '2026-11-19T08:00:00Z',
        update_time: `2026-11-19T${String(18 - (i % 6)).padStart(2, '0')}:${String((n * 7) % 60).padStart(2, '0')}:00Z`,
      })
    }
  }
  return items
}

/** ---------------- CDP 客户端（浏览器级 + 扁平会话） ---------------- */

class CDP {
  constructor(ws) {
    this.ws = ws
    this.seq = 0
    this.pending = new Map()
    this.listeners = new Map()
    // 连接意外断开：把所有挂起的 CDP 命令 reject 掉，脚本以明确错误退出而不是静默挂死
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

  /** 复用同一个 tab 时，每个用例开始前清掉上一轮的监听（否则拦截器会叠加） */
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

/** 被 Fetch 拦下但还没响应的请求 id：loading 用例故意挂住它，下一用例开头必须收掉 */
const pausedRequestIds = new Set()

const results = []
function check(name, ok, detail = '') {
  results.push({ name, ok, detail })
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? ` — ${detail}` : ''}`)
}


/**
 * 截图像素边界分析（在本页 canvas 里跑，与 design/refs 用同一套算法）：
 *   * 列左右边框是贯穿列高的 1px 亮线（lum >= 25），卡片边框只有卡片那么高 → 只认长亮线；
 *   * 列顶/列底由亮线段的起止给出（受 12 圆角影响，两端各缺约一个圆角，ref 与实现同等裁剪，比较差值即可）；
 *   * bottomAnchor = 桌面视口底 / 移动 TabBar 顶，用来算「列底留白」。
 */
async function analyzeShot({ url, bottomAnchor, minX }) {
  const img = new Image()
  img.src = url
  await img.decode()
  const canvas = document.createElement('canvas')
  canvas.width = img.naturalWidth
  canvas.height = img.naturalHeight
  const ctx = canvas.getContext('2d', { willReadFrequently: true })
  ctx.drawImage(img, 0, 0)
  const { data, width, height } = ctx.getImageData(0, 0, canvas.width, canvas.height)
  const lum = (x, y) => {
    const i = (y * width + x) * 4
    return 0.2126 * data[i] + 0.7152 * data[i + 1] + 0.0722 * data[i + 2]
  }
  const lines = []
  for (let x = 0; x < width; x += 1) {
    let best = { len: 0, from: -1, to: -1 }
    let start = null
    for (let y = 0; y <= height; y += 1) {
      const on = y < height && lum(x, y) >= 25
      if (on && start === null) start = y
      if (!on && start !== null) {
        if (y - start > best.len) best = { len: y - start, from: start, to: y - 1 }
        start = null
      }
    }
    if (x >= minX && best.len > Math.max(400, 0.5 * height)) lines.push({ x, ...best })
  }
  const groups = []
  for (const line of lines) {
    const last = groups[groups.length - 1]
    if (last && line.x - last.x2 <= 1) {
      last.x2 = line.x
      last.to = Math.max(last.to, line.to)
      last.from = Math.min(last.from, line.from)
    } else groups.push({ x1: line.x, x2: line.x, from: line.from, to: line.to })
  }
  const columns = []
  for (let i = 0; i < groups.length; i += 1) {
    const left = groups[i]
    const right = groups[i + 1]
    if (!right) break
    const columnWidth = right.x1 - left.x1 + 1
    if (columnWidth < 100) continue
    columns.push({
      left: left.x1,
      right: right.x2,
      width: columnWidth,
      top: Math.min(left.from, right.from),
      bottom: Math.max(left.to, right.to) + 1,
    })
    i += 1
  }
  const first = columns[0]
  const gaps = []
  for (let i = 1; i < columns.length; i += 1) gaps.push(columns[i].left - columns[i - 1].right - 1)

  // 横向扫描：取列底上方 30px（卡片之下的空白带），列左右边框是孤立的 1px 亮线
  let rowLines = []
  if (first) {
    const scanY = first.bottom - 30
    let start = null
    for (let x = 0; x <= width; x += 1) {
      const on = x < width && x >= minX && lum(x, scanY) >= 25
      if (on && start === null) start = x
      if (!on && start !== null) {
        rowLines.push(Math.round((start + x - 1) / 2))
        start = null
      }
    }
  }
  const rowCols = []
  for (let i = 0; i + 1 < rowLines.length; i += 2) {
    const l = rowLines[i]
    const r = rowLines[i + 1]
    if (r - l + 1 >= 100) rowCols.push({ left: l, right: r, width: r - l + 1 })
  }
  const rowFirst = rowCols[0]
  // 间距按相邻边框线算（列 1 右边框 → 列 2 左边框），不依赖两侧列都完整可见
  const rowGaps = []
  for (let i = 2; i <= rowLines.length - 1; i += 2) {
    const gap = rowLines[i] - rowLines[i - 1] - 1
    if (gap >= 0 && gap < 100) rowGaps.push(gap)
  }

  return {
    rowCols: rowCols.length,
    rowColLeft: rowFirst ? rowFirst.left : null,
    rowColWidth: rowFirst ? rowFirst.width : null,
    rowColGap: rowGaps.length ? rowGaps[0] : null,
    width,
    height,
    columns: columns.length,
    colLeft: first ? first.left : null,
    colWidth: first ? first.width : null,
    colGap: gaps.length ? gaps[0] : null,
    colTop: first ? first.top : null,
    colBottom: first ? first.bottom : null,
    bottomGap: first ? Math.round(bottomAnchor) - first.bottom : null,
  }
}

/** 图片 → 像素几何（refs 与我的截图走同一个函数） */
async function pixelMetrics(cdp, sessionId, pngBytes, options) {
  const url = `data:image/png;base64,${pngBytes.toString('base64')}`
  const args = JSON.stringify({ url, bottomAnchor: options.bottomAnchor, minX: options.minX })
  return evaluate(cdp, sessionId, `(${analyzeShot.toString()})(${args})`)
}

/** ---------------- 一次「视口 × 状态」的完整观测 ---------------- */

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
    const { sessionId } = await cdp.send('Target.attachToTarget', { targetId, flatten: true })
    if (sessionId) await cdp.send('Target.detachFromTarget', { sessionId }, sessionId).catch(() => undefined)
  } catch {
    // 目标可能已不存在
  }
  try {
    await cdp.send('Target.closeTarget', { targetId })
  } catch {
    // 页面已关闭
  }
}

async function evaluate(cdp, sessionId, expression) {
  const result = await cdp.send(
    'Runtime.evaluate',
    { expression, awaitPromise: true, returnByValue: true },
    sessionId,
  )
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
  const board = document.querySelector('[data-ui="board"]')
  const scroller = document.querySelector('[data-ui="board-scroller"]')
  const cols = Array.from(document.querySelectorAll('.bp-col')).map((el) => {
    const rect = el.getBoundingClientRect()
    const head = el.querySelector('.ui-board-column__head')
    const bodyRoot = el.querySelector('.ui-board-column__body')
    const body = bodyRoot ? bodyRoot.firstElementChild ?? bodyRoot : null
    return {
      status: el.dataset.status,
      width: Math.round(rect.width * 100) / 100,
      left: Math.round(rect.left * 100) / 100,
      right: Math.round(rect.right * 100) / 100,
      collapsed: el.classList.contains('ui-board-column--collapsed'),
      count: Number(el.querySelector('.ui-board-column__count')?.textContent ?? -1),
      cards: el.querySelectorAll('.ui-task-card').length,
      skeletons: el.querySelectorAll('.ui-skeleton-card').length,
      headTop: head ? Math.round(head.getBoundingClientRect().top * 100) / 100 : null,
      headPosition: head ? getComputedStyle(head).position : null,
      bodyScrollTop: body ? body.scrollTop : null,
      bodyScrollHeight: body ? body.scrollHeight : null,
      bodyClientHeight: body ? body.clientHeight : null,
    }
  })
  return {
    state: board?.dataset.boardState ?? null,
    viewport: { width: window.innerWidth, height: window.innerHeight },
    doc: {
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth: document.documentElement.clientWidth,
      scrollHeight: document.documentElement.scrollHeight,
      clientHeight: document.documentElement.clientHeight,
    },
    boardHeight: board ? Math.round(board.getBoundingClientRect().height * 100) / 100 : null,
    // 底部锚点：桌面/平板 = 视口底；移动 = 看板块底边（正好是 TabBar 顶，见 board.css）
    boardBottom: board ? Math.round(board.getBoundingClientRect().bottom * 100) / 100 : null,
    sidebarRight: document.querySelector('.ui-sidebar-nav')?.getBoundingClientRect().right ?? 0,
    notice: document.querySelector('[data-ui="board-unknown"]')?.textContent ?? null,
    totalNumber: Number(document.querySelector('[data-ui="board-total"]')?.textContent?.match(/\\d+/)?.[0] ?? -1),
    scroller: scroller
      ? {
          scrollWidth: scroller.scrollWidth,
          clientWidth: scroller.clientWidth,
          scrollLeft: Math.round(scroller.scrollLeft * 100) / 100,
          left: Math.round(scroller.getBoundingClientRect().left * 100) / 100,
        }
      : null,
    total: document.querySelector('[data-ui="board-total"]')?.textContent ?? null,
    bar: document.querySelector('[data-ui="board-bar"]')?.textContent ?? null,
    cursor: document.querySelector('[data-ui="board-cursor"]')?.textContent ?? null,
    errorBar: document.querySelector('[data-ui="error-bar"]')?.textContent ?? null,
    emptyState: document.querySelector('.ui-empty-state')?.textContent ?? null,
    columns: cols,
    collapsedPref: window.localStorage.getItem('${COLLAPSED_KEY}'),
    activeElement: document.activeElement ? {
      tag: document.activeElement.tagName,
      id: document.activeElement.id,
      value: 'value' in document.activeElement ? document.activeElement.value : null,
    } : null,
  }
})()`

async function runCase(cdp, page, viewport, state, options) {
  const { sessionId } = page
  // 复用同一个 tab：先回 about:blank、清 localStorage，再把设备指标与拦截器重新装上。
  // （每用例新建 target 会在 browserless 上累积会话，跑到第 16 个用例连接会被断开。）
  // 上一用例故意挂住的请求（loading 态）会让 Fetch.disable 卡住，先逐个收尾再继续
  for (const requestId of [...pausedRequestIds]) {
    await cdp
      .send('Fetch.failRequest', { requestId, errorReason: 'Aborted' }, sessionId)
      .catch(() => undefined)
    pausedRequestIds.delete(requestId)
  }
  await cdp.send('Page.navigate', { url: 'about:blank' }, sessionId)
  await cdp.send('Page.enable', {}, sessionId)
  await cdp.send('Runtime.enable', {}, sessionId)
  try {
    await cdp.send('Storage.clearDataForOrigin', { origin: new URL(options.url).origin, storageTypes: 'local_storage' }, sessionId)
  } catch {
    // 清存储失败不影响用例（脏值会被实现按默认值降级）
  }
  await cdp.send('Fetch.disable', {}, sessionId).catch(() => undefined)
  cdp.clearListeners()
  const shape = state === 'long-list' ? LONG_SHAPE : DEFAULT_SHAPE
  // fixture 可改：未知状态注入（AC10）、下一笔延迟（验证刷新不降级）、POST 计数（双击守卫）
  const fixture = {
    tasks: state === 'empty' ? [] : makeTasks(shape),
    delayNextMs: 0,
    posts: 0,
  }
  let taskCalls = 0

  await cdp.send('Page.enable', {}, sessionId)
  await cdp.send('Runtime.enable', {}, sessionId)
  await cdp.send(
    'Emulation.setDeviceMetricsOverride',
    { width: viewport.width, height: viewport.height, deviceScaleFactor: 1, mobile: viewport.name === 'mobile' },
    sessionId,
  )
  await cdp.send('Emulation.setFocusEmulationEnabled', { enabled: true }, sessionId)
  await cdp.send('Network.enable', {}, sessionId)
  await cdp.send('Network.setCacheDisabled', { cacheDisabled: true }, sessionId)

  // 请求拦截：/v1/* 全部由本脚本伪造（静态托管没有后端，这是允许的做法）
  await cdp.send(
    'Fetch.enable',
    { patterns: [{ urlPattern: '*/v1/*', requestStage: 'Request' }] },
    sessionId,
  )
  cdp.on(
    'Fetch.requestPaused',
    async ({ requestId, request }) => {
      const url = request.url
      const headers = [{ name: 'content-type', value: 'application/json' }]
      if (process.env.ECHUAN_DEBUG && url.includes('/v1/')) console.log('[debug] paused', request.method, url)
      pausedRequestIds.add(requestId)
      const fulfill = (responseCode, body) =>
        cdp
          .send(
            'Fetch.fulfillRequest',
            { requestId, responseCode, responseHeaders: headers, body: Buffer.from(body).toString('base64') },
            sessionId,
          )
          .catch(() => undefined)
          .then(() => {
            pausedRequestIds.delete(requestId)
          })

      if (url.includes('/v1/projects')) {
        await fulfill(200, JSON.stringify(PROJECT))
        return
      }
      if (url.includes('/v1/tasks')) {
        // 新建任务（POST）：单独计数，用来验证「同一 tick 双击只发 1 个 POST」
        if (request.method === 'POST') {
          fixture.posts += 1
          await fulfill(200, JSON.stringify(makeTasks({ pending: 1 })[0]))
          return
        }
        taskCalls += 1
        // loading：把这一笔拖到 60s（远长于本用例的观测窗口）→ 页面停在 SkeletonCard；
        // 不能「永不响应」：永远挂着的 paused 请求会让后续用例的 Fetch/Runtime 命令卡死。
        if (state === 'loading') {
          await sleep(20_000)
          await fulfill(200, JSON.stringify({ items: [] }))
          return
        }
        // error：先成功一次，再让自动刷新那一笔 500 → 保留上次数据 + 错误条
        if (state === 'error' && taskCalls > 1) {
          await fulfill(500, JSON.stringify({ message: 'internal error' }))
          return
        }
        // 需要「刷新在飞」的用例：把这一笔真的拖住（页面必须保留上次数据、不降级）
        if (fixture.delayNextMs > 0) {
          const delay = fixture.delayNextMs
          fixture.delayNextMs = 0
          await sleep(delay)
        }
        await fulfill(200, JSON.stringify({ items: fixture.tasks }))
        return
      }
      await fulfill(404, JSON.stringify({ message: 'not found' }))
    },
    sessionId,
  )

  await cdp.send('Page.navigate', { url: `${options.url}#/projects/echuan-1/tasks` }, sessionId)

  const settle = state === 'loading' || state === 'error' ? 1500 : 900
  if (state === 'error') {
    // 等 15s 自动刷新那一笔 500 回来（错误条出现即视为刷新已发生）
    await waitFor(
      cdp,
      sessionId,
      `document.querySelector('[data-ui="error-bar"]') !== null`,
      AUTO_REFRESH_MS + 8000,
      '自动刷新失败 → 错误条出现',
    )
  } else {
    await waitFor(
      cdp,
      sessionId,
      `document.querySelector('[data-ui="board"]') !== null`,
      20_000,
      '看板挂载',
    )
    await sleep(settle)
  }

  const measure = await evaluate(cdp, sessionId, MEASURE)
  // 筛选控件的可访问名（标签在移动端视觉收起，可访问性不能牺牲）
  const ax = state === 'default' ? await axProbe(cdp, sessionId) : null

  // 移动端 M1：滚到最右，最后一列右边界必须落在视口内（AC2）
  let m1 = null
  if (viewport.name === 'mobile') {
    m1 = await evaluate(
      cdp,
      sessionId,
      `(() => {
        const scroller = document.querySelector('[data-ui="board-scroller"]')
        if (!scroller) return null
        scroller.scrollLeft = scroller.scrollWidth
        const cols = Array.from(document.querySelectorAll('.bp-col'))
        const last = cols[cols.length - 1]
        const rect = last.getBoundingClientRect()
        const firstRect = cols[0].getBoundingClientRect()
        return {
          scrollWidth: scroller.scrollWidth,
          clientWidth: scroller.clientWidth,
          scrollLeft: Math.round(scroller.scrollLeft * 100) / 100,
          lastColRight: Math.round(rect.right * 100) / 100,
          lastColWidth: Math.round(rect.width * 100) / 100,
          colWidth: Math.round(firstRect.width * 100) / 100,
          viewport: window.innerWidth,
          expectedMinWidth: Math.min(window.innerWidth * 0.86, 320),
          snapType: getComputedStyle(scroller).scrollSnapType,
        }
      })()`,
    )
    // 量完回到起点：截图要拍的是「第 1 / 7 列」的初始取景
    await evaluate(
      cdp,
      sessionId,
      `(() => { const el = document.querySelector('[data-ui="board-scroller"]'); if (el) el.scrollLeft = 0; return true })()`,
    )
    await sleep(300)
  }

  // long-list：滚动列内正文，验证列头 sticky（列头在滚动前后位置不变）+ 列内滚动真的发生（AC1）
  let sticky = null
  if (state === 'long-list' || state === 'default') {
    sticky = await evaluate(
      cdp,
      sessionId,
      `(() => {
        const col = document.querySelector('.bp-col')
        const root = col?.querySelector('.ui-board-column__body')
        const body = root ? root.firstElementChild ?? root : null
        const head = col?.querySelector('.ui-board-column__head')
        if (!body || !head) return null
        const before = { top: Math.round(head.getBoundingClientRect().top * 100) / 100, scrollTop: body.scrollTop }
        body.scrollTop = 200
        const after = { top: Math.round(head.getBoundingClientRect().top * 100) / 100, scrollTop: body.scrollTop }
        const scrollHeight = body.scrollHeight
        const clientHeight = body.clientHeight
        body.scrollTop = before.scrollTop
        return {
          headTopBefore: before.top,
          headTopAfter: after.top,
          bodyScrollTopBefore: before.scrollTop,
          bodyScrollTopAfter: after.scrollTop,
          scrollHeight,
          clientHeight,
        }
      })()`,
    )
  }

  // 折叠（AC3）：真实点击 failed 列头 → 折叠 → 刷新页面 → 折叠仍在 → 再展开复原。
  // 同时把「折叠前后 localStorage 值 + 刷新后列宽」原样记下来，供验收报告引用。
  let collapse = null
  if (state === 'collapsed') {
    collapse = await collapsedProbe(cdp, sessionId)
  }

  // 自动刷新不打断：滚动位置与输入焦点在一轮 15s 刷新前后保持不变（卡片约束）
  // 用 long-list 跑才有可滚动的列内正文；default 同时覆盖横向滚动与筛选焦点。
  let refresh = null
  if ((state === 'default' || state === 'long-list') && viewport.name === 'desktop') {
    refresh = await refreshProbe(cdp, sessionId, () => taskCalls)
  }

  // 截图取景：collapsed 滚到最右让 56 竖条入画（对齐 design/refs 的 collapsed 取景）；
  // 其余态回到 0 起点（前面的 M1 / 刷新探针动过横向与列内滚动，不还原会拍到中途位置）。
  await evaluate(
    cdp,
    sessionId,
    state === 'collapsed'
      ? `(() => { const el = document.querySelector('[data-ui="board-scroller"]'); if (el) el.scrollLeft = el.scrollWidth; return true })()`
      : `(() => {
          const el = document.querySelector('[data-ui="board-scroller"]')
          if (el) el.scrollLeft = 0
          const root = document.querySelector('.bp-col .ui-board-column__body')
          const body = root ? root.firstElementChild ?? root : null
          if (body) body.scrollTop = 0
          return true
        })()`,
  )
  await sleep(400)

  const shotDir = options.shotDir
  const shotPath = join(shotDir, `board__${viewport.name}__dark__${state}.png`)
  const shot = await cdp.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false }, sessionId)
  const shotBytes = Buffer.from(shot.data, 'base64')
  writeFileSync(shotPath, shotBytes)

  // ===== 像素量化：我的截图 vs design/refs（同一套边界算法），只对 default 态做 =====
  let pixels = null
  if (state === 'default') {
    const minX = Math.round(measure.sidebarRight) + 2
    const refAnchor = viewport.name === 'mobile' ? viewport.height - REF_MOBILE_TABBAR : viewport.height
    const myAnchor = Math.round(measure.boardBottom)
    const shotPixel = await pixelMetrics(cdp, sessionId, shotBytes, { bottomAnchor: myAnchor, minX })
    const refPath = join(REPO_DIR, 'design', 'refs', `board__${viewport.name}__dark__default.png`)
    let refPixel = null
    try {
      refPixel = await pixelMetrics(cdp, sessionId, readFileSync(refPath), { bottomAnchor: refAnchor, minX })
    } catch {
      refPixel = null
    }
    pixels = { refPixel, shotPixel, refAnchor, myAnchor, minX, refPath }
  }

  // ===== 刷新在飞不降级（AC4 口径的补充）：把下一笔 /v1/tasks 拖住 4s，此时 DOM 必须还是「有数据态」=====
  let midflight = null
  if (state === 'default' && viewport.name === 'desktop') {
    midflight = await midflightProbe(cdp, sessionId, fixture, () => taskCalls)
  }

  // ===== 同一 tick 双击「创建」只发 1 个 POST（CreateTaskSheet 的 ref 守门）=====
  let doubleSubmit = null
  if (state === 'default' && viewport.name === 'desktop') {
    doubleSubmit = await doubleSubmitProbe(cdp, sessionId, fixture)
  }

  // ===== 未知 status 不静默记账：页头总数必须等于七列计数之和（AC10）=====
  let unknownStatus = null
  if (state === 'default' && viewport.name === 'desktop') {
    unknownStatus = await unknownStatusProbe(cdp, sessionId, fixture, options.url)
  }

  // 点卡片 → 详情独立路由（design/screens/board.md「交互」：点卡片 → #/projects/:pid/tasks/:tid）
  // 放在截图之后：这一步会离开看板，不能影响已拍到的画面。
  let navigate = null
  if (state === 'default' && viewport.name === 'desktop') {
    const target = await evaluate(
      cdp,
      sessionId,
      `(() => {
        const card = document.querySelector('.ui-task-card__hit')
        if (!card) return null
        const label = card.getAttribute('aria-label')
        card.click()
        return { label }
      })()`,
    )
    await sleep(700)
    const hash = await evaluate(cdp, sessionId, 'location.hash')
    navigate = { cardLabel: target?.label ?? null, hash }
  }

  return { viewport, state, measure, m1, sticky, refresh, collapse, navigate, midflight, doubleSubmit, unknownStatus, ax, pixels, shotPath }
}

/** 筛选控件的可访问名（移动端视觉上收起了标签，读屏必须仍念「类型」「状态」） */
async function axProbe(cdp, sessionId) {
  await cdp.send('DOM.enable', {}, sessionId)
  await cdp.send('Accessibility.enable', {}, sessionId)
  const { root } = await cdp.send('DOM.getDocument', { depth: -1 }, sessionId)
  const names = {}
  for (const id of ['bp-filter-kind', 'bp-filter-status']) {
    const { nodeId } = await cdp.send('DOM.querySelector', { nodeId: root.nodeId, selector: '#' + id }, sessionId)
    if (!nodeId) {
      names[id] = null
      continue
    }
    const tree = await cdp.send('Accessibility.getPartialAXTree', { nodeId, fetchRelatives: false }, sessionId)
    const node = tree.nodes?.[0]
    names[id] = { role: node?.role?.value ?? null, name: node?.name?.value ?? null }
  }
  return names
}

/** 刷新在飞：拖住下一笔 /v1/tasks，断言页面仍是「有数据态」（不降级成 loading / 不清计数） */
async function midflightProbe(cdp, sessionId, fixture, taskCallCount) {
  const callsBefore = taskCallCount()
  fixture.delayNextMs = 4000
  // 等自动刷新那一笔真的进到拦截器（在飞）
  const deadline = Date.now() + AUTO_REFRESH_MS + 8000
  while (Date.now() < deadline && taskCallCount() === callsBefore) await sleep(100)
  const inFlight = taskCallCount() > callsBefore
  const during = await evaluate(
    cdp,
    sessionId,
    `(() => {
      const board = document.querySelector('[data-ui="board"]')
      const cols = Array.from(document.querySelectorAll('.bp-col'))
      return {
        state: board?.dataset.boardState ?? null,
        total: document.querySelector('[data-ui="board-total"]')?.textContent ?? null,
        countSum: cols.reduce((sum, el) => sum + Number(el.querySelector('.ui-board-column__count')?.textContent ?? 0), 0),
        cards: document.querySelectorAll('.ui-task-card').length,
        skeletons: document.querySelectorAll('.ui-skeleton-card').length,
      }
    })()`,
  )
  // 等在飞那笔落地，别把未完成的请求留给后续用例
  await sleep(4200)
  return { inFlight, during }
}

/** 双击守门：同一 JS tick 里连点两次「创建」，只允许 1 个 POST /v1/tasks */
async function doubleSubmitProbe(cdp, sessionId, fixture) {
  const opened = await evaluate(
    cdp,
    sessionId,
    `(() => {
      const button = Array.from(document.querySelectorAll('.bp-head__actions button'))
        .find((el) => el.textContent.includes('新建任务'))
      if (!button) return false
      button.click()
      return true
    })()`,
  )
  if (!opened) return null
  await sleep(500)
  // 用真实输入事件（CDP Input.insertText）而不是改 value 再派发合成事件：
  // React 的受控输入对合成 input 事件不可靠，前者才是「用户输入」的真实路径。
  const doc = await cdp.send('DOM.getDocument', { depth: -1 }, sessionId)
  const inputNode = await cdp.send('DOM.querySelector', { nodeId: doc.root.nodeId, selector: '#bp-create-title' }, sessionId)
  if (inputNode.nodeId) await cdp.send('DOM.focus', { nodeId: inputNode.nodeId }, sessionId)
  await cdp.send('Input.insertText', { text: '双击守门用例' }, sessionId)
  await sleep(300)
  const typed = await evaluate(
    cdp,
    sessionId,
    `(() => {
      const input = document.querySelector('#bp-create-title')
      return Boolean(input) && input.value === '双击守门用例'
    })()`,
  )
  if (!typed) return { opened, typed, clicked: false, posts: fixture.posts, buttonState: null }
  const before = fixture.posts
  // 同一 tick 连点两次（busy 这个 state 还没落地，只有 ref 能挡住）
  const clicked = await evaluate(
    cdp,
    sessionId,
    `(() => {
      const buttons = Array.from(document.querySelectorAll('.ui-detail-sheet__footer button'))
      const submit = buttons.find((el) => el.textContent.includes('创建'))
      if (!submit) return false
      submit.click()
      submit.click()
      return true
    })()`,
  )
  if (!clicked) return { opened, typed, clicked, posts: fixture.posts - before }
  const buttonState = await evaluate(
    cdp,
    sessionId,
    `(() => {
      const buttons = Array.from(document.querySelectorAll('.ui-detail-sheet__footer button'))
      const submit = buttons.find((el) => el.textContent.includes('创建'))
      return submit ? { text: submit.textContent, disabled: submit.disabled } : null
    })()`,
  )
  await sleep(1200)
  if (process.env.ECHUAN_DEBUG) {
    const dbg = await evaluate(cdp, sessionId, `(() => {
      const sheet = document.querySelector('.ui-detail-sheet')
      const submit = Array.from(document.querySelectorAll('.ui-detail-sheet__footer button')).find((el) => el.textContent.includes('创建'))
      return {
        sheetText: sheet ? sheet.textContent.slice(0, 200) : null,
        submit: submit ? { text: submit.textContent, disabled: submit.disabled, busy: submit.getAttribute('aria-busy'), cls: submit.className.slice(0, 80) } : null,
        inputValue: document.querySelector('#bp-create-title')?.value ?? null,
      }
    })()`)
    console.log('[debug] posts =', fixture.posts, JSON.stringify(dbg))
  }
  // 关掉面板（创建成功后组件自己会关；这里兜底）
  await evaluate(
    cdp,
    sessionId,
    `(() => {
      const close = document.querySelector('.ui-detail-sheet button[aria-label="关闭详情"]')
      if (close) close.click()
      return true
    })()`,
  )
  await sleep(300)
  return { opened, typed, clicked, posts: fixture.posts - before, buttonState }
}

/** 未知 status：注入 2 条 7 列枚举外的状态，断言页头总数 = 七列计数之和（且给出提示，不静默丢） */
async function unknownStatusProbe(cdp, sessionId, fixture, url) {
  const known = makeTasks(DEFAULT_SHAPE)
  fixture.tasks = [
    ...known,
    { ...known[0], task_id: 't_unknown_status_1', status: 'archived', title: '异常状态任务 A' },
    { ...known[1], task_id: 't_unknown_status_2', status: 'deleted', title: '异常状态任务 B' },
  ]
  // 上一步「点卡片进详情」把路由切走了，这里必须重新导航回看板（reload 会停在详情路由）
  await cdp.send('Page.navigate', { url: `${url}#/projects/echuan-1/tasks` }, sessionId)
  await sleep(300)
  // 只改 hash 属同一文档导航（不会重新取数），必须整页 reload 才会用新的 fixture 再拉一次
  await cdp.send('Page.reload', {}, sessionId)
  await waitFor(cdp, sessionId, `document.querySelector('[data-ui="board"]') !== null`, 20_000, '未知状态用例重载')
  await sleep(800)
  const measured = await evaluate(cdp, sessionId, MEASURE)
  const columns = measured.columns ?? []
  return {
    rawItems: fixture.tasks.length,
    total: measured.totalNumber,
    countSum: columns.reduce((sum, column) => sum + column.count, 0),
    notice: measured.notice,
  }
}

/** 桌面 default / long-list：滚动到中段 + 聚焦筛选输入 → 等一轮自动刷新 → 比对前后 */
async function refreshProbe(cdp, sessionId, taskCallCount) {
  const callsBefore = taskCallCount()
  const before = await evaluate(
    cdp,
    sessionId,
    `(() => {
      const scroller = document.querySelector('[data-ui="board-scroller"]')
      const root = document.querySelector('.bp-col .ui-board-column__body')
      const body = root ? root.firstElementChild ?? root : null
      scroller.scrollLeft = 240
      if (body) body.scrollTop = 120
      const input = document.querySelector('#bp-filter-kind')
      if (input) input.focus()
      return {
        scrollLeft: Math.round(scroller.scrollLeft * 100) / 100,
        scrollTop: body ? body.scrollTop : null,
        activeId: document.activeElement?.id ?? null,
        activeValue: document.activeElement?.value ?? null,
      }
    })()`,
  )
  // 一轮 15s 自动刷新 + 余量
  await sleep(AUTO_REFRESH_MS + 2500)
  const after = await evaluate(
    cdp,
    sessionId,
    `(() => {
      const scroller = document.querySelector('[data-ui="board-scroller"]')
      const root = document.querySelector('.bp-col .ui-board-column__body')
      const body = root ? root.firstElementChild ?? root : null
      return {
        scrollLeft: Math.round(scroller.scrollLeft * 100) / 100,
        scrollTop: body ? body.scrollTop : null,
        activeId: document.activeElement?.id ?? null,
        activeValue: document.activeElement?.value ?? null,
        cards: document.querySelectorAll('.ui-task-card').length,
      }
    })()`,
  )
  const callsAfter = taskCallCount()
  return { before, after, taskCallsBefore: callsBefore, taskCallsAfter: callsAfter, waitedMs: AUTO_REFRESH_MS + 2500 }
}

/** 折叠态（AC3）：读现状 → 点 failed 列头折叠 → 刷新页面验证粘性 → 再展开复原 */
async function collapsedProbe(cdp, sessionId) {
  const readState = () =>
    evaluate(
      cdp,
      sessionId,
      `(() => {
        const col = document.querySelector('.bp-col[data-status="failed"]')
        return {
          width: col ? Math.round(col.getBoundingClientRect().width * 100) / 100 : null,
          collapsed: col ? col.classList.contains('ui-board-column--collapsed') : null,
          ariaExpanded: col ? col.querySelector('.ui-board-column__head')?.getAttribute('aria-expanded') : null,
          pref: window.localStorage.getItem('${COLLAPSED_KEY}'),
        }
      })()`,
    )

  // 同一浏览器 profile 跨用例共享 localStorage：先清掉偏好并刷新，回到「无偏好 → 默认折叠」的起点
  await evaluate(
    cdp,
    sessionId,
    `(() => { window.localStorage.removeItem('${COLLAPSED_KEY}'); return true })()`,
  )
  await cdp.send('Page.reload', {}, sessionId)
  await waitFor(
    cdp,
    sessionId,
    `document.querySelector('.bp-col[data-status="failed"]') !== null`,
    20_000,
    '清偏好后看板挂载',
  )
  await sleep(600)

  const initial = await readState()

  // 点 failed 列头：折叠（列头按钮的 aria-expanded 同步翻转）
  await evaluate(
    cdp,
    sessionId,
    `(() => {
      const head = document.querySelector('.bp-col[data-status="failed"] .ui-board-column__head')
      if (head) head.click()
      return true
    })()`,
  )
  await sleep(250)
  const afterCollapse = await readState()

  // 刷新页面：折叠状态应当从 localStorage 还原
  const href = await evaluate(cdp, sessionId, 'location.href')
  await cdp.send('Page.navigate', { url: href }, sessionId)
  await waitFor(
    cdp,
    sessionId,
    `document.querySelector('.bp-col[data-status="failed"]') !== null`,
    20_000,
    '刷新后看板挂载',
  )
  await sleep(700)
  const afterReload = await readState()

  // 复原：再点一次展开（回到默认的两列折叠）
  await evaluate(
    cdp,
    sessionId,
    `(() => {
      const head = document.querySelector('.bp-col[data-status="failed"] .ui-board-column__head')
      if (head) head.click()
      return true
    })()`,
  )
  await sleep(250)
  const afterExpand = await readState()

  return { initial, afterCollapse, afterReload, afterExpand }
}

/** ---------------- 断言 ---------------- */

function assertionsOf(record) {
  const { viewport, state, measure, m1, sticky } = record
  const tag = `${viewport.name}/${state}`
  const columns = measure.columns
  const collapsedWidth = columns.filter((column) => column.collapsed).map((column) => column.width)
  const expandedWidth = columns.filter((column) => !column.collapsed).map((column) => column.width)
  // 展开列宽随视口：桌面/平板 280（design/screens/board.md「断点」），移动端 min(86vw, 320)
  const expectedExpanded = viewport.name === 'mobile' ? Math.min(viewport.width * 0.86, 320) : 280
  const dataCount = measure.total?.match(/\d+/)?.[0]
  const cardSum = columns.reduce((sum, column) => sum + column.cards, 0)
  const countSum = columns.reduce((sum, column) => sum + column.count, 0)

  check(
    `${tag} 页面无纵向滚动（列内滚动）`,
    measure.doc.scrollHeight === measure.doc.clientHeight,
    `scrollHeight=${measure.doc.scrollHeight} clientHeight=${measure.doc.clientHeight}`,
  )
  check(
    `${tag} 文档无横向溢出`,
    measure.doc.scrollWidth === measure.doc.clientWidth,
    `scrollWidth=${measure.doc.scrollWidth} clientWidth=${measure.doc.clientWidth}`,
  )

  if (state === 'empty') {
    // 空态按设计不渲染 7 个空列（design/screens/board.md empty 行）
    check(
      `${tag} 空态整块空态、不渲染 7 个空列`,
      measure.emptyState !== null && columns.length === 0,
      `emptyState=${Boolean(measure.emptyState)} columns=${columns.length}`,
    )
    return
  }

  check(`${tag} 列数 = 7`, columns.length === 7, `columns=${columns.length}`)

  if (state === 'loading') {
    // 折叠列按设计不渲染正文，骨架只出现在展开列
    const skeletonOk = columns.filter((column) => !column.collapsed).every((column) => column.skeletons === 2)
    check(`${tag} 每列 2 个 SkeletonCard`, skeletonOk, `skeletons=${columns.map((column) => column.skeletons).join(',')}`)
    check(`${tag} 列头与筛选稳定（7 个列头都在）`, columns.every((column) => column.headTop !== null), `heads=${columns.map((column) => column.headTop).join(',')}`)
    return
  }

  if (measure.total !== null) {
    // 折叠列（done/cancelled）按设计只留 56 竖条 + 计数，卡片不进 DOM，所以卡片数和只算展开列
    const expandedCardSum = columns.filter((column) => !column.collapsed).reduce((sum, column) => sum + column.cards, 0)
    const expandedCountSum = columns.filter((column) => !column.collapsed).reduce((sum, column) => sum + column.count, 0)
    check(
      `${tag} 计数与数据条数一致`,
      dataCount === String(countSum) &&
        expandedCountSum === expandedCardSum &&
        columns.every((column) => column.collapsed || column.cards === column.count),
      `页头=${measure.total} 列头计数和=${countSum} 展开列卡片数=${expandedCardSum}`,
    )
    check(
      `${tag} 折叠列计数仍可读`,
      columns.filter((column) => column.collapsed).every((column) => column.count > 0),
      `折叠列计数=${columns.filter((column) => column.collapsed).map((column) => `${column.status}:${column.count}`).join(`,`)}`,
    )
  }

  check(
    `${tag} 展开列宽 = ${Math.round(expectedExpanded)}（${viewport.name === 'mobile' ? 'min(86vw,320)' : '280'}）`,
    expandedWidth.every((width) => Math.abs(width - expectedExpanded) < 0.51),
    `expanded=${[...new Set(expandedWidth)].join(',')}`,
  )
  check(
    `${tag} 折叠列宽 = 56（done/cancelled 默认折叠）`,
    collapsedWidth.length > 0 && collapsedWidth.every((width) => Math.abs(width - 56) < 0.51),
    `collapsed=${[...new Set(collapsedWidth)].join(',')} 列=${columns.filter((column) => column.collapsed).map((column) => column.status).join('+')}`,
  )

  if (state === 'error') {
    check(`${tag} 错误条出现（shell/ErrorBar）`, measure.errorBar !== null, `errorBar=${JSON.stringify(measure.errorBar)}`)
    check(`${tag} 保留上次数据（卡片仍在、无白屏）`, cardSum > 0, `卡片数=${cardSum}`)
  }

  if (sticky) {
    check(
      `${tag} 列头 sticky（滚动列内正文后位置不变）`,
      sticky.headTopBefore === sticky.headTopAfter,
      `headTop ${sticky.headTopBefore} → ${sticky.headTopAfter}；body scrollTop ${sticky.bodyScrollTopBefore} → ${sticky.bodyScrollTopAfter}`,
    )
  }

  if (state === 'long-list') {
    const expandedForScroll = columns.filter((column) => !column.collapsed)
    check(
      `${tag} 列内真的滚动（long-list）`,
      expandedForScroll.every((column) => column.bodyScrollHeight > column.bodyClientHeight),
      `scrollHeight/clientHeight=${
        expandedForScroll.map((column) => `${column.status}:${column.bodyScrollHeight}/${column.bodyClientHeight}`).join(',')
      }`,
    )
    check(
      `${tag} 每列 ≥8 张卡`,
      columns.filter((column) => !column.collapsed).every((column) => column.cards >= 8),
      `cards=${columns.map((column) => `${column.status}:${column.cards}`).join(',')}`,
    )
  }

  if (record.collapse) {
    const { initial, afterCollapse, afterReload, afterExpand } = record.collapse
    check(
      `${tag} done/cancelled 默认折叠为 56 竖条`,
      columns.filter((column) => column.collapsed).map((column) => column.status).join('+') === 'done+cancelled',
      `折叠列=${columns.filter((column) => column.collapsed).map((column) => column.status).join('+')}`,
    )
    check(
      `${tag} 点击列头折叠 failed：${Math.round(expectedExpanded)} → 56（aria-expanded 同步）`,
      Math.abs(initial.width - expectedExpanded) < 0.51 &&
        Math.abs(afterCollapse.width - 56) < 0.51 &&
        afterCollapse.ariaExpanded === 'false',
      `width ${initial.width} → ${afterCollapse.width}；aria-expanded ${initial.ariaExpanded} → ${afterCollapse.ariaExpanded}`,
    )
    check(
      `${tag} 折叠偏好写入 localStorage`,
      initial.pref === null && afterCollapse.pref === '["done","failed","cancelled"]',
      `localStorage['${COLLAPSED_KEY}']: ${initial.pref} → ${afterCollapse.pref}`,
    )
    check(
      `${tag} 刷新页面后折叠状态保持（列宽仍 56）`,
      Math.abs(afterReload.width - 56) < 0.51 && afterReload.pref === '["done","failed","cancelled"]',
      `刷新后 width=${afterReload.width}；localStorage=${afterReload.pref}`,
    )
    check(
      `${tag} 再点一次展开回 ${Math.round(expectedExpanded)} 并写回偏好`,
      Math.abs(afterExpand.width - expectedExpanded) < 0.51 && afterExpand.pref === '["done","cancelled"]',
      `width=${afterExpand.width}；localStorage=${afterExpand.pref}`,
    )
  }

  // ===== 与 design/refs 的像素边界对照（第 1/3 项返工的主证据）=====
  if (record.pixels?.refPixel) {
    const { refPixel, shotPixel } = record.pixels
    const domLeft = columns[0]?.left ?? null
    const domGap = columns.length > 1 ? Math.round(columns[1].left - columns[0].right) : null
    const near = (a, b, tol) => a !== null && b !== null && Math.abs(a - b) <= tol
    const refLeft = refPixel.rowColLeft ?? refPixel.colLeft
    const refGap = refPixel.rowColGap ?? refPixel.colGap
    const refWidth = refPixel.rowColWidth ?? refPixel.colWidth
    check(
      `${tag} 列 1 左边 = ${refLeft}（ref 像素）`,
      near(domLeft, refLeft, 1) && near(shotPixel.rowColLeft ?? shotPixel.colLeft, refLeft, 1),
      `DOM=${domLeft} 截图=${shotPixel.rowColLeft ?? shotPixel.colLeft} ref=${refLeft}`,
    )
    check(
      `${tag} 列间距 = ${refGap}（ref 像素）`,
      near(domGap, refGap, 1) && near(shotPixel.rowColGap ?? shotPixel.colGap, refGap, 1),
      `DOM=${domGap} 截图=${shotPixel.rowColGap ?? shotPixel.colGap} ref=${refGap}`,
    )
    check(
      `${tag} 列宽 = ${refWidth}（ref 像素）`,
      near(domLeft !== null ? columns[0].width : null, refWidth, 1) &&
        near(shotPixel.rowColWidth ?? shotPixel.colWidth, refWidth, 1),
      `DOM=${columns[0]?.width} 截图=${shotPixel.rowColWidth ?? shotPixel.colWidth} ref=${refWidth}`,
    )
    check(
      `${tag} 列顶 = ${refPixel.colTop}±2（ref 像素，页头高度）`,
      near(shotPixel.colTop, refPixel.colTop, 2),
      `截图=${shotPixel.colTop} ref=${refPixel.colTop}`,
    )
    check(
      `${tag} 列底留白 = ${refPixel.bottomGap}±2（ref 像素）`,
      near(shotPixel.bottomGap, refPixel.bottomGap, 2),
      `截图=${shotPixel.bottomGap}（锚点 ${record.pixels.myAnchor}）ref=${refPixel.bottomGap}（锚点 ${record.pixels.refAnchor}）`,
    )
  } else if (state === 'default') {
    check(`${tag} 与 ref 的像素对照`, false, `读不到 design/refs/${viewport.name} 基准图`)
  }

  // ===== 筛选控件的可访问名（移动端视觉收起标签，但 <label for> 仍在，读屏必须念得出）=====
  if (record.ax) {
    const kind = record.ax['bp-filter-kind']
    const status = record.ax['bp-filter-status']
    check(
      `${tag} 筛选控件可访问名（AX）：类型 / 状态`,
      // Mantine Select 的输入框在 AX 树里是 textbox/combobox（版本/场景不同），这里只断言「有可访问名」
      kind?.name === '类型' && status?.name === '状态' && Boolean(kind?.role) && Boolean(status?.role),
      `kind=${kind?.role}/${kind?.name} status=${status?.role}/${status?.name}`,
    )
  }

  // ===== 刷新在飞不降级：有数据时刷新期间不得回到 loading / 不丢计数 =====
  if (record.midflight) {
    const { inFlight, during } = record.midflight
    check(
      `${tag} 刷新在飞时保留上次计数与卡片（不降级成 loading）`,
      inFlight && during.state === 'default' && during.cards > 0 && during.skeletons === 0,
      `在飞=${inFlight} state=${during.state} 页头=${during.total} 列计数和=${during.countSum} 卡片=${during.cards} 骨架=${during.skeletons}`,
    )
  }

  // ===== 同一 tick 双击「创建」只发 1 个 POST =====
  if (record.doubleSubmit) {
    check(
      `${tag} 同一 tick 双击创建只发 1 个 POST`,
      record.doubleSubmit.posts === 1,
      `POST /v1/tasks 次数=${record.doubleSubmit.posts}（opened=${record.doubleSubmit.opened} typed=${record.doubleSubmit.typed} clicked=${record.doubleSubmit.clicked} button=${JSON.stringify(record.doubleSubmit.buttonState)}）`,
    )
  }

  // ===== 未知 status：页头总数 = 七列计数之和，且显式提示被排除的条数 =====
  if (record.unknownStatus) {
    const { rawItems, total, countSum, notice } = record.unknownStatus
    check(
      `${tag} 未知 status 不计入页头：页头总数 == 七列计数之和`,
      total === countSum && countSum === rawItems - 2 && /2 条任务的状态不在 7 列枚举内/.test(notice ?? ''),
      `原始 ${rawItems} 条 → 页头 ${total} / 列计数和 ${countSum}；提示=${JSON.stringify(notice)}`,
    )
  }

  if (m1) {
    check(
      `${tag} scrollWidth > clientWidth`,
      m1.scrollWidth > m1.clientWidth,
      `scrollWidth=${m1.scrollWidth} clientWidth=${m1.clientWidth} 视口=${m1.viewport}`,
    )
    check(
      `${tag} 滚到最右后最后一列右边界 ≤ 视口+1`,
      m1.lastColRight <= m1.viewport + 1,
      `lastColRight=${m1.lastColRight} 视口=${m1.viewport}`,
    )
    check(
      `${tag} 列宽 ≥ min(86vw,320)`,
      m1.colWidth + 0.01 >= m1.expectedMinWidth,
      `colWidth=${m1.colWidth} min(86vw,320)=${Math.round(m1.expectedMinWidth * 100) / 100}`,
    )
    check(`${tag} scroll-snap 生效`, m1.snapType.includes('mandatory'), `scroll-snap-type=${m1.snapType}`)
  }

  if (record.navigate) {
    check(
      `${tag} 点卡片进入详情路由`,
      /^#\/projects\/echuan-1\/tasks\/t_/.test(record.navigate.hash ?? ''),
      `hash=${record.navigate.hash}；卡片 aria-label=${record.navigate.cardLabel}`,
    )
  }

  if (record.refresh) {
    const { before, after } = record.refresh
    check(
      `${tag} 确实发生了一轮自动刷新（/v1/tasks 请求数增加）`,
      record.refresh.taskCallsAfter > record.refresh.taskCallsBefore,
      `/v1/tasks 请求数 ${record.refresh.taskCallsBefore} → ${record.refresh.taskCallsAfter}（等待 ${record.refresh.waitedMs}ms）`,
    )
    check(
      `${tag} 自动刷新不打断横向滚动`,
      before.scrollLeft === after.scrollLeft,
      `scrollLeft ${before.scrollLeft} → ${after.scrollLeft}`,
    )
    check(
      `${tag} 自动刷新不打断列内滚动`,
      before.scrollTop === after.scrollTop,
      `scrollTop ${before.scrollTop} → ${after.scrollTop}`,
    )
    check(
      `${tag} 自动刷新不打断输入焦点`,
      before.activeId === after.activeId && before.activeId === 'bp-filter-kind' && before.activeValue === after.activeValue,
      `focus ${before.activeId}(${before.activeValue}) → ${after.activeId}(${after.activeValue})`,
    )
    check(`${tag} 刷新后仍有数据`, after.cards > 0, `卡片数=${after.cards}`)
  }
}

/** ---------------- 主流程 ---------------- */

async function main() {
  const wsUrl = process.env.ECHUAN_CDP_WS
  if (!wsUrl) {
    console.error('[board] 错误：需要 ECHUAN_CDP_WS（浏览器级 CDP WebSocket 地址，含 token）')
    process.exit(2)
  }
  const url = process.env.ECHUAN_BOARD_URL ?? 'http://127.0.0.1:4191/'
  const shotDirRaw = process.env.ECHUAN_SHOT_DIR ?? join(REPO_DIR, '.shots')
  const shotDir = isAbsolute(shotDirRaw) ? shotDirRaw : resolve(REPO_DIR, shotDirRaw)
  mkdirSync(shotDir, { recursive: true })

  const only = (process.env.ECHUAN_ONLY ?? '')
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)

  const cdp = await CDP.connect(wsUrl)
  const records = []
  // 一个用例一个 tab：同一 tab 连续跑多个用例后 Chromium 侧会变慢甚至卡死（实测），
  // 用例之间不共享状态（localStorage 由 runCase 每次清），换 tab 的代价远小于整轮挂掉；
  // 单用例失败时再换一个新 tab 重试一次，仍失败才记 FAIL（不会被 infra 抖动伪装成通过）。
  try {
    for (const viewport of VIEWPORTS) {
      for (const state of STATES) {
        const key = `${viewport.name}:${state}`
        if (only.length > 0 && !only.includes(key) && !only.includes(viewport.name) && !only.includes(state)) {
          continue
        }
        console.log(`\n[board] ${key} —— ${viewport.width}×${viewport.height}`)
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
            check(
              `${key} 用例执行`,
              false,
              `${err2 instanceof Error ? err2.message : String(err2)}（首次失败：${err instanceof Error ? err.message : String(err)}）`,
            )
            await closePage(cdp, page.targetId).catch(() => undefined)
            continue
          }
        }
        await closePage(cdp, page.targetId).catch(() => undefined)
        records.push(record)
        console.log(`[board] 截图 ${record.shotPath}`)
        console.log(
          `[board] 度量 ${JSON.stringify({
            state: record.measure.state,
            total: record.measure.total,
            bar: record.measure.bar,
            cursor: record.measure.cursor,
            scroller: record.measure.scroller,
            columns: record.measure.columns.map((column) => ({
              status: column.status,
              width: column.width,
              count: column.count,
              cards: column.cards,
              collapsed: column.collapsed,
            })),
            m1: record.m1,
            sticky: record.sticky,
            refresh: record.refresh,
          })}`,
        )
        assertionsOf(record)
      }
    }
  } finally {
    cdp.close()
  }

  const failed = results.filter((item) => !item.ok)
  console.log(`\n[board] 合计 ${results.length} 条断言，失败 ${failed.length} 条`)
  if (failed.length > 0) {
    for (const item of failed) console.log(`[board] FAIL ${item.name} — ${item.detail}`)
    process.exit(1)
  }
  console.log(`[board] 通过；截图目录 ${shotDir}`)
  process.exit(0)
}

await main()
