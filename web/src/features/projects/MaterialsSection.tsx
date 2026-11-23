// features/projects/MaterialsSection —— 项目详情「资料」区块
//
// 规格对照：docs/tech/project-materials-ui-v0.1.md §4；design/screens/projects.md「资料区」。
//
// 口径：
//   * 资料按任务归属，本区块只是 `GET /v1/projects/materials` 的分组投影，不改数据；
//   * 入参 `materials` 是**已收敛的可见集合**（调用方用 `lib/current-versions` 的
//     `currentProjectMaterials` 投影过，每 (任务, kind) 只剩当前版本）：分组、计数、折叠
//     都只认这一个数组，所以「计数取可见条数」是本文件不需要额外分支的自然结果；
//   * 简报（kind=context 且有 source_material_id）不单独成组，缩进挂在其正文版本下；
//     正文被版本收敛隐藏时简报一并消失（同隐在投影层完成，这里不会收到它的孤儿行）；
//     真孤儿简报（source 从未出现在返回集合）落到「其他」，不丢数据；
//   * 组内按接口插入顺序倒序（最新在前）；报告组超过 5 条折叠，阈值作用于可见行。
import { useState } from 'react'
import type { ProjectMaterial } from '../../api/types'
import { Button, EmptyState } from '../../ui'
import { ErrorBar } from '../shell/ErrorBar'

const GROUPS = [
  { kind: 'requirement', label: '需求' },
  { kind: 'design', label: '方案' },
  { kind: 'plan', label: '拆分' },
  { kind: 'report', label: '报告' },
] as const

/** 报告组默认展示条数；超出给「展开全部」 */
const REPORT_FOLD = 5

export interface MaterialsSectionProps {
  /** 已收敛的可见集合（每 (任务, kind) 只剩当前版本，简报跟随正文）：计数与分组都取它的长度 */
  materials: ProjectMaterial[]
  loading: boolean
  error: string | null
  onRetry: () => void
  onOpen: (material: ProjectMaterial) => void
}

interface Group {
  label: string
  items: ProjectMaterial[]
}

/** 把扁平的资料列表切成「分组 + 简报配对」的只读视图 */
function buildGroups(materials: ProjectMaterial[]): Group[] {
  const byId = new Set(materials.map(material => material.material_id))
  const briefs = new Map<string, ProjectMaterial>()
  const orphans: ProjectMaterial[] = []

  for (const material of materials) {
    if (material.kind !== 'context') continue
    const source = material.source_material_id
    if (source !== undefined && byId.has(source)) {
      // 同一版本理论上只有一份简报（服务端唯一索引保证），这里保留最新一条即可
      briefs.set(source, material)
    } else {
      orphans.push(material)
    }
  }

  const groups: Group[] = GROUPS.map(group => ({
    label: group.label,
    items: materials.filter(material => material.kind === group.kind).slice().reverse(),
  }))

  const known = new Set<string>([...GROUPS.map(group => group.kind), 'context'])
  const other = materials
    .filter(material => !known.has(material.kind))
    .slice()
    .reverse()
  if (orphans.length > 0) other.unshift(...orphans)

  if (other.length > 0) groups.push({ label: '其他', items: other })
  return groups.filter(group => group.items.length > 0)
}

/** 简报行：缩进一级，带「简报」徽标，和正文行一样可打开预览 */
function BriefRow({ material, onOpen }: { material: ProjectMaterial; onOpen: (m: ProjectMaterial) => void }) {
  return (
    <button
      type="button"
      className="fx-materials__row fx-materials__row--brief"
      onClick={() => onOpen(material)}
      aria-label={`简报：${material.title || material.material_id}`}
    >
      <span className="fx-materials__brief-tag">简报</span>
      <span className="fx-materials__title">{material.title || material.material_id}</span>
    </button>
  )
}

export function MaterialsSection({ materials, loading, error, onRetry, onOpen }: MaterialsSectionProps) {
  const [reportsExpanded, setReportsExpanded] = useState(false)
  const briefs = new Map(
    materials
      .filter(material => material.kind === 'context' && material.source_material_id !== undefined)
      .map(material => [material.source_material_id as string, material]),
  )
  const groups = buildGroups(materials)
  // 计数取可见条数（不是接口返回条数）：入参已是可见集合，长度即答案
  const visibleCount = materials.length

  return (
    <section className="fx-materials" data-ui="project-materials" aria-busy={loading || undefined}>
      <div className="fx-materials__head">
        <h3 className="fx-materials__title-group">资料</h3>
        <span className="fx-materials__hint">
          {loading ? '读取中' : `${visibleCount} 项 · 来自任务`}
        </span>
      </div>

      {error !== null ? (
        <ErrorBar title={error} message="项目字段不受影响，可以重试。" retryLabel="重试" onRetry={onRetry} />
      ) : null}

      {loading ? (
        <div className="fx-materials__skeleton" aria-hidden="true">
          <span className="fx-materials__skeleton-line" />
          <span className="fx-materials__skeleton-line" />
          <span className="fx-materials__skeleton-line" />
        </div>
      ) : null}

      {!loading && error === null && groups.length === 0 ? (
        <EmptyState title="暂无资料" description="这个项目下的任务还没有产出材料。" />
      ) : null}

      {!loading && error === null
        ? groups.map(group => {
            const folded = group.label === '报告' && !reportsExpanded && group.items.length > REPORT_FOLD
            const visible = folded ? group.items.slice(0, REPORT_FOLD) : group.items
            return (
              <div className="fx-materials__group" key={group.label}>
                <div className="fx-materials__group-head">
                  {group.label}
                  <span className="fx-materials__count">{group.items.length}</span>
                </div>
                <ul className="fx-materials__list">
                  {visible.map(material => {
                    const brief = briefs.get(material.material_id)
                    return (
                      <li key={material.material_id}>
                        <button
                          type="button"
                          className="fx-materials__row"
                          onClick={() => onOpen(material)}
                          aria-label={`${group.label}：${material.title || material.material_id}`}
                        >
                          <span className="fx-materials__title">{material.title || material.material_id}</span>
                          <span className="fx-materials__meta">输出</span>
                        </button>
                        {brief === undefined ? null : <BriefRow material={brief} onOpen={onOpen} />}
                      </li>
                    )
                  })}
                </ul>
                {group.label === '报告' && group.items.length > REPORT_FOLD ? (
                  <Button
                    variant="default"
                    size="sm"
                    onClick={() => setReportsExpanded(value => !value)}
                  >
                    {reportsExpanded ? '收起' : `展开全部（${group.items.length}）`}
                  </Button>
                ) : null}
              </div>
            )
          })
        : null}
    </section>
  )
}
