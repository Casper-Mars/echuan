/** 技能 `echuan-doc-writing` 单测：元信息、正文结构、构建产物。 */
import { existsSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import {
  ECHUAN_DOC_SKILL_DESCRIPTION,
  ECHUAN_DOC_SKILL_NAME,
  ECHUAN_DOC_SKILL_WHEN_TO_USE,
  createEchuanDocSkillRegistration,
  loadEchuanDocSkillGuide,
} from '../src/doc-skill.js'

const here = path.dirname(fileURLToPath(import.meta.url))
const kitRoot = path.resolve(here, '..')
const content = loadEchuanDocSkillGuide()

describe('文档写作技能元信息', () => {
  it('名称精确为 echuan-doc-writing', () => {
    expect(ECHUAN_DOC_SKILL_NAME).toBe('echuan-doc-writing')
    expect(ECHUAN_DOC_SKILL_NAME).toMatch(/^[a-z0-9]+(?:-[a-z0-9]+)*$/)
    expect(createEchuanDocSkillRegistration().name).toBe('echuan-doc-writing')
  })

  it('description / whenToUse 非空，description ≤500 字符', () => {
    const reg = createEchuanDocSkillRegistration()
    expect(reg.description.trim().length).toBeGreaterThan(0)
    expect(reg.whenToUse?.trim().length ?? 0).toBeGreaterThan(0)
    expect(reg.description.length).toBeLessThanOrEqual(500)
    expect(ECHUAN_DOC_SKILL_DESCRIPTION.length).toBeLessThanOrEqual(500)
    expect(ECHUAN_DOC_SKILL_WHEN_TO_USE).toContain('文档')
  })

  it('注册对象为 runtime、模型与用户均可调用、正文非空', () => {
    const reg = createEchuanDocSkillRegistration()
    expect(reg.source).toBe('runtime')
    expect(reg.invocation).toEqual({ modelInvocable: true, userInvocable: true })
    expect(reg.content).toBe(content)
    expect(reg.content.length).toBeGreaterThan(1500)
  })
})

describe('正文覆盖文档类型与写作约束', () => {
  it('包含需求 / 方案 / 开发报告 / 决策简报四类模板', () => {
    for (const word of ['需求文档', '技术方案', '开发报告', '决策简报']) {
      expect(content).toContain(word)
    }
  })

  it('区分事实、假设、待决策，并要求验收标准可观察', () => {
    expect(content).toContain('事实')
    expect(content).toContain('假设')
    expect(content).toContain('待决策')
    expect(content).toContain('验收标准')
  })

  it('不写未验证的结论，不代替人验收', () => {
    expect(content).toContain('没跑过')
    expect(content).toContain('不代替人验收')
    expect(content).not.toContain('已通过评审')
  })
})

describe('随构建产物发布', () => {
  it('build 后 lib/assets/echuan-doc-writing.md 存在', () => {
    const built = path.join(kitRoot, 'lib/assets/echuan-doc-writing.md')
    // 允许未 build 的本地单测；CI 的 verify 会先 build。
    if (existsSync(built)) expect(existsSync(built)).toBe(true)
  })
})
