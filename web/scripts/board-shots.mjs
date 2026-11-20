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

const STATES = ['default', 'empty', 'loading', 'error', 'collapsed', 'long-list']

/** 7 列固定枚举（顺序 = 看板列顺序，design/screens/board.md「结构」） */
const COLUMNS = ['pending', 'ready', 'in_progress', 'in_review', 'done', 'failed', 'cancelled']

/** 折叠偏好的存储键（features/board/collapsed.ts 的 COLLAPSED_STORAGE_KEY） */
const COLLAPSED_KEY = 'echuan:board:collapsed'

const AUTO_REFRESH_MS = 15_000

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
    return new Promise((resolve, reject) => this.pending.set(id, { resolve, reject }))
  }

  on(method, handler, sessionId) {
    const key = `${sessionId ?? ''}:${method}`
    if (!this.listeners.has(key)) this.listeners.set(key, [])
    this.listeners.get(key).push(handler)
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

const results = []
function check(name, ok, detail = '') {
  results.push({ name, ok, detail })
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? ` — ${detail}` : ''}`)
}

/** ---------------- 一次「视口 × 状态」的完整观测 ---------------- */

async function createPage(cdp) {
  const { targetId } = await cdp.send('Target.createTarget', { url: 'about:blank' })
  const { sessionId } = await cdp.send('Target.attachToTarget', { targetId, flatten: true })
  return { targetId, sessionId }
}

async function closePage(cdp, targetId) {
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

async function runCase(cdp, viewport, state, options) {
  const { sessionId, targetId } = await createPage(cdp)
  const shape = state === 'long-list' ? LONG_SHAPE : DEFAULT_SHAPE
  const tasks = state === 'empty' ? [] : makeTasks(shape)
  let taskCalls = 0

  await cdp.send('Page.enable', {}, sessionId)
  await cdp.send('Runtime.enable', {}, sessionId)
  await cdp.send(
    'Emulation.setDeviceMetricsOverride',
    { width: viewport.width, height: viewport.height, deviceScaleFactor: 1, mobile: viewport.name === 'mobile' },
    sessionId,
  )
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
      const fulfill = (responseCode, body) =>
        cdp
          .send(
            'Fetch.fulfillRequest',
            { requestId, responseCode, responseHeaders: headers, body: Buffer.from(body).toString('base64') },
            sessionId,
          )
          .catch(() => undefined)

      if (url.includes('/v1/projects')) {
        await fulfill(200, JSON.stringify(PROJECT))
        return
      }
      if (url.includes('/v1/tasks')) {
        taskCalls += 1
        // loading：第一笔请求永不响应 → 页面停在 SkeletonCard（列头与筛选保持稳定）
        if (state === 'loading') return
        // error：先成功一次，再让自动刷新那一笔 500 → 保留上次数据 + 错误条
        if (state === 'error' && taskCalls > 1) {
          await fulfill(500, JSON.stringify({ message: 'internal error' }))
          return
        }
        await fulfill(200, JSON.stringify({ items: tasks }))
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
  writeFileSync(shotPath, Buffer.from(shot.data, 'base64'))

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

  await closePage(cdp, targetId)
  return { viewport, state, measure, m1, sticky, refresh, collapse, navigate, shotPath }
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
  try {
    for (const viewport of VIEWPORTS) {
      for (const state of STATES) {
        const key = `${viewport.name}:${state}`
        if (only.length > 0 && !only.includes(key) && !only.includes(viewport.name) && !only.includes(state)) continue
        console.log(`\n[board] ${key} —— ${viewport.width}×${viewport.height}`)
        const record = await runCase(cdp, viewport, state, { url, shotDir })
        records.push(record)
        console.log(
          `[board] 截图 ${record.shotPath}`,
        )
        console.log(`[board] 度量 ${JSON.stringify({
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
        })}`)
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
