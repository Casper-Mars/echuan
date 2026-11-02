// echuan 前端骨架的端到端自检（CDP + headless Chrome）
// 覆盖 A4/A5/A6/A7/A8/A9 的可执行部分。

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
  await evaluate("document.querySelector('.product-card .ghost').click()")
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

  await evaluate("document.querySelector('.product-card .ghost').click()")
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

main().catch((err) => {
  console.error('VERIFY FAILED:', err)
  process.exitCode = 1
})
