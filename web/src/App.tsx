import { HashRouter, Navigate, Route, Routes } from 'react-router-dom'
import { Layout } from './components/Layout'
import { InboxPage } from './pages/InboxPage'
import { ProductsPage } from './pages/ProductsPage'
import { QueuePage } from './pages/QueuePage'
import { ProjectsPage } from './pages/ProjectsPage'
import { WorkItemsPage } from './pages/WorkItemsPage'
import { WorkersPage } from './pages/WorkersPage'

/**
 * hash 路由（可直接分享/刷新）：
 *   #/inbox
 *   #/queue
 *   #/products
 *   #/products/:productId/projects
 *   #/products/:productId/projects/:projectId/work-items
 *   #/workers
 * 根路径与未知 hash 一律重定向到产品列表。
 */
export function App() {
  return (
    <HashRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/inbox" element={<InboxPage />} />
          <Route path="/queue" element={<QueuePage />} />
          <Route path="/products" element={<ProductsPage />} />
          <Route path="/products/:productId/projects" element={<ProjectsPage />} />
          <Route
            path="/products/:productId/projects/:projectId/work-items"
            element={<WorkItemsPage />}
          />
          <Route path="/workers" element={<WorkersPage />} />
        </Route>
        <Route path="*" element={<Navigate to="/products" replace />} />
      </Routes>
    </HashRouter>
  )
}
