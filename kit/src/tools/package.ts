/**
 * 资料包下载工具：`GET /v1/projects/package?id=` → 二进制 zip 落盘到系统临时目录。
 *
 * 这是**唯一**允许写盘的 kit 工具（测试 hygiene 有专门例外）：
 * - 路径固定 `os.tmpdir()`，文件名 `<project_id>-资料包-<timestamp>.zip`；
 * - 用 `wx` 独占创建，已存在就加序号，**绝不覆盖**；
 * - 写入失败（含半成品）立即 unlink，失败不留在磁盘上；
 * - 不落库、不缓存、不改变任何平台状态。
 *
 * `entries` 从 zip 的 EOCD（End of Central Directory）解析：解析不出来时为
 * `undefined` 并在文案里说明，不伪造 0。
 */
import { unlinkSync, writeFileSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { defineTool, type ToolDefinition } from '@deepseek-ai/dsh-tools'
import { EchuanValidationError } from '../errors.js'
import { textBlock, toToolError, type TextContent } from './render.js'
import type { EchuanToolOptions } from './write.js'

export interface PackageDownloadedValue {
  kind: 'package_downloaded'
  project_id: string
  saved_path: string
  bytes: number
  entries?: number
}

const packageDownloadedSchema = {
  type: 'object',
  additionalProperties: false,
  properties: {
    kind: { type: 'string', const: 'package_downloaded', required: true },
    project_id: { type: 'string', required: true },
    saved_path: { type: 'string', required: true },
    bytes: { type: 'integer', required: true },
    /** zip 条目数；EOCD 解析失败时不出现（不伪造 0）。 */
    entries: { type: 'integer' },
  },
} as const

const EOCD_SIGNATURE = [0x50, 0x4b, 0x05, 0x06] as const
const EOCD_MIN_BYTES = 22
/** EOCD 尾部最大长度：22 字节结构 + 65535 字节注释。 */
const EOCD_MAX_SCAN = 22 + 0xffff

/**
 * 从 zip 字节里读 EOCD 的总条目数（offset 10，uint16 LE）。
 *
 * 从尾部向前找签名；找不到（截断 / 非 zip）返回 `undefined`。
 */
export function countZipEntries(bytes: Uint8Array): number | undefined {
  if (bytes.length < EOCD_MIN_BYTES) return undefined
  const lowest = Math.max(0, bytes.length - EOCD_MAX_SCAN)
  for (let index = bytes.length - EOCD_MIN_BYTES; index >= lowest; index -= 1) {
    if (
      bytes[index] === EOCD_SIGNATURE[0]
      && bytes[index + 1] === EOCD_SIGNATURE[1]
      && bytes[index + 2] === EOCD_SIGNATURE[2]
      && bytes[index + 3] === EOCD_SIGNATURE[3]
    ) {
      return (bytes[index + 10] ?? 0) | ((bytes[index + 11] ?? 0) << 8)
    }
  }
  return undefined
}

/** 文件名里去掉路径分隔符与控制字符，保证落点仍在 tmpdir 内。 */
function safeSegment(value: string): string {
  const cleaned = value.replace(/[\\/:*?"<>|\u0000-\u001f]/g, '_').trim()
  return cleaned === '' ? 'project' : cleaned
}

/**
 * 独占写入：`wx` 命中已有文件时换下一个序号重试，不覆盖任何既有文件。
 *
 * 非 EEXIST 的写入失败会尝试删除半成品后再抛出。
 */
function writeExclusive(dir: string, baseName: string, bytes: Uint8Array): string {
  const stem = baseName.endsWith('.zip') ? baseName.slice(0, -4) : baseName
  for (let attempt = 0; attempt < 100; attempt += 1) {
    const name = attempt === 0 ? `${stem}.zip` : `${stem}-${attempt + 1}.zip`
    const full = path.join(dir, name)
    try {
      writeFileSync(full, bytes, { flag: 'wx' })
      return full
    } catch (err) {
      const code = (err as NodeJS.ErrnoException).code
      if (code === 'EEXIST') continue
      try {
        unlinkSync(full)
      } catch {
        // 半成品本就不存在（例如权限错误发生在创建前），忽略。
      }
      throw err
    }
  }
  throw new EchuanValidationError(
    '临时目录里同名资料包过多（尝试 100 次仍冲突）。下一步：清理 os.tmpdir() 下的旧资料包后重试。',
    'conflict',
    'saved_path',
  )
}

export function renderPackageDownloaded(value: PackageDownloadedValue): TextContent[] {
  const entries = value.entries === undefined
    ? '条目数未知（zip EOCD 解析失败），请用 `unzip -l` 核对文件'
    : `${value.entries} 个条目`
  return [textBlock([
    `✅ 资料包已下载：${value.project_id}`,
    `- 文件：${value.saved_path}`,
    `- 大小：${value.bytes}B，${entries}`,
    '下一步：直接解压查看（每份文档一个 Markdown 文件），本工具不改动平台状态。',
  ].join('\n'))]
}

/** `echuan_project_package`：下载整套资料包到系统临时目录。 */
export function createProjectPackageTool(options: EchuanToolOptions): ToolDefinition {
  const { client } = options

  return defineTool({
    name: 'echuan_project_package',
    description:
      '下载驿传项目资料包（GET /v1/projects/package?id=）到系统临时目录：'
      + '需求当前版本 + 每份独立方案的当前版本，zip 内每份文档一个 Markdown 文件。'
      + '返回 saved_path / bytes / entries；同名不覆盖，失败会清理半成品。',
    parameters: {
      project_id: { type: 'string', required: true, description: '项目编号（必填，走 query `?id=`）' },
    },
    output: {
      schema: packageDownloadedSchema,
      render: (_args, value) => renderPackageDownloaded(value),
    },
    async execute(args) {
      try {
        if (typeof args.project_id !== 'string' || args.project_id.trim() === '') {
          throw new EchuanValidationError(
            'project_id 不能为空。下一步：补齐项目编号后重试，本次没有发出任何请求。',
            'invalid_argument',
            'project_id',
          )
        }
        const projectId = args.project_id.trim()
        const response = await client.downloadProjectPackage({ id: projectId })
        if (response.bytes.length === 0) {
          throw new EchuanValidationError(
            '平台返回了空的资料包（0 字节）。下一步：确认项目是否有需求/方案，不要原样重试。',
            'invalid_response',
            'package',
          )
        }

        const entries = countZipEntries(response.bytes)
        const filename = `${safeSegment(projectId)}-资料包-${Date.now()}.zip`
        const savedPath = writeExclusive(os.tmpdir(), filename, response.bytes)

        return {
          kind: 'package_downloaded' as const,
          project_id: projectId,
          saved_path: savedPath,
          bytes: response.bytes.length,
          ...(entries === undefined ? {} : { entries }),
        }
      } catch (err) {
        return toToolError(err)
      }
    },
  })
}
