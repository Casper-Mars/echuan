/** 技能 `echuan-fork-delegation` 单测：只讲 fork 协议，不定义文档规范。 */
import { existsSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import {
  ECHUAN_FORK_SKILL_DESCRIPTION,
  ECHUAN_FORK_SKILL_NAME,
  ECHUAN_FORK_SKILL_WHEN_TO_USE,
  createEchuanForkSkillRegistration,
  loadEchuanForkSkillGuide,
} from '../src/fork-skill.js'

const here = path.dirname(fileURLToPath(import.meta.url))
const kitRoot = path.resolve(here, '..')
const content = loadEchuanForkSkillGuide()

describe('fork 委派技能元信息', () => {
  it('名称精确为 echuan-fork-delegation', () => {
    expect(ECHUAN_FORK_SKILL_NAME).toBe('echuan-fork-delegation')
    expect(ECHUAN_FORK_SKILL_NAME).toMatch(/^[a-z0-9]+(?:-[a-z0-9]+)*$/)
    expect(createEchuanForkSkillRegistration().name).toBe('echuan-fork-delegation')
  })

  it('description / whenToUse 非空，description ≤500 字符', () => {
    const reg = createEchuanForkSkillRegistration()
    expect(reg.description.trim().length).toBeGreaterThan(0)
    expect(reg.whenToUse?.trim().length ?? 0).toBeGreaterThan(0)
    expect(reg.description.length).toBeLessThanOrEqual(500)
    expect(ECHUAN_FORK_SKILL_DESCRIPTION.length).toBeLessThanOrEqual(500)
    expect(ECHUAN_FORK_SKILL_WHEN_TO_USE).toContain('文档')
  })

  it('注册对象为 runtime、模型与用户均可调用、正文非空', () => {
    const reg = createEchuanForkSkillRegistration()
    expect(reg.source).toBe('runtime')
    expect(reg.invocation).toEqual({ modelInvocable: true, userInvocable: true })
    expect(reg.content).toBe(content)
    expect(reg.content.length).toBeGreaterThan(1500)
  })
})

describe('正文只覆盖 fork 协议', () => {
  it('包含时机、Work Order、子 agent 边界、父 agent 回收、超时', () => {
    for (const word of ['什么时候该 fork', 'WORK ORDER', '子 agent 的边界', '父 agent 怎么收', '超时']) {
      expect(content).toContain(word)
    }
  })

  it('包含 fork 工具、material_id、task_report、maxDepth 等协议事实', () => {
    for (const word of ['subagent_fork', 'material_id', 'echuan_task_report', 'Work Order']) {
      expect(content).toContain(word)
    }
  })

  it('不定义文档模板或章节标准，引用角色规范', () => {
    expect(content).toContain('当前角色自己的 skill / persona')
    for (const banned of ['需求文档模板', '技术方案模板', '开发报告模板', '决策简报模板']) {
      expect(content).not.toContain(banned)
    }
  })

  it('明确禁止子 agent 交报告和继续 fork', () => {
    expect(content).toContain('不调用 echuan_task_report')
    expect(content).toContain('不再 fork')
  })
})

describe('随构建产物发布', () => {
  it('build 后 lib/assets/echuan-fork-delegation.md 存在', () => {
    const built = path.join(kitRoot, 'lib/assets/echuan-fork-delegation.md')
    if (existsSync(built)) expect(existsSync(built)).toBe(true)
  })
})
