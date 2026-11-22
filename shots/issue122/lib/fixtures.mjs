// TaskCard 几何 / 断行取证的确定性任务数据（三页共用同一份，保证「三处复用一致」可比）。
//
// 覆盖的状态矩阵数据面：
//   rest（普通卡）/ 多标签（kind + P + 尝试 N/M + 时间）/ 长标题（≥120 全角）/
//   长不可断串（100+ 无空格 URL）/ 长 task_id（mono 单行省略 + title 完整值）
//   in_review 首卡（focused 变体）/ ready（队列页有数据，不走「未复核」豁免）
const PAD2020 = 'PaddingBugTokenXYZ0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijklmnopqrstuvwxyz__END'
// 120+ 全角字符（中文按字换行路径）
const LONG_TITLE = '任务卡片排版回归：这是一条用于验证三行省略与行尾省略标记的超长中文标题，'.repeat(4)
// 100+ 字符无空格串（URL / token 路径：必须按字符断行，不得横向溢出）
const LONG_TOKEN = `https://example.com/${PAD2020}?q=${PAD2020.toLowerCase()}&r=${PAD2020}`
const LONG_TASK_ID = 't_01M81W481ESJGYM9BGF3NMVWYS_0123456789abcdefghijklmnopqrstuvwxyz'

let seq = 0
function task(projectId, status, patch) {
  seq += 1
  const base = {
    task_id: `t_01M8BF${String(seq).padStart(23, '0')}`,
    project_id: projectId,
    parent_task_id: null,
    kind: 'dev',
    title: `任务卡片 ${seq}：看板列内常规标题`,
    description_md: '',
    context_md: '',
    priority: 0,
    status,
    attempt: 1,
    max_attempts: 3,
    lease_id: null,
    lease_until: null,
    worker_id: null,
    reason: null,
    create_time: '2026-11-22T02:00:00Z',
    update_time: `2026-11-22T1${seq % 10}:12:00Z`,
  }
  return { ...base, ...patch }
}

/** 三页共用的同一份任务集：board 全量、inbox 取 in_review + pending + ready、queue 只取 ready。 */
export function fixtures() {
  seq = 0
  const projectId = 'echuan-6'
  const other = 'echuan-5'
  const tasks = [
    // in_review：本列首卡即 focused 变体（看板 + 收件箱「待审核」首卡）
    task(projectId, 'in_review', { title: '待审核首卡：品牌描边聚焦变体', priority: 60, update_time: '2026-11-22T11:12:00Z' }),
    task(projectId, 'in_review', { title: LONG_TITLE, priority: 30, kind: 'requirement', attempt: 2 }),
    task(projectId, 'in_review', { title: LONG_TOKEN, priority: 20, kind: 'design' }),
    task(projectId, 'in_review', { title: '长 task_id 样例（mono 单行省略）', priority: 10, task_id: LONG_TASK_ID }),

    // pending：看板「待领取」列，收件箱「待领取」区
    task(projectId, 'pending', { title: '看板常规卡：内距与状态条回归', priority: 40, kind: 'breakdown' }),
    task(projectId, 'pending', { title: LONG_TITLE, priority: 35, kind: 'requirement', attempt: 1, max_attempts: 3 }),
    task(projectId, 'pending', { title: LONG_TOKEN, priority: 25, kind: 'dev', attempt: 2, max_attempts: 5 }),
    task(projectId, 'pending', { title: '长 task_id 样例（mono 单行省略）', priority: 15, task_id: LONG_TASK_ID }),
    task(projectId, 'pending', { title: '多标签卡：kind + 优先级 + 尝试 + 更新时间', priority: 99, kind: 'breakdown', attempt: 3, max_attempts: 3 }),

    // ready：队列页数据来源（有数据，不做「未复核」豁免）
    task(projectId, 'ready', { title: '派单队列常规卡', priority: 50, kind: 'dev', attempt: 1, max_attempts: 3 }),
    task(projectId, 'ready', { title: LONG_TITLE, priority: 45, kind: 'requirement', attempt: 1, max_attempts: 2 }),
    task(projectId, 'ready', { title: LONG_TOKEN, priority: 40, kind: 'design' }),
    task(projectId, 'ready', { title: '长 task_id 样例（mono 单行省略）', priority: 30, task_id: LONG_TASK_ID }),
    task(projectId, 'ready', { title: '多标签卡：kind + 优先级 + 尝试 + 更新时间', priority: 88, kind: 'breakdown', attempt: 2, max_attempts: 4 }),
    task(other, 'ready', { title: '另一项目的 ready 卡（队列分组用）', priority: 10, kind: 'design' }),

    // 其余列：列骨架与非首卡状态
    task(projectId, 'in_progress', { title: '进行中的任务', priority: 20, kind: 'dev' }),
    task(projectId, 'done', { title: '已完成的任务', priority: 0, kind: 'dev' }),
    task(projectId, 'done', { title: '已完成的任务 2', priority: 0, kind: 'design' }),
    task(projectId, 'failed', { title: '失败的任务', priority: 0, kind: 'dev' }),
    task(projectId, 'cancelled', { title: '已取消的任务', priority: 0, kind: 'dev' }),
  ]
  return { tasks, projectId, other, LONG_TITLE, LONG_TOKEN, LONG_TASK_ID }
}

export const MARKERS = {
  longTitle: '任务卡片排版回归',
  longToken: 'https://example.com/',
  longTaskId: LONG_TASK_ID,
}
