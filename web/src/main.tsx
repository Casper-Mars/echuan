import { StrictMode, useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { AppShell, MantineProvider } from '@mantine/core'
import { App } from './App'
// Mantine 底座样式必须在最前面，令牌层与 base.css 才能稳定压过它的默认值
import '@mantine/core/styles.css'
import './theme/tokens.css'
import './theme/base.css'
// 旧样式层（三代视觉叠加）：本卡不动，T11 才删；放在最后保证旧页面视觉不变
import './styles.css'
import { applyThemeMode, readThemeMode, subscribeThemeMode } from './theme/color-scheme'
import { mantineTheme } from './theme/mantine-theme'
import type { ThemeMode } from './theme/tokens'

const container = document.getElementById('root')
if (!container) throw new Error('root element missing')

/**
 * 主题根：data-theme（CSS 令牌层）与 MantineProvider（组件库）跟随同一个状态，
 * 默认暗色（design/components.md：暗色 = 默认与验收基准），可切亮色对照。
 */
function ThemeRoot() {
  const [mode, setMode] = useState<ThemeMode>(() => readThemeMode())

  useEffect(() => subscribeThemeMode(setMode), [])

  useEffect(() => {
    applyThemeMode(mode)
  }, [mode])

  return (
    <MantineProvider theme={mantineTheme} forceColorScheme={mode} defaultColorScheme="dark">
      {/*
        AppShell 只做一件事：提供 Mantine 的 AppShell 安全上下文。
        ui/SidebarNav 内部是 AppShell.Navbar，Mantine 要求它必须挂在 <AppShell> 里（否则
        createSafeContext 直接抛错）。而 features/** 禁止 import '@mantine/core'（分层红线 AC7），
        所以这一层接线只能留在应用入口——三形态、几何与顶栏仍然全由 features/shell 决定。
      */}
      <AppShell
        withBorder={false}
        padding={0}
        navbar={{ width: 'var(--ui-layout-sidebar)', breakpoint: 'sm', collapsed: { mobile: true } }}
      >
        <App />
      </AppShell>
    </MantineProvider>
  )
}

createRoot(container).render(
  <StrictMode>
    <ThemeRoot />
  </StrictMode>,
)
