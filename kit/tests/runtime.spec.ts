/**
 * 运行期骨架单测：验收项 7（poll=false 期间零定时器）与验收项 8（卸载后干净）。
 */
import { describe, expect, it, vi } from 'vitest'
import type { ResolvedConfig } from '../src/config.js'
import {
  DEFAULT_POLL_INTERVAL_MS,
  createEchuanRuntime,
  type Logger,
  type TimerApi,
} from '../src/runtime.js'

/** 收集日志的假 logger。 */
function createLogger(): { logger: Logger; lines: string[] } {
  const lines: string[] = []
  const push = (message: string) => lines.push(message)
  return { logger: { info: push, warn: push, debug: push }, lines }
}

/** 假定时器：记录创建/清除次数，并可手动触发 tick。 */
function createTimers(): {
  api: TimerApi
  setIntervalCalls: Array<{ ms: number }>
  clearIntervalCalls: unknown[]
  fire(): void
} {
  const setIntervalCalls: Array<{ ms: number }> = []
  const clearIntervalCalls: unknown[] = []
  let handler: (() => void) | undefined
  let nextId = 1

  return {
    api: {
      setInterval: vi.fn((h: () => void, ms: number) => {
        setIntervalCalls.push({ ms })
        handler = h
        return nextId++
      }),
      clearInterval: vi.fn((handle: unknown) => {
        clearIntervalCalls.push(handle)
      }),
    },
    setIntervalCalls,
    clearIntervalCalls,
    // 刻意保留 handler：模拟「clearInterval 之前已排队的回调」，用于验证 dispose 守卫。
    fire: () => handler?.(),
  }
}

function config(pollEnable: boolean): ResolvedConfig {
  return {
    baseUrl: 'http://127.0.0.1:8720',
    actor: 'agent:echuan',
    requestTimeoutMs: 35_000,
    worker: { poll: { enable: pollEnable }, metadata: {} },
  }
}

describe('worker.poll.enable = false', () => {
  it('start() 不创建任何定时器', () => {
    const timers = createTimers()
    const { logger, lines } = createLogger()
    const runtime = createEchuanRuntime(config(false), { logger, timerApi: timers.api })

    runtime.start()

    expect(timers.api.setInterval).not.toHaveBeenCalled()
    expect(timers.setIntervalCalls).toHaveLength(0)
    expect(runtime.polling).toBe(false)
    expect(lines.join('\n')).toContain('worker.poll.enable=false')
  })

  it('重复 start() 仍然零定时器', () => {
    const timers = createTimers()
    const { logger } = createLogger()
    const runtime = createEchuanRuntime(config(false), { logger, timerApi: timers.api })

    runtime.start()
    runtime.start()
    runtime.start()

    expect(timers.setIntervalCalls).toHaveLength(0)
  })

  it('不调用 start() 时同样零定时器（构造本身无副作用）', () => {
    const timers = createTimers()
    const { logger } = createLogger()
    createEchuanRuntime(config(true), { logger, timerApi: timers.api })

    expect(timers.setIntervalCalls).toHaveLength(0)
  })
})

describe('worker.poll.enable = true', () => {
  it('start() 创建且只创建一个定时器，间隔取默认值', () => {
    const timers = createTimers()
    const { logger } = createLogger()
    const runtime = createEchuanRuntime(config(true), { logger, timerApi: timers.api })

    runtime.start()
    runtime.start()

    expect(timers.setIntervalCalls).toEqual([{ ms: DEFAULT_POLL_INTERVAL_MS }])
    expect(runtime.polling).toBe(true)
  })

  it('tick 调用注入的 onPoll，本阶段缺省时不产生任何调用', async () => {
    const timers = createTimers()
    const { logger } = createLogger()
    const onPoll = vi.fn()
    const runtime = createEchuanRuntime(config(true), { logger, timerApi: timers.api, onPoll })

    runtime.start()
    timers.fire()
    await Promise.resolve()
    expect(onPoll).toHaveBeenCalledTimes(1)

    const silent = createEchuanRuntime(config(true), { logger, timerApi: createTimers().api })
    silent.start()
    // onPoll 缺省：tick 空转，不抛错、不发请求（本阶段没有 HTTP 客户端）
    expect(() => silent.dispose()).not.toThrow()
  })
})

describe('dispose：卸载后干净', () => {
  it('清除定时器并落一条卸载日志，重复 dispose 不重复清理、不再记日志', () => {
    const timers = createTimers()
    const { logger, lines } = createLogger()
    const runtime = createEchuanRuntime(config(true), { logger, timerApi: timers.api })

    runtime.start()
    expect(runtime.polling).toBe(true)

    runtime.dispose('plugin dispose')
    expect(timers.clearIntervalCalls).toHaveLength(1)
    expect(runtime.polling).toBe(false)
    expect(runtime.disposed).toBe(true)

    const afterFirst = lines.length
    runtime.dispose()
    runtime.dispose()
    expect(timers.clearIntervalCalls).toHaveLength(1)
    expect(lines).toHaveLength(afterFirst)
  })

  it('dispose 之后已排队的 tick 不再触发 onPoll', async () => {
    const timers = createTimers()
    const { logger } = createLogger()
    const onPoll = vi.fn()
    const runtime = createEchuanRuntime(config(true), { logger, timerApi: timers.api, onPoll })

    runtime.start()
    runtime.dispose()
    // 模拟事件循环里已排队的回调在 dispose 之后才执行
    timers.fire()
    await Promise.resolve()

    expect(onPoll).not.toHaveBeenCalled()
  })

  it('dispose 之后 start() 不再创建定时器', () => {
    const timers = createTimers()
    const { logger } = createLogger()
    const runtime = createEchuanRuntime(config(true), { logger, timerApi: timers.api })

    runtime.dispose()
    runtime.start()

    expect(timers.setIntervalCalls).toHaveLength(0)
  })

  it('poll=false 时 dispose 也幂等且不调用 clearInterval', () => {
    const timers = createTimers()
    const { logger } = createLogger()
    const runtime = createEchuanRuntime(config(false), { logger, timerApi: timers.api })

    runtime.start()
    runtime.dispose()
    runtime.dispose()

    expect(timers.clearIntervalCalls).toHaveLength(0)
    expect(runtime.disposed).toBe(true)
  })
})
