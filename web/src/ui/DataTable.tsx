// ui/DataTable —— 产品 / 项目 / 执行节点的列表，design/components.md §ui/DataTable
//
// 结构：thead（全大写小字号）→ 行（首列标题 + 副标题 mono）→ 行内操作。
// 状态：rest / hover / focus-visible（行内按钮）/ disabled（行不可用）/ loading（表体换骨架行）/ warn-row（整行淡底）。
// 可访问性：保留 <table> 语义；移动端转卡片式时每格带 data-label（字段名，见 ui.css 的媒体块）。
import type { ReactNode } from 'react'
import { Skeleton, Table } from '@mantine/core'
import { cssVar } from '../theme/tokens'
import { cx } from './internal'

export interface DataTableColumn<Row> {
  /** 列标识（同一表格内唯一） */
  key: string
  /** 表头文案（字符串时同时写入 data-label 供移动端卡片式展示） */
  header: ReactNode
  /** 单元格渲染；缺省取 row[key] */
  render?: (row: Row) => ReactNode
  align?: 'left' | 'right' | 'center'
  /** 该列是否是「行操作」列（移动端卡片式下不显示字段名） */
  actions?: boolean
}

export interface DataTableProps<Row> {
  columns: Array<DataTableColumn<Row>>
  rows: Row[]
  getRowKey: (row: Row) => string
  /** 加载中：表体换骨架行 */
  loading?: boolean
  loadingRows?: number
  /** 空态（建议传 EmptyState）；不传则用默认文案 */
  empty?: ReactNode
  /** 整行提示态（如心跳过期 / 已关闭项目） */
  isWarnRow?: (row: Row) => boolean
  /** 行不可用（操作置灰） */
  isDisabledRow?: (row: Row) => boolean
  onRowClick?: (row: Row) => void
  /** 表格的无障碍名称 */
  ariaLabel: string
  className?: string
}

export function DataTable<Row>({
  columns,
  rows,
  getRowKey,
  loading = false,
  loadingRows = 3,
  empty,
  isWarnRow,
  isDisabledRow,
  onRowClick,
  ariaLabel,
  className,
}: DataTableProps<Row>) {
  const showEmpty = !loading && rows.length === 0

  return (
    <Table
      className={cx('ui-data-table', className)}
      aria-label={ariaLabel}
      highlightOnHover={Boolean(onRowClick)}
      verticalSpacing={cssVar('space-3')}
      horizontalSpacing={cssVar('space-3')}
      style={{
        borderCollapse: 'separate',
        borderSpacing: 0,
      }}
    >
      <Table.Thead>
        <Table.Tr>
          {columns.map((column) => (
            <Table.Th key={column.key} data-align={column.align ?? 'left'} scope="col">
              {column.header}
            </Table.Th>
          ))}
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {loading
          ? Array.from({ length: loadingRows }, (_, rowIndex) => (
              <Table.Tr key={`skeleton-${rowIndex}`} aria-hidden="true">
                {columns.map((column) => (
                  <Table.Td key={column.key}>
                    <Skeleton className="ui-skeleton" height={cssVar('space-3')} radius="sm" />
                  </Table.Td>
                ))}
              </Table.Tr>
            ))
          : rows.map((row) => {
              const warn = isWarnRow?.(row) ?? false
              const disabled = isDisabledRow?.(row) ?? false
              return (
                <Table.Tr
                  key={getRowKey(row)}
                  data-warn={warn || undefined}
                  data-disabled={disabled || undefined}
                  onClick={onRowClick ? () => onRowClick(row) : undefined}
                  tabIndex={onRowClick ? 0 : undefined}
                  onKeyDown={
                    onRowClick
                      ? (event) => {
                          // 只在事件源是「行本身」（tr 拿到焦点）时响应 Enter/Space。
                          // 行内控件（行操作按钮 / 链接）把焦点接走时，按键属于它们自己的语义：
                          // 这里若继续 preventDefault，按钮的默认点击会被吃掉 → 键盘路径上行内主操作
                          // 变成开抽屉（鼠标路径因 stopPropagation 正常），行为与鼠标不一致。
                          // 所以事件来自行内控件时直接放行，不拦截、不开抽屉。
                          if (event.target !== event.currentTarget) return
                          if (event.key === 'Enter' || event.key === ' ') {
                            event.preventDefault()
                            onRowClick(row)
                          }
                        }
                      : undefined
                  }
                  style={onRowClick ? { cursor: 'pointer' } : undefined}
                >
                  {columns.map((column, index) => (
                    <Table.Td
                      key={column.key}
                      data-align={column.align ?? 'left'}
                      data-label={typeof column.header === 'string' && !column.actions ? column.header : undefined}
                      data-first={index === 0 || undefined}
                    >
                      {column.render
                        ? column.render(row)
                        : String((row as Record<string, unknown>)[column.key] ?? '')}
                    </Table.Td>
                  ))}
                </Table.Tr>
              )
            })}
        {showEmpty ? (
          <Table.Tr>
            <Table.Td colSpan={columns.length} data-empty="true">
              {empty ?? '暂无数据'}
            </Table.Td>
          </Table.Tr>
        ) : null}
      </Table.Tbody>
    </Table>
  )
}
