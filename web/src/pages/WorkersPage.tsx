import { EmptyState } from '../components/States'

/**
 * Worker 页面占位：仅提供路由落点与外壳，真实列表/详情由任务-10 实现。
 */
export function WorkersPage() {
  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>Worker</h1>
          <p className="muted">查看执行节点与其任务负载。</p>
        </div>
      </header>

      <EmptyState title="Worker 视图建设中" hint="页面框架已就位，真实数据由后续任务接入。" />
    </section>
  )
}
