/**
 * 派单消息的**唯一**渲染出口。
 *
 * 只放平台事实：工作项编号、类别、标题、项目、产品、仓库、开发分支、状态、
 * 租约、返工原因、任务正文、上游与输入物料。
 *
 * 不放任何操作指示：「怎么开工、谁来做、怎么交付、开不开收尾 PR」属于各领域
 * 角色自己的规范。写进派单消息会让平台与领域互相耦合——实测事故：dev-leader
 * 会话收到的派单里带「只建任务 PR」这类指示，加上技能里的「收到派单直接开工」，
 * 直接把 Leader 压成了执行者。
 *
 * v2 心跳派单（worker.ts）与 v3 领卡派单（pull-worker.ts）共用本函数，
 * 两条路径的措辞与字段永远一致。
 */

/** kind 的中文名（只用于渲染，不参与任何判断）。 */
const KIND_LABELS: Record<string, string> = {
  requirement: '需求',
  design: '方案',
  breakdown: '拆解',
  dev: '开发',
}

/** 渲染派单消息所需的事实；缺失字段整行不渲染，不用占位噪声填满。 */
export interface DispatchFacts {
  workId: string
  kind: string
  title: string
  projectId: string
  projectName?: string | undefined
  productId?: string | undefined
  productName?: string | undefined
  gitUrl?: string | undefined
  devBranch?: string | undefined
  status?: string | undefined
  attempt?: number | undefined
  maxAttempts?: number | undefined
  priority?: number | undefined
  leaseId?: string | null | undefined
  leaseUntil?: string | null | undefined
  leaseTtlSeconds?: number | undefined
  rework?: boolean | undefined
  reason?: string | null | undefined
  descriptionMd?: string | undefined
  contextMd?: string | undefined
  upstream?: ReadonlyArray<{
    taskId: string
    materialId?: string | null | undefined
    title?: string | null | undefined
  }> | undefined
  inputs?: ReadonlyArray<{
    materialId: string
    kind?: string | undefined
    role?: string | undefined
    title?: string | undefined
  }> | undefined
}

/**
 * 渲染派单消息。
 *
 * @param facts 平台事实；空字符串/null 视为缺失。
 * @returns 稳定的纯事实文案，首行固定为 `【驿传派单】`。
 */
export function renderDispatchFacts(facts: DispatchFacts): string {
  const lines: string[] = ['【驿传派单】', '', `工作项编号：${facts.workId}`]

  const kindLabel = KIND_LABELS[facts.kind]
  lines.push(`类别：${facts.kind}${kindLabel === undefined ? '' : `（${kindLabel}）`}`)
  lines.push(`标题：${facts.title}`)
  lines.push(
    facts.projectName === undefined || facts.projectName.trim() === ''
      ? `项目：${facts.projectId}`
      : `项目：${facts.projectId}（${facts.projectName}）`,
  )

  if (facts.productName !== undefined && facts.productName.trim() !== '') {
    lines.push(
      facts.productId === undefined || facts.productId.trim() === ''
        ? `产品：${facts.productName}`
        : `产品：${facts.productName}（${facts.productId}）`,
    )
  }
  if (facts.gitUrl !== undefined && facts.gitUrl.trim() !== '') lines.push(`仓库：${facts.gitUrl}`)
  if (facts.devBranch !== undefined && facts.devBranch.trim() !== '') {
    lines.push(`开发分支：${facts.devBranch.trim()}`)
  }

  const attemptText = facts.attempt === undefined
    ? undefined
    : `尝试 ${facts.attempt}${facts.maxAttempts === undefined ? '' : `/${facts.maxAttempts}`}`
  const statusParts = [facts.status, attemptText].filter(
    (part): part is string => part !== undefined && part !== '',
  )
  if (facts.priority !== undefined) statusParts.push(`优先级：${facts.priority}`)
  if (statusParts.length > 0) lines.push(`状态：${statusParts.join('，')}`)

  if (facts.leaseId !== undefined && facts.leaseId !== null && facts.leaseId !== '') {
    const until = facts.leaseUntil ?? ''
    const ttl = facts.leaseTtlSeconds === undefined ? '' : `（TTL ${facts.leaseTtlSeconds}s）`
    lines.push(`租约：lease_id=${facts.leaseId}，到期 ${until}${ttl}`)
  }

  const reason = facts.reason?.trim() ?? ''
  if (facts.rework === true || reason !== '') {
    lines.push(
      '',
      '【返工】本卡是上一版结果被驳回后重新派单的，不是首次执行。',
      `驳回原因：${reason === '' ? '（平台未给出原因）' : reason}`,
    )
  }

  const description = facts.descriptionMd?.trim() ?? ''
  if (description !== '') lines.push('', '任务描述：', description)

  const context = facts.contextMd?.trim() ?? ''
  if (context !== '') lines.push('', '任务上下文：', context)

  const upstream = facts.upstream ?? []
  lines.push('', '上游产出：')
  if (upstream.length === 0) lines.push('- 无')
  else {
    for (const item of upstream) {
      const material = item.materialId === undefined || item.materialId === null || item.materialId === ''
        ? '（无）'
        : item.materialId
      const title = item.title === undefined || item.title === null || item.title === ''
        ? ''
        : ` title=${item.title}`
      lines.push(`- task_id=${item.taskId} material_id=${material}${title}`)
    }
  }

  const inputs = facts.inputs ?? []
  lines.push('', '输入物料：')
  if (inputs.length === 0) lines.push('- 无')
  else {
    for (const input of inputs) {
      const parts = [`material_id=${input.materialId}`]
      if (input.kind !== undefined && input.kind !== '') parts.push(`kind=${input.kind}`)
      if (input.role !== undefined && input.role !== '') parts.push(`role=${input.role}`)
      if (input.title !== undefined && input.title !== '') parts.push(`title=${input.title}`)
      lines.push(`- ${parts.join(' ')}`)
    }
  }

  return lines.join('\n')
}
