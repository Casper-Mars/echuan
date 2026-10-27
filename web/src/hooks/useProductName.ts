import { useEffect, useState } from 'react'
import { getProduct, isProduct } from '../api/client'

/**
 * 面包屑优先显示产品名称，取不到时回落到产品编号。
 * 只在 productId 变化时请求一次；失败静默回落，不打扰页面。
 */
export function useProductName(productId: string | undefined): string | undefined {
  const [name, setName] = useState<string | undefined>(undefined)

  useEffect(() => {
    setName(undefined)
    if (!productId) return
    const controller = new AbortController()
    getProduct(productId, controller.signal)
      .then((value) => {
        if (controller.signal.aborted) return
        if (isProduct(value) && value.name.trim()) setName(value.name.trim())
      })
      .catch(() => {
        // 名称只是展示优化，失败时面包屑回落编号。
      })
    return () => controller.abort()
  }, [productId])

  return name
}
