// features/home/BoardRedirect —— `#/projects/:pid`（无 tasks 段）→ 该项目看板
//
// 旧收藏 / 手工删段后的 URL 归一到看板；与 flows F1 第 3 步一致：
// 「无 taskId（旧收藏/历史）→ 回看板」。
import { Navigate, useParams } from 'react-router-dom'

export function BoardRedirect() {
  const { projectId = '' } = useParams()
  return <Navigate to={`/projects/${projectId}/tasks`} replace />
}
