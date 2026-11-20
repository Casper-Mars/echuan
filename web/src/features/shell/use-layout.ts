// features/shell/use-layout —— 三形态判定（桌面 / 平板 / 移动）
//
// 断点不在本文件写死：取值来自 theme/tokens.ts 的 breakpoint（= design/tokens.json 单一来源），
// 对应 design/screens/board.md「断点」节：
//   ≥1280 桌面（侧栏 240 有文字）/ 768–1279 平板（侧栏收 64 图标栏）/ <768 移动（无侧栏 + 底部 TabBar）。
//
// 为什么用 matchMedia 而不是 useMediaQuery（@mantine/hooks）：分层红线只允许壳层 import
// ui / theme / api / hooks（web/src/hooks），不能直接碰 Mantine 包（AC7）。
import { useSyncExternalStore } from 'react'
import { breakpoint } from '../../theme/tokens'

export type ShellLayout = 'desktop' | 'tablet' | 'mobile'

/** 三种形态各自的最小宽度（px 数值来自令牌，拼接成媒体查询串） */
const DESKTOP_QUERY = `(min-width: ${breakpoint.lg}px)`
const TABLET_QUERY = `(min-width: ${breakpoint.sm}px) and (max-width: ${breakpoint.lg - 1}px)`

/** ssr / 无 matchMedia 环境（jsdom 默认没有）按桌面处理，保证首帧有完整导航 */
function matches(query: string): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false
  return window.matchMedia(query).matches
}

function getSnapshot(): ShellLayout {
  if (matches(DESKTOP_QUERY)) return 'desktop'
  if (matches(TABLET_QUERY)) return 'tablet'
  return 'mobile'
}

function subscribe(onChange: () => void): () => void {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return () => {}
  const list = [window.matchMedia(DESKTOP_QUERY), window.matchMedia(TABLET_QUERY)]
  for (const mql of list) mql.addEventListener('change', onChange)
  return () => {
    for (const mql of list) mql.removeEventListener('change', onChange)
  }
}

/**
 * 当前形态。窗口跨断点时由 matchMedia 的 change 事件驱动重渲染，
 * 不需要 resize 轮询，也不需要 SSR 占位（getSnapshot 同步可判）。
 */
export function useShellLayout(): ShellLayout {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
}
