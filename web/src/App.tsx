import { lazy, Suspense } from 'react'
import { HashRouter, Navigate, Route, Routes, useParams } from 'react-router-dom'
import { AppFrame } from './features/shell/AppFrame'
import { RouteLoading } from './features/shell/RouteLoading'
import { BoardRedirect } from './features/home/BoardRedirect'
import { HomeRoute } from './features/home/HomeRoute'
import { BoardPage } from './features/board/BoardPage'
import { InboxPage } from './features/inbox/InboxPage'
import { ProductsPage } from './features/products/ProductsPage'
import { ProjectsPage } from './features/projects/ProjectsPage'
import { QueuePage } from './features/queue/QueuePage'
import { WorkersPage } from './features/workers/WorkersPage'

// 详情按需加载：既让「详情首次打开」有个真实的 loading 态（抽屉内骨架），
// 也让首屏 JS 不背详情页的代码。
const TaskDetailRoute = lazy(() => import('./features/task-detail/TaskDetailRoute'))

/**
 * `/projects/:projectId/tasks` 与 `/projects/:projectId/tasks/:taskId` 的**同一个挂载点**（T6）。
 *
 * 为什么不是两条并列路由：design/screens/task-detail.md「断点」要求桌面 720 抽屉
 * **保留看板上下文**（design/refs 里抽屉左侧就是看板列与卡片）。详情升为独立路由后（T4），
 * 两条并列路由会让看板在进入详情时被卸载 → 抽屉左侧空白。
 *
 * 做法：`:taskId` 用可选段（react-router 6.5+ 支持），看板**常驻**，详情以抽屉叠加其上：
 *   * 深链直接打开 `#/…/tasks/:tid`：看板照常挂载（看板自身 loading 允许）；
 *   * 关闭详情（× / Esc / 取消）→ `#/…/tasks`：只卸载抽屉子层，看板不重挂、不重拉、不白屏；
 *   * 评审冲突后重拉详情只动抽屉内部状态，不触发看板整体重挂。
 * 只动「详情路由与看板路由的嵌套关系」，看板本身（features/board/**）零改动。
 */
function BoardWithDetail() {
  const { taskId } = useParams()
  return (
    <>
      <BoardPage />
      {taskId ? (
        // 内层 Suspense 只兜详情 chunk：chunk 未到时看板保持可见，不被壳层整页骨架顶掉
        <Suspense fallback={<RouteLoading />}>
          <TaskDetailRoute />
        </Suspense>
      ) : null}
    </>
  )
}

/**
 * 路由表（HashRouter，可直接刷新 / 分享）：
 *
 *   全部由 `features/**` 承载：
 *   `#/`                                        → 上次访问的项目看板；无记录 → 产品页（flows F1，T4）
 *   `#/inbox`                                   → 收件箱（features/inbox，T8）
 *   `#/queue`                                   → 派单队列（features/queue，T8）
 *   `#/products`                                → 产品列表（features/products，T9）
 *   `#/products/:productId/projects`            → 该产品下的项目列表（features/projects，T9）
 *   `#/projects/:projectId`                     → 该项目看板（旧收藏缺段时归一，T4）
 *   `#/projects/:projectId/tasks/:taskId?`      → 看板（常驻）+ 详情抽屉（可选段，T6），关闭回看板
 *   `#/workers`                                 → 执行节点（features/workers，T9）
 *   `*`                                         → 重定向到 `#/products`
 *
 * 详情不是独立同级路由，而是看板挂载点里的可选段：抽屉背后始终保留看板上下文（T6）。
 * 全部路由都挂在 `features/shell/AppFrame` 下：壳层（三形态导航 + 顶栏 + 页级错误边界）只有一份。
 * 旧壳 `components/Layout.tsx` + `components/Sidebar.tsx` 因此失去引用，已删除（见 T4 报告）；
 * 旧 `pages/{Inbox,Queue,Products,Projects,Workers}Page.tsx` 也失去引用（T8 / T9 收口）。
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
          <Route path="/projects/:projectId/tasks/:taskId?" element={<BoardWithDetail />} />
          <Route path="/workers" element={<WorkersPage />} />
        </Route>
        <Route path="*" element={<Navigate to="/products" replace />} />
      </Routes>
    </HashRouter>
  )
}
