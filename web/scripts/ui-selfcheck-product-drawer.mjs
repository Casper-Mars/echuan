// 产品详情抽屉的可执行自检（CDP + headless Chrome）
//
// 只覆盖 #61 产品抽屉的验收点，与 ui-verify.mjs 并存、互不替代：
// ui-verify.mjs 一字不动（其 A4–A8 漂移属 #60 整改项），本脚本专门断言 P1–P7。
//
// 用法：
//   ECHUAN_UI_BASE=http://127.0.0.1:18720 \
//   CHROME_CDP=http://127.0.0.1:19222 \
//   node web/scripts/ui-selfcheck-product-drawer.mjs
//
// 退出码：全部 PASS 为 0，任一 FAIL 为 1，起不来/环境缺失为 2。
//
// 断言目的：
//   P1 点卡片打开抽屉（标题含产品编号 + 六项正文与卡片一致）
//   P2 ESC 关闭
//   P3 点遮罩关闭
//   P4 点右上 × 关闭
//   P5 点「查看项目」进入项目页且不弹抽屉
//   P6 详情 404 时抽屉内有可见错误提示（列表不白屏，可重开其它卡片）
//   P7 空值占位与仓库地址两种形态（git@ 纯文本 / https 新标签链接）
//
// P6 / P7 用 CDP Fetch 拦截改写「详情请求」的响应，不依赖、也不修改服务端数据，
// 因此在同一实例上可重复执行。

const BASE = process.env.ECHUAN_UI_BASE ?? 'http://127.0.0.1:18720'
const CDP = process.env.CHROME_CDP ?? 'http://127.0.0.1:19222'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const results = []

async function main() {
  let page
  try {
    const tabs = await (await fetch(`${CDP}/json/list`)).json()
    page = tabs.find((t) => t.type === 'page')
  } catch (err) {
    throw new EnvError(`无法连接 CHROME_CDP=${CDP}（${err.message}）`)
  }
  if (!page) throw new EnvError('CDP 里没有 page target')

  const ws = new WebSocket(page.webSocketDebuggerUrl)
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true })
    ws.addEventListener('error', () => reject(new EnvError('CDP WebSocket 连接失败')), { once: true })
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
    if (msg.method) for (const fn of listeners.get(msg.method) ?? []) fn(msg.params)
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
  const drawerOpen = () => evaluate("!!document.querySelector('.drawer')")
  const navigate = (url) => send('Page.navigate', { url })
  const clickSelector = (selector) =>
    evaluate(`(() => {
      const node = document.querySelector(${JSON.stringify(selector)})
      if (!node) return false
      node.click()
      return true
    })()`)
  const pressEscape = async () => {
    for (const type of ['keyDown', 'keyUp']) {
      await send('Input.dispatchKeyEvent', {
        type,
        key: 'Escape',
        code: 'Escape',
        windowsVirtualKeyCode: 27,
        nativeVirtualKeyCode: 27,
      })
    }
  }

  await send('Page.enable')
  await send('Runtime.enable')

  // ---- 详情请求改写：detailMode 决定 /v1/products?id= 的响应 --------------
  // 只拦详情请求（带 id 的 /v1/products），列表与其它接口一律放行。
  let detailMode = 'passthrough'
  const SYNTHETIC_SSH = {
    product_id: '__SELFCHECK_EMPTY__',
    name: '',
    git_url: 'git@github.com:org/repo.git',
    description: null,
    license: null,
    project_count: 0,
  }
  const SYNTHETIC_HTTPS = {
    product_id: '__SELFCHECK_HTTPS__',
    name: '自检用产品',
    git_url: 'https://github.com/Casper-Mars/echuan',
    description: '自检用描述',
    license: 'MIT',
    project_count: 2,
  }

  const fulfill = (requestId, code, payload) =>
    send('Fetch.fulfillRequest', {
      requestId,
      responseCode: code,
      responseHeaders: [{ name: 'Content-Type', value: 'application/json' }],
      body: Buffer.from(JSON.stringify(payload)).toString('base64'),
    })

  on('Fetch.requestPaused', async ({ requestId, request }) => {
    try {
      const url = new URL(request.url)
      const detail = url.pathname === '/v1/products' && url.searchParams.get('id') !== null
      if (detail && detailMode === 'not-found') {
        return fulfill(requestId, 404, {
          code: 'not_found',
          message: 'product __SELFCHECK_404__ not found',
        })
      }
      if (detail && detailMode === 'empty-and-ssh') return fulfill(requestId, 200, SYNTHETIC_SSH)
      if (detail && detailMode === 'https') return fulfill(requestId, 200, SYNTHETIC_HTTPS)
      return send('Fetch.continueRequest', { requestId })
    } catch (err) {
      console.error('Fetch handler error:', err.message)
      try {
        await send('Fetch.continueRequest', { requestId })
      } catch {
        /* 连接已断时忽略 */
      }
    }
  })
  await send('Fetch.enable', { patterns: [{ urlPattern: '*/v1/products*', requestStage: 'Request' }] })

  const openProductsPage = async () => {
    await navigate(`${BASE}/#/products`)
    await waitFor('产品列表', async () => (await evaluate("document.querySelectorAll('.product-card').length")) > 0)
  }

  const closeDrawer = async () => {
    if (await drawerOpen()) {
      await clickSelector('.drawer-mask')
      await waitFor('抽屉关闭', async () => !(await drawerOpen()))
    }
  }

  // 卡片快照：编号 / 名称 / 仓库地址 / 项目数（P1 用来比对抽屉内容）
  const readCard = (index = 0) =>
    evaluate(`(() => {
      const card = document.querySelectorAll('.product-card')[${index}]
      if (!card) return null
      const title = card.querySelector('h2')?.innerText ?? ''
      const spans = Array.from(card.querySelectorAll('.product-body header span'))
      const gitUrl = card.querySelector('.product-body p')?.innerText ?? ''
      const stats = card.querySelector('.product-stats')?.innerText ?? ''
      const count = (stats.match(/(\\d+)/) ?? [])[1] ?? ''
      return { title, productId: spans.map(s => s.innerText).join(' ').trim(), gitUrl, count }
    })()`)

  const runPoint = async (label, fn) => {
    const failures = []
    const note = (message) => failures.push(message)
    try {
      await fn(note)
    } catch (err) {
      failures.push(String(err.message ?? err))
    }
    const ok = failures.length === 0
    results.push({ label, ok })
    console.log(`${ok ? 'PASS' : 'FAIL'} ${label}`)
    for (const failure of failures) console.log(`    - ${failure}`)
    return ok
  }

  // ---- P1：点卡片（非链接/按钮区）打开抽屉 --------------------------------
  await runPoint('P1 点卡片打开产品详情抽屉', async (note) => {
    await openProductsPage()
    const card = await readCard(0)
    if (!card) return note('没读到产品卡片')
    if (!(await clickSelector('.product-card .card-title'))) return note('点不到 .product-card .card-title')
    await waitFor('抽屉出现', drawerOpen)
    // 抽屉先渲染 loading，详情请求回来后才有正文：先等内容态，再逐项断言。
    await waitFor('详情内容', async () => !!(await evaluate("!!document.querySelector('.drawer .detail-stack')")))

    const dialog = await evaluate(`(() => {
      const drawer = document.querySelector('.drawer')
      if (!drawer) return null
      return {
        role: drawer.getAttribute('role'),
        ariaModal: drawer.getAttribute('aria-modal'),
        ariaLabel: drawer.getAttribute('aria-label') ?? '',
        eyebrow: document.querySelector('.drawer-eyebrow')?.innerText ?? '',
        text: document.querySelector('.drawer-body')?.innerText ?? '',
        closeLabel: drawer.querySelector('.drawer-header button')?.getAttribute('aria-label') ?? '',
        links: Array.from(drawer.querySelectorAll('a')).map(a => ({
          href: a.getAttribute('href') ?? '',
          target: a.getAttribute('target') ?? '',
          rel: a.getAttribute('rel') ?? '',
        })),
      }
    })()`)
    if (!dialog) return note('抽屉未出现')

    if (dialog.role !== 'dialog') note(`抽屉 role=${dialog.role}（期望 dialog）`)
    if (dialog.ariaModal !== 'true') note(`抽屉 aria-modal=${dialog.ariaModal}（期望 true）`)
    if (!dialog.ariaLabel.includes(card.productId)) {
      note(`抽屉 aria-label=${JSON.stringify(dialog.ariaLabel)} 不含产品编号 ${card.productId}`)
    }
    if (dialog.eyebrow !== 'PRODUCT') note(`抽屉顶字=${JSON.stringify(dialog.eyebrow)}（期望 PRODUCT）`)
    if (dialog.closeLabel !== '关闭') note(`关闭按钮 aria-label=${JSON.stringify(dialog.closeLabel)}（期望 关闭）`)

    // 六项正文：名称 / 编号 / 仓库地址 / 描述 / 证书 / 项目数
    if (card.title && !dialog.text.includes(card.title)) note(`正文缺名称 ${card.title}`)
    if (!dialog.text.includes(card.productId)) note(`正文缺编号 ${card.productId}`)
    if (card.gitUrl && card.gitUrl !== '—' && !dialog.text.includes(card.gitUrl)) {
      note(`正文缺仓库地址 ${card.gitUrl}`)
    }
    if (!dialog.text.includes('仓库地址')) note('正文缺「仓库地址」小节')
    if (!dialog.text.includes('描述')) note('正文缺「描述」小节')
    if (!dialog.text.includes('证书')) note('正文缺「证书」项')
    if (!dialog.text.includes('项目数')) note('正文缺「项目数」项')
    if (card.count && !dialog.text.includes(`${card.count} 个项目`)) {
      note(`抽屉项目数 ${JSON.stringify(dialog.text.match(/\d+ 个项目/)?.[0])} 与卡片 ${card.count} 不一致`)
    }
    if (/\bundefined\b|\bnull\b/.test(dialog.text)) note('正文出现 undefined/null')
  })

  // ---- P2：ESC 关闭 ------------------------------------------------------
  await runPoint('P2 ESC 关闭抽屉', async (note) => {
    if (!(await drawerOpen())) return note('前置失败：抽屉未打开')
    await pressEscape()
    await waitFor('ESC 关闭', async () => !(await drawerOpen())).catch((err) => note(err.message))
    if (await drawerOpen()) note('ESC 后抽屉仍在')
  })

  // ---- P3：点遮罩关闭 ----------------------------------------------------
  await runPoint('P3 点遮罩关闭抽屉', async (note) => {
    if (!(await clickSelector('.product-card .card-title'))) return note('无法重新打开抽屉')
    await waitFor('抽屉重新打开', drawerOpen)
    if (!(await clickSelector('.drawer-mask'))) return note('点不到 .drawer-mask')
    await waitFor('遮罩关闭', async () => !(await drawerOpen())).catch((err) => note(err.message))
    if (await drawerOpen()) note('点遮罩后抽屉仍在')
  })

  // ---- P4：点右上 × 关闭 -------------------------------------------------
  await runPoint('P4 点右上 × 关闭抽屉', async (note) => {
    if (!(await clickSelector('.product-card .card-title'))) return note('无法重新打开抽屉')
    await waitFor('抽屉重新打开', drawerOpen)
    if (!(await clickSelector('.drawer-header button'))) return note('点不到关闭按钮')
    await waitFor('× 关闭', async () => !(await drawerOpen())).catch((err) => note(err.message))
    if (await drawerOpen()) note('点 × 后抽屉仍在')
  })

  // ---- P5：查看项目链接导航且不弹抽屉 ------------------------------------
  await runPoint('P5 点「查看项目」进入项目页且不弹抽屉', async (note) => {
    await openProductsPage()
    const card = await readCard(0)
    if (!(await clickSelector('.product-card .card-title'))) note('打开抽屉失败（可继续判导航）')
    await sleep(300)
    await closeDrawer().catch(() => {})
    if (!(await clickSelector('.product-card a.btn'))) return note('找不到卡片内「查看项目」链接')
    await waitFor('项目页', async () => !!(await evaluate("document.querySelector('.drawer') === null && location.hash.includes('/projects')")))
    const hash = await evaluate('location.hash')
    if (!hash.includes(`/products/${card.productId}/projects`) && !hash.includes('/projects')) {
      note(`点链接后 hash=${hash}（期望进入项目页）`)
    }
    if (await drawerOpen()) note('点「查看项目」时弹出了抽屉（事件冒泡未被拦截）')
  })

  // ---- P6：详情 404 的可见错误 ------------------------------------------
  await runPoint('P6 详情 404 时抽屉内有可见错误提示', async (note) => {
    await openProductsPage()
    const before = await evaluate("document.querySelectorAll('.product-card').length")
    detailMode = 'not-found'
    if (!(await clickSelector('.product-card .card-title'))) return note('无法打开抽屉')
    await waitFor('错误提示', async () =>
      !!(await evaluate(
        "document.querySelector('.drawer .alert.alert-error')?.innerText?.includes('HTTP 404')",
      )),
    ).catch((err) => note(err.message))
    const alertText = await evaluate("document.querySelector('.drawer .alert.alert-error')?.innerText ?? ''")
    if (!alertText.includes('HTTP 404')) note(`错误条文本=${JSON.stringify(alertText)} 不含 HTTP 404`)
    const after = await evaluate("document.querySelectorAll('.product-card').length")
    if (after !== before) note(`列表卡片数从 ${before} 变成 ${after}（不应白屏/清空）`)
    detailMode = 'passthrough'
    await clickSelector('.drawer-mask')
    await waitFor('错误态抽屉关闭', async () => !(await drawerOpen())).catch((err) => note(err.message))
    if (!(await clickSelector('.product-card .card-title'))) return note('关闭后无法再打开抽屉')
    await waitFor('恢复后可再次打开', drawerOpen).catch((err) => note(err.message))
    const recovered = await evaluate("!!document.querySelector('.drawer .detail-stack')")
    if (!recovered) {
      // 恢复路径同样要等详情请求回来（新实例会重新走一次 loading）。
      await waitFor('恢复后的详情内容', async () =>
        !!(await evaluate("!!document.querySelector('.drawer .detail-stack')")),
      ).catch((err) => note(err.message))
    }
    await closeDrawer().catch(() => {})
  })

  // ---- P7：空值占位与仓库地址两种形态 ------------------------------------
  await runPoint('P7 空值占位与仓库地址两种形态', async (note) => {
    await openProductsPage()
    // (a) git@ 形态 + 空 description/license → 纯文本 + 占位符
    detailMode = 'empty-and-ssh'
    if (!(await clickSelector('.product-card .card-title'))) return note('无法打开抽屉')
    await waitFor('抽屉内容', async () => !!(await evaluate("!!document.querySelector('.drawer .detail-stack')")))
    const ssh = await evaluate(`(() => {
      const drawer = document.querySelector('.drawer')
      const text = drawer.querySelector('.drawer-body')?.innerText ?? ''
      return { text, links: Array.from(drawer.querySelectorAll('a')).map(a => a.getAttribute('href') ?? '') }
    })()`)
    if (!ssh.text.includes('git@github.com:org/repo.git')) note('git@ 形态未原样展示')
    if (ssh.links.some((href) => href.startsWith('http'))) {
      note(`git@ 形态被渲染成链接：${JSON.stringify(ssh.links)}`)
    }
    if (!ssh.text.includes('—')) note('空 description/license 未显示占位符 —')
    if (/undefined|null/.test(ssh.text)) note('空值显示成 undefined/null')
    await clickSelector('.drawer-mask')
    await waitFor('关闭 ssh 抽屉', async () => !(await drawerOpen())).catch(() => {})

    // (b) https 形态 → 可点链接、新标签打开
    detailMode = 'https'
    if (!(await clickSelector('.product-card .card-title'))) return note('无法再次打开抽屉')
    await waitFor('https 抽屉内容', async () => !!(await evaluate("!!document.querySelector('.drawer .detail-stack')")))
    const https = await evaluate(`(() => {
      const link = document.querySelector('.drawer a[href^="https://"]')
      return link ? { href: link.getAttribute('href'), target: link.getAttribute('target'), rel: link.getAttribute('rel') } : null
    })()`)
    if (!https) note('https 仓库地址未渲染成链接')
    else {
      if (!https.href.startsWith('https://')) note(`链接 href=${https.href}`)
      if (https.target !== '_blank') note(`链接 target=${https.target}（期望 _blank）`)
      if (!(https.rel ?? '').includes('noopener')) note(`链接 rel=${https.rel}（期望含 noopener）`)
    }
    detailMode = 'passthrough'
    await clickSelector('.drawer-mask')
    await waitFor('关闭 https 抽屉', async () => !(await drawerOpen())).catch(() => {})
  })

  const failed = results.filter((item) => !item.ok)
  console.log(`\n自检结论：${results.length - failed.length}/${results.length} 通过`)
  try {
    ws.close()
  } catch {
    /* 忽略 */
  }
  return failed.length === 0 ? 0 : 1
}

class EnvError extends Error {}

main()
  .then((code) => process.exit(code))
  .catch((err) => {
    console.error(`自检无法开始：${err.message}`)
    process.exit(err instanceof EnvError ? 2 : 1)
  })
