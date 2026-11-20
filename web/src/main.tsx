import { StrictMode, useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { MantineProvider } from '@mantine/core'
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
      <App />
    </MantineProvider>
  )
}

createRoot(container).render(
  <StrictMode>
    <ThemeRoot />
  </StrictMode>,
)
