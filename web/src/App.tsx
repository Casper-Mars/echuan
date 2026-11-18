import { HashRouter, Navigate, Route, Routes } from 'react-router-dom'
import { Layout } from './components/Layout'
import { InboxPage } from './pages/InboxPage'
import { ProductsPage } from './pages/ProductsPage'
import { ProjectsPage } from './pages/ProjectsPage'
import { QueuePage } from './pages/QueuePage'
import { TasksPage } from './pages/TasksPage'
import { WorkersPage } from './pages/WorkersPage'

export function App() {
  return (
    <HashRouter>
      <Routes>
        <Route element={<Layout />}>
          <Route path="/inbox" element={<InboxPage />} />
          <Route path="/queue" element={<QueuePage />} />
          <Route path="/products" element={<ProductsPage />} />
          <Route path="/products/:productId/projects" element={<ProjectsPage />} />
          <Route path="/projects/:projectId/tasks" element={<TasksPage />} />
          <Route path="/workers" element={<WorkersPage />} />
        </Route>
        <Route path="*" element={<Navigate to="/products" replace />} />
      </Routes>
    </HashRouter>
  )
}
