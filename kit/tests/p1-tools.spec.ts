/**
 * 任务-08 P1 工具单测：单卡增改删 / 项目改标题 / 建产品 / 资料包下载。
 *
 * 全部假平台 + 真 client；资料包是唯一会写盘的工具，测试后清理 tmpdir 文件。
 */
import { existsSync, readFileSync, unlinkSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import type { ToolDefinition } from '@deepseek-ai/dsh-tools'
import { createClient, type FetchLike } from '../src/client.js'
import { resolveConfig } from '../src/config.js'
import { createAllEchuanTools } from '../src/tools/index.js'
import { countZipEntries } from '../src/tools/package.js'
import { buildZip } from './helpers/zip.js'

interface RecordedCall {
  url: URL
  method: string
  body: string | undefined
}

function createPlatform(respond: (call: RecordedCall) => Response): {
  calls: RecordedCall[]
  fetchImpl: FetchLike
} {
  const calls: RecordedCall[] = []
  const fetchImpl: FetchLike = async (input, init = {}) => {
    const call: RecordedCall = {
      url: new URL(input),
      method: init.method ?? 'GET',
      body: init.body === undefined ? undefined : String(init.body),
    }
    calls.push(call)
    return respond(call)
  }
  return { calls, fetchImpl }
}

function json(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

function jsonError(status: number, code: string, message: string): Response {
  return json({ code, message, details: [], request_id: `req-${code}` }, status)
}

const config = resolveConfig({ baseUrl: 'http://platform.test' })
const exec = { signal: new AbortController().signal } as unknown as Parameters<
  ToolDefinition['execute']
>[1]

function toolsFor(fetchImpl: FetchLike): Map<string, ToolDefinition> {
  const options = { client: createClient(config, { fetchImpl }), identity: { workerId: undefined } }
  return new Map(createAllEchuanTools(options).map(tool => [tool.name, tool]))
}

async function run(
  tool: ToolDefinition | undefined,
  args: Record<string, unknown>,
): Promise<unknown> {
  if (tool === undefined) throw new Error('工具未注册')
  return tool.execute(args, exec)
}

async function failureOf(promise: Promise<unknown>): Promise<Error> {
  try {
    await promise
  } catch (err) {
    return err as Error
  }
  throw new Error('预期失败，但调用成功了')
}

const savedFiles: string[] = []

afterEach(() => {
  for (const file of savedFiles.splice(0)) {
    if (existsSync(file)) unlinkSync(file)
  }
})

describe('echuan_task_update：本地校验零请求', () => {
  it('空 patch（只传 task_id）→ 本地拒绝且 0 请求', async () => {
    const platform = createPlatform(() => json({}))
    const err = await failureOf(run(toolsFor(platform.fetchImpl).get('echuan_task_update'), {
      task_id: 'APP-2-T0001',
    }))
    expect(err.message).toMatch(/invalid argument|\[invalid_argument\]/)
    expect(err.message).toContain('至少')
    expect(platform.calls).toHaveLength(0)
  })

  it('空白 title 不算有效修改 → 本地拒绝且 0 请求', async () => {
    const platform = createPlatform(() => json({}))
    const err = await failureOf(run(toolsFor(platform.fetchImpl).get('echuan_task_update'), {
      task_id: 'APP-2-T0001',
      title: '   ',
    }))
    expect(err.message).toContain('title')
    expect(platform.calls).toHaveLength(0)
  })

  it('只改 title：PATCH /v1/tasks?id=，body 只有 title', async () => {
    const platform = createPlatform(() => json({
      task_id: 'APP-2-T0001',
      title: '新标题',
      status: 0,
      status_name: '待办',
    }))
    const value = await run(toolsFor(platform.fetchImpl).get('echuan_task_update'), {
      task_id: 'APP-2-T0001',
      title: '新标题',
    }) as { kind: string; title: string }

    expect(value.kind).toBe('task_updated')
    expect(value.title).toBe('新标题')
    expect(platform.calls).toHaveLength(1)
    expect(platform.calls[0]!.method).toBe('PATCH')
    expect(JSON.parse(platform.calls[0]!.body as string)).toEqual({ title: '新标题' })
  })
})

describe('echuan_task_delete：204 成功 / 409 原样透出', () => {
  it('204 → deleted，DELETE 带 ?id=', async () => {
    const platform = createPlatform(() => new Response(null, { status: 204 }))
    const value = await run(toolsFor(platform.fetchImpl).get('echuan_task_delete'), {
      task_id: 'APP-2-T0002',
    }) as { kind: string; task_id: string }

    expect(value).toEqual({ kind: 'task_deleted', task_id: 'APP-2-T0002' })
    expect(platform.calls).toHaveLength(1)
    expect(platform.calls[0]!.method).toBe('DELETE')
    expect(platform.calls[0]!.url.searchParams.get('id')).toBe('APP-2-T0002')
  })

  it('409 已领取 → 错误原样透出（含状态码与平台 message）', async () => {
    const platform = createPlatform(() => jsonError(409, 'task_claimed', '任务已被领取，不能删除'))
    let failure: unknown
    try {
      await run(toolsFor(platform.fetchImpl).get('echuan_task_delete'), { task_id: 'APP-2-T0002' })
    } catch (err) {
      failure = err
    }
    expect(failure).toBeInstanceOf(Error)
    expect((failure as Error).message).toContain('409')
    expect((failure as Error).message).toContain('任务已被领取')
  })
})

describe('echuan_project_update：只改标题', () => {
  it('PATCH /v1/projects?id= 的 body 只有 title，状态原样返回', async () => {
    const platform = createPlatform(() => json({
      project_id: 'APP-2',
      title: '改过的标题',
      status: 2,
      status_name: '方案草稿',
      version: 5,
    }))
    const value = await run(toolsFor(platform.fetchImpl).get('echuan_project_update'), {
      project_id: 'APP-2',
      title: '改过的标题',
    }) as { kind: string; status: number; title: string }

    expect(value.kind).toBe('project_updated')
    expect(value.title).toBe('改过的标题')
    expect(value.status).toBe(2)
    expect(JSON.parse(platform.calls[0]!.body as string)).toEqual({ title: '改过的标题' })
  })

  it('空白标题本地拒绝且 0 请求', async () => {
    const platform = createPlatform(() => json({}))
    await failureOf(run(toolsFor(platform.fetchImpl).get('echuan_project_update'), {
      project_id: 'APP-2',
      title: ' ',
    }))
    expect(platform.calls).toHaveLength(0)
  })
})

describe('echuan_product_create：建产品后立即建项目', () => {
  it('产品 201 后项目创建成功（同一产品 id）', async () => {
    const products = new Set<string>()
    const platform = createPlatform((call) => {
      if (call.url.pathname === '/v1/products' && call.method === 'POST') {
        const body = JSON.parse(call.body as string) as { product_id: string }
        products.add(body.product_id)
        return json({ product_id: body.product_id, name: '示例产品', git_url: 'git@example.com:x.git' }, 201)
      }
      if (call.url.pathname === '/v1/projects' && call.method === 'POST') {
        const body = JSON.parse(call.body as string) as { product_id: string }
        if (!products.has(body.product_id)) return jsonError(404, 'not_found', '产品不存在')
        return json({ project_id: 'APPX-1', product_id: body.product_id, title: '新项目', status: 0 }, 201)
      }
      return json({})
    })

    const tools = toolsFor(platform.fetchImpl)
    const product = await run(tools.get('echuan_product_create'), {
      product_id: 'APPX',
      name: '示例产品',
      git_url: 'git@example.com:x.git',
      description: '描述',
      license: 'MIT',
    }) as { kind: string; product_id: string }
    expect(product).toMatchObject({ kind: 'product_created', product_id: 'APPX' })

    const project = await run(tools.get('echuan_project_create'), {
      product_id: 'APPX',
      title: '新项目',
    }) as { kind: string; project_id: string }
    expect(project).toMatchObject({ kind: 'project_created', project_id: 'APPX-1' })
    expect(platform.calls).toHaveLength(2)
  })
})

describe('echuan_project_package：zip 落盘与 EOCD 条目数', () => {
  const zipBytes = buildZip([
    { name: '需求-v2-登录页改版.md', content: '# 需求' },
    { name: '方案-S0001-v1-接口设计.md', content: '# 方案 A' },
    { name: '方案-S0002-v3-数据迁移.md', content: '# 方案 B' },
  ])

  it('下载到 tmpdir，entries=3，文件名含“资料包”与中文', async () => {
    const platform = createPlatform(() => new Response(zipBytes, {
      status: 200,
      headers: { 'content-type': 'application/zip' },
    }))
    const value = await run(toolsFor(platform.fetchImpl).get('echuan_project_package'), {
      project_id: 'APP-2',
    }) as { kind: string; saved_path: string; bytes: number; entries: number }
    savedFiles.push(value.saved_path)

    expect(value.kind).toBe('package_downloaded')
    expect(value.saved_path.startsWith(os.tmpdir())).toBe(true)
    expect(path.dirname(value.saved_path)).toBe(os.tmpdir())
    expect(path.basename(value.saved_path)).toContain('APP-2-资料包-')
    expect(path.basename(value.saved_path).endsWith('.zip')).toBe(true)
    expect(value.bytes).toBe(zipBytes.length)
    expect(value.entries).toBe(3)
    expect(new Uint8Array(readFileSync(value.saved_path))).toEqual(zipBytes)
  })

  it('重复下载不覆盖：两次落盘路径不同且内容各自可读', async () => {
    const platform = createPlatform(() => new Response(zipBytes, {
      status: 200,
      headers: { 'content-type': 'application/zip' },
    }))
    const tools = toolsFor(platform.fetchImpl)
    const first = await run(tools.get('echuan_project_package'), { project_id: 'APP-2' }) as {
      saved_path: string
    }
    savedFiles.push(first.saved_path)
    const second = await run(tools.get('echuan_project_package'), { project_id: 'APP-2' }) as {
      saved_path: string
    }
    savedFiles.push(second.saved_path)

    expect(second.saved_path).not.toBe(first.saved_path)
    expect(existsSync(first.saved_path)).toBe(true)
    expect(existsSync(second.saved_path)).toBe(true)
  })

  it('EOCD 无法解析时 entries 缺省（不伪造 0）', async () => {
    const bogus = new Uint8Array([1, 2, 3, 4, 5, 6, 7, 8, 9])
    const platform = createPlatform(() => new Response(bogus, {
      status: 200,
      headers: { 'content-type': 'application/zip' },
    }))
    const tool = toolsFor(platform.fetchImpl).get('echuan_project_package')
    const value = await run(tool, { project_id: 'APP-2' }) as {
      saved_path: string
      entries?: number
    }
    savedFiles.push(value.saved_path)

    expect(value.entries).toBeUndefined()
    const text = (tool!.output?.render as (args: unknown, value: unknown) => Array<{ text: string }>)(
      {},
      value,
    )[0]!.text
    expect(text).toContain('条目数未知')
    expect(countZipEntries(bogus)).toBeUndefined()
  })

  it('404 未知项目：错误透出且不落盘', async () => {
    const platform = createPlatform(() => jsonError(404, 'not_found', '项目不存在'))
    const err = await failureOf(run(toolsFor(platform.fetchImpl).get('echuan_project_package'), {
      project_id: 'NOPE-1',
    }))
    expect(err.message).toContain('404')
    expect(err.message).toContain('not_found')
    expect(platform.calls).toHaveLength(1)
    // tmpdir 下不该出现 NOPE-1 的残留。
    expect(existsSync(path.join(os.tmpdir(), 'NOPE-1-资料包-failed.zip'))).toBe(false)
  })
})

describe('客户端空 patch：平台 400 原样透出（工具层已本地拦截）', () => {
  it('client.updateTask({ id }) 发空对象，平台 400 的 code/message 保留', async () => {
    const platform = createPlatform(() => jsonError(
      400,
      'invalid_argument',
      'at least one of title, body_md, priority is required',
    ))
    const client = createClient(config, { fetchImpl: platform.fetchImpl })

    let failure: unknown
    try {
      await client.updateTask({ id: 'APP-2-T0001' })
    } catch (err) {
      failure = err
    }
    expect(failure).toBeInstanceOf(Error)
    expect((failure as Error).message).toContain('400')
    expect((failure as Error).message).toContain('at least one of title')
    expect(platform.calls).toHaveLength(1)
    expect(JSON.parse(platform.calls[0]!.body as string)).toEqual({})
  })
})
