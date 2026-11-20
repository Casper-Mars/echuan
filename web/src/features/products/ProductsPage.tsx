// features/products/ProductsPage —— 产品列表（`#/products`）
//
// 规格对照：
//   design/screens/products.md   页头（产品数 + 项目总数 + 筛选 + 新建产品）→ 表格（产品 · 仓库 · 项目数 · 操作）；
//                                四态 default / empty / loading / error；<768 卡片式行（字段名走 ui/DataTable 的 data-label）。
//   design/components.md         §ui/DataTable（表体换骨架行 / 移动端 data-label）、§ui/EmptyState、§ui/SkeletonCard、
//                                §ui/Alert（danger 可重试）、§ui/Button、§ui/SelectField。
//   docs/tech/web-ui-redo-v0.1.md §7   数据源不新增接口：GET /v1/products（project_count）+ GET /v1/projects（按需）。
//
// 分层：本文件只 import ui / api / hooks / lib，不直接 import Mantine 包（分层红线，验收用 grep 查）。
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
import { Alert, Button, DataTable, EmptyState, SelectField, SkeletonCard } from '../../ui'
import type { DataTableColumn } from '../../ui'
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
        <Button variant="default" size="sm" onClick={() => navigate(`/products/${product.product_id}/projects`)}>
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
            // 筛掉全部行 ≠ 没有产品：空态只在真的没有产品时出现，筛空由表内提示兜住
            empty="没有符合筛选条件的产品（按项目状态）"
          />
        </div>
      )}
    </section>
  )
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
