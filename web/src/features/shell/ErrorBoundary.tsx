// features/shell/ErrorBoundary —— 页级渲染错误边界（技术方案 §10「运行期错误边界」）
//
// 形状：class 组件 + getDerivedStateFromError（React 18 只支持 class 边界）。
// 状态矩阵（design/components.md 通用状态口径）：
//   default —— 无错，原样渲染 children（壳层导航与顶栏保持可用，错误不吞掉整个应用）；
//   error   —— 渲染统一错误条（features/shell/ErrorBar，ui/Alert danger + role="alert"）+ 「重试 / 返回看板」；
//   loading —— 点「重试」后到子树重挂之间的一帧，错误条动作按钮进入 loading（ui/Alert actionLoading）。
//
// 边界范围：只包路由内容（AppFrame 的 <main>），不包导航与顶栏——页面渲染崩了仍能切到别的入口。
// 文案：不写死接口文案（骨架只给「页面渲染出错 / 可重试 / 返回看板」，技术细节走 detail）。
import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Button } from '../../ui'
import { ErrorBar } from './ErrorBar'

export interface ErrorBoundaryProps {
  children: ReactNode
  /** 「返回看板」动作（AppFrame 传 boardPath()：上次项目看板，无记录则产品页） */
  onExit?: () => void
  exitLabel?: string
}

interface ErrorBoundaryState {
  error: Error | null
  retrying: boolean
}

export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null, retrying: false }

  private timer: ReturnType<typeof setTimeout> | null = null

  static getDerivedStateFromError(error: Error): Partial<ErrorBoundaryState> {
    return { error, retrying: false }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // 无远程上报端点（技术方案 §10「不做的观测」），只在控制台留组件栈便于复现。
    console.error('[shell] 页面渲染异常：', error, info.componentStack)
  }

  componentWillUnmount() {
    if (this.timer !== null) clearTimeout(this.timer)
  }

  private handleRetry = () => {
    // 先给「重试中」反馈（按钮 loading），再重挂子树；若子组件仍抛错会立刻回到 error 态。
    this.setState({ retrying: true })
    this.timer = setTimeout(() => {
      this.timer = null
      this.setState({ error: null, retrying: false })
    }, 0)
  }

  render() {
    const { error, retrying } = this.state
    const { children, onExit, exitLabel = '返回看板' } = this.props

    if (!error) return children

    return (
      <div className="fx-page-error" data-ui="page-error" data-state="error">
        <ErrorBar
          title="页面渲染出错"
          message="当前页面无法显示。可以先重试；若持续失败，返回看板继续处理其他工作。"
          detail={error.message}
          retrying={retrying}
          onRetry={this.handleRetry}
          extraActions={
            onExit ? (
              <Button variant="default" size="sm" onClick={onExit}>
                {exitLabel}
              </Button>
            ) : null
          }
        />
      </div>
    )
  }
}
