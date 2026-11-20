import { lazy } from 'react'
import { HashRouter, Navigate, Route, Routes } from 'react-router-dom'
import { AppFrame } from './features/shell/AppFrame'
import { BoardRedirect } from './features/home/BoardRedirect'
import { HomeRoute } from './features/home/HomeRoute'
import { BoardPage } from './features/board/BoardPage'
import { InboxPage } from './features/inbox/InboxPage'
import { ProductsPage } from './pages/ProductsPage'
import { ProjectsPage } from './pages/ProjectsPage'
import { QueuePage } from './features/queue/QueuePage'
import { WorkersPage } from './pages/WorkersPage'

// 详情是独立路由，按需加载：既让「详情首次打开」有个真实的 loading 态（壳层骨架），
// 也让首屏 JS 不背详情页的代码。T6 会在这里挂真实的元信息 / 附件 / 评审。
const TaskDetailRoute = lazy(() => import('./features/task-detail/TaskDetailRoute'))

/**
 * 路由表（HashRouter，可直接刷新 / 分享）：
 *
 *   新增（T4）：
 *   `#/`                                        → 上次访问的项目看板；无记录 → 产品页（flows F1）
 *   `#/projects/:projectId`                     → 该项目看板（旧收藏缺段时归一）
 *   `#/projects/:projectId/tasks/:taskId`       → 详情独立路由，可直接打开、关闭回看板
 *   保留（旧书签）：
 *   `#/inbox` `#/queue` `#/products`
 *   `#/products/:productId/projects` `#/projects/:projectId/tasks` `#/workers`
 *
 * 全部路由都挂在 `features/shell/AppFrame` 下：壳层（三形态导航 + 顶栏 + 页级错误边界）只有一份。
 * 旧壳 `components/Layout.tsx` + `components/Sidebar.tsx` 因此失去引用，已删除（见 T4 报告）。
 */
export function App() {
  return (
    <HashRouter>
      <Routes>
        <Route element={<AppFrame />}>
          <Route index element={<HomeRoute />} />
          <Route path="/inbox" element={<InboxPage />} />
          <Route path="/queue" element={<QueuePage />} />
          <Route path="/products" element={<ProductsPage />} />
          <Route path="/products/:productId/projects" element={<ProjectsPage />} />
          <Route path="/projects/:projectId" element={<BoardRedirect />} />
          <Route path="/projects/:projectId/tasks" element={<BoardPage />} />
          <Route path="/projects/:projectId/tasks/:taskId" element={<TaskDetailRoute />} />
          <Route path="/workers" element={<WorkersPage />} />
        </Route>
        <Route path="*" element={<Navigate to="/products" replace />} />
      </Routes>
    </HashRouter>
  )
}
