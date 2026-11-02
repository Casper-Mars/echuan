import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ApiError, getWorker, isAbortError, listWorkers } from '../api/client'
import type { WorkerPresence } from '../api/types'
import { usePolling } from './usePolling'

/**
 * 任务卡只需要「本页任务涉及的 worker」的在线状态，不必拉全量：
 *
 * - 先用 `GET /v1/workers` 首页（200 条，按 last_seen 倒序）建 id→presence 映射；
 * - 首页未命中的 id 再用 `GET /v1/workers?id=` 逐个补查（worker 超过 200 个时才会走到）；
 * - 补查结果缓存在内存里，并在每次轮询成功（5s 一档）后重新校验，
 *   期间继续沿用旧值，所以既不会每次渲染都打接口，也不会闪烁。
 *
 * 取不到明确结论时一律返回 null（未知），调用方不得据此判定离线。
 */
export interface WorkerPresenceLookup {
  /** 该 worker 的在线状态；null 表示未知（尚未补查到 / 补查失败）。 */
  presenceOf: (workerId: string) => WorkerPresence | null
  /** 至少成功拿到过一次 worker 数据，才允许做离线判定。 */
  known: boolean
}

/** 补查并发上限：正常情况下未命中数是个位数，这里只防御异常数据。 */
const PROBE_CONCURRENCY = 6

async function mapWithConcurrency<T, R>(
  items: T[],
  limit: number,
  run: (item: T) => Promise<R>,
): Promise<R[]> {
  const results = new Array<R>(items.length)
  let cursor = 0
  const runners = Array.from({ length: Math.min(limit, items.length) }, async () => {
    while (cursor < items.length) {
      const index = cursor
      cursor += 1
      results[index] = await run(items[index])
    }
  })
  await Promise.all(runners)
  return results
}

export function useWorkerPresence(workerIds: string[]): WorkerPresenceLookup {
  // 首页 200 条：绝大多数任务卡的执行者都在内，命中就不必补查。
  const { data, lastUpdated } = usePolling(
    (signal) => listWorkers({ limit: 200, signal }),
    [],
  )

  // 每次轮询成功递增（Date 时间戳足够区分），用作补查缓存的失效信号。
  const generation = lastUpdated ? lastUpdated.getTime() : 0

  const pagePresence = useMemo(() => {
    const map = new Map<string, WorkerPresence>()
    for (const worker of data?.items ?? []) {
      map.set(worker.worker_id, worker.presence)
    }
    return map
  }, [data])

  // 去重 + 排序后拼串：内容不变时 key 不变，effect 不会因数组引用变化而重跑。
  const idsKey = useMemo(() => {
    const unique = new Set<string>()
    for (const id of workerIds) {
      if (id) unique.add(id)
    }
    return Array.from(unique).sort().join(',')
  }, [workerIds])

  // 首页未命中的补查结果；null 表示这一轮没拿到明确结论。
  const [probes, setProbes] = useState<Map<string, WorkerPresence | null>>(
    () => new Map(),
  )
  // effect 内需要读到最新缓存来做失败回退，用 ref 镜像避免闭包过期。
  const probesRef = useRef(probes)
  probesRef.current = probes

  useEffect(() => {
    if (generation === 0) return
    const ids = idsKey ? idsKey.split(',') : []
    const missing = ids.filter((id) => !pagePresence.has(id))
    if (missing.length === 0) {
      // 首页已覆盖全部需要的 worker，清掉旧缓存避免无界增长。
      setProbes((prev) => (prev.size === 0 ? prev : new Map()))
      return
    }

    const controller = new AbortController()
    let cancelled = false
    void mapWithConcurrency(missing, PROBE_CONCURRENCY, async (id) => {
      try {
        const worker = await getWorker(id, controller.signal)
        return [id, worker.presence] as const
      } catch (error) {
        if (isAbortError(error)) return null
        // 404：注册表里已没有该 id（未注册 / 被保留期回收）→ 按离线处理。
        if (error instanceof ApiError && error.status === 404) {
          return [id, 'offline'] as const
        }
        // 网络/服务错误：沿用上一次已知结果，从未查到过就保持未知，绝不判离线。
        return [id, probesRef.current.get(id) ?? null] as const
      }
    }).then((results) => {
      if (cancelled) return
      const next = new Map<string, WorkerPresence | null>()
      for (const entry of results) {
        if (!entry) continue
        next.set(entry[0], entry[1])
      }
      setProbes(next)
    })

    return () => {
      cancelled = true
      controller.abort()
    }
  }, [generation, idsKey, pagePresence])

  const presenceOf = useCallback(
    (workerId: string) => pagePresence.get(workerId) ?? probes.get(workerId) ?? null,
    [pagePresence, probes],
  )

  return { presenceOf, known: data !== null }
}
