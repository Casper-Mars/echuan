import { useCallback, useEffect, useRef, useState } from 'react'
import { describeError, nextDelay } from '../lib/refresh'

/** 触发来源：定时器到期 / 写后补拉 / 手动点击 / 切回可见。 */
type Trigger = 'auto' | 'write' | 'manual' | 'visible'

/**
 * 单飞状态：全部塞进同一个 ref 对象，保证跨渲染、跨定时器读到的是同一份真相。
 * - gen      刷新对象代数，单调递增；旧代响应据此整笔丢弃
 * - inFlight 在飞请求的 gen，0 = 空闲
 * - queued   在飞期间发生过写后补拉，本轮的收口处补跑 1 次
 * - failures 连续失败次数，成功归零
 * - timer    自续 setTimeout 的句柄，同一时刻最多一个
 */
interface AutoRefreshState {
  gen: number
  inFlight: number
  queued: boolean
  failures: number
  timer: ReturnType<typeof setTimeout> | null
}

/**
 * 列表页的「单飞」自动刷新。
 *
 * - 用自续 `setTimeout`（不是 `setInterval`）：失败要退避成变间隔；
 * - 同一时刻最多一笔在飞请求；定时器到期撞上在飞请求时跳过本轮、按当前退避值重排；
 * - `scope` 变化（例如切到另一个项目）= 换代：清定时器、失败计数归零、立即跑一次，
 *   旧代响应回来时整笔丢弃（不 setState、不动失败计数）；
 * - `data` 只在成功时整体替换，失败保留上一次的数据（失败不清空）；
 * - 页面不可见时不排下一轮（不累积补发），切回可见只补 1 次；
 *   不可见时挂载/换代则不首拉（也不排定时器），同样等切回可见补跑 1 次。
 *
 * `load` 的调用方契约：**只负责取数并返回 Promise，不得 setState、不得 catch**。
 * 状态与错误文案由本 hook 统一维护。
 *
 * `refresh()` 缺省按 `'manual'`（在飞则丢弃、不动计时）处理；
 * 写操作之后需要补拉一次请显式传 `'write'`（在飞则排队，本轮收口后立刻补跑 1 次）。
 *
 * 注意 `web/src/main.tsx` 开着 `<StrictMode>`：dev 下 effect 会 mount → cleanup → mount，
 * 第一个请求回来时 gen 已变 → 丢弃，第二次 mount 重新计时。所以 dev 每次进页会看到
 * 2 个请求、只有 1 个 commit，且不会有两条定时器并存 —— 这不是 bug，不要加防抖绕过。
 */
export function useAutoRefresh<T>(options: {
  intervalMs: number
  load: () => Promise<T>
  scope?: string
}): { data: T | undefined; error: string; refresh: (reason?: 'write' | 'manual') => void } {
  const { intervalMs, load, scope } = options

  const [data, setData] = useState<T | undefined>(undefined)
  const [error, setError] = useState('')

  // 只服务于「失败时该用哪条错误文案」：成功那一笔会同时更新它，
  // 这样同代补跑（写后补拉）拿到的也是最新是否有数据的判断，而不是上一轮渲染的闭包值。
  const dataRef = useRef<T | undefined>(undefined)

  // 每次渲染都更新引用：定时器与回调只读 ref，避免 load / intervalMs 换引用导致计时被重排。
  const loadRef = useRef(load)
  loadRef.current = load
  const intervalRef = useRef(intervalMs)
  intervalRef.current = intervalMs

  const stateRef = useRef<AutoRefreshState>({
    gen: 0,
    inFlight: 0,
    queued: false,
    failures: 0,
    timer: null,
  })

  const clearTimer = useCallback(() => {
    const state = stateRef.current
    if (state.timer === null) return
    clearTimeout(state.timer)
    state.timer = null
  }, [])

  /** 定时器里要调的是「最近一次渲染的 run」，用 ref 转发避免计时被重新排期。 */
  const runRef = useRef<(reason: Trigger) => void>(() => {})

  const schedule = useCallback(
    (delayMs: number) => {
      clearTimer()
      // 不可见就不排下一轮：后台不累积补发，回来时由 visible 触发续排。
      if (typeof document !== 'undefined' && document.hidden) return
      stateRef.current.timer = setTimeout(() => {
        stateRef.current.timer = null
        runRef.current('auto')
      }, delayMs)
    },
    [clearTimer],
  )

  const run = (reason: Trigger) => {
    const state = stateRef.current

    if (state.inFlight !== 0) {
      // 单飞：按触发来源决定丢弃 / 排队 / 重排，绝不叠发第二笔。
      if (reason === 'write') state.queued = true
      else if (reason === 'auto') schedule(nextDelay(intervalRef.current, state.failures))
      return
    }

    // 本次请求的对象代数快照：返回时若已换代，整笔丢弃。
    const myGen = state.gen
    state.inFlight = myGen

    let pending: Promise<T>
    try {
      // load 契约：只取数、返回 Promise，不 setState、不 catch；万一同步抛出也按失败收口。
      pending = Promise.resolve(loadRef.current())
    } catch (err) {
      pending = Promise.reject(err)
    }

    /** 本轮收口：在飞期间攒下的写后补拉，立刻补跑 1 次。 */
    const drainQueued = () => {
      if (!stateRef.current.queued) return
      stateRef.current.queued = false
      runRef.current('write')
    }

    pending.then(
      value => {
        if (myGen !== stateRef.current.gen) return
        const current = stateRef.current
        current.inFlight = 0
        current.failures = 0
        dataRef.current = value
        setData(value)
        setError('')
        // 成功后排基础间隔：写后补拉与手动刷新因此自动获得重置计时。
        schedule(intervalRef.current)
        drainQueued()
      },
      err => {
        if (myGen !== stateRef.current.gen) return
        const current = stateRef.current
        current.inFlight = 0
        current.failures += 1
        // data 不动：失败保留上一次的成功数据。
        const reasonText = describeError(err)
        setError(
          dataRef.current === undefined
            ? `加载失败：${reasonText}`
            : `自动刷新失败，正在重试：${reasonText}`,
        )
        schedule(nextDelay(intervalRef.current, current.failures))
        drainQueued()
      },
    )
  }

  runRef.current = run

  // scope 变化（含首次挂载）：换代 → 清定时器 → 失败计数归零 → 立即跑一次。
  // 挂载时先读 `document.hidden`：不可见则跳过首拉（不排定时器、也不置位 inFlight），
  // 变可见后由 visible 分支补跑恰好 1 次，避免后台挂载的页面先打一发无谓请求。
  // 换代语义不变：gen++ / 清定时器 / 失败计数归零 / 放掉旧代 inFlight 照旧执行。
  useEffect(() => {
    const state = stateRef.current
    state.gen += 1
    clearTimer()
    // 旧代的在飞请求已经作废，必须放掉 inFlight，否则单飞会一直挡住新一代。
    state.inFlight = 0
    state.queued = false
    state.failures = 0
    // 不可见就不首拉（cleanup 仍照常注册，隐藏挂载同样要 gen++ / clearTimeout）。
    const hidden = typeof document !== 'undefined' && document.hidden
    if (!hidden) runRef.current('manual')
    return () => {
      state.gen += 1
      clearTimer()
    }
  }, [scope, clearTimer])

  // 可见性：单一监听；不可见清定时器且不排下一轮，变可见只补 1 次。
  useEffect(() => {
    const onVisibilityChange = () => {
      if (document.hidden) {
        clearTimer()
        return
      }
      runRef.current('visible')
    }
    document.addEventListener('visibilitychange', onVisibilityChange)
    return () => {
      document.removeEventListener('visibilitychange', onVisibilityChange)
      clearTimer()
      stateRef.current.gen += 1
    }
  }, [clearTimer])

  /** 外部触发：缺省 manual；写后补拉请显式传 'write'。 */
  const refresh = useCallback((reason: 'write' | 'manual' = 'manual') => {
    runRef.current(reason)
  }, [])

  return { data, error, refresh }
}
