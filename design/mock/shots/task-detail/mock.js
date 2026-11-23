/* 驿传 Echuan 设计原型：状态与主题由 URL 参数驱动。
   index.html?screen=board&state=default&theme=light
   截图脚本只传 state/theme，故每个屏目录里由 window.__SCREEN__ 指定屏幕。 */

const params = new URLSearchParams(location.search)
const state = params.get('state') || 'default'
const theme = params.get('theme') || 'dark'
const screen = window.__SCREEN__ || params.get('screen') || 'board'

document.documentElement.dataset.state = state
document.documentElement.dataset.theme = theme

/* ---------------- fixtures ---------------- */

const STATUS = {
  pending: { label: '待处理', tone: 'neutral' },
  ready: { label: '待领取', tone: 'info' },
  in_progress: { label: '进行中', tone: 'info' },
  in_review: { label: '待审核', tone: 'warning' },
  done: { label: '已完成', tone: 'success' },
  failed: { label: '失败', tone: 'danger' },
  cancelled: { label: '已取消', tone: 'neutral' },
}

const KIND = { requirement: '需求', design: '方案', breakdown: '拆解', dev: '开发' }

const TASKS = [
  { id: 't_01M7TKBXJ4KAMEWN07J6YQ18X3', title: 'Web UI 重做：技术选型决策 + 11 章技术方案', kind: 'design', status: 'in_review', priority: 60, attempt: 2, max: 3, updated: '19:13', focus: true },
  { id: 't_01M7TKBXJ4KAMEWN07J6YQ18X4', title: '看板列折叠与横向切列（移动端）', kind: 'dev', status: 'in_progress', priority: 55, attempt: 1, max: 3, updated: '18:52' },
  { id: 't_01M7TKBXJ4KAMEWN07J6YQ18X5', title: '详情路由化：可分享 URL + 抽屉承载', kind: 'dev', status: 'in_progress', priority: 50, attempt: 1, max: 3, updated: '18:40' },
  { id: 't_01M7TKBXJ4KAMEWN07J6YQ18X6', title: '附件 markdown 应用内预览与降级', kind: 'dev', status: 'ready', priority: 45, attempt: 0, max: 3, updated: '18:31' },
  { id: 't_01M7TKBXJ4KAMEWN07J6YQ18X7', title: '样式债出清：单一令牌源 + 死类清理', kind: 'dev', status: 'ready', priority: 40, attempt: 0, max: 3, updated: '18:30' },
  { id: 't_01M7TKBXJ4KAMEWN07J6YQ18X8', title: '体积门禁脚本与构建期打印', kind: 'dev', status: 'pending', priority: 35, attempt: 0, max: 3, updated: '18:12' },
  { id: 't_01M7TKBXJ4KAMEWN07J6YQ18X9', title: '评审动作：material_id 自动带入', kind: 'dev', status: 'pending', priority: 30, attempt: 0, max: 3, updated: '18:05' },
  { id: 't_01M7TKBXJ4KAMEWN07J6YQ18XA', title: '移动端底部 TabBar 与触控目标', kind: 'dev', status: 'done', priority: 25, attempt: 1, max: 3, updated: '17:44' },
  { id: 't_01M7TKBXJ4KAMEWN07J6YQ18XB', title: '任务看板接入 15s 自动刷新', kind: 'dev', status: 'done', priority: 20, attempt: 1, max: 3, updated: '17:20' },
  { id: 't_01M7TKBXJ4KAMEWN07J6YQ18XC', title: '旧看板 .board-column* 样式块删除', kind: 'dev', status: 'failed', priority: 15, attempt: 3, max: 3, updated: '16:58' },
  { id: 't_01M7TKBXJ4KAMEWN07J6YQ18XD', title: '图片预览能力（平台无二进制存储）', kind: 'dev', status: 'cancelled', priority: 10, attempt: 0, max: 3, updated: '16:30' },
  { id: 't_01M7TKBXJ4KAMEWN07J6YQ18XE', title: '收件箱合并派单队列入口', kind: 'dev', status: 'in_review', priority: 25, attempt: 1, max: 3, updated: '16:10' },
]

const ATTACHMENTS = [
  { material_id: 'AGQ7KPGXQD2XJD7LQDR6H44UY4', title: 'Web UI 重做 技术方案 v0.2', kind: '方案', role: 'output' },
  { material_id: 'AGQ7KPGXQD2XJD7LQDR6H44UY3', title: 'ADR-0001 Web UI 技术底座', kind: '决策', role: 'input' },
  { material_id: 'AGQ7KPGXQD2XJD7LQDR6H44UY2', title: 'issue #76 原始诉求记录', kind: '需求', role: 'input' },
]

const PRODUCTS = [
  { id: 'echuan', name: '驿传 Echuan', git: 'github.com/Casper-Mars/echuan', projects: 3 },
  { id: 'design-kit', name: '设计能力包', git: 'github.com/Casper-Mars/design-kit', projects: 1 },
  { id: 'relay', name: '中继网关', git: 'github.com/Casper-Mars/relay', projects: 0 },
  { id: 'sandbox', name: '沙箱实验台', git: 'github.com/Casper-Mars/sandbox', projects: 2 },
]

const PROJECTS = [
  { id: 'echuan-1', name: 'Web UI 重做（issue #76）', seq: 1, status: 'open', updated: '11-19 19:13' },
  { id: 'echuan-2', name: '附件预览能力', seq: 2, status: 'open', updated: '11-19 18:50' },
  { id: 'echuan-3', name: '自动刷新与观测量', seq: 3, status: 'closed', updated: '11-19 11:44' },
]

const WORKERS = [
  { id: 'w_d6f2a1', state: 'busy', task: 't_01M7TKBXJ4KAMEWN07J6YQ18X4', beat: '3s 前', warn: false },
  { id: 'w_a91c33', state: 'busy', task: 't_01M7TKBXJ4KAMEWN07J6YQ18X5', beat: '8s 前', warn: false },
  { id: 'w_77b0e4', state: 'idle', task: '—', beat: '21s 前', warn: false },
  { id: 'w_2fe8c0', state: 'busy', task: 't_01M7TKBXJ4KAMEWN07J6YQ18X6', beat: '4m 12s 前', warn: true },
]

/* ---------------- atoms ---------------- */

const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]))

const pill = (status) => {
  const s = STATUS[status] || STATUS.pending
  return `<span class="pill" data-tone="${s.tone}"><i class="pill-dot"></i>${s.label}</span>`
}

const kindBadge = (kind) => `<span class="kind">${KIND[kind] || kind}</span>`

const taskCard = (t) => `
  <button class="task-card" type="button" data-status="${t.status}" ${t.focus ? 'data-focus="true"' : ''}>
    <span class="task-title">${esc(t.title)}</span>
    <span class="task-meta">${kindBadge(t.kind)}<span>P${t.priority}</span><span>尝试 ${t.attempt}/${t.max}</span><span>${t.updated}</span></span>
    <span class="task-id">${esc(t.id)}</span>
  </button>`

const banner = (text, action = '重试') => `
  <div class="banner" data-tone="danger" role="alert">
    <span>${esc(text)}</span><span class="spacer"></span>
    <button class="btn" type="button">${esc(action)}</button>
  </div>`

const skeletonCards = (n) => Array.from({ length: n }, () => '<div class="sk sk-card"></div>').join('')

const emptyBlock = (title, text, action) => `
  <div class="empty">
    <p class="empty-title">${esc(title)}</p>
    <p class="empty-text">${esc(text)}</p>
    ${action ? `<button class="btn btn-primary" type="button">${esc(action)}</button>` : ''}
  </div>`

const shell = (body) => `
  <div class="app">
    <aside class="sidebar">
      <div class="brand"><span class="brand-mark">驿</span><span>驿传</span></div>
      <nav class="side-nav">
        <a class="nav-item is-active" href="#/projects/echuan-1/tasks"><span class="nav-icon">▤</span><span>看板</span></a>
        <a class="nav-item" href="#/inbox"><span class="nav-icon">✉</span><span>收件箱</span></a>
        <a class="nav-item" href="#/products"><span class="nav-icon">◫</span><span>产品</span></a>
        <a class="nav-item" href="#/workers"><span class="nav-icon">◉</span><span>执行节点</span></a>
      </nav>
      <div class="side-foot">v0.1 · 单二进制 · 内网</div>
    </aside>
    <div class="main">
      <header class="topbar">
        <nav class="crumbs"><span>产品</span><span class="sep">/</span><span>echuan</span><span class="sep">/</span><span>echuan-1</span></nav>
        <div class="topbar-actions">
          <button class="btn btn-ghost" type="button">刷新</button>
        </div>
      </header>
      ${body}
    </div>
  </div>
  <nav class="tabbar">
    <a class="tab is-active" href="#/projects/echuan-1/tasks"><span>▤</span><span>看板</span></a>
    <a class="tab" href="#/inbox"><span>✉</span><span>收件箱</span></a>
    <a class="tab" href="#/products"><span>◫</span><span>产品</span></a>
    <a class="tab" href="#/more"><span>⋯</span><span>更多</span></a>
  </nav>`

/* ---------------- screens ---------------- */

function productsPage() {
  if (state === 'loading') {
    return `<section class="page"><header class="page-head"><div><h1 class="page-title">产品</h1><p class="page-sub">4 个产品 · 6 个项目</p></div></header>
      <div class="card">${Array.from({ length: 4 }, () => '<div class="sk" style="height:44px;margin-bottom:8px"></div>').join('')}</div></section>`
  }
  if (state === 'empty') {
    return `<section class="page"><header class="page-head"><div><h1 class="page-title">产品</h1><p class="page-sub">还没有产品</p></div></header>
      ${emptyBlock('还没有产品', '产品是项目与任务的容器。创建第一个产品后，看板、收件箱与执行节点都会有数据。', '新建产品')}</section>`
  }
  if (state === 'error') {
    return `<section class="page"><header class="page-head"><div><h1 class="page-title">产品</h1></div></header>
      ${banner('加载失败：连接超时（GET /v1/products）')}</section>`
  }
  return `<section class="page">
    <header class="page-head">
      <div><h1 class="page-title">产品</h1><p class="page-sub">4 个产品 · 共 6 个项目</p></div>
      <div class="page-actions"><select class="select"><option>全部状态</option></select><button class="btn btn-primary" type="button">新建产品</button></div>
    </header>
    <div class="card"><table class="table">
      <thead><tr><th>产品</th><th>仓库</th><th>项目</th><th>操作</th></tr></thead>
      <tbody>${PRODUCTS.map((p) => `<tr>
        <td data-label="产品"><div class="row-title">${esc(p.name)}</div><div class="row-sub mono">${esc(p.id)}</div></td>
        <td data-label="仓库"><span class="mono">${esc(p.git)}</span></td>
        <td data-label="项目">${p.projects}</td>
        <td data-label="操作"><button class="btn" type="button">查看项目</button></td>
      </tr>`).join('')}</tbody>
    </table></div>
  </section>`
}

function projectsPage() {
  if (state === 'loading') return `<section class="page"><header class="page-head"><div><h1 class="page-title">项目</h1></div></header><div class="card">${skeletonCards(3)}</div></section>`
  if (state === 'empty') return `<section class="page"><header class="page-head"><div><h1 class="page-title">项目</h1><p class="page-sub">驿传 Echuan</p></div></header>${emptyBlock('这个产品还没有项目', '项目是任务的容器。创建项目后，看板会出现在项目详情里。', '新建项目')}</section>`
  if (state === 'error') return `<section class="page"><header class="page-head"><div><h1 class="page-title">项目</h1></div></header>${banner('加载失败：服务不可用（GET /v1/projects）')}</section>`
  return `<section class="page">
    <header class="page-head">
      <div><h1 class="page-title">项目</h1><p class="page-sub">驿传 Echuan · 3 个项目</p></div>
      <div class="page-actions"><button class="btn btn-primary" type="button">新建项目</button></div>
    </header>
    <div class="card"><table class="table">
      <thead><tr><th>序号</th><th>项目</th><th>状态</th><th>更新时间</th><th>操作</th></tr></thead>
      <tbody>${PROJECTS.map((p) => `<tr>
        <td data-label="序号">#${p.seq}</td>
        <td data-label="项目"><div class="row-title">${esc(p.name)}</div><div class="row-sub mono">${esc(p.id)}</div></td>
        <td data-label="状态">${p.status === 'open' ? '<span class="pill" data-tone="info"><i class="pill-dot"></i>进行中</span>' : '<span class="pill" data-tone="neutral"><i class="pill-dot"></i>已关闭</span>'}</td>
        <td data-label="更新时间">${p.updated}</td>
        <td data-label="操作"><button class="btn" type="button">打开看板</button></td>
      </tr>`).join('')}</tbody>
    </table></div>
  </section>`
}

const COLUMNS = ['pending', 'ready', 'in_progress', 'in_review', 'done', 'failed', 'cancelled']

function columnHtml(status, opts = {}) {
  const items = TASKS.filter((t) => t.status === status)
  const shown = opts.more ? items.concat(items, items).slice(0, 8) : items
  const collapsed = opts.collapse && (status === 'done' || status === 'cancelled')
  const body = opts.loading
    ? skeletonCards(2)
    : shown.length
      ? shown.map(taskCard).join('')
      : '<p class="col-empty">暂无任务</p>'
  return `<div class="col${collapsed ? ' collapsed' : ''}" data-accent="${status === 'in_review'}">
    <div class="col-head"><span class="col-name">${status}</span><span class="col-count">${shown.length}</span></div>
    <div class="col-body">${body}</div>
  </div>`
}

function boardBody(opts = {}) {
  return `<div class="board-wrap">
    <div class="board-bar">
      <span>7 列 · ${TASKS.length} 个任务</span>
      <span class="col-indicator">${opts.collapse ? "第 7 / 7 列 · done/cancelled 已折叠" : "第 1 / 7 列 · pending"}</span>
      <span>横向滑动查看后续列</span>
    </div>
    ${opts.error ? banner('看板刷新失败：保留上次数据（GET /v1/tasks）') : ''}
    <div class="board">${COLUMNS.map((s) => columnHtml(s, opts)).join('')}</div>
  </div>`
}

function boardPage() {
  const opts = {
    collapse: state === 'collapsed',
    more: state === 'long-list',
    loading: state === 'loading',
    error: state === 'error',
  }
  const empty = state === 'empty'
  return `<section class="page">
    <header class="page-head">
      <div><h1 class="page-title">Web UI 重做（issue #76）</h1><p class="page-sub">echuan-1 · ${TASKS.length} 个任务 · 15s 自动刷新</p></div>
      <div class="page-actions">
        <select class="select"><option>全部类型</option><option>开发</option><option>方案</option></select>
        <select class="select"><option>全部状态</option></select>
        <button class="btn btn-primary" type="button">新建任务</button>
      </div>
    </header>
    ${empty ? emptyBlock('这个项目还没有任务', '任务卡会按状态落进 7 列。先新建一条，或把 pending 的任务推进到 ready。', '新建任务') : boardBody(opts)}
  </section>`
}

function detailBody() {
  const leased = state === 'leased'
  const failed = state === 'failed'
  const rejected = state === 'rejected'
  const review = !failed
  const loading = state === 'loading'
  const error = state === 'error'

  if (loading) {
    return `<div class="drawer-body">
      <div class="meta-grid">${Array.from({ length: 4 }, () => '<div class="meta-item"><div class="sk sk-line w-40"></div><div class="sk sk-line w-60"></div></div>').join('')}</div>
      <div class="sk" style="height:120px"></div><div class="sk" style="height:160px"></div>
    </div>`
  }

  return `<div class="drawer-body">
    ${error ? banner('详情加载失败：附件服务返回 503（GET /v1/tasks）') : ''}
    <div class="meta-grid">
      <div class="meta-item"><span class="meta-label">状态</span><span class="meta-value">${pill(failed ? 'failed' : 'in_review')}</span></div>
      <div class="meta-item"><span class="meta-label">类型</span><span class="meta-value">${kindBadge('design')}</span></div>
      <div class="meta-item"><span class="meta-label">尝试</span><span class="meta-value">${failed ? '3/3' : '2/3'}</span></div>
      <div class="meta-item"><span class="meta-label">优先级</span><span class="meta-value">P60</span></div>
    </div>
    ${leased ? `<div class="lease"><span class="pill" data-tone="info"><i class="pill-dot"></i>租约中</span>
      <span class="mono">l_01M7TKK7TZ557VKBCF19Y5FXS2</span><span>到期 19:13:36</span><span class="mono">w_d6f2a1</span></div>` : ''}
    ${rejected ? `<div class="banner" data-tone="info"><span>上次驳回：需要我定的问题都按照默认的来做</span><span class="spacer"></span><span class="row-sub">19:09</span></div>` : ''}
    ${review ? `<div class="review">
      <span class="section-title">评审</span>
      <label class="field">驳回原因（驳回必填）
        <textarea class="textarea" placeholder="写清缺口与需要改的地方"></textarea>
      </label>
    </div>` : ''}
    <section class="section">
      <div class="section-head"><span class="section-title">附件</span><span class="section-count">3 个 · 可在线读正文</span></div>
      ${ATTACHMENTS.map((a) => `<button class="att-row" type="button">
        <span class="kind">${a.role === 'output' ? '输出' : '输入'}</span>
        <span class="att-name">${esc(a.title)}</span>
        <span class="att-meta">${esc(a.kind)}</span>
      </button>`).join('')}
    </section>
    <section class="section">
      <div class="section-head"><span class="section-title">任务描述</span></div>
      <div class="markdown">
        <p>针对 issue #76 完成 Web UI 重做的技术选型决策与技术方案，使后续 dev 任务可以照做。</p>
        <ul><li>选哪套 UI 技术底座</li><li>改动边界：不改 /v1 契约、不改数据库</li><li>信息架构：看板为主路径</li></ul>
      </div>
    </section>
    <section class="section">
      <div class="section-head"><span class="section-title">前置依赖</span></div>
      <div class="chip-row"><span class="chip mono">t_01M7SJ3K3JVCS9FW8218CP9MBB</span><span class="chip mono">t_01M7SJ3K3JVCS9FW8218CP9MBC</span></div>
    </section>
    ${failed ? `<section class="section"><div class="section-head"><span class="section-title">失败原因</span></div><p>尝试 3/3 用尽：镜像构建阶段 pnpm --frozen-lockfile 失败。</p></section>` : ''}
  </div>`
}

function detailFoot() {
  if (state === 'failed') {
    return `<div class="drawer-foot"><button class="btn btn-danger" type="button">删除</button><span style="margin-left:auto"></span><button class="btn btn-primary" type="button">重置为待处理</button></div>`
  }
  if (state === 'loading' || state === 'error') {
    return `<div class="drawer-foot"><button class="btn" type="button">关闭</button><span style="margin-left:auto"></span><button class="btn" type="button">重试</button></div>`
  }
  return `<div class="drawer-foot"><button class="btn btn-ghost" type="button">取消</button><span style="margin-left:auto"></span><button class="btn" type="button">驳回</button><button class="btn btn-primary" type="button">通过并关闭</button></div>`
}

function detailPage() {
  const background = `<section class="page">${boardBody({})}</section>`
  return `${background}
    <div class="scrim"></div>
    <aside class="drawer" role="dialog" aria-label="任务详情">
      <header class="drawer-head">
        <h2 class="drawer-title">${esc(TASKS[0].title)}</h2>
        <span style="margin-left:auto"></span>
        <button class="icon-btn" type="button" aria-label="关闭">✕</button>
      </header>
      ${detailBody()}
      ${detailFoot()}
    </aside>`
}

function attachmentPage() {
  const over = state === 'oversized'
  const empty = state === 'empty'
  const loading = state === 'loading'
  const error = state === 'error'
  const img = state === 'image-placeholder'

  const body = loading
    ? `<div class="split-body">${Array.from({ length: 5 }, () => '<div class="sk sk-line" style="margin-bottom:12px"></div>').join('')}</div>`
    : error
      ? `<div class="split-body">${banner('附件不存在或已删除（HTTP 404）', '在新标签打开原文')}</div>`
      : over
        ? `<div class="split-body"><div class="banner" data-tone="info">
            <span>正文较大：1.2 MB（超过 512 KB 自动渲染上限）</span><span class="spacer"></span>
            <button class="btn" type="button">仍然加载</button><button class="btn btn-ghost" type="button">在新标签打开原文</button>
          </div></div>`
        : empty
          ? `<div class="split-body">${emptyBlock('附件为空', '这个 material 存的是空正文。回到详情确认是否选错了附件。', '返回详情')}</div>`
          : `<div class="split-body"><div class="markdown">
              <h1>Web UI 重做 技术方案 v0.2</h1>
              <p>在 Mantine 8 之上建 <code>web/src/ui/</code> 封装层，重做 6 个页面的信息架构与样式层。</p>
              <h2>改动边界</h2>
              <ul><li>不改 <code>/v1</code> 契约、不动数据库</li><li>不升级 React 19</li><li>不引图片预览</li></ul>
              <h2>移动端三条</h2>
              <table><thead><tr><th>条目</th><th>口径</th></tr></thead><tbody>
                <tr><td>切列</td><td>横向 scroll-snap，滚到底最后一列完整可见</td></tr>
                <tr><td>读附件</td><td>应用内渲染正文，不跳新标签</td></tr>
                <tr><td>推进状态</td><td>底部操作条，触控 ≥44px</td></tr>
              </tbody></table>
              <pre><code>make web   # 构建 + 体积门禁
make docker-build</code></pre>
              ${img ? `<p>示意图：<span class="img-placeholder">图片：架构图（平台不存二进制附件，无法预览）</span></p>` : ''}
              <p>证据基准 <code>main@3cdc89e</code>。</p>
            </div></div>`

  return `
    <div class="scrim"></div>
    <aside class="drawer" role="dialog" aria-label="附件预览">
      <header class="drawer-head">
        <span class="kind">输出</span>
        <h2 class="drawer-title">${esc(ATTACHMENTS[0].title)}</h2>
        <span style="margin-left:auto"></span>
        <button class="icon-btn" type="button" aria-label="关闭预览">✕</button>
      </header>
      <div class="drawer-body" style="padding:0;flex-direction:row">
        <div class="split">
          <div class="split-list">
            ${ATTACHMENTS.map((a, i) => `<button class="att-row" type="button" ${i === 0 ? 'data-focus="true"' : ''}>
              <span class="kind">${a.role === 'output' ? '输出' : '输入'}</span>
              <span class="att-name">${esc(a.title)}</span>
            </button>`).join('')}
          </div>
          ${body}
        </div>
      </div>
    </aside>`
}

function inboxPage() {
  const loading = state === 'loading'
  const empty = state === 'empty'
  const error = state === 'error'
  const review = TASKS.filter((t) => t.status === 'in_review')
  const ready = TASKS.filter((t) => t.status === 'ready')

  const section = (title, hint, items, emptyText) => `<section class="section">
    <div class="section-head"><span class="section-title">${title}</span><span class="section-count">${items.length} 个 · ${hint}</span></div>
    ${loading
      ? `<div class="grid-cards">${skeletonCards(3)}</div>`
      : items.length
        ? `<div class="grid-cards">${items.map(taskCard).join('')}</div>`
        : `<p class="col-empty">${emptyText}</p>`}
  </section>`

  return `<section class="page">
    <header class="page-head">
      <div><h1 class="page-title">收件箱</h1><p class="page-sub">待审核优先处理；待领取由 worker 自动拉取</p></div>
      <div class="page-actions"><button class="btn btn-ghost" type="button">刷新</button></div>
    </header>
    ${error ? banner('收件箱加载失败：连接超时（GET /v1/inbox）') : ''}
    ${empty ? emptyBlock('收件箱已清空', '没有待审核、待领取或待派发的任务。去创建任务或等 worker 回报。', '打开看板')
      : `${section('待审核', '需要你拍板', review, '没有待审核任务。')}
         ${section('待领取', 'worker 按 kind 拉取', ready, '没有待领取任务。')}
         ${section('派单队列', '依赖已满足，等待领取', [], '队列是空的，worker 可以休息一下。')}`}
  </section>`
}

function workersPage() {
  const loading = state === 'loading'
  const empty = state === 'empty'
  const error = state === 'error'
  const stale = state === 'stale'

  const rows = WORKERS.map((w) => {
    const warn = stale && w.warn
    return `<tr ${warn ? 'data-warn="true"' : ''}>
      <td data-label="节点"><span class="mono">${w.id}</span></td>
      <td data-label="状态">${w.state === 'busy'
        ? '<span class="pill" data-tone="info"><i class="pill-dot"></i>忙碌</span>'
        : '<span class="pill" data-tone="success"><i class="pill-dot"></i>空闲</span>'}</td>
      <td data-label="当前任务"><span class="mono">${w.task}</span></td>
      <td data-label="心跳">${warn ? `<span class="pill" data-tone="danger"><i class="pill-dot"></i>${w.beat}</span>` : w.beat}</td>
      <td data-label="操作"><button class="btn" type="button">查看任务</button></td>
    </tr>`
  }).join('')

  return `<section class="page">
    <header class="page-head">
      <div><h1 class="page-title">执行节点</h1><p class="page-sub">2 个忙碌 · 1 个空闲${stale ? ' · 1 个心跳过期' : ''}</p></div>
      <div class="page-actions"><button class="btn btn-ghost" type="button">刷新</button></div>
    </header>
    ${error ? banner('执行节点加载失败：连接超时（GET /v1/workers）') : ''}
    ${loading ? `<div class="card">${skeletonCards(3)}</div>`
      : empty ? emptyBlock('还没有执行节点', 'worker 上线并心跳后会出现在这里。检查 worker 进程与 relay 连接。', '查看部署说明')
        : `<div class="card"><table class="table">
            <thead><tr><th>节点</th><th>状态</th><th>当前任务</th><th>心跳</th><th>操作</th></tr></thead>
            <tbody>${rows}</tbody></table></div>`}
  </section>`
}

function queuePage() {
  const loading = state === 'loading'
  const empty = state === 'empty'
  const error = state === 'error'
  const more = state === 'long-list'
  const ready = TASKS.filter((t) => t.status === 'ready')
  const list = more ? ready.concat(ready, ready, ready) : ready
  const groups = { 'echuan-1': list, 'echuan-2': more ? list.slice(0, 3) : [] }

  return `<section class="page">
    <header class="page-head">
      <div><h1 class="page-title">派单队列</h1><p class="page-sub">只展示 ready：依赖已满足、等待 worker 领取</p></div>
      <div class="page-actions"><span class="section-count">${list.length} 待派发</span><button class="btn btn-ghost" type="button">刷新</button></div>
    </header>
    ${error ? banner('队列加载失败：连接超时（GET /v1/tasks?status=ready）') : ''}
    ${loading ? `<div class="card">${skeletonCards(4)}</div>`
      : empty ? emptyBlock('队列是空的', '没有 ready 状态的任务。看板里把 pending 推进到 ready 后，任务会出现在这里。', '打开看板')
        : Object.entries(groups).map(([pid, items]) => `<section class="section">
            <div class="section-head"><span class="section-title mono">${pid}</span><span class="section-count">${items.length} 个任务</span></div>
            <div class="grid-cards">${items.map(taskCard).join('')}</div>
          </section>`).join('')}
  </section>`
}

/* ---------------- mount ---------------- */

const PAGES = {
  products: productsPage,
  projects: projectsPage,
  board: boardPage,
  'task-detail': detailPage,
  'attachment-preview': attachmentPage,
  inbox: inboxPage,
  workers: workersPage,
  queue: queuePage,
}

const overlayOnly = screen === 'attachment-preview' || screen === 'task-detail'
document.getElementById('root').innerHTML = shell((PAGES[screen] || boardPage)()) + (overlayOnly ? '' : '')

/* collapsed 状态的证据在列尾：把看板滚到最右，截图才看得到折叠后的 done/cancelled */
if (state === 'collapsed') {
  const board = document.querySelector('.board')
  if (board) board.scrollLeft = board.scrollWidth
}
