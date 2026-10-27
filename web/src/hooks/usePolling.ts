import { useCallback, useEffect, useRef, useState } from 'react'
import { isAbortError } from '../api/client'
import { isUserEditing } from '../lib/interaction'

export interface PollingOptions {
  /** 基础轮询间隔，默认 5000ms。 */
  intervalMs?: number
  enabled?: boolean
}

export interface PollingResult<T> {
  data: T | null
  error: Error | null
  loading: boolean
  refreshing: boolean
  lastUpdated: Date | null
  refresh: () => void
}

/**
 * 统一轮询封装（技术方案 §8）：
 *
 * - 进入页面立即拉一次，之后按间隔轮询；
 * - 路由切换/组件卸载清理 timer 与 in-flight 请求；
 * - 失败不终止轮询，只退避一档（5s → 10s），成功后恢复；
 * - 用户正在输入时不覆盖其输入（保留旧数据）；
 * - 同一时刻只有一个请求在飞，避免慢后端下堆积。
 */
export function usePolling<T>(
  load: (signal: AbortSignal) => Promise<T>,
  deps: unknown[],
  options: PollingOptions = {},
): PollingResult<T> {
  const { intervalMs = 5000, enabled = true } = options

  // 用 ref 保存最新 loader，避免调用方每次渲染新建函数导致轮询重启。
  const loadRef = useRef(load)
  loadRef.current = load

  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<Error | null>(null)
  const [loading, setLoading] = useState(enabled)
  const [refreshing, setRefreshing] = useState(false)
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null)

  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const abortRef = useRef<AbortController | null>(null)
  const refreshRef = useRef<(() => void) | null>(null)

  useEffect(() => {
    if (!enabled) {
      setLoading(false)
      return
    }

    let cancelled = false
    let inFlight = false
    let failing = false
    // 写操作成功后的手动刷新：若刚好有请求在飞，则在它结束后立即补拉一次，
    // 而不是等下一个 5s 周期（列表状态必须在 5s 内，实际上立即更新）。
    let pendingRefresh = false

    const clearTimer = () => {
      if (timerRef.current !== null) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
    }

    const schedule = () => {
      clearTimer()
      if (cancelled) return
      // 失败时退避一次（10s），避免后端异常时每 5 秒刷屏。
      const delay = failing ? intervalMs * 2 : intervalMs
      timerRef.current = setTimeout(() => {
        void tick(false)
      }, delay)
    }

    const tick = async (manual: boolean) => {
      if (cancelled) return
      if (inFlight) {
        if (manual) {
          pendingRefresh = true
          setRefreshing(true)
        }
        return
      }
      inFlight = true
      const controller = new AbortController()
      abortRef.current = controller
      if (manual) setRefreshing(true)
      try {
        const next = await loadRef.current(controller.signal)
        if (cancelled) return
        // 输入中不覆盖：保留上一次数据，等用户离开输入态后的下一次轮询再更新。
        if (!isUserEditing()) setData(next)
        setError(null)
        setLastUpdated(new Date())
        failing = false
      } catch (err) {
        if (cancelled || isAbortError(err)) return
        failing = true
        setError(err instanceof Error ? err : new Error(String(err)))
      } finally {
        inFlight = false
        if (!cancelled) {
          setLoading(false)
          setRefreshing(false)
          if (pendingRefresh) {
            pendingRefresh = false
            void tick(false)
          } else {
            schedule()
          }
        }
      }
    }

    refreshRef.current = () => {
      void tick(true)
    }

    setLoading(true)
    setError(null)
    setData(null)
    setLastUpdated(null)
    failing = false
    void tick(false)

    return () => {
      cancelled = true
      clearTimer()
      abortRef.current?.abort()
      refreshRef.current = null
    }
    // deps 由调用方声明（如 [productId]）；intervalMs/enabled 变化也需要重启。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, intervalMs, ...deps])

  const refresh = useCallback(() => {
    refreshRef.current?.()
  }, [])

  return { data, error, loading, refreshing, lastUpdated, refresh }
}
