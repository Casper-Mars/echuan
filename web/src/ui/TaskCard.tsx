// ui/TaskCard —— 任务的唯一卡片形态（看板列 / 收件箱 / 队列复用），design/components.md §ui/TaskCard
//
// 结构：状态色条 → 标题（最多 3 行省略）→ 元信息行（kind 徽标 · 优先级 · 尝试次数 · 更新时间）→ task_id（mono 截断）。
// 状态：rest / hover / focus-visible；disabled 与 loading 不适用（加载态统一用 SkeletonCard）。
// 可访问性：整卡是一个 UnstyledButton（Enter/Space 打开详情），卡片内不再嵌二级按钮（避免误触）。
import { Paper, UnstyledButton } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { KindBadge } from './KindBadge'
import { cssVars, cx } from './internal'

export interface TaskCardProps {
  title: string
  /** 任务 id（mono 截断显示，title 里保留完整值） */
  taskId?: string
  /** 环节 name（配置驱动，见 ui/KindBadge）；不给则不显示类型徽标 */
  kind?: string
  /** 环节的显示名（任务 DTO 的 `kind_label`）；不传则徽标回退显示 `kind` 原值 */
  kindLabel?: string
  /** 状态只用于左侧状态色条（状态的文字表达交给 StatusPill，页面按需放在元信息行） */
  status?: string
  priority?: number
  /**
   * 尝试次数。给了 `maxAttempts` 时按 design/refs 的「尝试 2/3」呈现；
   * 单独给 attempts（无上限）时退化为「尝试 2」——两者都没有则整段不出现。
   */
  attempts?: number
  /** 尝试上限（`task.max_attempts`）；design/refs 的卡片元信息是「尝试 N/M」 */
  maxAttempts?: number
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
  kindLabel,
  status,
  priority,
  attempts,
  maxAttempts,
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
          {kind ? <KindBadge kind={kind} label={kindLabel} /> : null}
          {priority !== undefined ? <span className="ui-task-card__num">P{priority}</span> : null}
          {attempts !== undefined ? (
            <span className="ui-task-card__num">
              尝试 {attempts}
              {maxAttempts !== undefined ? `/${maxAttempts}` : ''}
            </span>
          ) : null}
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
