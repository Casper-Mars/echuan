// features/task-detail/TaskDetailRoute —— 详情独立路由（`#/projects/:pid/tasks/:tid`）
//
// 规格：design/flows.md F1 第 2 步（点卡片 → 详情路由，URL 可分享；桌面右侧 720 抽屉、
// 移动全屏 sheet —— 由 ui/DetailSheet 的令牌与媒体规则承接）、第 3 步（无 taskId 回看板）、
// 第 4 步（关闭抽屉 = URL 回看板路由）。
//
// 本卡（T4）只把「详情是路由」做实：可直接打开、可分享、关闭回看板。
// 详情内容（元信息 / 附件预览 / 评审动作）由 T6 接入，这里给的是明确的占位与说明，
// 不是假数据，也不是灰骨架——骨架只用于「正在加载」，这里没有请求。
import { useNavigate, useParams } from 'react-router-dom'
import { Alert, Button, DetailSheet } from '../../ui'
import './task-detail.css'

export default function TaskDetailRoute() {
  const { projectId = '', taskId = '' } = useParams()
  const navigate = useNavigate()
  const boardUrl = `/projects/${projectId}/tasks`
  const backToBoard = () => navigate(boardUrl)

  return (
    <div className="fx-detail" data-ui="task-detail" data-state="default" data-task-id={taskId}>
      <DetailSheet
        opened
        title={`任务 ${taskId}`}
        onClose={backToBoard}
        footer={
          <div className="fx-detail__actions">
            {/* 全屏唯一主操作：关闭详情回看板（AC5 的计数锚点是外层 data-ui 容器） */}
            <div data-ui="primary-action" className="fx-detail__primary">
              <Button variant="primary" size="md" onClick={backToBoard}>
                返回看板
              </Button>
            </div>
          </div>
        }
      >
        <div className="fx-detail__body">
          <Alert tone="info" title="详情内容由 T6 落地">
            本路由已可直接打开与分享（<code>#/projects/:pid/tasks/:tid</code>）。元信息、附件预览与评审动作在 T6
            接入，本卡只保证「详情是独立路由、关闭即回看板」。
          </Alert>
          <dl className="fx-detail__meta">
            <div className="fx-detail__meta-row">
              <dt>项目</dt>
              <dd>{projectId}</dd>
            </div>
            <div className="fx-detail__meta-row">
              <dt>任务</dt>
              <dd>{taskId}</dd>
            </div>
            <div className="fx-detail__meta-row">
              <dt>看板路由</dt>
              <dd>
                <code>#/projects/{projectId}/tasks</code>
              </dd>
            </div>
          </dl>
        </div>
      </DetailSheet>
    </div>
  )
}
