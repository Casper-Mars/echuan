/**
 * 项目阶段条：把九个项目状态收敛成五个人类可读阶段。
 *
 * 需求=(0 需求草稿, 1 需求评审)
 * 方案=(2 方案草稿, 3 方案评审)
 * 拆解=(4 开发任务草稿, 5 开发任务评审)
 * 开发=(6 待完成)
 * 完成=(7 已完成)
 *
 * 纯展示组件，不新增接口；status=8（已取消）时整体置灰并标注「已取消」。
 */

interface Stage {
  key: string
  label: string
  statuses: readonly number[]
}

const STAGES: Stage[] = [
  { key: 'requirement', label: '需求', statuses: [0, 1] },
  { key: 'spec', label: '方案', statuses: [2, 3] },
  { key: 'plan', label: '拆解', statuses: [4, 5] },
  { key: 'dev', label: '开发', statuses: [6] },
  { key: 'done', label: '完成', statuses: [7] },
]

const CANCELLED_STATUS = 8

export function ProjectStageBar({ status }: { status: number }) {
  const cancelled = status === CANCELLED_STATUS
  const currentIndex = STAGES.findIndex((stage) => stage.statuses.includes(status))
  const currentLabel = currentIndex >= 0 ? STAGES[currentIndex].label : '未知'

  return (
    <div
      className={cancelled ? 'stage-bar is-cancelled' : 'stage-bar'}
      role="group"
      aria-label={
        cancelled
          ? '项目阶段：已取消'
          : currentIndex >= 0
            ? `项目阶段：当前处于${currentLabel}`
            : '项目阶段：未知'
      }
    >
      {STAGES.map((stage, index) => {
        const done = !cancelled && currentIndex > index
        const current = !cancelled && currentIndex === index
        return (
          <div
            key={stage.key}
            className={[
              'stage-step',
              done ? 'is-done' : '',
              current ? 'is-current' : '',
            ]
              .filter(Boolean)
              .join(' ')}
            aria-current={current ? 'step' : undefined}
          >
            <span className="stage-dot" aria-hidden="true">
              {done ? '✓' : index + 1}
            </span>
            <span className="stage-label">{stage.label}</span>
          </div>
        )
      })}
      {cancelled && <span className="stage-cancelled">已取消</span>}
    </div>
  )
}
