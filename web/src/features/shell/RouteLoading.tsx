// features/shell/RouteLoading —— 路由级加载骨架（壳层 loading 态）
//
// 用在哪：AppFrame 的 <Suspense fallback>，详情路由是 lazy 加载的独立 chunk，
// chunk 到位前后各一帧 loading。规格：design/components.md §ui/SkeletonCard
// （外层 aria-busy，占位块不朗读）+ board.md 状态矩阵 loading 行（结构不塌）。
import { SkeletonCard } from '../../ui'

export function RouteLoading() {
  return (
    <div className="fx-route-loading" data-ui="route-loading" aria-busy="true" aria-live="polite">
      <p className="fx-route-loading__text">正在加载…</p>
      <SkeletonCard lines={3} />
      <SkeletonCard lines={2} />
    </div>
  )
}
