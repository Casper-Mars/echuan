// features/projects/ProjectsPage —— 某产品下的项目列表（`#/products/:productId/projects`）
//
// 规格对照：
//   design/screens/projects.md   页头（产品名 + 项目数 + 新建项目）→ 表格（序号 · 项目 · 状态 · 更新时间 · 操作）；
//                                四态 default / empty / loading / error；<768 卡片式行（data-label 字段名）；
//                                closed 项目操作保留（只读可看）。
//   design/components.md         §ui/DataTable、§ui/StatusPill（圆点 + 文案，不只靠颜色）、§ui/EmptyState、
//                                §ui/SkeletonCard、§ui/Alert、§ui/Button。
//   docs/tech/web-ui-redo-v0.1.md §7   数据源：GET /v1/projects（按需带 product_id / status），产品名走 GET /v1/products?id=。
//
// 分层：只 import ui / api / hooks / lib，不直接 import Mantine（分层红线）；样式见 projects.css（只消费令牌）。
//
// 不做（design/screens/projects.md「不做」）：关闭 / 重开项目（本期不接 POST /v1/projects/close|reopen），
// 页头「新建项目」按 design/refs 展示但**不发写请求**。
import { useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { api } from '../../api/client'
import type { Project } from '../../api/types'
import { useAutoRefresh } from '../../hooks/useAutoRefresh'
import { REFRESH_INTERVALS } from '../../lib/refresh'
import { Alert, Button, DataTable, EmptyState, SkeletonCard, StatusPill } from '../../ui'
import type { DataTableColumn } from '../../ui'
import './projects.css'

/** 稳定的空数组：避免每渲染新建引用，也避免把空态误判成 loading。 */
const EMPTY_PROJECTS: Project[] = []

/** 骨架卡片数：design/screens/projects.md loading 态「3 个 SkeletonCard」 */
const SKELETON_CARDS = 3

/** 更新时间列：design/refs 是「月-日 时:分」（MM-DD HH:mm），不显示年份与秒。 */
const pad = (value: number) => String(value).padStart(2, '0')

/**
 * 「新建项目」入口按 design/refs 展示，但本卡数据源只有三个 GET、不做写操作，
 * 所以按钮不带副作用（不发 POST、不弹表单）。创建流程不在本卡范围，见交付的「未覆盖项」。
 */
const CREATE_PROJECT_PLACEHOLDER = () => {}

export function ProjectsPage() {
  const { productId = '' } = useParams()
  const navigate = useNavigate()

  const { data, error, refresh } = useAutoRefresh({
    intervalMs: REFRESH_INTERVALS.projects,
    scope: productId,
    load: () => api.listProjects(productId),
  })

  // 产品名只服务于页头文案（design/refs：「驿传 Echuan · 3 个项目」）。
  // 它不是本页数据源，取不到就回退显示 product_id，不把整页打成错误态。
  const [productName, setProductName] = useState('')
  useEffect(() => {
    let alive = true
    setProductName('')
    api
      .getProduct(productId)
      .then((product) => {
        if (alive) setProductName(product.name)
      })
      .catch(() => {
        if (alive) setProductName('')
      })
    return () => {
      alive = false
    }
  }, [productId])

  const items = data?.items ?? EMPTY_PROJECTS
  const loading = data === undefined && error === ''
  const owner = productName === '' ? productId : productName

  const columns: Array<DataTableColumn<Project>> = [
    {
      key: 'seq_no',
      header: '序号',
      // design/refs 的序号是弱化的「#1」：ui/DataTable 会给首列加粗，这里按 ref 压回去
      render: (project) => <span className="pj-seq">#{project.seq_no}</span>,
    },
    {
      key: 'name',
      header: '项目',
      render: (project) => (
        <div className="pj-name">
          <span className="pj-name__title">{project.name}</span>
          <span className="pj-name__id">{project.project_id}</span>
        </div>
      ),
    },
    {
      key: 'status',
      header: '状态',
      // 状态是数据不是控件：圆点 + 中文文案（design/components.md §ui/StatusPill 反例=只用颜色）
      render: (project) =>
        project.status === 'open' ? (
          <StatusPill status="in_progress" label="进行中" />
        ) : (
          <StatusPill status="cancelled" label="已关闭" />
        ),
    },
    {
      key: 'update_time',
      header: '更新时间',
      render: (project) => <span className="pj-time">{formatUpdated(project.update_time)}</span>,
    },
    {
      key: 'actions',
      header: '操作',
      // 不设 actions: true —— design/refs 移动端卡片里「操作」也带字段名（data-label）
      render: (project) => (
        <Button variant="default" size="sm" onClick={() => navigate(`/projects/${project.project_id}/tasks`)}>
          打开看板
        </Button>
      ),
    },
  ]

  return (
    <section className="pj-page">
      <header className="pj-head">
        <div className="pj-head__main">
          <h1 className="pj-title">项目</h1>
          <p className="pj-sub">{subtitleOf(loading, error !== '', owner, items.length)}</p>
        </div>
        {/* 加载中 / 错误态页头不出现新建（design/refs 三种状态一致） */}
        {!loading && error === '' && items.length > 0 ? (
          <div className="pj-actions">
            <Button variant="primary">新建项目</Button>
          </div>
        ) : null}
      </header>

      {error !== '' ? (
        <Alert tone="danger" actionLabel="重试" onAction={() => refresh()}>
          {error}（GET /v1/projects）
        </Alert>
      ) : loading ? (
        <div className="pj-card pj-card--skeleton">
          {Array.from({ length: SKELETON_CARDS }, (_, index) => (
            <SkeletonCard key={index} lines={2} />
          ))}
        </div>
      ) : items.length === 0 ? (
        <EmptyState
          title="这个产品还没有项目"
          description="项目是任务的容器。创建项目后，看板会出现在项目详情里。"
          actionLabel="新建项目"
          onAction={CREATE_PROJECT_PLACEHOLDER}
        />
      ) : (
        <div className="pj-card">
          <DataTable
            ariaLabel="项目列表"
            columns={columns}
            rows={items}
            getRowKey={(project) => project.project_id}
          />
        </div>
      )}
    </section>
  )
}

/** 页头说明：default =「产品名 · N 个项目」；empty = 只有产品名；loading / error 不报数。 */
function subtitleOf(loading: boolean, failed: boolean, owner: string, count: number): string {
  if (loading) return `${owner} · 加载中…`
  if (failed) return owner
  if (count === 0) return owner
  return `${owner} · ${count} 个项目`
}

/** ISO 时间 → 「MM-DD HH:mm」；脏值原样返回，不让页头/表格因一条坏数据崩掉。 */
function formatUpdated(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}
