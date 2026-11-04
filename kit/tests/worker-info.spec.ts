/**
 * `echuan_worker_info` 单测：只读、零 HTTP、零副作用。
 *
 * 覆盖：
 * 1. 输出覆盖 poll / kinds / worker_id（打码）/ 派单目标 / inflight；
 * 2. 空 inflight、空 kinds、无 agent、agents 服务缺失都只降级不抛错；
 * 3. 租约过期与剩余时间；
 * 4. 零 HTTP、零写盘、不读 env；
 * 5. output.schema 与 render 的真实链路。
 */
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'
import { validateJsonSchemaValue, type ToolDefinition } from '@deepseek-ai/dsh-tools'
import type { FetchLike } from '../src/client.js'
import { createWorkerInfoTool, type WorkerInfoDeps } from '../src/tools/worker-info.js'
import type { EchuanToolOptions } from '../src/tools/write.js'

const here = path.dirname(fileURLToPath(import.meta.url))
const kitRoot = path.resolve(here, '..')
const WORKER_ID = 'w_TEST01ABCDEFGHIJKLMN'

const exec = { signal: new AbortController().signal } as unknown as Parameters<ToolDefinition['execute']>[1]

/** 哨兵 fetch：诊断工具调用它就直接失败。 */
function sentinelFetch(): { calls: number; fetchImpl: FetchLike } {
  const state = { calls: 0 }
  const fetchImpl: FetchLike = async () => {
    state.calls += 1
    throw new Error('echuan_worker_info 不应发出 HTTP 请求')
  }
  return {
    get calls() {
      return state.calls
    },
    fetchImpl,
  } as { calls: number; fetchImpl: FetchLike }
}

/** 只读的依赖替身；默认给一份「全都可用」的诊断环境。 */
function createDeps(overrides: Partial<WorkerInfoDeps> = {}): WorkerInfoDeps {
  const now = Date.now()
  const deps: WorkerInfoDeps = {
    pollEnabled: true,
    kinds: ['dev', 'design'],
    workerId: () => WORKER_ID,
    inflight: () => [
      { workId: 'w_01M5Z4AAAA', kind: 'dev', leaseUntil: new Date(now + 65_000).toISOString() },
      { workId: 'w_01M5Z4BBBB', kind: 'design', leaseUntil: new Date(now - 5_000).toISOString() },
    ],
    agents: { roots: () => [{ id: 'agent-root', status: 'running' }] },
    ...overrides,
  }
  return deps
}

async function run(
  options: EchuanToolOptions,
  args: Record<string, unknown> = {},
): Promise<{ text: string; value: unknown; tool: ToolDefinition }> {
  const tool = createWorkerInfoTool(options)
  const value = await tool.execute(args, exec)
  const blocks = tool.output.render(args, value as never) as Array<{ type: string; text?: string }>
  return {
    text: blocks.map(block => (block.type === 'text' ? block.text ?? '' : '')).join('\n'),
    value,
    tool,
  }
}

function optionsWith(deps: WorkerInfoDeps | undefined): EchuanToolOptions {
  const sentinel = sentinelFetch()
  return {
    client: { getInbox: async () => sentinel.fetchImpl('', {}) } as never,
    identity: { workerId: undefined },
    ...(deps === undefined ? {} : { workerInfo: deps }),
  }
}

describe('正常路径：覆盖 poll / kinds / worker_id / 派单目标 / inflight', () => {
  it('输出全部关键项，worker_id 打码、目标为 roots()[0]', async () => {
    const deps = createDeps()
    const { text, value, tool } = await run(optionsWith(deps))

    expect(validateJsonSchemaValue(tool.output.schema, value)).toEqual([])

    expect(text).toContain('poll.enable：true')
    expect(text).toContain('worker.kinds：dev、design')
    expect(text).toContain('worker_id：w_TE****')
    expect(text).not.toContain(WORKER_ID)
    expect(text).toContain('派单目标：按项目会话投递（当前 root agent：agent-root，running）')
    expect(text).toContain('inflight：2 条')
    expect(text).toContain('工作项 w_01M5Z4AAAA｜kind=dev')
    expect(text).toContain('剩余 ')
    expect(text).toContain('工作项 w_01M5Z4BBBB｜kind=design')
    expect(text).toContain('已过期')
  })
})

describe('降级路径：缺服务 / 空集合都不抛错', () => {
  it('未注册 + 空 kinds + 无 inflight + 没有 root agent', async () => {
    const deps = createDeps({
      pollEnabled: false,
      kinds: [],
      workerId: () => undefined,
      inflight: () => [],
      agents: { roots: () => [] },
    })
    const { text } = await run(optionsWith(deps))

    expect(text).toContain('poll.enable：false')
    expect(text).toContain('worker.kinds：（未声明；心跳循环不会启动）')
    expect(text).toContain('worker_id：未注册')
    expect(text).toContain('本进程暂无 agent')
    expect(text).toContain('inflight：（无）')
  })

  it('agents 服务缺失：只标注无法判定', async () => {
    const deps = createDeps()
    delete (deps as { agents?: unknown }).agents
    const { text } = await run(optionsWith(deps))

    expect(text).toContain('派单目标：按项目会话投递（agents 服务缺失，无法列出当前 agent）')
    // 其余段落仍完整。
    expect(text).toContain('inflight：2 条')
  })

  it('workerInfo 依赖缺失：返回「诊断不可用」，零请求', async () => {
    const { text } = await run(optionsWith(undefined))
    expect(text).toContain('worker 诊断不可用')
  })

  it('依赖读取抛错：只降级该段落，不整体失败', async () => {
    const deps = createDeps({
      workerId: () => {
        throw new Error('id boom')
      },
      agents: {
        roots: () => {
          throw new Error('roots boom')
        },
      },
    })
    const { text } = await run(optionsWith(deps))
    expect(text).toContain('worker_id：未注册')
    expect(text).toContain('无法判定（读取 agents.roots() 失败）')
  })
})

describe('安全约束：零 HTTP、零写盘、不读 env', () => {
  it('执行诊断不发出任何 HTTP 请求', async () => {
    const sentinel = sentinelFetch()
    const tool = createWorkerInfoTool({
      client: { getInbox: async () => sentinel.fetchImpl('', {}) } as never,
      identity: { workerId: WORKER_ID },
      workerInfo: createDeps(),
    })
    await tool.execute({}, exec)
    expect(sentinel.calls).toBe(0)
  })

  it('源码不读 env、不写盘', () => {
    const source = readFileSync(path.resolve(kitRoot, 'src', 'tools', 'worker-info.ts'), 'utf8')
    expect(source).not.toContain('process.env')
    expect(source).not.toContain('node:fs')
    expect(source).not.toContain('writeFile')
  })
})
