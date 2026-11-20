// features/shell/ErrorBoundary —— 页级渲染错误边界（技术方案 §10「运行期错误边界」）
//
// 形状：class 组件 + getDerivedStateFromError（React 18 只支持 class 边界）。
// 状态矩阵（design/components.md 通用状态口径）：
//   default —— 无错，原样渲染 children（壳层导航与顶栏保持可用，错误不吞掉整个应用）；
//   error   —— 渲染统一错误条（features/shell/ErrorBar，ui/Alert danger + role="alert"）+「重试 / 返回看板」；
//   loading —— 点「重试」后到子树重挂之间的一帧，动作按钮进入 loading（aria-busy）。
//
// 边界范围：只包路由内容（AppFrame 的 <main>），不包导航与顶栏——页面渲染崩了仍能切到别的入口。
// 文案：不写死接口文案（骨架只给「页面渲染出错 / 可重试 / 返回看板」，技术细节走 detail）。
//
// 路由重置（Tester 复核项 D1）：错误态必须能被「下一次导航」清掉，否则点侧栏/返回看板只是改了
// hash，界面永远停在错误条上，用户只能整页刷新。实现方式：
//   * AppFrame 传 resetKey = react-router 的 location.key（每次导航都会换，含「导航到当前路径」与
//     push/replace 两种，比 pathname+search 更严：同路径重入也会重置）；
//   * componentDidUpdate 比对 resetKey，变化即清 error/retrying，并取消挂起的重试定时器。
//   * 根因仍存在（例如接口持续坏数据）时会重新抛错、再次进入 error —— 这是期望行为，不是粘住。
import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Button } from '../../ui'
import { ErrorBar } from './ErrorBar'

export interface ErrorBoundaryProps {
  children: ReactNode
  /**
   * 路由标识（AppFrame 传 location.key）。值变化 = 用户发起了一次导航 → 无条件重置错误态。
   * 不传时退回「只有重试能恢复」的老行为，仅用于不接路由的独立使用场景。
   */
  resetKey?: string
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

  componentDidUpdate(prevProps: ErrorBoundaryProps) {
    // 导航即视为「用户已经离开出错的页面」：清错误态让新路由正常渲染。
    if (prevProps.resetKey !== this.props.resetKey && this.state.error !== null) {
      this.clearTimer()
      this.setState({ error: null, retrying: false })
    }
  }

  componentWillUnmount() {
    this.clearTimer()
  }

  private clearTimer() {
    if (this.timer !== null) {
      clearTimeout(this.timer)
      this.timer = null
    }
  }

  private handleRetry = () => {
    // 先给「重试中」反馈（按钮 loading），再重挂子树；若子组件仍抛错会立刻回到 error 态。
    this.setState({ retrying: true })
    this.clearTimer()
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
