// features/home/HomeRoute —— `#/` 入口重定向（design/flows.md F1 第 1 步）
//
// 有「上次访问的项目」记录 → 该项目看板；无记录 → 产品页。
// 用 <Navigate replace> 而不是渲染两份内容：替换历史项，浏览器的后退不会在 `#/` 上打转。
import { Navigate } from 'react-router-dom'
import { boardPath } from './last-project'

export function HomeRoute() {
  return <Navigate to={boardPath()} replace />
}
