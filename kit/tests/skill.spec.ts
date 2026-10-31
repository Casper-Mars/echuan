/**
 * 技能 `echuan-task-execution` 单测。
 *
 * 覆盖验收项：
 * 1. 技能名精确、description / whenToUse 非空且 description ≤500 字符；
 * 2. 正文六条规则逐条可检索（关键词断言）；
 * 3. 正文不含「反问是否开工 / 等确认」类话术；
 * 4. 正文含 `echuan_task_renew`，不含不存在的 `echuan_task_heartbeat`；
 * 5. `ctx.skills.register` 只调一次、返回注销器，注销后 `list()` 不含该技能，重注册后再次出现；
 * 6. git 交付协议可检索：开工前 `git fetch` + `origin/<base>`、`task/<task_id>` worktree、交付 `push -u origin` + `ls-remote` 校验；
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
import { REPORT_STRUCTURE_HINT } from '../src/tools/write.js'

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

describe('正文六条规则逐条可检索', () => {
  it('① 任务来自 Worker 派单：派单直接开工，不反问', () => {
    expect(content).toContain('派单')
    expect(content).toContain('直接开工')
    expect(content).toContain('echuan_task_claim')
  })

  it('② 先读上下文：echuan_project_get 看方案，echuan_material_get 取全文', () => {
    expect(content).toContain('echuan_project_get')
    expect(content).toContain('echuan_material_get')
    expect(content).toContain('不要凭任务标题猜需求')
  })

  it('③ 干完必须汇报：echuan_task_report + 四段式（改了什么/怎么自检/git 交付/遗留风险）', () => {
    expect(content).toContain('echuan_task_report')
    expect(content).toContain('改了什么')
    expect(content).toContain('怎么自检')
    expect(content).toContain('git 交付')
    expect(content).toContain('遗留风险')
    // 四段式必须能按顺序读出：①②③④ 依次出现。
    const a = content.indexOf('改了什么')
    const b = content.indexOf('怎么自检')
    const c = content.indexOf('**git 交付**')
    const d = content.indexOf('遗留风险')
    expect(a).toBeGreaterThan(-1)
    expect(b).toBeGreaterThan(a)
    expect(c).toBeGreaterThan(b)
    expect(d).toBeGreaterThan(c)
  })

  it('④ 做不下去也要汇报：写清卡在哪、缺什么、需要谁决策', () => {
    expect(content).toContain('卡在哪')
    expect(content).toContain('缺什么')
    expect(content).toContain('需要谁决策')
    expect(content).toContain('禁止静默失败')
  })

  it('⑤ 手动模式才需续租：echuan_task_renew，Worker 派发的卡不续租', () => {
    expect(content).toContain('echuan_task_renew')
    expect(content).toContain('手动模式')
    // Worker 派发模式下不得调用续租工具（会与心跳抢租约语义）。
    const heartbeatSection = content.slice(content.indexOf('Worker 派发模式'))
    expect(heartbeatSection).toContain('不要')
    expect(heartbeatSection).toContain('续租')
  })

  it('⑥ 不碰门禁：不验收、不返工、不取消、不改状态', () => {
    expect(content).toContain('不验收')
    expect(content).toContain('不返工')
    expect(content).toContain('不取消')
    expect(content).toContain('不改任务状态')
  })

  it('补充：无活不是错误，不重试风暴', () => {
    expect(content).toContain('当前无活')
    expect(content).toContain('重试风暴')
  })
})

describe('git 交付协议', () => {
  it('开工前必须同步远程，base 用 origin/<base> 而非本地 main', () => {
    expect(content).toContain('git fetch --prune origin')
    expect(content).toContain('git symbolic-ref refs/remotes/origin/HEAD --short')
    expect(content).toContain('origin/')
    expect(content).toContain('不得用本地 `main`')
    expect(content).toContain('禁止跳过')
  })

  it('从项目分支 feature/<project_id> 的最新远程提交建独立 worktree 与 task/<task_id> 分支', () => {
    expect(content).toContain('WORK=feature/<project_id>')
    expect(content).toContain('BASE=$(git symbolic-ref refs/remotes/origin/HEAD --short 2>/dev/null | sed')
    expect(content).toContain('git worktree add ../wt-<task_id> -b task/<task_id> "origin/$WORK"')
    expect(content).toContain('task/')
    expect(content).toContain('不复用上一张卡的')
    expect(content).toContain('git worktree remove')
  })

  it('新分支上游先随 base、push -u 后改为 origin/task/<id>（属正常，不重推/不 force）', () => {
    expect(content).toContain('上游暂时设为 base')
    expect(content).toContain('不要因此重推或 force')
  })

  it('交付必须 push 并校验远程 commit 与本地 HEAD 一致', () => {
    expect(content).toContain('git push -u origin task/<task_id>')
    expect(content).toContain('git ls-remote --heads origin task/<task_id>')
    expect(content).toContain('git rev-parse HEAD')
    expect(content).toContain('没 push 成功就等于没交付')
  })

  it('报告必须带分支 / commit / 远程分支校验 / PR 或 compare 链接', () => {
    expect(content).toContain('**git 交付**')
    expect(content).toContain('commit 短 hash')
    expect(content).toContain('origin/task/<task_id>')
    expect(content).toContain('compare 链接')
  })
})

describe('报告结构提示与正文口径一致（四段）', () => {
  it('REPORT_STRUCTURE_HINT 为四段、含 git 交付与 push 前不得报完成', () => {
    expect(REPORT_STRUCTURE_HINT).toContain('四段')
    expect(REPORT_STRUCTURE_HINT).toContain('改了什么')
    expect(REPORT_STRUCTURE_HINT).toContain('怎么自检')
    expect(REPORT_STRUCTURE_HINT).toContain('git 交付')
    expect(REPORT_STRUCTURE_HINT).toContain('遗留风险')
    expect(REPORT_STRUCTURE_HINT).toContain('未 push 不得报完成')
  })

  it('技能 description / whenToUse 与正文同为四段口径，不含旧口径表述', () => {
    // 旧口径词拼接构造：避免本文件自身被旧口径检索命中。
    const legacy = '三' + '段'
    expect(ECHUAN_SKILL_DESCRIPTION).toContain('四段式（含 git 交付）')
    expect(ECHUAN_SKILL_WHEN_TO_USE).toContain('四段式（含 git 交付）')
    expect(ECHUAN_SKILL_DESCRIPTION).not.toContain(legacy)
    expect(ECHUAN_SKILL_WHEN_TO_USE).not.toContain(legacy)
    expect(REPORT_STRUCTURE_HINT).not.toContain(legacy)
  })

  it('push --force 只以禁止形式出现，且禁止直接推 main', () => {
    const idx = content.indexOf('push --force')
    expect(idx).toBeGreaterThan(-1)
    expect(content.slice(Math.max(0, idx - 6), idx)).toContain('禁止')
    expect(content).toContain('禁止直接推 `main`/`master`')
  })
})

describe('正文禁用话术与工具名约束', () => {
  it('不含反问「是否开始 / 等确认」类话术', () => {
    for (const banned of ['要不要开始', '等待确认', '请用户确认', '等确认', '是否要开始']) {
      expect(content).not.toContain(banned)
    }
  })

  it('含 echuan_task_renew，且不含不存在的 echuan_task_heartbeat', () => {
    expect(content).toContain('echuan_task_renew')
    expect(content).not.toContain('echuan_task_heartbeat')
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
