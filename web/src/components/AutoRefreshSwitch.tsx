import { useSyncExternalStore } from 'react'
import {
  isAutoRefreshEnabled,
  setAutoRefreshEnabled,
  subscribeAutoRefresh,
} from '../lib/refresh'

/**
 * 顶栏的全局自动刷新开关：全站唯一，页面里不要再放第二个。
 *
 * 状态由 lib/refresh 的模块级单例托管（持久化、存储不可用与脏值一律回落为开），
 * 本组件只负责读与写，不直接触碰存储。
 */
export function AutoRefreshSwitch() {
  const enabled = useSyncExternalStore(subscribeAutoRefresh, isAutoRefreshEnabled, () => true)

  return (
    <label className="toggle">
      <input
        type="checkbox"
        checked={enabled}
        onChange={(e) => setAutoRefreshEnabled(e.target.checked)}
      />
      自动刷新
    </label>
  )
}
