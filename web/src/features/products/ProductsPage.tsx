// features/products/ProductsPage —— 产品列表（`#/products`）
//
// 规格对照：
//   design/screens/products.md   页头（产品数 + 项目总数 + 筛选 + 新建产品）→ 表格（产品 · 仓库 · 项目数 · 操作）；
//                                四态 default / empty / loading / error；<768 卡片式行（字段名走 ui/DataTable 的 data-label）；
//                                「交互」行点击 → 只读详情抽屉（6 字段，0 请求，字段取当前行），行内「查看项目」只导航不开抽屉。
//   design/components.md         §ui/DataTable（表体换骨架行 / 移动端 data-label / onRowClick）、§ui/EmptyState、
//                                §ui/SkeletonCard、§ui/Alert（danger 可重试）、§ui/Button、§ui/SelectField、
//                                §ui/DetailSheet（`readonly` 变体 + 字段键值网格）、§「屏内私有详情抽屉」（共用键值网格 = features/shell/DetailFields）。
//   docs/tech/web-ui-redo-v0.1.md §7   数据源不新增接口：GET /v1/products（project_count）+ GET /v1/projects（按需）。
//   docs/tech/web-ui-102-interaction-fix-v0.1.md §7.2（D1）/ §7.3（D2）/ §7.8（一次点击一个结果）。
//
// 分层：本文件只 import ui / api / hooks / lib 与 features/shell（壳层共用件），不直接 import Mantine 包
// （分层红线，验收用 grep 查）。
// 几何与配色全部由 products.css 消费 theme 令牌，文件里不出现 hex / px 字面量（验收 grep）。
//
// 不做（design/screens/products.md「不做」）：产品编辑、删除——后端无对应动作，页面上也不出现入口。
// 页头的「新建产品」按 design/refs 展示，但**不发写请求**（本卡范围为列表重做，创建流程不在其中）。
import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../../api/client'
import type { Product, Project } from '../../api/types'
import { useAutoRefresh } from '../../hooks/useAutoRefresh'
import { REFRESH_INTERVALS } from '../../lib/refresh'
import { Alert, Button, DataTable, DetailSheet, EmptyState, SelectField, SkeletonCard } from '../../ui'
import type { DataTableColumn } from '../../ui'
import { DetailFields } from '../shell/DetailFields'
import type { DetailField } from '../shell/DetailFields'
import './products.css'

/** 稳定的空数组：避免每次渲染新建引用，也避免把「还没有数据」误判成 loading。 */
const EMPTY_PRODUCTS: Product[] = []
const EMPTY_PROJECTS: Project[] = []

/** 筛选口径：产品自身没有状态，筛的是「该产品下的项目状态」（design/refs 页头只有「全部状态」一项默认值）。 */
const STATUS_FILTERS = [
  { value: 'all', label: '全部状态' },
  { value: 'open', label: '进行中' },
  { value: 'closed', label: '已关闭' },
]

/** 骨架行数：design/screens/products.md loading 态「4 行 Skeleton」 */
const SKELETON_ROWS = 4

/**
 * 「新建产品」入口按 design/refs 展示，但本卡数据源只有三个 GET、不做写操作，
 * 所以按钮不带副作用（不发 POST、不弹表单）。创建流程不在本卡范围，见交付的「未覆盖项」。
 */
const CREATE_PRODUCT_PLACEHOLDER = () => {}

export function ProductsPage() {
  const navigate = useNavigate()

  // 主数据源：GET /v1/products。失败进 error 态（页头保留、表体换成错误条 + 重试）。
  const { data, error, refresh } = useAutoRefresh({
    intervalMs: REFRESH_INTERVALS.projects,
    load: () => api.listProducts(),
  })

  // 补充数据源：GET /v1/projects（卡片允许的按需调用），只为「项目」列的准确计数与状态筛选。
  // 它失败不影响页面可用：计数回退到 /v1/products 的 project_count，筛选退化为不过滤。
  const [projects, setProjects] = useState<Project[]>(EMPTY_PROJECTS)
  const [status, setStatus] = useState<string | null>('all')

  useEffect(() => {
    let alive = true
    api
      .listAllProjects()
      .then((result) => {
        if (alive) setProjects(result.items)
      })
      .catch(() => {
        if (alive) setProjects(EMPTY_PROJECTS)
      })
    return () => {
      alive = false
    }
  }, [])

  const items = data?.items ?? EMPTY_PRODUCTS
  const loading = data === undefined && error === ''

  // 详情抽屉的打开状态只存 id（不复制行对象）：每次渲染按 id 从**当前** items 反查，
  // 自动刷新 / 筛掉行 / 字段被改后，抽屉内容跟着当前行走，不会停在打开那一刻的快照上。
  // 行被删除或刷新后不存在 → 反查为 undefined → 抽屉自动关闭（不会显示幽灵数据）。
  const [detailId, setDetailId] = useState<string | null>(null)

  // 每个产品的项目数：有 projects 明细就用明细，否则回退接口给的 project_count（列表响应可能缺省）。
  const projectCounts = useMemo(() => {
    const map = new Map<string, number>()
    for (const project of projects) {
      map.set(project.product_id, (map.get(project.product_id) ?? 0) + 1)
    }
    return map
  }, [projects])

  const countOf = (product: Product) => projectCounts.get(product.product_id) ?? product.project_count ?? 0

  // 页头统计恒等于表格里的行：项目总数 = 各产品项目数之和（不与筛选耦合，筛选只影响表格）。
  const totalProjects = items.reduce((sum, product) => sum + countOf(product), 0)

  const rows = useMemo(() => {
    if (status === null || status === 'all' || projects.length === 0) return items
    const matched = new Set(
      projects.filter((project) => project.status === status).map((project) => project.product_id),
    )
    return items.filter((product) => matched.has(product.product_id))
    // countOf 只依赖 projects，projects 变化时 rows 必须跟着重算
  }, [items, projects, status])

  // 抽屉对应的当前行：从 items（不是 rows）反查，筛选变化不会把已打开的抽屉变成空壳。
  const detailProduct = detailId === null ? undefined : items.find((product) => product.product_id === detailId)

  const columns: Array<DataTableColumn<Product>> = [
    {
      key: 'name',
      header: '产品',
      // 首列：标题 + 副标题（product_id，mono）。design/refs 里副标题在主标题下方，字号更小。
      render: (product) => (
        <div className="pd-name">
          <span className="pd-name__title">{product.name}</span>
          <span className="pd-name__id">{product.product_id}</span>
        </div>
      ),
    },
    {
      key: 'git_url',
      header: '仓库',
      render: (product) => (
        <span className="pd-mono" title={product.git_url}>
          {product.git_url}
        </span>
      ),
    },
    {
      key: 'projects',
      header: '项目',
      render: (product) => <span className="pd-count">{countOf(product)}</span>,
    },
    {
      key: 'actions',
      header: '操作',
      // 不设 actions: true —— design/refs 的移动端卡片里「操作」也带字段名（data-label），
      // 设了 actions 会被 ui/DataTable 去掉标签。
      render: (product) => (
        // 「一次点击一个结果」（docs/tech §7.8-1）：行内按钮必须阻断冒泡，否则 tr 的 onRowClick
        // 会同时把抽屉打开——点「查看项目」只导航、不开抽屉。
        <Button
          variant="default"
          size="sm"
          onClick={(event) => {
            event.stopPropagation()
            navigate(`/products/${product.product_id}/projects`)
          }}
        >
          查看项目
        </Button>
      ),
    },
  ]

  return (
    <section className="pd-page">
      <header className="pd-head">
        <div className="pd-head__main">
          <h1 className="pd-title">产品</h1>
          <p className="pd-sub">{subtitleOf(loading, error !== '', items.length, totalProjects)}</p>
        </div>
        {/* 加载中 / 错误态不出现筛选与新建：页头只留标题与说明（design/refs 三种状态都如此） */}
        {!loading && error === '' && items.length > 0 ? (
          <div className="pd-actions">
            <SelectField
              className="pd-filter"
              label="按项目状态筛选"
              data={STATUS_FILTERS}
              value={status}
              onChange={setStatus}
            />
            <Button variant="primary">新建产品</Button>
          </div>
        ) : null}
      </header>

      {error !== '' ? (
        <Alert tone="danger" actionLabel="重试" onAction={() => refresh()}>
          {error}（GET /v1/products）
        </Alert>
      ) : loading ? (
        <div className="pd-card pd-card--skeleton">
          {Array.from({ length: SKELETON_ROWS }, (_, index) => (
            <SkeletonCard key={index} lines={1} />
          ))}
        </div>
      ) : items.length === 0 ? (
        <EmptyState
          title="还没有产品"
          description="产品是项目与任务的容器。创建第一个产品后，看板、收件箱与执行节点都会有数据。"
          actionLabel="新建产品"
          onAction={CREATE_PRODUCT_PLACEHOLDER}
        />
      ) : (
        <div className="pd-card">
          <DataTable
            ariaLabel="产品列表"
            columns={columns}
            rows={rows}
            getRowKey={(product) => product.product_id}
            // 行点击 → 只读详情抽屉（ui/DataTable 已内置 tabIndex + Enter/Space，键盘同样可达）。
            // 只记 id：抽屉内容每次渲染按 id 从当前 items 派生（刷新后跟随当前行）。
            onRowClick={(product) => setDetailId(product.product_id)}
            // 筛掉全部行 ≠ 没有产品：空态只在真的没有产品时出现，筛空由表内提示兜住
            empty="没有符合筛选条件的产品（按项目状态）"
          />
        </div>
      )}

      {/* 只读详情抽屉（design/screens/products.md「交互」）：复用 ui/DetailSheet 的 readonly 变体
          （design/components.md §ui/DetailSheet「变体」），0 请求、字段取当前行。
          抽屉内不放任何动作（footer 不传）——动作只留在列表行，避免同一动作两处可见（docs/tech §7.2）。 */}
      <DetailSheet
        variant="readonly"
        opened={detailProduct !== undefined}
        title={detailProduct?.name ?? ''}
        onClose={() => setDetailId(null)}
      >
        {detailProduct ? <DetailFields fields={fieldsOfProduct(detailProduct, countOf(detailProduct))} /> : null}
      </DetailSheet>
    </section>
  )
}

/**
 * 抽屉字段（design/screens/products.md「交互」6 项 + docs/tech §7.2 字段表）：
 * 产品编号 / 名称 / 仓库 / 描述 / 证书 / 项目数；空值文案按契约逐项给（—/未设置/未填写）。
 * 字段一律从传入行派生，不 copy 行对象、不发请求。
 */
function fieldsOfProduct(product: Product, projectCount: number): DetailField[] {
  return [
    { label: '产品编号', value: product.product_id, mono: true },
    { label: '名称', value: textOr(product.name, '—') },
    { label: '仓库', value: textOr(product.git_url, '未设置'), mono: true },
    { label: '描述', value: textOr(product.description, '未填写'), multiline: true },
    { label: '证书', value: textOr(product.license, '未设置') },
    { label: '项目数', value: String(projectCount) },
  ]
}

/** 空值判据：undefined / null / 全空白都算「没有值」，由调用方给出本地化文案。 */
function textOr(value: string | null | undefined, fallback: string): string {
  return value !== null && value !== undefined && value.trim() !== '' ? value : fallback
}

/**
 * 页头说明文案。
 * loading / error 时不报数（数据还不可信），empty 时如实说明「还没有产品」。
 */
function subtitleOf(loading: boolean, failed: boolean, productCount: number, projectCount: number): string {
  if (loading) return '加载中…'
  if (failed) return '数据加载失败'
  if (productCount === 0) return '还没有产品'
  return `${productCount} 个产品 · 共 ${projectCount} 个项目`
}
