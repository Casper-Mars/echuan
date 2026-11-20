// echuan 前端自检脚本（一个脚本，两种跑法）
//
//   1. 默认（无参数）：A4–A9 冒烟 —— 裸 CDP（`CHROME_CDP` 指向一个已有页面目标的 Chrome）。
//   2. `--viewports 1440x900,834x1112,390x844`：三视口移动端回归（T10）——
//      真 Chromium（browserless）+ puppeteer 请求拦截伪造后端，逐条跑
//      design/flows.md F4 的三条专项（M1 横向切列 / M2 附件同页预览 / M3 推进状态）与通用项
//      （无横向溢出 / 正文字号 ≥15 / 可点元素 <768 ≥44×44、≥768 按设计基线）。任一失败 → 退出码 1。
//      用法与环境变量见 web/README.md「三视口回归」。

import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { join } from 'node:path'

const BASE = process.env.ECHUAN_UI_BASE ?? 'http://127.0.0.1:18720'
const CDP = process.env.CHROME_CDP ?? 'http://127.0.0.1:19222'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const results = []
function check(name, ok, detail = '') {
  results.push({ name, ok, detail })
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? ` — ${detail}` : ''}`)
}

async function main() {
  const tabs = await (await fetch(`${CDP}/json/list`)).json()
  const page = tabs.find((t) => t.type === 'page')
  if (!page) throw new Error('no page target')

  const ws = new WebSocket(page.webSocketDebuggerUrl)
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true })
    ws.addEventListener('error', reject, { once: true })
  })

  let seq = 0
  const pending = new Map()
  const listeners = new Map()

  ws.addEventListener('message', (event) => {
    const msg = JSON.parse(event.data)
    if (msg.id && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id)
      pending.delete(msg.id)
      if (msg.error) reject(new Error(`${msg.error.message} (${JSON.stringify(msg.error)})`))
      else resolve(msg.result)
      return
    }
    if (msg.method) {
      for (const fn of listeners.get(msg.method) ?? []) fn(msg.params)
    }
  })

  function send(method, params = {}) {
    const id = ++seq
    return new Promise((resolve, reject) => {
      pending.set(id, { resolve, reject })
      ws.send(JSON.stringify({ id, method, params }))
    })
  }

  function on(method, fn) {
    if (!listeners.has(method)) listeners.set(method, [])
    listeners.get(method).push(fn)
  }

  async function evaluate(expression) {
    const result = await send('Runtime.evaluate', {
      expression,
      awaitPromise: true,
      returnByValue: true,
    })
    if (result.exceptionDetails) {
      throw new Error(result.exceptionDetails.text + ' ' + JSON.stringify(result.exceptionDetails))
    }
    return result.result.value
  }

  async function waitFor(name, fn, timeout = 10000) {
    const start = Date.now()
    let last
    while (Date.now() - start < timeout) {
      try {
        last = await fn()
        if (last) return last
      } catch (err) {
        last = String(err)
      }
      await sleep(100)
    }
    throw new Error(`timeout waiting for ${name} (last=${JSON.stringify(last)})`)
  }

  const bodyText = () => evaluate('document.body.innerText')
  const hash = () => evaluate('location.hash')

  function navigate(url) {
    return send('Page.navigate', { url })
  }

  await send('Page.enable')
  await send('Runtime.enable')

  // Fetch 拦截：用于模拟 5xx 与网络不可达，验证错误提示与恢复能力。
  let fetchMode = 'passthrough'
  on('Fetch.requestPaused', async ({ requestId, request }) => {
    try {
      const url = new URL(request.url)
      if (fetchMode === 'fail-projects' && url.pathname.startsWith('/v1/projects')) {
        const body = Buffer.from(
          JSON.stringify({
            code: 'internal',
            message: '模拟服务异常',
            details: [],
            request_id: 'test-req-1',
          }),
        ).toString('base64')
        await send('Fetch.fulfillRequest', {
          requestId,
          responseCode: 503,
          responseHeaders: [
            { name: 'Content-Type', value: 'application/json' },
            { name: 'X-Request-Id', value: 'test-req-1' },
          ],
          body,
        })
      } else if (fetchMode === 'fail-tasks' && url.pathname.startsWith('/v1/tasks')) {
        await send('Fetch.failRequest', { requestId, errorReason: 'ConnectionRefused' })
      } else {
        await send('Fetch.continueRequest', { requestId })
      }
    } catch (err) {
      console.error('Fetch handler error:', err.message)
    }
  })

  // ---- A4/A5：根路径与未知 hash 重定向 ---------------------------------
  await navigate(`${BASE}/`)
  await waitFor('products page', async () => (await bodyText()).includes('驿传示例产品'))
  check('A4 根路径进入产品列表', (await hash()) === '#/products', await hash())

  await evaluate("location.hash = '#/definitely-unknown'")
  await waitFor('unknown hash redirect', async () => (await hash()) === '#/products')
  check('A5 未知 hash 重定向到 #/products', true)

  // ---- A5：三级路由直接打开 + 刷新 -------------------------------------
  const tasksUrl = `${BASE}/#/products/APP/projects/APP-1/tasks`
  await navigate(tasksUrl)
  await waitFor('tasks page', async () => (await bodyText()).includes('APP-1-T0001'))
  const tasksHash = await hash()
  check('A5 任务页可直接打开', tasksHash === '#/products/APP/projects/APP-1/tasks', tasksHash)

  await send('Page.reload')
  await waitFor('tasks page after reload', async () => (await bodyText()).includes('APP-1-T0001'))
  check('A5 刷新后仍在任务页', (await hash()) === '#/products/APP/projects/APP-1/tasks', await hash())

  // ---- A6：面包屑层级与链接 -------------------------------------------
  const crumbText = await evaluate("document.querySelector('.breadcrumbs')?.innerText ?? ''")
  const crumbLinks = await evaluate(
    "Array.from(document.querySelectorAll('.breadcrumbs a')).map(a => [a.innerText, a.getAttribute('href')])",
  )
  check(
    'A6 任务页面包屑为 产品名 / APP-1 / 任务',
    crumbText.includes('驿传示例产品') && crumbText.includes('APP-1') && crumbText.includes('任务'),
    JSON.stringify(crumbText),
  )
  check(
    'A6 面包屑链接指向上层',
    crumbLinks[0]?.[1] === '#/products' && crumbLinks[1]?.[1] === '#/products/APP/projects',
    JSON.stringify(crumbLinks),
  )

  await evaluate("document.querySelectorAll('.breadcrumbs a')[1].click()")
  await waitFor('project crumb navigation', async () => (await hash()) === '#/products/APP/projects')
  check('A6 点击项目面包屑回到项目列表', true, await hash())

  await evaluate("document.querySelectorAll('.breadcrumbs a')[0].click()")
  await waitFor('product crumb navigation', async () => (await hash()) === '#/products')
  check('A6 点击产品面包屑回到产品列表', true, await hash())

  // ---- A5：浏览器前进/后退 --------------------------------------------
  await evaluate("location.hash = '#/products/APP/projects/APP-1/tasks'")
  await waitFor('tasks route', async () => (await hash()) === '#/products/APP/projects/APP-1/tasks')
  await evaluate('history.back()')
  await waitFor('history back', async () => (await hash()) === '#/products')
  check('A5 浏览器后退可用', true, await hash())
  await evaluate('history.forward()')
  await waitFor('history forward', async () => (await hash()) === '#/products/APP/projects/APP-1/tasks')
  check('A5 浏览器前进可用', true, await hash())

  // ---- A7：5 秒轮询 + 页面切换停止旧请求 ------------------------------
  const countReqs = (path) =>
    evaluate(
      `performance.getEntriesByType('resource').filter(e => new URL(e.name).pathname === ${JSON.stringify(
        path,
      )}).length`,
    )

  await navigate(`${BASE}/#/products`)
  await waitFor('products rows', async () => (await bodyText()).includes('驿传示例产品'))
  const products0 = await countReqs('/v1/products')
  await sleep(11200)
  const products1 = await countReqs('/v1/products')
  check('A7 产品页每 5 秒自动刷新', products1 - products0 >= 2, `${products0} -> ${products1}`)

  await evaluate("location.hash = '#/products/APP/projects'")
  await waitFor('projects page', async () => (await bodyText()).includes('前端骨架与路由'))
  const prodBaseline = await countReqs('/v1/products')
  const projBaseline = await countReqs('/v1/projects')
  await sleep(11200)
  const prodAfter = await countReqs('/v1/products')
  const projAfter = await countReqs('/v1/projects')
  check(
    'A7 项目页轮询 /v1/projects',
    projAfter - projBaseline >= 2,
    `${projBaseline} -> ${projAfter}`,
  )
  check(
    'A7 切换页面后产品页轮询停止',
    prodAfter === prodBaseline,
    `${prodBaseline} -> ${prodAfter}`,
  )

  // ---- A9：抽屉开合、ESC、遮罩 ---------------------------------------
  await navigate(`${BASE}/#/products`)
  await waitFor('products page', async () => (await bodyText()).includes('驿传示例产品'))
  await evaluate("document.querySelector('.product-card .card-title').click()")
  await waitFor('drawer open', async () => (await evaluate("!!document.querySelector('.drawer')")) === true)
  check('A9 详情抽屉可打开', true)

  await send('Input.dispatchKeyEvent', {
    type: 'keyDown',
    key: 'Escape',
    code: 'Escape',
    windowsVirtualKeyCode: 27,
    nativeVirtualKeyCode: 27,
  })
  await send('Input.dispatchKeyEvent', {
    type: 'keyUp',
    key: 'Escape',
    code: 'Escape',
    windowsVirtualKeyCode: 27,
    nativeVirtualKeyCode: 27,
  })
  await waitFor('drawer closed by ESC', async () => (await evaluate("!!document.querySelector('.drawer')")) === false)
  check('A9 ESC 关闭抽屉', true)

  await evaluate("document.querySelector('.product-card .card-title').click()")
  await waitFor('drawer reopen', async () => (await evaluate("!!document.querySelector('.drawer')")) === true)
  await evaluate("document.querySelector('.drawer-mask').click()")
  await waitFor('drawer closed by mask', async () => (await evaluate("!!document.querySelector('.drawer')")) === false)
  check('A9 遮罩点击关闭抽屉', true)

  // ---- A8：5xx 错误体（code + request_id）与恢复 ----------------------
  await send('Fetch.enable', { patterns: [{ urlPattern: '*/v1/*', requestStage: 'Request' }] })
  fetchMode = 'fail-projects'
  await evaluate("location.hash = '#/products/APP/projects'")
  const notice = await waitFor(
    '503 error notice',
    async () => {
      const text = await evaluate("document.querySelector('.notice.error')?.innerText ?? ''")
      return text.includes('test-req-1') ? text : ''
    },
    12000,
  )
  check(
    'A8 5xx 提示包含 code 与 request_id',
    notice.includes('code: internal') && notice.includes('request_id: test-req-1') && notice.includes('服务异常'),
    notice.replace(/\n+/g, ' | '),
  )

  fetchMode = 'passthrough'
  await evaluate("document.querySelector('.notice.error button').click()")
  await waitFor('projects recovered', async () => (await bodyText()).includes('前端骨架与路由'))
  const recoveredNotice = await evaluate("!!document.querySelector('.notice.error')")
  check('A8 后端恢复后重试成功', recoveredNotice === false)

  // ---- A8：网络不可达提示 --------------------------------------------
  fetchMode = 'fail-tasks'
  await evaluate("location.hash = '#/products/APP/projects/APP-1/tasks'")
  const netNotice = await waitFor(
    'network error notice',
    async () => {
      const text = await evaluate("document.querySelector('.notice.error')?.innerText ?? ''")
      return text.includes('网络错误') ? text : ''
    },
    12000,
  )
  check('A8 网络失败给可读提示', netNotice.includes('code: network_error'), netNotice.replace(/\n+/g, ' | '))

  fetchMode = 'passthrough'
  await evaluate("document.querySelector('.notice.error button').click()")
  await waitFor('tasks recovered', async () => (await bodyText()).includes('APP-1-T0001'))
  check('A8 网络恢复后轮询/重试继续工作', true)

  await send('Fetch.disable')
  ws.close()

  const failed = results.filter((r) => !r.ok)
  console.log(`\n${results.length - failed.length}/${results.length} checks passed`)
  if (failed.length) process.exitCode = 1
}

/* ==========================================================================
 * T10：三视口移动端回归（`--viewports`）
 *
 * 设计口径：design/flows.md F4 三条 + design/screens/{board,task-detail,attachment-preview}.md
 *   M1 看板横向切列：容器可横向滚动 / 滚到最右最后一列完整可见 / 列宽（<768 min(86vw,320)，≥768 桌面 280）/
 *                    列头 sticky（列内卡片栈滚动后 top 不变）
 *   M2 详情读附件：深链打开详情 → 点附件 → **同页**出现 markdown 预览、不新开标签、正文可见
 *   M3 推进状态：in_review 下评审区可见 / 驳回空意见被拦（不发 POST）/ 填意见后只发 1 次 POST
 *   通用项：三视口无横向溢出、正文字号 ≥15px、可点元素尺寸
 *          （<768 全部 ≥44×44；≥768 按 design/components.md 控件基线 md36/sm32/icon40/row44，**不判 44**）
 *          口径说明：AC 原文「所有可点元素 ≥44×44」出自「触控目标」语境，桌面 36/32 是设计明确规定，
 *          两者冲突时以「移动断点 44 + 桌面按设计基线」为准（不把设计正确的界面判成红）。
 *
 * 为什么用 puppeteer 而不是第一段的裸 CDP：browserless 的 `/json/list` 是空数组（页面目标按需创建），
 * 只有浏览器级 ws 端点；另外「不新开标签」要数 `browser.pages()`。puppeteer-core 装在
 * `<workspace>/.agent-env/browser-tools/node_modules`，**不进 web/package.json**，用动态解析。
 * 数据全部用请求拦截伪造：静态托管没有后端，`/v1/*` 一律本地应答，不打真实服务。
 * ========================================================================== */

const DEFAULT_VIEWPORTS = '1440x900,834x1112,390x844'

function readFlag(argv, name) {
  const eq = argv.find((arg) => arg.startsWith(`${name}=`))
  if (eq !== undefined) return eq.slice(name.length + 1)
  const at = argv.indexOf(name)
  if (at === -1) return undefined
  const next = argv[at + 1]
  return next === undefined || next.startsWith('--') ? '' : next
}

function parseViewports(spec) {
  const text = (spec ?? '').trim() === '' ? DEFAULT_VIEWPORTS : spec.trim()
  return text.split(',').map((item) => {
    const match = /^(\d+)x(\d+)$/.exec(item.trim())
    if (match === null) throw new Error(`视口格式应为 <宽>x<高>，收到：${item}`)
    const width = Number(match[1])
    const height = Number(match[2])
    return { width, height, label: `${width}x${height}` }
  })
}

/** 站点地址：静态托管容器（busybox httpd）在 host.docker.internal 上；容器内 127.0.0.1 指浏览器自己 */
function uiBase() {
  return (process.env.ECHUAN_UI_BASE ?? 'http://host.docker.internal:4198').replace(/\/$/, '')
}

/**
 * 浏览器端点：优先整条 `DSH_BROWSER_CDP_URL`（含 token）；否则读 token 文件拼 browserless 默认地址。
 * 默认 token 文件是本机生成的一次性凭证（不入库），可用 ECHUAN_BROWSER_TOKEN_FILE 覆盖。
 */
function browserWSEndpoint() {
  const direct = process.env.DSH_BROWSER_CDP_URL
  if (direct !== undefined && direct !== '') return direct
  const tokenFile = process.env.ECHUAN_BROWSER_TOKEN_FILE ?? '/workspace/.agent-env/echuan/browser-token'
  let token = ''
  try {
    token = readFileSync(tokenFile, 'utf8').trim()
  } catch {
    throw new Error(
      `读不到 browserless token：${tokenFile}（用 ECHUAN_BROWSER_TOKEN_FILE 或 DSH_BROWSER_CDP_URL 指定）`,
    )
  }
  return `ws://host.docker.internal:3010?token=${token}`
}

/** puppeteer-core 所在目录：项目依赖里刻意不装浏览器驱动，从 agent 侧工具目录解析 */
function loadPuppeteer() {
  const toolsDir = process.env.ECHUAN_BROWSER_TOOLS ?? '/workspace/.agent-env/browser-tools'
  const requireFromTools = createRequire(join(toolsDir, 'index.cjs'))
  try {
    return requireFromTools('puppeteer-core')
  } catch (err) {
    throw new Error(`解析 puppeteer-core 失败（ECHUAN_BROWSER_TOOLS=${toolsDir}）：${err.message}`)
  }
}

// ---- 伪造后端数据 ---------------------------------------------------------

const MOCK_PROJECT_ID = 'echuan-1'
const MOCK_REVIEW_TASK_ID = 'T10-T0102'
const MOCK_MATERIAL_ID = 'M-T10-OUT-1'
const MOCK_TIME = '2026-02-20T10:00:00Z'
const MOCK_MARKDOWN =
  '# T10 三视口回归\n\n附件正文在应用内渲染，不新开标签。\n\n- 视口：1440x900 / 834x1112 / 390x844\n- 断言：M1 / M2 / M3 + 通用项\n'

const MOCK_PRODUCT = {
  product_id: 'PROD-T10',
  name: '驿传示例产品',
  git_url: 'https://github.com/Casper-Mars/echuan.git',
  description: 'T10 回归用伪造数据',
  license: 'MIT',
}

const MOCK_PROJECT = {
  project_id: MOCK_PROJECT_ID,
  product_id: MOCK_PRODUCT.product_id,
  seq_no: 1,
  name: '移动端回归项目',
  context_md: '## 项目上下文\n\n三视口回归。',
  status: 'open',
  create_time: MOCK_TIME,
  update_time: MOCK_TIME,
}

function mockTask(taskId, status, title) {
  return {
    task_id: taskId,
    project_id: MOCK_PROJECT_ID,
    parent_task_id: null,
    kind: 'dev',
    title,
    description_md: `# ${title}\n\n看板与详情共用的正文。`,
    context_md: '## 上下文\n\nT10 三视口回归。',
    priority: 50,
    status,
    attempt: 0,
    max_attempts: 3,
    lease_id: null,
    lease_until: null,
    worker_id: null,
    reason: null,
    create_time: MOCK_TIME,
    update_time: MOCK_TIME,
  }
}

/** 7 列都有内容；in_review 给足 7 张卡，让列内卡片栈真的可滚动（M1 列头 sticky 才有意义） */
const MOCK_TASKS = [
  mockTask('T10-T0101', 'pending', '待领取：回归脚本参数化视口'),
  mockTask(MOCK_REVIEW_TASK_ID, 'in_review', '待审核：附件 markdown 同页预览'),
  mockTask('T10-T0103', 'in_review', '待审核：横向切列与列指示器'),
  mockTask('T10-T0104', 'in_review', '待审核：驳回必填意见'),
  mockTask('T10-T0105', 'in_review', '待审核：填意见后只发一次请求'),
  mockTask('T10-T0106', 'in_review', '待审核：列头 sticky 复验'),
  mockTask('T10-T0107', 'in_review', '待审核：移动端操作条不遮内容'),
  mockTask('T10-T0108', 'in_review', '待审核：三视口无横向溢出'),
  mockTask('T10-T0113', 'in_review', '待审核：列头计数与折叠'),
  mockTask('T10-T0114', 'in_review', '待审核：滚到最右完整可见'),
  mockTask('T10-T0115', 'in_review', '待审核：附件列表与正文切换'),
  mockTask('T10-T0109', 'ready', '待开工：触控目标自检'),
  mockTask('T10-T0110', 'in_progress', '进行中：正文字号抽查'),
  mockTask('T10-T0111', 'done', '已完成：构造用例数据'),
  mockTask('T10-T0112', 'failed', '失败：网络异常兜底'),
]

const MOCK_ATTACHMENTS = [
  { material_id: 'M-T10-IN-1', title: 'T10 任务卡.md', kind: 'markdown', role: 'input' },
  { material_id: MOCK_MATERIAL_ID, title: 'T10 交付说明.md', kind: 'markdown', role: 'output' },
]

/** 单请求应答；只覆盖 `/v1/*`，静态资源一律放行（见 runViewportSuite 的拦截器） */
function mockReply(method, url) {
  const json = (value, status = 200) => ({
    status,
    contentType: 'application/json',
    body: JSON.stringify(value),
  })
  const path = url.pathname

  if (path === '/v1/products') return json({ items: [MOCK_PRODUCT] })
  if (path === '/v1/projects') {
    return url.searchParams.get('id') === null ? json({ items: [MOCK_PROJECT] }) : json(MOCK_PROJECT)
  }
  if (path === '/v1/tasks/decide' && method === 'POST') {
    // 伪造成功应答：不改任务状态，详情会在 load() 后自行收口
    return json({ ...MOCK_TASKS[1], reason: '回归脚本伪造的驳回意见' })
  }
  if (path === '/v1/tasks/dependencies') return json({ items: [] })
  if (path === '/v1/tasks/attachments') return json({ items: MOCK_ATTACHMENTS })
  if (path === '/v1/tasks') {
    const id = url.searchParams.get('id')
    if (id === null) return json({ items: MOCK_TASKS })
    return json(MOCK_TASKS.find((task) => task.task_id === id) ?? MOCK_TASKS[1])
  }
  if (path === '/v1/materials') {
    return { status: 200, contentType: 'text/markdown; charset=utf-8', body: MOCK_MARKDOWN }
  }
  if (path === '/v1/inbox' || path === '/v1/workers') return json({ items: [] })
  return json({ items: [] })
}

// ---- 通用项（三视口都要过） -----------------------------------------------

async function checkNoHorizontalOverflow(page, label, suffix = '') {
  const geo = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
    bodyScrollWidth: document.body.scrollWidth,
  }))
  check(
    `[${label}] 通用${suffix} 无横向溢出`,
    geo.scrollWidth === geo.clientWidth,
    `document.scrollWidth=${geo.scrollWidth} clientWidth=${geo.clientWidth} body.scrollWidth=${geo.bodyScrollWidth}`,
  )
}

async function checkFontSizes(page, label, selectors, suffix = '') {
  const samples = await page.evaluate((list) => {
    const out = []
    for (const sel of list) {
      const nodes = Array.from(document.querySelectorAll(sel)).slice(0, 20)
      let min = Number.POSITIVE_INFINITY
      for (const node of nodes) {
        const px = Number.parseFloat(getComputedStyle(node).fontSize)
        if (Number.isFinite(px)) min = Math.min(min, px)
      }
      if (Number.isFinite(min)) out.push({ sel, px: min, count: nodes.length })
    }
    return out
  }, selectors)
  const min = samples.reduce((acc, item) => Math.min(acc, item.px), Number.POSITIVE_INFINITY)
  check(
    `[${label}] 通用${suffix} 正文字号 ≥15px`,
    samples.length > 0 && min >= 15,
    samples.map((item) => `${item.sel}=${item.px}px×${item.count}`).join(' · ') || '未抽到正文容器',
  )
}

/**
 * 可点元素逐个量 boundingBox：不达标的**全部枚举**（正常应为空列表 + 计数 0）。
 *
 * 判据按断点分开（口径纠正见 web/README.md「可点元素的判据为什么分断点」）：
 *   * `<768`（移动断点）：**所有**可点元素 ≥44×44（design/flows.md F4-3「触控 ≥44×44」）；
 *   * `≥768`（桌面/平板）：按 design/components.md 的控件基线判尺寸 —— `ui/Button` md 36 / sm 32、
 *     `ui/IconButton` 40、`ui/AttachmentRow` 44、`ui/TabBar` 44；**不判 44**（桌面是鼠标口径，
 *     36/32 是设计明确规定的，硬套 44 会把设计正确的界面判成红）。设计未规定尺寸的元素
 *     （面包屑链接、Mantine 关闭键等）只统计、不计入门禁，逐项列在 detail 里。
 *
 * 元素集：可见（display/visibility/零尺寸）、未禁用（disabled / aria-disabled）的
 * `a[href] / button / summary / [role=button|tab|radio|option] / input / select / textarea`，
 * 外加绑着单选/复选的 `label`（SegmentedControl 这类控件的真实命中区是 label，不是里面的 input）。
 */
async function checkTouchTargets(page, label, viewport, suffix = '') {
  const mobile = viewport.width < 768
  const rows = await page.evaluate((isMobile) => {
    const selector = [
      'a[href]',
      'button',
      'summary',
      '[role="button"]',
      '[role="tab"]',
      '[role="radio"]',
      '[role="option"]',
      'input:not([type="hidden"])',
      'select',
      'textarea',
      'label',
    ].join(', ')

    /** 该元素在本断点下至少该有多大；null = 设计没规定尺寸，不计入门禁 */
    const designMin = (el) => {
      if (isMobile) return 44
      if (el.matches('.ui-button[data-ui-size="md"]')) return 36
      if (el.matches('.ui-button[data-ui-size="sm"]')) return 32
      if (el.matches('.ui-icon-button')) return 40
      if (el.matches('.ui-attachment-row')) return 44
      if (el.matches('.ui-tab-bar__tab')) return 44
      return null
    }

    const out = []
    for (const el of document.querySelectorAll(selector)) {
      // `<label>` 只算「绑着单选/复选」的那些（SegmentedControl 的命中区是 label，input 被视觉隐藏）；
      // 表单字段的说明 label 不是用户去点的地方，不算可点元素。
      if (el.tagName === 'LABEL') {
        const owner = el.parentElement
        const bound =
          owner !== null &&
          owner.querySelector('input[type="radio"], input[type="checkbox"]') !== null
        if (!bound) continue
      }
      if (el.disabled === true || el.getAttribute('aria-disabled') === 'true') continue
      const style = getComputedStyle(el)
      if (style.display === 'none' || style.visibility === 'hidden') continue
      const rect = el.getBoundingClientRect()
      if (rect.width === 0 && rect.height === 0) continue
      out.push({
        tag: el.tagName.toLowerCase(),
        // 归属用：优先报自有类名（ui-/fx-/bp-），Mantine 的 m_* 只是底层实现噪声
        cls:
          (el.getAttribute('class') ?? '')
            .split(' ')
            .filter((item) => /^(ui-|fx-|bp-)/.test(item))[0] ?? '',
        label: (el.getAttribute('aria-label') ?? el.textContent ?? el.getAttribute('id') ?? '')
          .replace(/\s+/g, ' ')
          .trim()
          .slice(0, 16),
        w: Math.round(rect.width * 10) / 10,
        h: Math.round(rect.height * 10) / 10,
        min: designMin(el),
      })
    }
    return out
  }, mobile)
  const describe = (row) =>
    `${row.tag}${row.cls === '' ? '' : `.${row.cls}`}[${row.label}]${row.w}×${row.h}`
  const bad = rows.filter((row) => row.min !== null && (row.w < row.min || row.h < row.min))
  // 桌面/平板上「设计没规定尺寸」且 <44 的元素：只统计不判（不把设计基线判成红）
  const unregulated = rows.filter((row) => row.min === null && (row.w < 44 || row.h < 44))
  const head = mobile
    ? '可点元素 ≥44×44（移动断点）'
    : '可点元素 ≥设计基线（md36/sm32/icon40/row·tab44）'
  const detail =
    bad.length === 0
      ? `不达标 0 个 / 可点元素 ${rows.length} 个（不达标列表为空）` +
        (unregulated.length === 0
          ? ''
          : `；另有 ${unregulated.length} 个 <44 但设计未规定尺寸，不计入门禁：${unregulated
              .slice(0, 12)
              .map(describe)
              .join(', ')}`)
      : `不达标 ${bad.length}/${rows.length} 个：` + bad.slice(0, 40).map(describe).join(', ')
  check(`[${label}] 通用${suffix} ${head}`, bad.length === 0, detail)
}

// ---- M1 看板横向切列 ------------------------------------------------------

async function runBoardChecks(page, viewport) {
  const label = viewport.label
  await page.goto(`${uiBase()}/#/projects/${MOCK_PROJECT_ID}/tasks`, { waitUntil: 'load' })
  await page.waitForSelector('[data-ui="board-scroller"] .bp-col', { timeout: 20000 })
  await page.waitForSelector('.ui-task-card', { timeout: 20000 })

  await checkNoHorizontalOverflow(page, label)
  await checkFontSizes(page, label, ['.ui-task-card__title'])
  await checkTouchTargets(page, label, viewport)

  const readGeometry = () =>
    page.evaluate(() => {
      const scroller = document.querySelector('[data-ui="board-scroller"]')
      const columns = Array.from(scroller.querySelectorAll('.bp-col'))
      return {
        scrollWidth: scroller.scrollWidth,
        clientWidth: scroller.clientWidth,
        scrollLeft: scroller.scrollLeft,
        innerWidth: window.innerWidth,
        columns: columns.map((column) => {
          const rect = column.getBoundingClientRect()
          return {
            status: column.getAttribute('data-status'),
            collapsed: column.classList.contains('ui-board-column--collapsed'),
            right: Math.round(rect.right),
            width: Math.round(rect.width * 10) / 10,
          }
        }),
      }
    })

  const before = await readGeometry()
  check(
    `[${label}] M1 看板容器可横向切列`,
    before.scrollWidth > before.clientWidth,
    `scrollWidth=${before.scrollWidth} > clientWidth=${before.clientWidth}（${before.columns.length} 列）`,
  )

  // 列宽口径：<768 移动 min(86vw,320)；≥768 桌面/平板 280（design/screens/board.md「断点」）
  const expectedWidth = viewport.width < 768 ? Math.min((viewport.width * 86) / 100, 320) : 280
  const expanded = before.columns.filter((column) => !column.collapsed)
  const narrow = expanded.filter((column) => column.width < expectedWidth - 1)
  check(
    `[${label}] M1 列宽 ≥ ${Math.round(expectedWidth)}px（${
      viewport.width < 768 ? 'min(86vw,320)' : '桌面/平板 280'
    }）`,
    expanded.length >= 4 && narrow.length === 0,
    `展开列 ${expanded.length} 个：${expanded
      .map((column) => `${column.status}=${column.width}`)
      .join(' ')}${narrow.length === 0 ? '' : `；不达标：${narrow.map((column) => column.status).join(',')}`}`,
  )

  // 列头 sticky：在 in_review 列（卡片最多）里把卡片栈滚到底，列头 top 不应移动
  const sticky = await page.evaluate(() => {
    const column = document.querySelector('.bp-col[data-status="in_review"]')
    if (column === null) return null
    const head = column.querySelector('.ui-board-column__head')
    if (head === null) return null
    const beforeTop = head.getBoundingClientRect().top
    const stack = Array.from(column.querySelectorAll('*')).find((el) => {
      const style = getComputedStyle(el)
      return (
        (style.overflowY === 'auto' || style.overflowY === 'scroll') &&
        el.scrollHeight > el.clientHeight + 1
      )
    })
    let scrolled = 0
    if (stack !== undefined) {
      scrolled = stack.scrollHeight - stack.clientHeight
      stack.scrollTop = scrolled
    }
    return {
      before: Math.round(beforeTop * 10) / 10,
      after: Math.round(head.getBoundingClientRect().top * 10) / 10,
      scrolled,
    }
  })
  check(
    `[${label}] M1 列头 sticky（列内滚动后 top 不变）`,
    sticky !== null && Math.abs(sticky.after - sticky.before) <= 1,
    sticky === null
      ? 'in_review 列或其列头缺失'
      : `列内滚动 ${sticky.scrolled}px：top ${sticky.before} → ${sticky.after}`,
  )

  // 滚到最右：最后一列必须完整落在视口内
  await page.evaluate(() => {
    const scroller = document.querySelector('[data-ui="board-scroller"]')
    scroller.scrollLeft = scroller.scrollWidth
  })
  await sleep(250)
  const after = await readGeometry()
  const last = after.columns[after.columns.length - 1]
  check(
    `[${label}] M1 滚到最右最后一列完整可见`,
    after.scrollLeft + after.clientWidth >= after.scrollWidth - 1 && last.right <= after.innerWidth + 1,
    `scrollLeft=${Math.round(after.scrollLeft)}+clientWidth=${Math.round(after.clientWidth)} vs scrollWidth=${
      after.scrollWidth
    }；最后一列 ${last.status} right=${last.right} ≤ 视口 ${after.innerWidth}+1`,
  )
}

// ---- M2 详情读附件 markdown / M3 推进任务状态 ------------------------------

async function waitUntil(fn, timeout, what) {
  const start = Date.now()
  while (Date.now() - start < timeout) {
    if (await fn()) return true
    await sleep(100)
  }
  throw new Error(`超时等待 ${what}`)
}

async function runDetailChecks(page, browser, viewport, counters) {
  const label = viewport.label
  await page.goto(`${uiBase()}/#/projects/${MOCK_PROJECT_ID}/tasks/${MOCK_REVIEW_TASK_ID}`, {
    waitUntil: 'load',
  })
  await page.waitForSelector('[data-ui="detail-attachments"] .ui-attachment-row', { timeout: 20000 })

  await checkNoHorizontalOverflow(page, label, '（详情）')
  await checkFontSizes(page, label, ['.ui-markdown p', '.fx-detail__meta-value'], '（详情）')
  await checkTouchTargets(page, label, viewport, '（详情）')

  // M2：点附件 → 同页二级面板里出现 markdown 正文，且不新开标签
  const pagesBefore = (await browser.pages()).length
  await page.click('[data-ui="detail-attachments"] .ui-attachment-row')
  await page.waitForSelector('[data-testid="attachment-preview"] [data-testid="markdown-viewer"]', {
    timeout: 20000,
  })
  const preview = await page.evaluate(() => {
    const panel = document.querySelector('[data-testid="attachment-preview"]')
    const viewer = panel?.querySelector('[data-testid="markdown-viewer"]')
    return {
      panel: panel !== null,
      viewer: viewer !== null,
      clientHeight: viewer?.clientHeight ?? 0,
      text: (viewer?.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 40),
    }
  })
  const pagesAfter = (await browser.pages()).length
  check(
    `[${label}] M2 点附件同页出现 markdown 预览`,
    preview.panel && preview.viewer,
    `[data-testid=attachment-preview] 内 [data-testid=markdown-viewer] 存在=${preview.viewer}`,
  )
  check(
    `[${label}] M2 预览不新开标签`,
    pagesAfter === pagesBefore,
    `browser.pages() ${pagesBefore} → ${pagesAfter}`,
  )
  check(
    `[${label}] M2 附件正文可见`,
    preview.clientHeight > 0 && preview.text.includes('三视口回归'),
    `clientHeight=${preview.clientHeight} text="${preview.text}"`,
  )

  // 返回详情：预览态下头部关闭键即「返回详情」
  await page.click('[aria-label="关闭详情"]')
  await page.waitForFunction(() => document.querySelector('[data-testid="attachment-preview"]') === null, {
    timeout: 10000,
  })

  // M3：评审区在 in_review 任务下可见
  const review = await page.evaluate(() => {
    const section = document.querySelector('[data-ui="review"]')
    if (section === null) return null
    const rect = section.getBoundingClientRect()
    return {
      width: Math.round(rect.width),
      height: Math.round(rect.height),
      rejectButton: document.querySelector('[data-ui="review-reject"] button') !== null,
      submitButton: document.querySelector('[data-ui="review-submit"] button') !== null,
    }
  })
  check(
    `[${label}] M3 评审区在 in_review 下可见`,
    review !== null && review.width > 0 && review.rejectButton && review.submitButton,
    review === null ? '未找到 [data-ui=review]' : `${review.width}×${review.height}，驳回/通过按钮都在`,
  )

  // M3：驳回不带意见 → 就地标红、不发请求
  await page.click('[data-ui="review-reject"] button')
  const rejectedHint = await waitUntil(
    () =>
      page.evaluate(() =>
        (document.querySelector('[data-ui="review"]')?.innerText ?? '').includes('驳回必须填写意见'),
      ),
    5000,
    '驳回意见必填提示',
  ).catch(() => false)
  check(
    `[${label}] M3 驳回空意见被拦下（不发请求）`,
    rejectedHint === true && counters.decide === 0,
    `必填提示出现=${rejectedHint}；POST /v1/tasks/decide 计数=${counters.decide}`,
  )

  // M3：填意见 → 二次确认 → 只发 1 次 POST
  await page.type('[data-ui="review"] textarea', '三视口日志不全，请补 390x844 的原始输出')
  await page.click('[data-ui="review-reject"] button')
  await waitUntil(
    () =>
      page.evaluate(() =>
        Array.from(document.querySelectorAll('button')).some(
          (button) => button.textContent.trim() === '确认驳回',
        ),
      ),
    5000,
    '二次确认弹窗',
  )
  await page.evaluate(() => {
    const button = Array.from(document.querySelectorAll('button')).find(
      (item) => item.textContent.trim() === '确认驳回',
    )
    button.click()
  })
  const sent = await waitUntil(() => counters.decide === 1, 8000, 'POST /v1/tasks/decide').catch(
    () => counters.decide >= 1,
  )
  check(
    `[${label}] M3 填意见后提交发出 1 次 POST /v1/tasks/decide`,
    sent === true && counters.decide === 1,
    `POST /v1/tasks/decide 计数=${counters.decide}`,
  )
}

async function runViewportSuite(spec) {
  const viewports = parseViewports(spec)
  const puppeteer = loadPuppeteer()
  const browser = await puppeteer.connect({
    browserWSEndpoint: browserWSEndpoint(),
    defaultViewport: null,
  })
  try {
    for (const viewport of viewports) {
      const page = await browser.newPage()
      const counters = { decide: 0 }
      await page.setViewport({
        width: viewport.width,
        height: viewport.height,
        deviceScaleFactor: 1,
        isMobile: viewport.width < 768,
        hasTouch: viewport.width < 768,
      })
      await page.setRequestInterception(true)
      page.on('request', (request) => {
        const url = new URL(request.url())
        if (!url.pathname.startsWith('/v1/')) {
          request.continue().catch(() => {})
          return
        }
        if (url.pathname === '/v1/tasks/decide') counters.decide += 1
        request.respond(mockReply(request.method(), url)).catch(() => {})
      })
      console.log(`\n--- 视口 ${viewport.label}（${viewport.width}×${viewport.height}）---`)
      try {
        await runBoardChecks(page, viewport)
        await runDetailChecks(page, browser, viewport, counters)
      } finally {
        await page.close()
      }
    }
  } finally {
    await browser.disconnect()
  }

  const failed = results.filter((result) => !result.ok)
  console.log(
    `\n${results.length - failed.length}/${results.length} checks passed（视口：${viewports
      .map((viewport) => viewport.label)
      .join(', ')}）`,
  )
  if (failed.length > 0) process.exitCode = 1
}

const argv = process.argv.slice(2)
const viewportSpec = readFlag(argv, '--viewports')

if (viewportSpec !== undefined) {
  runViewportSuite(viewportSpec).catch((err) => {
    console.error('VERIFY FAILED:', err)
    process.exitCode = 1
  })
} else {
  main().catch((err) => {
    console.error('VERIFY FAILED:', err)
    process.exitCode = 1
  })
}
