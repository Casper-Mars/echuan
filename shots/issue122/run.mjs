// TaskCard 几何 / 断行取证（issue #122）：一次跑完「三页 × 多视口 × 状态矩阵」的断言与截图。
//
// 定位：AC9 要求的一次性断言脚本，**随卡交付**但不并入 `make web` 门禁（门禁只跑
// web/scripts/style-debt.mjs 与 web/scripts/check-size.mjs；`shots/` 不在 style-debt 扫描面内）。
//
// 用法：
//   node shots/issue122/run.mjs --dist <web/dist 或 internal/web/dist> --out <截图与结果目录> \
//     [--mode before|after] [--cdp 'ws://browserless:3000?token=test-token'] [--port 8931] \
//     [--host dsh-agent] [--expect pass|any] [--only board,inbox,queue] [--clips 1440x900,390x844] \
//     [--puppeteer-dir <装有 puppeteer-core 的目录>]
//
// 依赖：puppeteer-core **不进本仓依赖**，按以下顺序解析（先命中先用，任一项可覆盖）：
//   1) `--puppeteer-dir <dir>`        例：`--puppeteer-dir /path/to/browser-tools`
//   2) `PUPPETEER_DIR=<dir>`          同名环境变量
//   3) `NODE_PATH=<dir>[:<dir>…]`     Node 常规模块搜索路径
//   4) `<dir>/node_modules/puppeteer-core` 或全局安装处
//   5) 环境约定兜底 `/workspace/.agent-env/browser-tools`（agent 容器内预置；换机器时用 1–3 覆盖）
// 浏览器：CDP 端点默认 `ws://browserless:3000?token=test-token`（环境自带 browserless），
//   `--cdp` 或 `DSH_BROWSER_CDP_URL` 可覆盖；页面地址形如 `http://<--host>:<--port>/`，默认
//   `dsh-agent` = 本 agent 容器在浏览器网络里的别名（服务在宿主时改用 `host.docker.internal`）。
//
// 断言口径见 docs/tech/task-card-typography-122-v0.1.md §8.1（AC1–AC6）与 design/components.md §ui/TaskCard：
//   AC1 内距：命中区 block/inline 四边 = space-3(8)；标题/元信息/task_id 左缘距卡片内框 ≥ 8；状态条不压字形
//   AC2 长标题：≥120 全角 ≤ 3 行、行尾省略、无横向溢出
//   AC3 长串：100+ 无空格串 3 行内按字符断行、无横向溢出、无后代越出 padding box
//   AC4 窄视口：卡片宽 ≤ 容器内宽、元信息单项不被裁、task_id 单行省略且 title 保留完整值
//   AC6 焦点环：Tab 到卡片后焦点环完整包裹整卡（命中区矩形不越出卡片矩形）
import { existsSync, mkdirSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import path from 'node:path'
import process from 'node:process'
import { fileURLToPath } from 'node:url'

import { fixtures, MARKERS } from './lib/fixtures.mjs'
import { createStubServer } from './lib/stub.mjs'

const HERE = path.dirname(fileURLToPath(import.meta.url))

/** 解析 puppeteer-core：`--puppeteer-dir` → `PUPPETEER_DIR` → `NODE_PATH` → 环境约定兜底 */
function loadPuppeteer(dirArg) {
  const dirs = [dirArg, process.env.PUPPETEER_DIR]
  for (const entry of (process.env.NODE_PATH ?? '').split(path.delimiter)) dirs.push(entry)
  dirs.push('/workspace/.agent-env/browser-tools')
  const tried = []
  for (const dir of dirs) {
    if (dir === undefined || dir === '') continue
    tried.push(dir)
    try {
      return createRequire(path.join(path.resolve(dir), 'noop.js'))('puppeteer-core')
    } catch {
      // 换下一个候选目录
    }
  }
  throw new Error(`解析不到 puppeteer-core。已尝试：${tried.join(', ')}；请用 --puppeteer-dir 或 PUPPETEER_DIR / NODE_PATH 指定`)
}

const DEFAULT_CDP = 'ws://browserless:3000?token=test-token'
const PAD_TARGET = 8

function parseArgs(argv) {
  const out = {
    cdp: process.env.DSH_BROWSER_CDP_URL ?? DEFAULT_CDP,
    port: '8931',
    host: 'dsh-agent',
    mode: 'after',
    expect: 'pass',
    only: 'board,inbox,queue',
    clips: '1440x900,768x1024,390x844',
  }
  for (let i = 0; i < argv.length; i += 1) {
    const key = argv[i]
    if (!key.startsWith('--')) throw new Error(`未知参数：${key}`)
    out[key.slice(2)] = argv[++i]
  }
  for (const required of ['dist', 'out']) if (out[required] === undefined) throw new Error(`缺少 --${required}`)
  return out
}

const VIEWPORTS = [
  { name: '1440x900', width: 1440, height: 900 },
  { name: '768x1024', width: 768, height: 1024 },
  { name: '390x844', width: 390, height: 844 },
]
const NARROW = { name: '320x800', width: 320, height: 800 }

const PAGES = [
  { key: 'board', route: '/#/projects/echuan-6/tasks', cardSelector: '.ui-task-card', hasFocusVariant: true },
  { key: 'inbox', route: '/#/inbox', cardSelector: '.ix-grid .ui-task-card', hasFocusVariant: true },
  { key: 'queue', route: '/#/queue', cardSelector: '.qx-grid .ui-task-card', hasFocusVariant: false },
]

const EXTRACTS = [
  { state: 'longtitle', by: 'title', marker: MARKERS.longTitle },
  { state: 'longtoken', by: 'title', marker: MARKERS.longToken },
  { state: 'longid', by: 'id', marker: MARKERS.longTaskId },
  { state: 'multibadge', by: 'title', marker: '多标签卡' },
]

// 页内探针：只读几何，不做断言（断言在 Node 侧，便于输出「实际值 vs 期望值」）
const PROBE = () => {
  const round = value => Math.round(value * 100) / 100
  const roundRect = el => {
    if (el === null) return null
    const r = el.getBoundingClientRect()
    return { left: round(r.left), right: round(r.right), width: round(r.width), height: round(r.height) }
  }
  const lineHeightOf = el => (el === null ? null : round(parseFloat(getComputedStyle(el).lineHeight) || 0))
  const cards = Array.from(document.querySelectorAll('.ui-task-card'))
  const out = []
  cards.forEach((card, index) => {
    const hit = card.querySelector('.ui-task-card__hit')
    if (hit === null) {
      out.push({ index, missingHit: true })
      return
    }
    const cardRect = card.getBoundingClientRect()
    const hitRect = hit.getBoundingClientRect()
    const cardStyle = getComputedStyle(card)
    const hitStyle = getComputedStyle(hit)
    const borderLeft = parseFloat(cardStyle.borderLeftWidth) || 0
    const barWidth = parseFloat(getComputedStyle(card, '::before').width) || 0
    const pad = {
      blockStart: parseFloat(hitStyle.paddingBlockStart),
      blockEnd: parseFloat(hitStyle.paddingBlockEnd),
      inlineStart: parseFloat(hitStyle.paddingInlineStart),
      inlineEnd: parseFloat(hitStyle.paddingInlineEnd),
    }
    const titleEl = hit.querySelector('.ui-task-card__title')
    const metaEl = hit.querySelector('.ui-task-card__meta')
    const idEl = hit.querySelector('.ui-task-card__id')
    let firstCharLeft = null
    if (titleEl !== null && titleEl.firstChild !== null) {
      const node = titleEl.firstChild
      const range = document.createRange()
      range.setStart(node, 0)
      range.setEnd(node, Math.min(1, node.textContent.length))
      firstCharLeft = round(range.getBoundingClientRect().left)
    }
    const padLeft = hitRect.left + pad.inlineStart
    const padRight = hitRect.right - pad.inlineEnd
    let descendantOverflow = 0
    for (const el of hit.querySelectorAll('*')) {
      const r = el.getBoundingClientRect()
      if (r.width === 0 && r.height === 0) continue
      if (r.right > padRight + 1 || r.left < padLeft - 1) descendantOverflow += 1
    }
    let metaClipped = 0
    if (metaEl !== null) {
      for (const el of metaEl.children) if (el.scrollWidth > el.clientWidth + 1) metaClipped += 1
    }
    out.push({
      index,
      title: titleEl === null ? '' : titleEl.textContent.slice(0, 32),
      taskId: idEl === null ? null : idEl.textContent,
      padding: pad,
      borderLeft,
      barWidth,
      firstCharLeft,
      textLefts: {
        title: titleEl === null ? null : round(titleEl.getBoundingClientRect().left),
        meta: metaEl === null ? null : round(metaEl.getBoundingClientRect().left),
        id: idEl === null ? null : round(idEl.getBoundingClientRect().left),
      },
      card: roundRect(card),
      hit: roundRect(hit),
      parent: roundRect(card.parentElement),
      titleLineHeight: lineHeightOf(titleEl),
      titleLines: titleEl === null ? null : round(titleEl.clientHeight / (lineHeightOf(titleEl) || 1)),
      titleOverflowX: titleEl === null ? null : round(titleEl.scrollWidth - titleEl.clientWidth),
      titleClamped: titleEl === null ? null : titleEl.scrollHeight > titleEl.clientHeight + 1,
      metaClipped,
      descendantOverflow,
      idOverflowX: idEl === null ? null : round(idEl.scrollWidth - idEl.clientWidth),
      idWhiteSpace: idEl === null ? null : getComputedStyle(idEl).whiteSpace,
      idEllipsis: idEl === null ? null : getComputedStyle(idEl).textOverflow,
      idTitleAttr: idEl === null ? null : idEl.getAttribute('title'),
      idLines: idEl === null ? null : round(idEl.clientHeight / (lineHeightOf(idEl) || 1)),
    })
  })
  return {
    cards: out,
    docOverflowX: document.documentElement.scrollWidth - window.innerWidth,
    bodyOverflowX: document.body.scrollWidth - window.innerWidth,
  }
}

function near(a, b, tol = 0.5) {
  return Math.abs(a - b) <= tol
}

function assertionsOf(snapshot, page, viewport) {
  const list = []
  const add = (id, passed, detail) => list.push({ id: `${page.key}/${viewport.name}/${id}`, passed, detail })
  const real = snapshot.cards.filter(item => item.missingHit !== true)
  add('cards>=1', real.length >= 1, `${real.length} card(s)`)

  // AC1 内距四边 = space-3
  const padBad = real.filter(
    card =>
      !near(card.padding.blockStart, PAD_TARGET) ||
      !near(card.padding.blockEnd, PAD_TARGET) ||
      !near(card.padding.inlineStart, PAD_TARGET) ||
      !near(card.padding.inlineEnd, PAD_TARGET),
  )
  add(
    'AC1.padding=8',
    padBad.length === 0,
    padBad.length === 0
      ? `all ${real.length} cards 8/8/8/8`
      : `${padBad.length} card(s) 偏离，例：#${padBad[0].index} block ${padBad[0].padding.blockStart}/${padBad[0].padding.blockEnd} inline ${padBad[0].padding.inlineStart}/${padBad[0].padding.inlineEnd}`,
  )

  // AC1 文本左缘 ≥ 卡片内框左缘 + 8
  const gapBad = []
  for (const card of real) {
    const edge = card.card.left + card.borderLeft
    for (const [name, left] of Object.entries(card.textLefts)) {
      if (left === null) continue
      if (left - edge < PAD_TARGET - 0.5) gapBad.push(`#${card.index}.${name} ${Math.round((left - edge) * 100) / 100}px`)
    }
  }
  add('AC1.textGap>=8', gapBad.length === 0, gapBad.length === 0 ? `min gap ok on ${real.length} cards` : gapBad.slice(0, 4).join('; '))

  // AC1 状态条不压字形
  const barBad = real.filter(card => card.firstCharLeft !== null && card.firstCharLeft < card.card.left + card.borderLeft + card.barWidth - 0.5)
  add(
    'AC1.statusBarOnGlyph',
    barBad.length === 0,
    barBad.length === 0
      ? `bar ${real[0]?.barWidth ?? 'n/a'}px 未压字（${real.length} 卡）`
      : `${barBad.length} card(s) 首字符被压，例：#${barBad[0].index} glyph ${barBad[0].firstCharLeft} vs bar-edge ${Math.round((barBad[0].card.left + barBad[0].borderLeft + barBad[0].barWidth) * 100) / 100}`,
  )

  // AC2 长标题
  const longTitle = real.find(card => card.title.includes(MARKERS.longTitle)) ?? null
  if (longTitle === null) add('AC2.longTitle.present', false, '未找到长标题卡片')
  else {
    add('AC2.longTitle.lines<=3', longTitle.titleLines !== null && longTitle.titleLines <= 3.05, `lines=${longTitle.titleLines} lineHeight=${longTitle.titleLineHeight}`)
    add('AC2.longTitle.clamped', longTitle.titleClamped === true, `scrollHeight>clientHeight=${longTitle.titleClamped}`)
    add('AC2.longTitle.noOverflowX', longTitle.titleOverflowX !== null && longTitle.titleOverflowX <= 1, `scrollWidth-clientWidth=${longTitle.titleOverflowX}`)
  }

  // AC3 长不可断串
  const longToken = real.find(card => card.title.includes(MARKERS.longToken)) ?? null
  if (longToken === null) add('AC3.longToken.present', false, '未找到长串卡片')
  else {
    add('AC3.longToken.lines<=3', longToken.titleLines !== null && longToken.titleLines <= 3.05, `lines=${longToken.titleLines}`)
    add('AC3.longToken.noOverflowX', longToken.titleOverflowX !== null && longToken.titleOverflowX <= 1, `scrollWidth-clientWidth=${longToken.titleOverflowX}`)
    add('AC3.longToken.noDescendantOverflow', longToken.descendantOverflow === 0, `越出 padding box 的后代=${longToken.descendantOverflow}`)
  }

  // AC4 窄视口
  if (viewport.width <= 390) {
    const widthBad = real.filter(card => card.parent !== null && card.card.width > card.parent.width + 0.5)
    add(
      'AC4.cardFitsContainer',
      widthBad.length === 0,
      widthBad.length === 0 ? 'all cards <= parent width' : `${widthBad.length} card(s) 撑破，例：#${widthBad[0].index} ${widthBad[0].card.width} > ${widthBad[0].parent.width}`,
    )
    const metaBad = real.filter(card => card.metaClipped > 0)
    add('AC4.metaNotClipped', metaBad.length === 0, metaBad.length === 0 ? 'meta items 无裁剪' : `${metaBad.length} card(s) 元信息被裁，例：#${metaBad[0].index}`)
    const idBad = real.filter(card => card.idLines !== null && card.idLines > 1.05)
    add('AC4.taskIdSingleLine', idBad.length === 0, idBad.length === 0 ? 'task_id 均单行' : `${idBad.length} card(s) task_id 换行`)
    const titleBad = real.filter(card => card.idTitleAttr !== null && card.taskId !== null && card.idTitleAttr !== card.taskId)
    add('AC4.taskIdTitleAttr', titleBad.length === 0, titleBad.length === 0 ? 'title 保留完整 task_id' : `${titleBad.length} card(s) title 缺失/不等`)
  }

  // 后代不越出 padding box
  const overflowBad = real.filter(card => card.descendantOverflow > 0)
  add('innerOverflow=none', overflowBad.length === 0, overflowBad.length === 0 ? 'no descendant outside padding box' : `${overflowBad.length} card(s) 越界，例：#${overflowBad[0].index} (${overflowBad[0].descendantOverflow})`)
  return list
}

async function newPage(browser, viewport) {
  const page = await browser.newPage()
  await page.setCacheEnabled(false)
  await page.setViewport({ width: viewport.width, height: viewport.height, deviceScaleFactor: 1 })
  const errors = []
  page.on('pageerror', error => errors.push(String(error.message ?? error)))
  page.on('console', message => {
    if (message.type() === 'error') errors.push(message.text())
  })
  return { page, errors }
}

async function ready(page, selector) {
  await page.waitForFunction(sel => document.querySelectorAll(sel).length > 0, { timeout: 15_000 }, selector)
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
}

async function findCard(page, by, marker) {
  const index = await page.evaluate(
    (byArg, markerArg) => {
      const cards = Array.from(document.querySelectorAll('.ui-task-card'))
      return cards.findIndex(card => {
        const el = byArg === 'id' ? card.querySelector('.ui-task-card__id') : card.querySelector('.ui-task-card__title')
        return el !== null && el.textContent.includes(markerArg)
      })
    },
    by,
    marker,
  )
  if (index < 0) return null
  const handles = await page.$$('.ui-task-card')
  return handles[index] ?? null
}

async function tabToCard(page) {
  await page.evaluate(() => document.body.focus())
  for (let i = 0; i < 80; i += 1) {
    await page.keyboard.press('Tab')
    const hit = await page.evaluate(() => {
      const active = document.activeElement
      return active !== null && active.classList.contains('ui-task-card__hit')
    })
    if (hit) return true
  }
  return false
}

async function focusRingMetrics(page) {
  return page.evaluate(() => {
    const active = document.activeElement
    if (active === null || !active.classList.contains('ui-task-card__hit')) return null
    const card = active.closest('.ui-task-card')
    const hitRect = active.getBoundingClientRect()
    const cardRect = card.getBoundingClientRect()
    const style = getComputedStyle(active)
    return {
      outlineStyle: style.outlineStyle,
      outlineWidth: parseFloat(style.outlineWidth) || 0,
      outlineOffset: parseFloat(style.outlineOffset) || 0,
      hit: { left: hitRect.left, right: hitRect.right, top: hitRect.top, bottom: hitRect.bottom },
      card: { left: cardRect.left, right: cardRect.right, top: cardRect.top, bottom: cardRect.bottom },
    }
  })
}

function pngPath(outDir, pageKey, viewport, state) {
  return path.join(outDir, pageKey, `${viewport.name}-${state}.png`)
}

async function capturePage(page, file) {
  mkdirSync(path.dirname(file), { recursive: true })
  await page.screenshot({ path: file })
  return file
}

async function captureCard(handle, file) {
  mkdirSync(path.dirname(file), { recursive: true })
  // 居中滚动：移动端底部固定 TabBar 会盖住贴底的卡片
  await handle.evaluate(el => el.scrollIntoView({ block: 'center', inline: 'nearest' }))
  await handle.scrollIntoView()
  await handle.screenshot({ path: file })
  return file
}

async function runStatic(browser, baseUrl, outDir, pageKeys, viewports, clips) {
  const records = []
  const shots = []
  for (const pageKey of pageKeys) {
    const info = PAGES.find(item => item.key === pageKey)
    for (const viewport of viewports) {
      const { page, errors } = await newPage(browser, viewport)
      try {
        await page.goto(`${baseUrl}${info.route}`, { waitUntil: 'networkidle2', timeout: 30_000 })
        await ready(page, info.cardSelector)
        shots.push(await capturePage(page, pngPath(outDir, info.key, viewport, 'rest')))
        const snapshot = await page.evaluate(PROBE)
        // AC10 对照数据：真实卡片高度（与 loading.skeleton 的骨架高度对比，只记录不调）
        const heights = snapshot.cards.filter(item => item.missingHit !== true).map(item => item.card.height)
        records.push({
          id: `${info.key}/${viewport.name}/card.heights`,
          passed: heights.length > 0,
          detail: `min=${Math.min(...heights)}px max=${Math.max(...heights)}px n=${heights.length}`,
        })

        // hover：悬停「多标签卡」（元信息行最全的那张）
        const hoverIndex = await page.evaluate(() => {
          const list = Array.from(document.querySelectorAll('.ui-task-card'))
          const at = list.findIndex(card => (card.querySelector('.ui-task-card__title')?.textContent ?? '').includes('多标签卡'))
          return at >= 0 ? at : 0
        })
        const cards = await page.$$('.ui-task-card')
        const hoverCard = cards[hoverIndex]
        if (hoverCard !== undefined) {
          await hoverCard.scrollIntoView()
          await hoverCard.hover()
          await page.evaluate(() => new Promise(resolve => setTimeout(resolve, 150)))
          shots.push(await capturePage(page, pngPath(outDir, info.key, viewport, 'hover')))
          await page.mouse.move(1, 1)
        }

        // focus 变体（in_review 首卡）
        if (info.hasFocusVariant) {
          const focusCard = await page.$('.ui-task-card[data-focused]')
          if (focusCard !== null) {
            await focusCard.scrollIntoView()
            await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
            shots.push(await capturePage(page, pngPath(outDir, info.key, viewport, 'focus')))
          }
        }

        // focus-visible：Tab 到卡片（AC6）
        const tabbed = await tabToCard(page)
        const ring = tabbed ? await focusRingMetrics(page) : null
        if (tabbed) {
          shots.push(await capturePage(page, pngPath(outDir, info.key, viewport, 'focus-visible')))
          const focusedCard = await page.$('.ui-task-card .ui-task-card__hit:focus-visible')
          const focusedHandle = focusedCard === null ? null : await focusedCard.evaluateHandle(el => el.closest('.ui-task-card'))
          if (focusedHandle !== null) {
            const element = focusedHandle.asElement()
            if (element !== null) shots.push(await captureCard(element, pngPath(outDir, info.key, viewport, 'card-focusring')))
          }
        }

        // 极端卡片元素级截图
        if (clips.has(viewport.name)) {
          for (const extract of EXTRACTS) {
            const handle = await findCard(page, extract.by, extract.marker)
            if (handle === null) {
              records.push({ id: `${info.key}/${viewport.name}/extract.${extract.state}`, passed: false, detail: '未找到目标卡片' })
              continue
            }
            shots.push(await captureCard(handle, pngPath(outDir, info.key, viewport, `card-${extract.state}`)))
            records.push({ id: `${info.key}/${viewport.name}/extract.${extract.state}`, passed: true, detail: 'captured' })
          }
        }

        records.push(...assertionsOf(snapshot, info, viewport))
        // 焦点环不被裁：命中区贴满卡片 padding box，卡片 `overflow: hidden` 裁在 padding box 上，
        // 因此环的外沿必须在命中区盒子内侧（offset + width ≤ 0），否则整环被裁掉（改前实测像素 0 变化）。
        const ringOuterInset = ring === null ? null : ring.outlineOffset + ring.outlineWidth
        const ringInsideHit = ringOuterInset !== null && ringOuterInset <= 0.5
        const hitInsideCard =
          ring !== null &&
          ring.hit.left >= ring.card.left - 0.5 &&
          ring.hit.right <= ring.card.right + 0.5 &&
          ring.hit.top >= ring.card.top - 0.5 &&
          ring.hit.bottom <= ring.card.bottom + 0.5
        records.push({
          id: `${info.key}/${viewport.name}/AC6.focusRing`,
          passed: ring !== null && ring.outlineStyle === 'solid' && ring.outlineWidth >= 1 && ringInsideHit && hitInsideCard,
          detail:
            ring === null
              ? 'Tab 未落在卡片命中区'
              : `outline ${ring.outlineStyle} ${ring.outlineWidth}px offset ${ring.outlineOffset}px（外沿相对命中区 ${ringOuterInset}px，≤0 才不被裁）; hit⊂card=${hitInsideCard}`,
        })
        records.push({ id: `${info.key}/${viewport.name}/consoleErrors`, passed: errors.length === 0, detail: errors.slice(0, 3).join(' | ') || 'none' })
      } finally {
        await page.close()
      }
    }
  }
  return { records, shots }
}

async function runCollapsed(browser, baseUrl, outDir) {
  const shots = []
  const records = []
  for (const viewport of [VIEWPORTS[0], VIEWPORTS[2]]) {
    const { page } = await newPage(browser, viewport)
    try {
      await page.goto(`${baseUrl}/#/projects/echuan-6/tasks`, { waitUntil: 'networkidle2', timeout: 30_000 })
      await ready(page, '.ui-task-card')
      // 折叠偏好存 localStorage：上一档视口跑过之后 done 可能已被展开，先兜底「折一下 done」
      // 造出折叠态，再点开——避免第二次进入时点到的是「展开」按钮反而把列折起来。
      let collapsedHead = await page.$('.ui-board-column--collapsed .ui-board-column__head')
      if (collapsedHead === null) {
        const doneHead = await page.$('.ui-board-column[data-status="done"] .ui-board-column__head')
        if (doneHead === null) {
          records.push({ id: `board/${viewport.name}/collapsed.expand`, passed: false, detail: '找不到 done 列折叠按钮' })
          continue
        }
        await page.evaluate(el => el.click(), doneHead)
        await page.waitForFunction(() => document.querySelector('.ui-board-column--collapsed .ui-board-column__head') !== null, { timeout: 10_000 })
        collapsedHead = await page.$('.ui-board-column--collapsed .ui-board-column__head')
      }
      if (collapsedHead === null) {
        records.push({ id: `board/${viewport.name}/collapsed.expand`, passed: false, detail: '没有可展开的折叠列' })
        continue
      }
      const status = await page.evaluate(el => el.closest('.ui-board-column').dataset.status, collapsedHead)
      // 移动端看板是 scroll-snap 横滑：坐标点击会因吸附回弹打空，这里走同一个 React 处理器的
      // 合成点击（el.click()），再把展开后的列滚进可视区截图。
      await page.evaluate(el => el.click(), collapsedHead)
      await page.waitForFunction(
        (statusArg) => document.querySelectorAll(`.ui-board-column[data-status="${statusArg}"] .ui-task-card`).length > 0,
        { timeout: 10_000 },
        status,
      )
      const expandedHead = await page.$(`.ui-board-column[data-status="${status}"] .ui-board-column__head`)
      if (expandedHead !== null) await page.evaluate(el => el.scrollIntoView({ block: 'nearest', inline: 'center' }), expandedHead)
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
      shots.push(await capturePage(page, pngPath(outDir, 'board', viewport, 'collapsed')))
      const snapshot = await page.evaluate(PROBE)
      const card = snapshot.cards.find(item => item.missingHit !== true)
      records.push({
        id: `board/${viewport.name}/collapsed.expand`,
        passed: card !== undefined && near(card.padding.blockStart, PAD_TARGET) && near(card.padding.inlineStart, PAD_TARGET),
        detail: card === undefined ? '展开后无卡片' : `expanded card pad block/inline-start ${card.padding.blockStart}/${card.padding.inlineStart}`,
      })
    } finally {
      await page.close()
    }
  }
  return { records, shots }
}

async function runLoading(browser, baseUrl, stub, outDir) {
  const shots = []
  const records = []
  for (const viewport of [VIEWPORTS[0], VIEWPORTS[2]]) {
    for (const info of PAGES) {
      const { page } = await newPage(browser, viewport)
      try {
        stub.setDelay(6000)
        await page.goto(`${baseUrl}${info.route}`, { waitUntil: 'domcontentloaded', timeout: 30_000 })
        await page.waitForFunction(() => document.querySelectorAll('.ui-skeleton-card').length > 0, { timeout: 10_000 })
        await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
        shots.push(await capturePage(page, pngPath(outDir, info.key, viewport, 'loading')))
        const skeletons = await page.evaluate(() => {
          const list = Array.from(document.querySelectorAll('.ui-skeleton-card'))
          return { count: list.length, heights: list.map(el => Math.round(el.getBoundingClientRect().height * 100) / 100) }
        })
        // AC10：骨架卡高度（与真实卡片高度差由 result.json 的两条记录直接对比，只记录不调）
        records.push({ id: `${info.key}/${viewport.name}/loading.skeleton`, passed: skeletons.count > 0, detail: `${skeletons.count} skeleton(s), heights=${skeletons.heights.join('/')}px` })
      } catch (error) {
        records.push({ id: `${info.key}/${viewport.name}/loading.skeleton`, passed: false, detail: `未能抓到骨架态：${error.message}` })
      } finally {
        stub.setDelay(0)
        await page.close()
      }
    }
  }
  return { records, shots }
}

async function runNarrow(browser, baseUrl, outDir) {
  const shots = []
  const records = []
  for (const info of PAGES) {
    const { page } = await newPage(browser, NARROW)
    try {
      await page.goto(`${baseUrl}${info.route}`, { waitUntil: 'networkidle2', timeout: 30_000 })
      await ready(page, info.cardSelector)
      shots.push(await capturePage(page, pngPath(outDir, info.key, NARROW, 'rest')))
      const snapshot = await page.evaluate(PROBE)
      records.push(...assertionsOf(snapshot, info, NARROW))
    } finally {
      await page.close()
    }
  }
  return { records, shots }
}

function renderMarkdown(result) {
  const script = path.relative(process.cwd(), path.join(HERE, 'run.mjs')) || 'shots/issue122/run.mjs'
  const lines = [
    `# issue #122 断言结果（${result.mode}）`,
    '',
    '## 复跑',
    '',
    `- 脚本（随卡交付，不进 \`make web\` 门禁）：\`${script}\` + \`${path.dirname(script)}/lib/{fixtures,stub}.mjs\`；` +
      '参数 `--dist`（被测产物）/ `--out`（截图与结果目录）/ `--mode` / `--expect`；' +
      '`--cdp` 缺省 `ws://browserless:3000?token=test-token`；页面地址 `http://<--host>:<--port>/`（默认 `dsh-agent`）；' +
      'puppeteer-core 用 `--puppeteer-dir` / `PUPPETEER_DIR` / `NODE_PATH` 指定（不进仓库依赖）',
    '- after：`node shots/issue122/run.mjs --dist internal/web/dist --out <dir> --mode after --expect pass` —— 全套断言须全绿、exit 0',
    '- before：`node shots/issue122/run.mjs --dist <基线 commit 上 make web 的产物> --out <dir> --mode before --expect any` —— 失败项即缺陷面，exit 0',
    `- 本文件由上面「${result.mode}」那条命令生成；本次 dist = \`${result.dist}\`，` +
      `退出码由 \`--expect\` 决定（pass 时非全绿即 exit 1）`,
    '',
    `- dist: \`${result.dist}\``,
    `- base_url: \`${result.base_url}\``,
    `- generated_at: ${result.generated_at}`,
    `- 合计：${result.summary.passed}/${result.summary.total} 通过，${result.summary.failed} 失败；截图 ${result.summary.shots} 张`,
    '',
    '| 断言 | 结果 | 实际值 |',
    '|---|---|---|',
  ]
  for (const record of result.records) lines.push(`| ${record.id} | ${record.passed === true ? 'PASS' : 'FAIL'} | ${String(record.detail).replace(/\|/g, '\\|')} |`)
  lines.push('')
  return lines.join('\n')
}

async function main() {
  const args = parseArgs(process.argv.slice(2))
  const outDir = path.resolve(args.out)
  mkdirSync(outDir, { recursive: true })
  const stub = createStubServer({ dist: args.dist, fixtures: fixtures(), port: Number(args.port) })
  await stub.start()
  const baseUrl = `http://${args.host}:${args.port}`
  const clipSet = new Set(args.clips.split(',').filter(Boolean))
  const pageKeys = args.only.split(',').filter(Boolean)
  const puppeteer = loadPuppeteer(args['puppeteer-dir'])
  const browser = await puppeteer.connect({ browserWSEndpoint: args.cdp, defaultViewport: null })
  const result = {
    mode: args.mode,
    dist: path.resolve(args.dist),
    base_url: baseUrl,
    generated_at: new Date().toISOString(),
    records: [],
    shots: [],
  }
  try {
    const staticRun = await runStatic(browser, baseUrl, outDir, pageKeys, VIEWPORTS, clipSet)
    result.records.push(...staticRun.records)
    result.shots.push(...staticRun.shots)
    if (pageKeys.includes('board')) {
      const collapsed = await runCollapsed(browser, baseUrl, outDir)
      result.records.push(...collapsed.records)
      result.shots.push(...collapsed.shots)
    }
    const loading = await runLoading(browser, baseUrl, stub, outDir)
    result.records.push(...loading.records)
    result.shots.push(...loading.shots)
    const narrow = await runNarrow(browser, baseUrl, outDir)
    result.records.push(...narrow.records)
    result.shots.push(...narrow.shots)
  } finally {
    await browser.disconnect()
    await stub.close()
  }
  const failed = result.records.filter(item => item.passed !== true)
  const byId = {}
  for (const record of result.records) {
    const key = record.id.split('/').pop()
    byId[key] = byId[key] ?? { passed: 0, failed: 0 }
    if (record.passed === true) byId[key].passed += 1
    else byId[key].failed += 1
  }
  result.summary = { total: result.records.length, passed: result.records.length - failed.length, failed: failed.length, by_id: byId, shots: result.shots.length }
  writeFileSync(path.join(outDir, 'result.json'), `${JSON.stringify(result, null, 2)}\n`)
  writeFileSync(path.join(outDir, 'assertions.md'), renderMarkdown(result))
  console.log(JSON.stringify({ mode: result.mode, summary: result.summary, failed: failed.slice(0, 10).map(item => `${item.id} :: ${item.detail}`) }, null, 2))
  if (args.expect === 'pass' && failed.length > 0) process.exitCode = 1
}

main().catch(error => {
  console.error(error.stack ?? error.message)
  process.exitCode = 2
})
