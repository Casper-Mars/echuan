// ui/TaskCard —— 任务的唯一卡片形态（看板列 / 收件箱 / 队列复用），design/components.md §ui/TaskCard
//
// 结构：状态色条 → 标题（最多 3 行省略）→ 元信息行（kind 徽标 · 优先级 · 尝试次数 · 更新时间）→ task_id（mono 截断）。
// 状态：rest / hover / focus-visible；disabled 与 loading 不适用（加载态统一用 SkeletonCard）。
// 可访问性：整卡是一个 UnstyledButton（Enter/Space 打开详情），卡片内不再嵌二级按钮（避免误触）。
import { Paper, UnstyledButton } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { KindBadge, type TaskKind } from './KindBadge'
import { cssVars, cx } from './internal'

export interface TaskCardProps {
  title: string
  /** 任务 id（mono 截断显示，title 里保留完整值） */
  taskId?: string
  kind?: TaskKind
  /** 状态只用于左侧状态色条（状态的文字表达交给 StatusPill，页面按需放在元信息行） */
  status?: string
  priority?: number
  attempts?: number
  updatedAt?: string
  /** 变体 focus：当前打开的任务（品牌描边） */
  focused?: boolean
  /** 读屏名称，默认「标题 + 状态」 */
  ariaLabel?: string
  className?: string
  onClick?: () => void
}

export function TaskCard({
  title,
  taskId,
  kind,
  status,
  priority,
  attempts,
  updatedAt,
  focused = false,
  ariaLabel,
  className,
  onClick,
}: TaskCardProps) {
  return (
    <Paper
      className={cx('ui-task-card', className)}
      data-status={status}
      data-focused={focused || undefined}
      radius="md"
      style={cssVars({
        '--paper-radius': cssVar('radius-8'),
      })}
    >
      <UnstyledButton
        className="ui-task-card__hit"
        aria-label={ariaLabel ?? `${title}${status ? `（${status}）` : ''}`}
        onClick={onClick}
      >
        <span className="ui-task-card__title">{title}</span>
        <span className="ui-task-card__meta">
          {kind ? <KindBadge kind={kind} /> : null}
          {priority !== undefined ? <span className="ui-task-card__num">P{priority}</span> : null}
          {attempts !== undefined ? <span className="ui-task-card__num">尝试 {attempts}</span> : null}
          {updatedAt ? <span className="ui-task-card__time">{updatedAt}</span> : null}
        </span>
        {taskId ? (
          <span className="ui-task-card__id" title={taskId}>
            {taskId}
          </span>
        ) : null}
      </UnstyledButton>
    </Paper>
  )
}
