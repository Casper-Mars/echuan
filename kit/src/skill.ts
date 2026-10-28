/**
 * 技能（SKILL）注册 —— `echuan-task-execution` 的**唯一注册出口**。
 *
 * 职责：
 *   1. 读取随构建产物发布的技能正文（`./assets/echuan-task-execution.md`）；
 *   2. 组装为运行时技能贡献（`SkillRegistration`）；
 *   3. 经 `ctx.skills.register()` 注册到当前上下文作用域，并返回注销器。
 *
 * 正文来源：`src/assets/echuan-task-execution.md`（纯指令、无副作用）。
 *   `.md` 不属于 TS 编译产物，`tsc` 不搬运；由 `scripts/copy-assets.mjs` 在
 *   `pnpm build` 时复制到 `lib/assets/echuan-task-execution.md`，使源码态
 *   （`src/skill.ts` → `src/assets/...`）与编译态（`lib/skill.js` → `lib/assets/...`）
 *   的相对路径一致，因此本文件在 vitest 与运行时均可加载同一份正文。
 *
 * 依赖：调用方（`index.ts`）需 `inject` 含 `'skills'`，保证 `ctx.skills` 就绪；
 *   `@deepseek-ai/dsh-skill` 仅用于类型（编译期擦除，无运行时裸包导入）。
 *
 * 注册生命周期：`ctx.skills.register()` 返回的注销器由调用方经 `ctx.effect` 管理，
 *   插件 fiber 卸载（含 HMR 重载、profile 停用）时随插件一并注销该技能。
 */
import { readFileSync } from 'node:fs'
import type { Context } from '@deepseek-ai/cordis'
import type { SkillRegistration } from '@deepseek-ai/dsh-skill'

/** 技能标识符（kebab-case，满足 `^[a-z0-9]+(?:-[a-z0-9]+)*$`）。 */
export const ECHUAN_SKILL_NAME = 'echuan-task-execution'

/**
 * 面向模型的路由描述（供技能目录展示，长度须 ≤500 字符）。
 *
 * 说清「何时该加载」比复述正文更有用：正文在加载后才进入上下文。
 */
export const ECHUAN_SKILL_DESCRIPTION =
  '驿传（echuan）任务卡的执行流程指南：拿到 Worker 派单后如何直接开工、开工前该读哪些上下文'
  + '（echuan_project_get / echuan_material_get）、完成后如何用 echuan_task_report 做三段式汇报、'
  + '卡住时如何汇报、手动领卡何时用 echuan_task_renew 续租，以及验收/返工/取消/改状态等门禁禁区。'
  + '收到 echuan 任务派单、需要按驿传流程开工或汇报时加载。'

/** 补充路由提示（模型侧/用户侧路由参考）。 */
export const ECHUAN_SKILL_WHEN_TO_USE =
  '收到驿传任务卡派单、准备开工时；需要按驿传流程读项目方案/资料、提交三段式报告、'
  + '处理租约续租，或不确定哪些门禁动作不能做时。'

/**
 * 技能正文在运行时的绝对路径。
 *
 * 相对本模块（编译产物 `lib/skill.js`，源码 `src/skill.ts`）解析
 * `./assets/echuan-task-execution.md`，两种形态下均指向同名相对目录。
 */
const GUIDE_URL = new URL('./assets/echuan-task-execution.md', import.meta.url)

/**
 * 读取技能正文（Markdown）。
 *
 * @returns 技能正文。
 * @throws {Error} 正文缺失或不可读（属构建/打包缺陷，应显式暴露）。
 */
export function loadEchuanSkillGuide(): string {
  return readFileSync(GUIDE_URL, 'utf8')
}

/**
 * 组装运行时技能注册对象（无副作用，便于单测断言）。
 *
 * `source: 'runtime'` 表示运行时贡献；`invocation` 同时开放模型侧与用户侧，
 * 使技能既能被模型按描述自动加载，也能被用户显式调用。
 */
export function createEchuanSkillRegistration(): SkillRegistration {
  return {
    name: ECHUAN_SKILL_NAME,
    description: ECHUAN_SKILL_DESCRIPTION,
    whenToUse: ECHUAN_SKILL_WHEN_TO_USE,
    source: 'runtime',
    content: loadEchuanSkillGuide(),
    invocation: { modelInvocable: true, userInvocable: true },
  }
}

/**
 * 将 `echuan-task-execution` 技能注册到当前上下文（唯一注册出口）。
 *
 * @param ctx 插件上下文（需 `inject` 含 `'skills'`，`ctx.skills` 就绪）。
 * @returns 注销器：由调用方绑定到 `ctx.effect`，插件卸载时注销该技能。
 */
export function registerEchuanSkill(ctx: Context): () => void {
  return ctx.skills.register(createEchuanSkillRegistration())
}
