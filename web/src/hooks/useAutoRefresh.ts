import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from 'react'
import {
  describeError,
  isAutoRefreshEnabled,
  nextDelay,
  subscribeAutoRefresh,
} from '../lib/refresh'

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
 * - **全局开关（`lib/refresh` 的单例，与顶栏 `AutoRefreshSwitch` 同一套 API）**：
 *   关闭 → 清掉定时器且不再排下一轮，`auto` / `visible` 触发一律不取数（0 自动请求）；
 *   已经发出的在飞请求不中断，允许自然完成并提交（避免 UI 停在旧数据且无错误提示）；
 *   `manual` / `write` 触发不受开关影响，页面首拉（挂载即按 `manual` 跑）也照常执行，
 *   所以关掉开关后页面仍有数据、点「刷新」仍生效。开关切回打开 → **只重排定时器、
 *   不立即补拉**：§4.3 无此要求，立即补拉会和可见性/写后补拉叠加出额外请求；
 *   重排用当前退避值 `nextDelay(intervalMs, failures)`，避免关-开一瞬把退避清零后立刻追打。
 *   开关值只经 `useSyncExternalStore(subscribeAutoRefresh, isAutoRefreshEnabled, () => true)`
 *   读取，本 hook 不自持第二份开关状态、不直接读写 localStorage。
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

  // 全局开关：全站唯一的一份状态在 lib/refresh，本 hook 只订阅、只读，不另立开关。
  // 关闭时定时器不启动（auto / visible 不取数），手动刷新与写后补拉保持不变。
  const enabled = useSyncExternalStore(subscribeAutoRefresh, isAutoRefreshEnabled, () => true)

  // 只服务于「失败时该用哪条错误文案」：成功那一笔会同时更新它，
  // 这样同代补跑（写后补拉）拿到的也是最新是否有数据的判断，而不是上一轮渲染的闭包值。
  const dataRef = useRef<T | undefined>(undefined)

  // 每次渲染都更新引用：定时器与回调只读 ref，避免 load / intervalMs 换引用导致计时被重排。
  const loadRef = useRef(load)
  loadRef.current = load
  const intervalRef = useRef(intervalMs)
  intervalRef.current = intervalMs
  // 开关值同样用 ref 转发：schedule / run 只读 ref，开关变化不会让计时被重排。
  const enabledRef = useRef(enabled)
  enabledRef.current = enabled

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
      // 全局开关关闭：不排定时器（手动刷新与写后补拉不经过这里，仍可用）。
      if (!enabledRef.current) return
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

    // 开关关闭时只放行手动刷新与写后补拉：定时器到期、切回可见都不取数。
    if (!enabledRef.current && (reason === 'auto' || reason === 'visible')) return

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

  // 全局开关联动：开关关闭 → 立刻清定时器（在飞那一笔不打断，收口时 schedule 会自我拦截）；
  // 切回打开 → 只重排定时器、不立即补拉（§4.3 未要求补拉；避免与可见性/写路径叠加出额外请求）。
  // 首帧不在这里排期，挂载与换代的首拉排期由上面的 scope effect 负责，避免重复计时。
  const prevEnabledRef = useRef(enabled)
  useEffect(() => {
    const wasEnabled = prevEnabledRef.current
    prevEnabledRef.current = enabled
    if (!enabled) {
      clearTimer()
      return
    }
    if (wasEnabled) return
    const state = stateRef.current
    // 在飞那一笔收口时自己会排下一轮，这里不叠第二条定时器。
    if (state.inFlight === 0) schedule(nextDelay(intervalRef.current, state.failures))
  }, [enabled, clearTimer, schedule])

  /** 外部触发：缺省 manual；写后补拉请显式传 'write'。 */
  const refresh = useCallback((reason: 'write' | 'manual' = 'manual') => {
    runRef.current(reason)
  }, [])

  return { data, error, refresh }
}
