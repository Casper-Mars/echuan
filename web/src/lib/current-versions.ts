// lib/current-versions —— 「当前版本」的唯一判定处（技术方案 v0.5 §5.2，纯函数）
//
// 只做投影，不做 IO：不解析标题、不看 materials.version（线上恒 1）、不依赖服务端「当前」概念。
// 任务详情按 kind 分组、项目资料按 (task_id, kind) 分组，两处共用下面这一套规则 R-a…R-h：
//   R-a role=input 永不隐藏、位置不变；
//   R-b 只有 role=output 且 kind ∈ {requirement, design, plan, report} 参与去重，context / other 按条显示；
//   R-c 分组键由调用方注入（任务详情 = kind，项目资料 = (task_id, kind)）；
//   R-d 组内保留接口顺序的最后一条（接口顺序即交付插入顺序），输出保持原数组顺序；
//   R-e 简报（kind=context 且有 source_material_id）仅当其 source 被丢弃时同隐；
//   R-f 真孤儿简报（source 从未出现在返回集合里）与无 source 的 context 一律保留；
//   R-g 项目资料条目缺 task_id → 每条各成一版（旧后端窗口，宁可多显示不静默藏件）；
//   R-h reviewTargetId：最后一条 kinds 1–4 的 role=output；空集合 → null（提交时不带 material_id）。

/** 版本投影的最小形状：`api/types.ts` 的 AttachmentRef / ProjectMaterial 都结构性满足它 */
export interface VersionRef {
  material_id: string
  title: string
  /** material kind：requirement|design|plan|report|context|other */
  kind: string
  role: 'input' | 'output'
  /** 简报指向它解释的那一版正文（服务端唯一约束保证一对一） */
  source_material_id?: string
  /** 产出这条材料的任务业务号；任务详情不需要，项目资料的 (task, kind) 分组需要 */
  task_id?: string
}

/**
 * 参与去重的正文 kinds —— 必须与服务端 LatestOutput 的 kinds 1–4 同口径
 * （`internal/store/v3/attachment.go`、`internal/domain/enum/enum.go` 的 MaterialRequirement…MaterialReport）。
 * 改这里等于改「当前正文」定义，跨端漂移会直接让审计指向别的版本（方案 §11 R3）。
 */
const VERSIONED_KINDS: ReadonlySet<string> = new Set(['requirement', 'design', 'plan', 'report'])

function isVersionedBody(ref: VersionRef): boolean {
  return ref.role === 'output' && VERSIONED_KINDS.has(ref.kind)
}

/** 简报的 source 是否可判定；判定不了（缺字段/空串）就当作普通行保留（R-f） */
function briefSourceOf(ref: VersionRef): string | null {
  if (ref.kind !== 'context') return null
  const source = (ref.source_material_id ?? '').trim()
  return source === '' ? null : source
}

/**
 * 投影主流程：先按组算「被覆盖的旧版」，再让简报跟随其 source，最后按原顺序过滤。
 * `groupKeyOf` 返回 null 表示「不可判定归属」的条目——一律保留（R-a / R-b / R-g 的共同出口）。
 *
 * 丢弃按**下标**记账而不是 material_id：同一 material_id 可以同时出现为 input 与 output 两行
 * （例如把旧版正文补挂成输入附件），按 id 记会把那条 input 行一起抹掉，直接违反 R-a。
 */
function project<T extends VersionRef>(items: T[], groupKeyOf: (ref: T) => string | null): T[] {
  const lastIndexByGroup = new Map<string, number>()
  items.forEach((ref, index) => {
    const key = groupKeyOf(ref)
    if (key !== null) lastIndexByGroup.set(key, index)
  })

  const droppedIndexes = new Set<number>()
  const droppedIds = new Set<string>()
  items.forEach((ref, index) => {
    const key = groupKeyOf(ref)
    if (key !== null && lastIndexByGroup.get(key) !== index) {
      droppedIndexes.add(index)
      droppedIds.add(ref.material_id)
    }
  })

  // 简报链：source 被丢弃 → 简报同隐。正常只有一层，循环兜住「简报的 source 又是简报」这类异常数据。
  for (let changed = droppedIds.size > 0; changed; ) {
    changed = false
    items.forEach((ref, index) => {
      if (droppedIndexes.has(index)) return
      const source = briefSourceOf(ref)
      if (source !== null && droppedIds.has(source)) {
        droppedIndexes.add(index)
        droppedIds.add(ref.material_id)
        changed = true
      }
    })
  }

  if (droppedIndexes.size === 0) return items.slice()
  return items.filter((_, index) => !droppedIndexes.has(index))
}

/** 任务详情：同一任务内按 kind 保留最后一条正文输出（简报跟随，R-e） */
export function currentTaskVersions<T extends VersionRef>(items: T[]): T[] {
  return project(items, (ref) => (isVersionedBody(ref) ? ref.kind : null))
}

/** 项目资料：按 (task_id, kind) 保留最后一条；缺 task_id 的条目各成一版，不隐藏（R-g） */
export function currentProjectMaterials<T extends VersionRef>(items: T[]): T[] {
  return project(items, (ref) => {
    if (!isVersionedBody(ref)) return null
    const taskId = (ref.task_id ?? '').trim()
    if (taskId === '') return null
    // 分隔符不会出现在业务号里（`t_` + base32），避免 `a|b` 与 `a` + `|b` 撞键
    return `${taskId}\u0000${ref.kind}`
  })
}

/** 评审目标：最后一条 kinds 1–4 的 role=output；空集合 → null（R-h） */
export function reviewTargetId(items: VersionRef[]): string | null {
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const ref = items[index]
    if (isVersionedBody(ref)) return ref.material_id
  }
  return null
}
