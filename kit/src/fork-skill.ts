/**
 * 技能（SKILL）注册 —— `echuan-fork-delegation` 的注册出口。
 *
 * 本技能只讲 fork 协议：判断时机、Work Order、子 agent 边界、父 agent 回收。
 * 文档格式 / 验收标准属于各角色包，不在本技能定义。
 */
import { readFileSync } from 'node:fs'
import type { Context } from '@deepseek-ai/cordis'
import type { SkillRegistration } from '@deepseek-ai/dsh-skill'

/** Fork 委派技能标识符。 */
export const ECHUAN_FORK_SKILL_NAME = 'echuan-fork-delegation'

/** 面向模型的路由描述（≤500 字符）。 */
export const ECHUAN_FORK_SKILL_DESCRIPTION =
  '驿传 fork 委派协议：判断何时把长文档任务 fork 给子 agent、如何写 Work Order、'
  + '子 agent 只写文件并返回结果、父 agent 如何回收与处理超时。'
  + '需要继承当前会话上下文且正文较长时加载。'

/** 补充路由提示。 */
export const ECHUAN_FORK_SKILL_WHEN_TO_USE =
  '需要生成大文档 / 长报告，且任务需要继承当前会话上下文；'
  + '或需要判断该不该 fork、如何约束子 agent 收手时。'

const GUIDE_URL = new URL('./assets/echuan-fork-delegation.md', import.meta.url)

/** 读取 fork 委派技能正文。 */
export function loadEchuanForkSkillGuide(): string {
  return readFileSync(GUIDE_URL, 'utf8')
}

/** 组装 fork 委派技能注册对象。 */
export function createEchuanForkSkillRegistration(): SkillRegistration {
  return {
    name: ECHUAN_FORK_SKILL_NAME,
    description: ECHUAN_FORK_SKILL_DESCRIPTION,
    whenToUse: ECHUAN_FORK_SKILL_WHEN_TO_USE,
    source: 'runtime',
    content: loadEchuanForkSkillGuide(),
    invocation: { modelInvocable: true, userInvocable: true },
  }
}

/** 将 fork 委派技能注册到当前上下文，返回注销器。 */
export function registerEchuanForkSkill(ctx: Context): () => void {
  return ctx.skills.register(createEchuanForkSkillRegistration())
}
