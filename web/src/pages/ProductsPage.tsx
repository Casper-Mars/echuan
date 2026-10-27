import { useNavigate } from 'react-router-dom'
import { listProducts } from '../api/client'
import { itemsOf } from '../api/types'
import { useDrawer } from '../components/DrawerContext'
import {
  DetailButton,
  DrawerPlaceholder,
  EmptyState,
  ErrorNotice,
  LoadingState,
  RefreshBar,
} from '../components/States'
import { usePolling } from '../hooks/usePolling'
import { isInteractiveTarget } from '../lib/interaction'

/** 产品列表：卡片展示编号、名称、git 地址、证书等信息，点击进入项目列表。 */
export function ProductsPage() {
  const navigate = useNavigate()
  const openDrawer = useDrawer()
  const { data, error, loading, refreshing, lastUpdated, refresh } = usePolling(listProducts, [])
  const products = itemsOf(data)

  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>产品</h1>
          <p className="muted">选择一个产品，查看其需求项目与任务看板。</p>
        </div>
        <RefreshBar
          lastUpdated={lastUpdated}
          refreshing={refreshing}
          error={error}
          onRefresh={refresh}
        />
      </header>

      <ErrorNotice error={error} onRetry={refresh} />

      {!data && loading && <LoadingState label="正在加载产品…" />}
      {!data && !loading && error && (
        <EmptyState title="产品加载失败" hint="页面会自动重试；也可以点击上方“重试”。" />
      )}
      {data && products.length === 0 && (
        <EmptyState title="还没有产品" hint="创建产品后，这里会显示产品卡片。" />
      )}

      {products.length > 0 && (
        <div className="grid product-grid">
          {products.map((product) => (
            <article
              key={product.product_id}
              className="card product-card"
              onClick={(event) => {
                if (isInteractiveTarget(event.target)) return
                navigate(`/products/${encodeURIComponent(product.product_id)}/projects`)
              }}
            >
              <div className="card-head">
                <span className="code">{product.product_id}</span>
                <span className="chip">{product.license || '未设置证书'}</span>
              </div>
              <h2 className="card-title">{product.name || product.product_id}</h2>
              <p className="mono muted ellipsis" title={product.git_url}>
                {product.git_url || '未设置 git 地址'}
              </p>
              <p className="muted card-desc">{product.description || '暂无描述'}</p>
              <footer className="card-foot">
                <span>{product.project_count} 个项目</span>
                <div className="card-actions">
                  <DetailButton
                    onClick={() =>
                      openDrawer({
                        title: `产品 ${product.product_id}`,
                        body: (
                          <DrawerPlaceholder
                            kind="产品"
                            id={product.product_id}
                            status={product.name}
                          />
                        ),
                      })
                    }
                  />
                  <span className="link">查看项目 →</span>
                </div>
              </footer>
            </article>
          ))}
        </div>
      )}
    </section>
  )
}
