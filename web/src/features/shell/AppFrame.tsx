// features/shell/AppFrame —— 应用外壳（三形态导航 + 顶栏 + 页级错误边界）
//
// 规格对照：
//   design/screens/board.md「断点」：≥1280 桌面（左栏 240 有文字）/ 768–1279 平板（左栏收 64 图标栏）/
//     <768 移动（无侧栏，底部 TabBar）——本文件用 data-layout 表达，几何在 shell.css。
//   design/components.md §ui/SidebarNav（桌面 240 / ≤1024 收 64 + 文字进 Tooltip）、
//     §ui/TabBar（移动：图标 + 文字必须都有、safe-area、aria-current）。
//   design/flows.md F1：`#/` → 上次项目看板；一级导航不含看板（入口 = 产品 → 项目列表 → 打开看板）。
//
// 分层：本文件只 import ui / theme / react-router-dom / react（分层红线：壳层不得直接 import Mantine 包）。
// ui/SidebarNav 内部是 AppShell.Navbar，需要 Mantine 的 AppShell 安全上下文——那一层接线在
// src/main.tsx（唯一允许 import Mantine 的应用入口），壳层只负责传数据与几何。
import { Suspense, useEffect, useState } from 'react'
import { Link, Outlet, useLocation, useNavigate } from 'react-router-dom'
import { Button, DetailSheet, TabBar, SidebarNav } from '../../ui'
import { ErrorBoundary } from './ErrorBoundary'
import { RouteLoading } from './RouteLoading'
import { SIDEBAR_NAV, TAB_NAV, MORE_NAV, RefreshIcon, activeKeyOf } from './nav'
import { useShellLayout } from './use-layout'
import { useCrumbNames } from './use-crumb-names'
import type { CrumbNames } from './use-crumb-names'
import { boardPath, rememberLastProject } from '../home/last-project'
import './shell.css'

/** 版本备注（design/refs 侧栏底部「v0.1 · 单二进制 · 内网」）。单一来源：这里改，全形态跟着改。 */
const BUILD_NOTE = 'v0.1 · 单二进制 · 内网'

interface Crumb {
  label: string
  to?: string
  /** 标签是「名字没取到时的原始 ID」→ 加 mono 字体并用 title 兜底可读性（D6 / P4-3、P4-4） */
  fallbackId?: string
}

const SECTION_LABEL: Record<string, string> = {
  inbox: '收件箱',
  queue: '派单队列',
  workers: '执行节点',
  products: '产品',
}

/**
 * 顶栏面包屑（design/refs：桌面/平板/移动顶栏左侧都是「产品 / <产品> / <项目>」）。
 *
 * 两段信息源（方案 §7.7 D6）：
 *   - 结构（有没有第四段 taskId、哪段可点）只从 URL 段推导，**不依赖请求**；
 *   - 名字（产品名 / 项目名）与 product_id 由 useCrumbNames 反查，取不到就退回 ID：
 *     项目失败 → `产品 / <project_id>`（与今天一致，中间段不出现）；
 *     产品失败 → `产品 / <productId>`（该段仍链到项目列表）/ `<项目名>`。
 * 渲染不因取数失败而变：壳层把它当「可选装饰」，失败时保持 ID 形态（B2 想要的稳态）。
 */
function crumbsOf(pathname: string, names: CrumbNames): Crumb[] {
  const parts = pathname.split('/').filter(Boolean)
  if (parts.length === 0) return [{ label: '产品' }]

  if (parts[0] === 'products') {
    const crumbs: Crumb[] = [{ label: '产品', to: parts.length > 1 ? '/products' : undefined }]
    if (parts[1]) crumbs.push({ label: parts[1] })
    return crumbs
  }

  if (parts[0] === 'projects') {
    const projectId = parts[1]
    const crumbs: Crumb[] = [{ label: '产品', to: '/products' }]
    if (!projectId) return crumbs

    const taskId = parts[2] === 'tasks' && parts[3] ? parts[3] : ''

    if (names.productId === '') {
      // 项目反查失败（或首帧未回）：今天的形态，中间没有「项目列表」段
      crumbs.push({ label: projectId, fallbackId: projectId })
    } else {
      const listTo = `/products/${names.productId}/projects`
      // 中间段 = 产品（可点回项目列表）；名字没取到就显示 productId，链接照旧可用（P4-4）
      crumbs.push(
        names.productName === ''
          ? { label: names.productId, to: listTo, fallbackId: names.productId }
          : { label: names.productName, to: listTo },
      )
      // 项目段：没有 taskId 时它是末段（文本、不可点）；带 taskId 时它变成链接（P4-2）
      crumbs.push(
        names.projectName === ''
          ? { label: projectId, to: taskId ? listTo : undefined, fallbackId: projectId }
          : { label: names.projectName, to: taskId ? listTo : undefined },
      )
    }

    if (taskId) crumbs.push({ label: taskId })
    return crumbs
  }

  return [{ label: SECTION_LABEL[parts[0]] ?? '产品' }]
}

/** `/projects/:pid/...` → pid（其余返回 null） */
function projectIdOf(pathname: string): string | null {
  const match = /^\/projects\/([^/]+)(?:\/|$)/.exec(pathname)
  return match ? match[1] : null
}

export function AppFrame() {
  const layout = useShellLayout()
  const navigate = useNavigate()
  const { pathname, key: locationKey, search } = useLocation()
  const [moreOpen, setMoreOpen] = useState(false)

  // flows F1 第 1 步：进入某个项目看板就记住它，`#/` 下次直达（看板不是一级入口，见 nav.tsx 头注释）。
  const projectId = projectIdOf(pathname)
  useEffect(() => {
    if (projectId) rememberLastProject(projectId)
  }, [projectId])

  // 名字反查只在 `/projects/*` 启用；其它路由 projectId 为 null，hook 内直接返回空态、不发请求。
  const crumbNames = useCrumbNames(projectId)

  const activeKey = activeKeyOf(pathname)
  const collapsed = layout === 'tablet'
  const crumbs = crumbsOf(pathname, crumbNames)

  const go = (key: string) => {
    if (key === 'inbox') navigate('/inbox')
    else if (key === 'products') navigate('/products')
    else if (key === 'workers') navigate('/workers')
    else if (key === 'queue') navigate('/queue')
  }

  const handleTabChange = (key: string) => {
    if (key === 'more') {
      setMoreOpen(true)
      return
    }
    go(key)
  }

  // 顶栏手动刷新：先整页重载兜底（T4 还没有「按路由重新取数」的统一入口，
  // T5+ 页面接上刷新信号后改成局部刷新；见报告「未覆盖项」）。
  const handleRefresh = () => {
    window.location.reload()
  }

  return (
    <div className="fx-shell" data-ui="shell" data-layout={layout} data-shell-state="default">
      {layout === 'mobile' ? null : (
        <SidebarNav
          ariaLabel="主导航"
          items={SIDEBAR_NAV}
          activeKey={activeKey}
          onSelect={go}
          collapsed={collapsed}
          header={
            collapsed ? null : (
              <span className="fx-brand">
                <span className="fx-brand__mark" aria-hidden="true">
                  驿
                </span>
                <span className="fx-brand__text">驿传</span>
              </span>
            )
          }
          footer={
            <span className="fx-sidebar-note" data-collapsed={collapsed || undefined}>
              {BUILD_NOTE}
            </span>
          }
        />
      )}

      <div className="fx-shell__body">
        <header className="fx-topbar" data-ui="shell-topbar">
          <nav className="fx-crumbs" aria-label="面包屑">
            {crumbs.map((crumb, index) => {
              const last = index === crumbs.length - 1
              return (
                <span className="fx-crumbs__item" key={`${crumb.label}-${index}`}>
                  {index > 0 ? (
                    <span className="fx-crumbs__sep" aria-hidden="true">
                      /
                    </span>
                  ) : null}
                  {crumb.to && !last ? (
                    <Link
                      className={crumb.fallbackId === undefined ? 'fx-crumbs__link' : 'fx-crumbs__link fx-crumbs__id'}
                      title={crumb.fallbackId}
                      to={crumb.to}
                    >
                      {crumb.label}
                    </Link>
                  ) : (
                    // 文本段：末段加粗、非末段用 _label —— 两者都自己承担省略号（窄屏超长标识单行省略）。
                    // `--id` 只在「名字没取到、退回原始 ID」时加（mono + title），名字齐全时不加。
                    <span
                      className={`${last ? 'fx-crumbs__current' : 'fx-crumbs__label'}${
                        crumb.fallbackId === undefined ? '' : ' fx-crumbs__id'
                      }`}
                      title={crumb.fallbackId}
                    >
                      {crumb.label}
                    </span>
                  )}
                </span>
              )
            })}
          </nav>
          <div className="fx-topbar__actions">
            {/* 顶栏只放「刷新」这类次级动作；主操作属于页面头（AC5：全屏主操作只出现一次） */}
            <Button variant="ghost" size="sm" leftSection={<RefreshIcon size={16} />} onClick={handleRefresh}>
              刷新
            </Button>
          </div>
        </header>

        <main className="fx-main" id="main">
          {/* resetKey 用 location.key：任何一次导航（含导航到当前路径）都会换 key，
              错误态随之重置，用户不会被一条错误条锁死在页面上（D1） */}
          <ErrorBoundary
            resetKey={`${locationKey}${pathname}${search}`}
            onExit={() => navigate(boardPath(projectId))}
          >
            {/* 路由级 Suspense：详情路由是 lazy chunk，chunk 未到时渲染骨架（壳层 loading 态） */}
            <Suspense fallback={<RouteLoading />}>
              <Outlet />
            </Suspense>
          </ErrorBoundary>
        </main>

        {layout === 'mobile' ? (
          <TabBar items={TAB_NAV} activeKey={activeKey} onChange={handleTabChange} ariaLabel="主导航" />
        ) : null}
      </div>

      {/* 移动「更多」二级层：把侧栏放得下、TabBar 放不下的入口收在这里（design/components.md §ui/TabBar） */}
      <DetailSheet
        opened={moreOpen}
        title="更多"
        onClose={() => setMoreOpen(false)}
        footer={
          <Button variant="default" size="md" fullWidth onClick={() => setMoreOpen(false)}>
            关闭
          </Button>
        }
      >
        <div className="fx-more">
          {MORE_NAV.map((item) => (
            <Button
              key={item.key}
              variant="default"
              size="md"
              fullWidth
              leftSection={item.icon}
              onClick={() => {
                setMoreOpen(false)
                go(item.key)
              }}
            >
              {item.label}
            </Button>
          ))}
          <Button
            variant="ghost"
            size="md"
            fullWidth
            onClick={() => {
              setMoreOpen(false)
              go('queue')
            }}
          >
            派单队列
          </Button>
        </div>
      </DetailSheet>
    </div>
  )
}
