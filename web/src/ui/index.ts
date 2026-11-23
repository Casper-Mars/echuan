// echuan ui 封装层 · 唯一出口
//
// 页面层（T5–T9 的 features/**）只允许从本文件 import：
//   import { Button, TaskCard } from '../ui'
// 不允许 import '@mantine/core'（design/components.md 分层红线 / 本卡 AC3），
// 也不允许 import ui 内部的单个实现文件——类型与实现都从这一条口子出。
//
// 22 个组件与 design/components.md「总览」表一一对应，顺序也按该表排列。
// 运行时导出必须恰好是这 22 个组件（scripts/ui-imports-check.mjs 会断言）；
// 常量（STATUS_LABEL / KIND_LABEL / STATUS_TONE）留在各自实现文件内，页面需要时再从组件 props 传文案。
import './ui.css'

export { Button } from './Button'
export type { ButtonProps, ButtonVariant, ControlSize } from './Button'

export { IconButton } from './IconButton'
export type { IconButtonProps, IconButtonSize, IconButtonVariant } from './IconButton'

export { TaskCard } from './TaskCard'
export type { TaskCardProps } from './TaskCard'

export { StatusPill } from './StatusPill'
export type { StatusPillProps, StatusTone, TaskStatus } from './StatusPill'

export { KindBadge } from './KindBadge'
export type { KindBadgeProps, TaskKind } from './KindBadge'

export { BoardColumn } from './BoardColumn'
export type { BoardColumnProps } from './BoardColumn'

export { DataTable } from './DataTable'
export type { DataTableColumn, DataTableProps } from './DataTable'

export { TextField } from './TextField'
export type { TextFieldProps } from './TextField'

export { TextArea } from './TextArea'
export type { TextAreaProps } from './TextArea'

export { SelectField } from './SelectField'
export type { SelectFieldProps, SelectOption } from './SelectField'

export { Alert } from './Alert'
export type { AlertProps, AlertTone } from './Alert'

export { EmptyState } from './EmptyState'
export type { EmptyStateProps } from './EmptyState'

export { SkeletonCard } from './SkeletonCard'
export type { SkeletonCardProps } from './SkeletonCard'

export { ConfirmModal } from './ConfirmModal'
export type { ConfirmModalProps } from './ConfirmModal'

export { ContentModal } from './ContentModal'
export type { ContentModalProps } from './ContentModal'

export { DetailSheet } from './DetailSheet'
export type { DetailSheetProps } from './DetailSheet'

export { TabBar } from './TabBar'
export type { TabBarItem, TabBarProps } from './TabBar'

export { SidebarNav } from './SidebarNav'
export type { SidebarNavItem, SidebarNavProps } from './SidebarNav'

export { MarkdownViewer } from './MarkdownViewer'
export type { MarkdownViewerProps } from './MarkdownViewer'

export { AttachmentRow } from './AttachmentRow'
export type { AttachmentRole, AttachmentRowProps } from './AttachmentRow'

export { Segmented } from './Segmented'
export type { SegmentedOption, SegmentedProps } from './Segmented'


export { Chip } from './Chip'
export type { ChipProps } from './Chip'
