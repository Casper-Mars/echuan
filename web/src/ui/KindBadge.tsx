// ui/KindBadge —— 任务类型徽标（显示名来自流程配置），design/components.md §ui/KindBadge
//
// 语义（流程可配置之后，tech-echuan-180 §0.3）：一个「类型」就是流程配置里的一个环节；
// `name` 是唯一对外标识（HTTP / kit 配置 / 审计规则串都用它），`label` 只是显示名。
// 本组件只负责「怎么显示」：**不持有类型清单、不写死中文映射**——文案由调用方按任务 DTO 的
// `kind_label` 传进来（缺省回退 `kind` 原值），所以配置里新增环节不需要改前端，也不会出现空白。
//
// 类型不是状态：所有环节配色统一（描边 + 中性色），不给语义色，避免与状态色抢注意力。
import { Badge } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { cssVars, cx } from './internal'

export interface KindBadgeProps {
  /** 环节 name（内部标识；只用于 data-kind 定位与「没有显示名」时的兜底文案） */
  kind: string
  /**
   * 显示名（任务 DTO 的 `kind_label`，后端按 `stage.label` 给值、未配置时回退 name）。
   * 缺省或空白时显示 `kind` 原值——任何情况下都不渲染空徽标。
   */
  label?: string
  className?: string
}

export function KindBadge({ kind, label, className }: KindBadgeProps) {
  const given = (label ?? '').trim()
  const text = given !== '' ? given : kind
  return (
    <Badge
      component="span"
      className={cx('ui-kind-badge', className)}
      data-kind={kind}
      variant="default"
      radius="sm"
      // 单行省略：完整文案始终在 title 里可悬停查看（长 label 的口径见 design/components.md）
      title={text}
      styles={{
        label: { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
      }}
      style={cssVars({
        '--badge-height': cssVar('ui-pill-height'),
        '--badge-fz': cssVar('text-caption-size'),
        '--badge-padding-x': cssVar('space-2'),
        '--badge-radius': cssVar('radius-4'),
        '--badge-bg': cssVar('surface-overlay'),
        '--badge-color': cssVar('text-muted'),
        '--badge-bd': `${cssVar('border-width')} solid ${cssVar('border-subtle')}`,
      })}
    >
      {text}
    </Badge>
  )
}
