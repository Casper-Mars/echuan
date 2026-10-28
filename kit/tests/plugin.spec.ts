/**
 * 插件入口单测：加载日志、effect 生命周期、以及「不碰 tools/skills/agents 服务」。
 *
 * 验收项 7/8 的结构性证明：
 * - `apply` 只访问 `ctx.effect` 与 `ctx.logger`，**不访问** `ctx.tools` / `ctx.skills` /
 *   `ctx.agents`（用 Proxy 在访问时直接抛错来锁定这一点）——本阶段没有工具注册；
 * - 默认配置（poll=false）下 `apply` 不创建任何定时器；
 * - effect 的 disposer 执行后：定时器清除、只多一条卸载日志，再调用无副作用。
 */
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Context } from '@deepseek-ai/cordis'
import { apply, inject, name } from '../src/index.js'

const here = path.dirname(fileURLToPath(import.meta.url))
const kitRoot = path.resolve(here, '..')

interface FakeContext {
  ctx: Context
  /** effect 的 label 列表。 */
  labels: string[]
  /** 依次执行所有 disposer（逆序，模拟 fiber 卸载）。 */
  disposeAll(): void
  /** 被禁止访问的服务名。 */
  forbiddenAccess: string[]
}

/** 构造只提供 effect / logger 的假上下文；访问 tools/skills/agents 立即抛错。 */
function createFakeContext(): FakeContext {
  const labels: string[] = []
  const disposers: Array<() => void> = []
  const forbiddenAccess: string[] = []

  const base = {
    effect(execute: () => (() => void) | void, label?: string) {
      labels.push(label ?? '')
      const cleanup = execute()
      if (typeof cleanup === 'function') disposers.push(cleanup)
      return { dispose: async () => {} }
    },
    logger: {
      info: () => {},
      warn: () => {},
      debug: () => {},
      error: () => {},
    },
  }

  const ctx = new Proxy(base, {
    get(target, key, receiver) {
      if (key === 'tools' || key === 'skills' || key === 'agents') {
        forbiddenAccess.push(String(key))
        throw new Error(`本阶段不应访问 ctx.${String(key)}`)
      }
      return Reflect.get(target, key, receiver)
    },
  }) as unknown as Context

  return {
    ctx,
    labels,
    forbiddenAccess,
    disposeAll() {
      for (const disposer of [...disposers].reverse()) disposer()
    },
  }
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('插件标识', () => {
  it('id 为 echuan，依赖 tools/skills/agents', () => {
    expect(name).toBe('echuan')
    expect(inject).toEqual(['tools', 'skills', 'agents'])
  })

  it('patch 行与入口声明一致（id / name / inject）', () => {
    const patch = readFileSync(path.join(kitRoot, 'cordis.patch.yml'), 'utf8')
    expect(patch).toContain('- insert:')
    expect(patch).toContain('id: echuan')
    expect(patch).toContain('name: dsh-echuan')
    expect(patch).toContain("inject: ['tools', 'skills', 'agents']")
  })

  it('package.json 声明 dsh.bundle.patch 且指向存在的 patch 文件', () => {
    const pkg = JSON.parse(readFileSync(path.join(kitRoot, 'package.json'), 'utf8')) as {
      name: string
      type: string
      main: string
      dsh: { bundle: { patch: string } }
    }
    expect(pkg.type).toBe('module')
    expect(pkg.dsh.bundle.patch).toBe('./cordis.patch.yml')
    expect(() => readFileSync(path.join(kitRoot, pkg.dsh.bundle.patch), 'utf8')).not.toThrow()
  })
})

describe('apply：加载与生命周期', () => {
  it('打印 [dsh-echuan] 加载日志并含关键字段', () => {
    const spy = vi.spyOn(console, 'log').mockImplementation(() => {})
    const fake = createFakeContext()

    apply(fake.ctx, undefined)

    const lines = spy.mock.calls.map(call => String(call[0]))
    expect(lines.length).toBeGreaterThan(0)
    const loaded = lines.find(line => line.startsWith('[dsh-echuan]'))
    expect(loaded).toBeDefined()
    expect(loaded).toContain('name=echuan')
    expect(loaded).toContain('inject=[tools, skills, agents]')
    expect(loaded).toContain('baseUrl=http://127.0.0.1:8720')
    expect(loaded).toContain('actor=agent:echuan')
    expect(loaded).toContain('poll=false')
  })

  it('只注册一个 effect（echuan.runtime()），且不访问 tools/skills/agents', () => {
    vi.spyOn(console, 'log').mockImplementation(() => {})
    const fake = createFakeContext()

    apply(fake.ctx, undefined)

    expect(fake.labels).toEqual(['echuan.runtime()'])
    expect(fake.forbiddenAccess).toEqual([])
  })

  it('默认配置（poll=false）下零定时器', () => {
    vi.spyOn(console, 'log').mockImplementation(() => {})
    const setIntervalSpy = vi.spyOn(globalThis, 'setInterval')
    const fake = createFakeContext()

    apply(fake.ctx, undefined)

    expect(setIntervalSpy).not.toHaveBeenCalled()
  })

  it('disposer 执行后不再有日志与定时器，重复清理无副作用', () => {
    // 用假定时器：poll=true 会创建真实 interval，避免测试进程被未清除的句柄拖住。
    vi.useFakeTimers()
    try {
      const spy = vi.spyOn(console, 'log').mockImplementation(() => {})
      const setSpy = vi.spyOn(globalThis, 'setInterval')
      const clearSpy = vi.spyOn(globalThis, 'clearInterval')
      const fake = createFakeContext()

      apply(fake.ctx, { worker: { poll: { enable: true } } })
      expect(setSpy).toHaveBeenCalledTimes(1)

      fake.disposeAll()
      expect(clearSpy).toHaveBeenCalledTimes(1)

      const afterDispose = spy.mock.calls.length
      const lines = spy.mock.calls.map(call => String(call[0]))
      expect(lines.some(line => line.includes('已卸载'))).toBe(true)

      fake.disposeAll()
      expect(spy.mock.calls.length).toBe(afterDispose)
      expect(clearSpy).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })
})
