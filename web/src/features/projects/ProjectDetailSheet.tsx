// features/projects/ProjectDetailSheet —— 项目只读详情抽屉（屏内私有组件，不进 ui/ 导出面）
//
// 规格对照：
//   design/components.md §「屏内私有详情抽屉（不属于 ui/ 导出面）」——项目抽屉的页面私有部分落在
//     `features/projects/ProjectDetailSheet.tsx`（9 字段），键值网格复用壳层 `features/shell/DetailFields.tsx`；
//     容器复用 `ui/DetailSheet` 的 `readonly` 变体；`ui/` 导出面仍是 21 个组件（本文件不从 `ui/index.ts` 出）。
//   design/screens/projects.md「交互」——行点击 → 只读详情抽屉（9 字段：项目编号 / 产品编号 / 序号 / 名称 /
//     上下文 / 状态 / 创建时间 / 更新时间 / 关闭时间；抽屉不发请求，字段取当前行）；抽屉内不做编辑、不放动作
//     （关闭键除外），「打开看板」只留在列表行。
//   docs/tech/web-ui-102-interaction-fix-v0.1.md §7.2（D1 字段口径）/ §7.3（D2 选 A）/ §7.8（一次点击一个结果）。
//
// 口径：
//   * 只读、0 请求：字段全部从传入的当前行派生，不 copy 行对象、不发 API；
//   * 不持有打开状态：`project` 由页面按 id 从**当前** items 反查后传入，行不存在 → 抽屉自然关闭；
//   * 不写死颜色与间距：键值网格样式在 shell.css 的 .fx-detail-fields*，抽屉几何在 ui/DetailSheet；
//   * 状态沿用列表页既有映射（open→`in_progress`/进行中、closed→`cancelled`/已关闭），不新增 StatusPill 变体。
import type { Project } from '../../api/types'
import { DetailSheet, MarkdownViewer, StatusPill } from '../../ui'
import { DetailFields } from '../shell/DetailFields'
import type { DetailField } from '../shell/DetailFields'

export interface ProjectDetailSheetProps {
  /** 当前行：页面按 project_id 从当前列表数据反查（undefined = 抽屉关闭，不保留快照） */
  project: Project | undefined
  onClose: () => void
}

export function ProjectDetailSheet({ project, onClose }: ProjectDetailSheetProps) {
  // opened 由 project 派生而不是另立 prop：两者同时存在会出现「opened=true 但没有行」的中间态，
  // 那时抽屉只能显示上一个项目的残留内容——只读抽屉不允许出现这种幽灵数据。
  return (
    <DetailSheet variant="readonly" opened={project !== undefined} title={project?.name ?? ''} onClose={onClose}>
      {project ? <DetailFields fields={fieldsOfProject(project)} /> : null}
    </DetailSheet>
  )
}

/**
 * 抽屉字段（design/screens/projects.md「交互」9 项 + docs/tech §7.2 字段表）：
 * 项目编号 / 产品编号 / 序号 / 名称 / 上下文 / 状态 / 创建时间 / 更新时间 / 关闭时间。
 * 编号是长标识 → `mono` + 单行省略 + `title`；上下文走 `ui/MarkdownViewer` 只读渲染，
 * 空正文用它的 `emptyLabel` 兜住（不留空白）；时间统一 `MM-DD HH:mm`（与列表「更新时间」列同源）。
 */
function fieldsOfProject(project: Project): DetailField[] {
  return [
    { label: '项目编号', value: project.project_id, mono: true, truncate: true },
    { label: '产品编号', value: project.product_id, mono: true, truncate: true },
    // 序号与列表行的「#1」写法一致：同一屏里同一个值不出现两种形态
    { label: '序号', value: `#${project.seq_no}` },
    { label: '名称', value: textOr(project.name, '—') },
    {
      label: '上下文',
      value: <MarkdownViewer source={project.context_md} emptyLabel="未填写上下文" />,
    },
    {
      label: '状态',
      // 状态是数据不是控件：圆点 + 中文文案，沿用列表页的状态映射（方案 §3.1 S4）
      value:
        project.status === 'open' ? (
          <StatusPill status="in_progress" label="进行中" />
        ) : (
          <StatusPill status="cancelled" label="已关闭" />
        ),
    },
    { label: '创建时间', value: formatProjectTime(project.create_time) },
    { label: '更新时间', value: formatProjectTime(project.update_time) },
    // closed 项目才有关闭时间；open 行显示「—」（方案 §7.2「关闭时间：`open` → `—`」）
    { label: '关闭时间', value: project.closed_at ? formatProjectTime(project.closed_at) : '—' },
  ]
}

/**
 * ISO 时间 → 「MM-DD HH:mm」（方案 §7.2「格式沿用列表的 MM-DD HH:mm」）。
 * 列表「更新时间」列与抽屉三个时间字段共用这一个定义，避免两处各写一遍后漂移；
 * 空值 → `—`，脏值原样返回，不让一行坏数据把抽屉/表格打成空白。
 */
export function formatProjectTime(value: string | null | undefined): string {
  if (value === null || value === undefined || value.trim() === '') return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

/** 空值判据：undefined / null / 全空白都算「没有值」，由调用方给出本地化文案。 */
function textOr(value: string | null | undefined, fallback: string): string {
  return value !== null && value !== undefined && value.trim() !== '' ? value : fallback
}
