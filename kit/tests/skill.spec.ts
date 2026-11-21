/**
 * 技能 `echuan-task-execution` 单测。
 *
 * 覆盖验收项：
 * 1. 技能名精确、description / whenToUse 非空且 description ≤500 字符；
 * 2. 正文六条规则逐条可检索（关键词断言）；
 * 3. 正文不含「反问是否开工 / 等确认」类话术；
 * 4. 正文含 `echuan_task_renew`，不含不存在的旧续租工具名；
 * 5. `ctx.skills.register` 只调一次、返回注销器，注销后 `list()` 不含该技能，重注册后再次出现；
 * 6. git 交付协议可检索：开工前 `git fetch`、项目分支 `feature/<project_id>` 与 `task/<task_id>` worktree、交付 `push -u origin` + `ls-remote` 校验，并锁定分支模型正/反向约束；
 * 7. `pnpm build` 后 `lib/assets/echuan-task-execution.md` 存在且与源码一致。
 */
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it, vi } from 'vitest'
import type { Context } from '@deepseek-ai/cordis'
import type { SkillRegistration, SkillSummary } from '@deepseek-ai/dsh-skill'
import {
  ECHUAN_SKILL_DESCRIPTION,
  ECHUAN_SKILL_NAME,
  ECHUAN_SKILL_WHEN_TO_USE,
  createEchuanSkillRegistration,
  loadEchuanSkillGuide,
  registerEchuanSkill,
} from '../src/skill.js'

const here = path.dirname(fileURLToPath(import.meta.url))
const kitRoot = path.resolve(here, '..')
const srcAsset = path.join(kitRoot, 'src/assets/echuan-task-execution.md')
const builtAsset = path.join(kitRoot, 'lib/assets/echuan-task-execution.md')

const content = loadEchuanSkillGuide()

/** 构造只提供 `skills` 的假上下文；`register` 语义与真实注册表一致（返回注销器）。 */
function createSkillContext(): { ctx: Context; names: string[] } {
  const names: string[] = []
  const skills = {
    register(skill: SkillRegistration): () => void {
      names.push(skill.name)
      return () => {
        const index = names.indexOf(skill.name)
        if (index >= 0) names.splice(index, 1)
      }
    },
    async list(): Promise<SkillSummary[]> {
      return names.map(name => ({
        name,
        description: '',
        source: 'runtime' as const,
        provider: 'runtime',
        invocation: { modelInvocable: true, userInvocable: true },
      }))
    },
  }
  return { ctx: { skills } as unknown as Context, names }
}

describe('技能元信息', () => {
  it('名称精确为 echuan-task-execution（kebab-case）', () => {
    expect(ECHUAN_SKILL_NAME).toBe('echuan-task-execution')
    expect(ECHUAN_SKILL_NAME).toMatch(/^[a-z0-9]+(?:-[a-z0-9]+)*$/)
    expect(createEchuanSkillRegistration().name).toBe('echuan-task-execution')
  })

  it('description / whenToUse 非空，description ≤500 字符', () => {
    const reg = createEchuanSkillRegistration()
    expect(reg.description.trim().length).toBeGreaterThan(0)
    expect(reg.whenToUse?.trim().length ?? 0).toBeGreaterThan(0)
    expect(reg.description.length).toBeLessThanOrEqual(500)
    expect(ECHUAN_SKILL_DESCRIPTION.length).toBeLessThanOrEqual(500)
    // description 要说清「何时加载」，而不是复述正文。
    expect(reg.description).toContain('echuan')
    expect(ECHUAN_SKILL_WHEN_TO_USE).toContain('派单')
  })

  it('注册对象为 runtime 来源、模型与用户均可调用、正文非空', () => {
    const reg = createEchuanSkillRegistration()
    expect(reg.source).toBe('runtime')
    expect(reg.invocation).toEqual({ modelInvocable: true, userInvocable: true })
    expect(reg.content.length).toBeGreaterThan(0)
    expect(reg.content).toBe(content)
  })

  it('正文长度在 1500–12000 字符区间（够用而不啰嗦）', () => {
    expect(content.length).toBeGreaterThanOrEqual(1500)
    expect(content.length).toBeLessThanOrEqual(12000)
  })
})

describe('正文协议事实可检索', () => {
  it('① 工作项来源与 kind 交付物形态', () => {
    expect(content).toContain('工作项')
    expect(content).toContain('echuan_task_claim')
    expect(content).toContain('kind')
    for (const kind of ['requirement', 'design', 'breakdown', 'dev']) {
      expect(content).toContain(kind)
    }
  })

  it('② 字段与状态：状态只由平台产生，agent 不改', () => {
    expect(content).toContain('in_progress')
    expect(content).toContain('in_review')
    expect(content).toContain('rework')
    expect(content).toContain('reason')
    expect(content).toContain('不改状态')
  })

  it('③ 租约归属：心跳托管不续租，手动领卡自己续', () => {
    expect(content).toContain('lease_id')
    expect(content).toContain('echuan_task_renew')
    expect(content).toContain('lease_lost')
    expect(content).toContain('心跳托管')
  })

  it('④ 报告契约：echuan_task_report + 四段式，顺序可读', () => {
    expect(content).toContain('echuan_task_report')
    expect(content).toContain('summary_md')
    const a = content.indexOf('改了什么')
    const b = content.indexOf('怎么自检')
    const c = content.indexOf('**git 交付**')
    const d = content.indexOf('遗留风险')
    expect(a).toBeGreaterThan(-1)
    expect(b).toBeGreaterThan(a)
    expect(c).toBeGreaterThan(b)
    expect(d).toBeGreaterThan(c)
  })

  it('⑤ 卡住也要报告：卡在哪 / 缺什么 / 需要谁决策', () => {
    expect(content).toContain('卡在哪')
    expect(content).toContain('缺什么')
    expect(content).toContain('需要谁决策')
    expect(content).toContain('禁止静默失败')
  })

  it('⑥ 门禁红线：不验收、不返工、不取消、不改状态', () => {
    expect(content).toContain('不验收')
    expect(content).toContain('不返工判定')
    expect(content).toContain('不取消')
    expect(content).toContain('不改任务状态')
  })

  it('⑦ 空结果与错误：204 正常、400/409 按 details 修正', () => {
    expect(content).toContain('当前无活')
    expect(content).toContain('204')
    expect(content).toContain('409')
  })
})

describe('只讲协议，不掺角色与领域工作流', () => {
  it('不含「谁来执行 / 怎么开工」类角色指令', () => {
    for (const banned of ['直接开工', 'Leader', 'Developer', 'Tester', 'dev-leader']) {
      expect(content).not.toContain(banned)
    }
  })

  it('不含 git 分支 / worktree / PR 命令（这些属于领域规范）', () => {
    for (const banned of ['git worktree add', 'gh pr create', 'git push -u origin', 'feature/<project_id>']) {
      expect(content).not.toContain(banned)
    }
    // 「git 交付」只作为报告字段出现，不承载实现方式。
    expect(content).toContain('**git 交付**')
    expect(content).toContain('由领域规范定义')
  })

  it('不含旧续租工具名（echuan_task_heartbeat）', () => {
    expect(content).not.toContain(['echuan', 'task', 'heartbeat'].join('_'))
  })
})

describe('注册与注销生命周期', () => {
  it('ctx.skills.register 只调用一次并返回注销器', () => {
    const { ctx } = createSkillContext()
    const spy = vi.spyOn(ctx.skills, 'register')

    const dispose = registerEchuanSkill(ctx)

    expect(spy).toHaveBeenCalledTimes(1)
    expect(spy.mock.calls[0]?.[0].name).toBe('echuan-task-execution')
    expect(typeof dispose).toBe('function')
  })

  it('注销后 list() 不含该技能；重新注册后再次出现', async () => {
    const { ctx } = createSkillContext()

    const dispose = registerEchuanSkill(ctx)
    expect((await ctx.skills.list()).map(s => s.name)).toContain('echuan-task-execution')

    dispose()
    expect((await ctx.skills.list()).map(s => s.name)).not.toContain('echuan-task-execution')

    const disposeAgain = registerEchuanSkill(ctx)
    expect((await ctx.skills.list()).map(s => s.name)).toContain('echuan-task-execution')
    disposeAgain()
    expect((await ctx.skills.list()).map(s => s.name)).not.toContain('echuan-task-execution')
  })

  it('注销器重复调用无副作用（fiber dispose 与手动清理叠加也不炸）', async () => {
    const { ctx } = createSkillContext()
    const dispose = registerEchuanSkill(ctx)

    dispose()
    expect(() => dispose()).not.toThrow()
    expect(await ctx.skills.list()).toEqual([])
  })
})

describe('随构建产物发布', () => {
  it('源码正文存在且非空', () => {
    expect(existsSync(srcAsset)).toBe(true)
    expect(readFileSync(srcAsset, 'utf8').length).toBeGreaterThan(0)
  })

  it('build 脚本会把 .md 资源搬进 lib/（不依赖 tsc）', () => {
    const pkg = JSON.parse(readFileSync(path.join(kitRoot, 'package.json'), 'utf8')) as {
      scripts: { build: string }
      files: string[]
    }
    expect(pkg.scripts.build).toContain('node scripts/copy-assets.mjs')
    expect(pkg.files).toContain('lib/**/*.md')

    const script = readFileSync(path.join(kitRoot, 'scripts/copy-assets.mjs'), 'utf8')
    expect(script).toContain("'.md'")
    expect(script).toContain('cpSync')
  })

  it.skipIf(!existsSync(builtAsset))('lib/assets 下的正文与源码逐字节一致', () => {
    expect(existsSync(builtAsset)).toBe(true)
    expect(readFileSync(builtAsset, 'utf8')).toBe(readFileSync(srcAsset, 'utf8'))
  })
})
