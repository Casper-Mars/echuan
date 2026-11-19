import { useEffect, useState } from 'react'
import { api, ApiError } from '../api/client'
import type { Product } from '../api/types'
import { repoHref } from '../lib/url'
import { Drawer } from './Drawer'

/** 空值统一占位：全空白与 `null`/`undefined` 一样回退，非空值原样返回（不 trim）。 */
function textOrDash(value?: string | null): string {
  return typeof value === 'string' && value.trim() ? value : '—'
}

function message(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

function projectCountLabel(product: Product, fallbackProjectCount?: number): string {
  const count =
    typeof product.project_count === 'number' ? product.project_count : fallbackProjectCount
  return typeof count === 'number' ? `${count} 个项目` : '—'
}

/** 仓库地址：`http(s)` 渲成新标签页链接，其余形态（`git@…`、`ssh://…`）保持纯文本。 */
function RepoAddress({ gitUrl }: { gitUrl?: string | null }) {
  const href = repoHref(gitUrl)
  if (!href) return <p className="mono">{textOrDash(gitUrl)}</p>
  return (
    <a
      className="mono"
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      title={gitUrl ?? undefined}
    >
      {href}
    </a>
  )
}

/**
 * 产品详情抽屉：挂载时按 `product_id` 取 `GET /v1/products?id=`，
 * 产出 loading / error / 内容三态。
 *
 * 走请求而不是复用列表对象，是为了让「详情取不到」（产品被并发删除等）
 * 在抽屉内有可见失败态；列表已算出的项目数作为 `project_count` 缺省时的回退。
 */
export function ProductDetail({
  productId,
  fallbackProjectCount,
  onClose,
}: {
  productId: string
  fallbackProjectCount?: number
  onClose: () => void
}) {
  const [product, setProduct] = useState<Product | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [reloadToken, setReloadToken] = useState(0)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError('')
    api
      .getProduct(productId)
      .then((detail) => {
        if (cancelled) return
        setProduct(detail)
      })
      .catch((err: unknown) => {
        if (cancelled) return
        // 带状态码，便于一眼看出是「产品已不存在」还是别的失败。
        const status = err instanceof ApiError ? `（HTTP ${err.status}）` : ''
        setProduct(null)
        setError(`产品详情加载失败${status}：${message(err)}`)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [productId, reloadToken])

  return (
    <Drawer title={`产品 ${productId}`} eyebrow="PRODUCT" onClose={onClose}>
      {error && (
        <div className="alert alert-error">
          <span>{error}</span>
          <button
            className="btn btn-ghost btn-sm"
            onClick={() => setReloadToken((token) => token + 1)}
          >
            重试
          </button>
        </div>
      )}
      {loading && !product && !error && <p className="muted">正在加载产品详情…</p>}
      {product && (
        <div className="detail-stack">
          <div className="detail-hero">
            <div className="detail-hero-main">
              <span className="mono">{product.product_id}</span>
              <span className="chip">{product.license || '未设置证书'}</span>
            </div>
            <h3>{textOrDash(product.name)}</h3>
            <div className="detail-meta">
              <div>
                <span>项目数</span>
                <strong>{projectCountLabel(product, fallbackProjectCount)}</strong>
              </div>
              <div>
                <span>证书</span>
                <strong>{textOrDash(product.license)}</strong>
              </div>
            </div>
          </div>

          <section className="detail-block">
            <h3>仓库地址</h3>
            <RepoAddress gitUrl={product.git_url} />
          </section>
          <section className="detail-block">
            <h3>描述</h3>
            <p
              className={
                typeof product.description === 'string' && product.description.trim() ? '' : 'muted'
              }
            >
              {textOrDash(product.description)}
            </p>
          </section>
        </div>
      )}
    </Drawer>
  )
}
