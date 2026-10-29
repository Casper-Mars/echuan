import { Link, useLocation } from 'react-router-dom'
import { useProductName } from '../hooks/useProductName'

interface Crumb {
  label: string
  /** 缺省表示当前层，不渲染为链接。 */
  to?: string
}

function decode(value: string): string {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

/**
 * 内容区顶部面包屑：产品 → 项目 → 任务；Worker 页单独一层。
 * 标识从 hash 路由解析（HashRouter 下 location.pathname 即 hash 路径），
 * 产品层优先显示名称，缺失时回落产品编号。
 */
export function Breadcrumbs() {
  const { pathname } = useLocation()

  const workersMatch = /^\/workers\/?$/.test(pathname)
  const taskMatch = pathname.match(/^\/products\/([^/]+)\/projects\/([^/]+)\/tasks\/?$/)
  const projectsMatch = pathname.match(/^\/products\/([^/]+)\/projects\/?$/)

  const productId = taskMatch?.[1] ?? projectsMatch?.[1]
  const productName = useProductName(productId ? decode(productId) : undefined)

  let crumbs: Crumb[] = [{ label: '产品' }]

  if (workersMatch) {
    crumbs = [{ label: '执行节点' }]
  } else if (taskMatch) {
    const decodedProduct = decode(taskMatch[1])
    const decodedProject = decode(taskMatch[2])
    crumbs = [
      { label: productName ?? decodedProduct, to: '/products' },
      { label: decodedProject, to: `/products/${encodeURIComponent(decodedProduct)}/projects` },
      { label: '任务' },
    ]
  } else if (projectsMatch) {
    const decodedProduct = decode(projectsMatch[1])
    crumbs = [
      { label: productName ?? decodedProduct, to: '/products' },
      { label: '项目' },
    ]
  }

  return (
    <nav className="breadcrumbs" aria-label="面包屑">
      {crumbs.map((crumb, index) => (
        <span className="crumb" key={`${crumb.label}-${index}`}>
          {index > 0 && <span className="crumb-sep">›</span>}
          {crumb.to ? (
            <Link to={crumb.to}>{crumb.label}</Link>
          ) : (
            <span className="crumb-current" aria-current="page">
              {crumb.label}
            </span>
          )}
        </span>
      ))}
    </nav>
  )
}
