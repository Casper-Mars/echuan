import { HashRouter, Navigate, Route, Routes } from 'react-router-dom'
import { Layout } from './components/Layout'
import { ProductsPage } from './pages/ProductsPage'
import { ProjectsPage } from './pages/ProjectsPage'
import { TasksPage } from './pages/TasksPage'
import { WorkersPage } from './pages/WorkersPage'

/**
 * 三级 hash 路由（可直接分享/刷新）：
 *   #/products
 *   #/products/:productId/projects
 *   #/products/:productId/projects/:projectId/tasks
 *   #/workers
 * 根路径与未知 hash 一律重定向到产品列表。
 */
export function App() {
  return (
    <HashRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/products" element={<ProductsPage />} />
          <Route path="/products/:productId/projects" element={<ProjectsPage />} />
          <Route
            path="/products/:productId/projects/:projectId/tasks"
            element={<TasksPage />}
          />
          <Route path="/workers" element={<WorkersPage />} />
        </Route>
        <Route path="*" element={<Navigate to="/products" replace />} />
      </Routes>
    </HashRouter>
  )
}
