// 主题模式（亮/暗）单一入口
//
// 设计定稿：暗色为默认（design/components.md 末节）。实现上用 html[data-theme] 作为唯一开关：
//   * tokens.css 的默认解析就是暗色，html[data-theme='light'] 覆写为亮色；
//   * index.html 静态写 data-theme="dark"，首屏不会闪白；
//   * MantineProvider 用 forceColorScheme 跟随同一个值，两边不产生第二套真相。
//
// 页面/壳层切换主题只需 setThemeMode('light' | 'dark')，本模块负责落 localStorage + 通知订阅者。

import { DEFAULT_THEME_MODE, type ThemeMode } from './tokens'

const STORAGE_KEY = 'echuan-theme'
const EVENT_NAME = 'echuan:theme-mode'

const isThemeMode = (value: unknown): value is ThemeMode => value === 'dark' || value === 'light'

/** 读取持久化偏好；非法值回落默认（暗色） */
export function readThemeMode(): ThemeMode {
  if (typeof window === 'undefined') return DEFAULT_THEME_MODE
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY)
    return isThemeMode(stored) ? stored : DEFAULT_THEME_MODE
  } catch {
    // 隐私模式下 localStorage 可能抛错：主题偏好读不到就用默认值
    return DEFAULT_THEME_MODE
  }
}

/** 把模式写到 <html data-theme="…">，CSS 令牌层据此解析 */
export function applyThemeMode(mode: ThemeMode): void {
  if (typeof document === 'undefined') return
  document.documentElement.dataset.theme = mode
}

/** 切换主题：落盘 + 应用 + 通知订阅者（MantineProvider 侧据此更新 forceColorScheme） */
export function setThemeMode(mode: ThemeMode): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, mode)
  } catch {
    // 存不下也不影响本次会话的切换
  }
  applyThemeMode(mode)
  window.dispatchEvent(new CustomEvent<ThemeMode>(EVENT_NAME, { detail: mode }))
}

/** 订阅主题变化，返回取消订阅函数 */
export function subscribeThemeMode(listener: (mode: ThemeMode) => void): () => void {
  const handler = (event: Event) => {
    const detail = (event as CustomEvent<ThemeMode>).detail
    if (isThemeMode(detail)) listener(detail)
  }
  window.addEventListener(EVENT_NAME, handler)
  return () => window.removeEventListener(EVENT_NAME, handler)
}
