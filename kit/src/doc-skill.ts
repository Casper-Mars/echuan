/**
 * 技能（SKILL）注册 —— `echuan-doc-writing` 的注册出口。
 *
 * 职责与 `skill.ts` 相同，只是正文换成文档写作规范：
 *   1. 读取 `./assets/echuan-doc-writing.md`；
 *   2. 组装为运行时技能贡献；
 *   3. 经 `ctx.skills.register()` 注册，返回注销器。
 *
 * 文档方法论与工作项协议分开：任务协议讲“怎么交”，本技能讲“文档怎么写”。
 */
import { readFileSync } from 'node:fs'
import type { Context } from '@deepseek-ai/cordis'
import type { SkillRegistration } from '@deepseek-ai/dsh-skill'

/** 文档写作技能标识符。 */
export const ECHUAN_DOC_SKILL_NAME = 'echuan-doc-writing'

/** 面向模型的路由描述（≤500 字符）。 */
export const ECHUAN_DOC_SKILL_DESCRIPTION =
  '驿传文档写作规范：需求 PRD、技术方案、开发报告、决策简报的结构与模板，'
  + '证据 / 假设 / 待决策的区分，写作规则与提交前自检。'
  + '收到需要产出 Markdown 文档的工作项时加载。'

/** 补充路由提示。 */
export const ECHUAN_DOC_SKILL_WHEN_TO_USE =
  '需要写需求文档、技术方案、开发报告或决策简报，'
  + '或需要检查文档结构、证据与验收标准时。'

const GUIDE_URL = new URL('./assets/echuan-doc-writing.md', import.meta.url)

/** 读取文档写作技能正文。 */
export function loadEchuanDocSkillGuide(): string {
  return readFileSync(GUIDE_URL, 'utf8')
}

/** 组装文档写作技能注册对象。 */
export function createEchuanDocSkillRegistration(): SkillRegistration {
  return {
    name: ECHUAN_DOC_SKILL_NAME,
    description: ECHUAN_DOC_SKILL_DESCRIPTION,
    whenToUse: ECHUAN_DOC_SKILL_WHEN_TO_USE,
    source: 'runtime',
    content: loadEchuanDocSkillGuide(),
    invocation: { modelInvocable: true, userInvocable: true },
  }
}

/** 将文档写作技能注册到当前上下文，返回注销器。 */
export function registerEchuanDocSkill(ctx: Context): () => void {
  return ctx.skills.register(createEchuanDocSkillRegistration())
}
