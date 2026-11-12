#!/usr/bin/env node
/**
 * 解析「要对哪些 dsh 版本做兼容性校验」，产出 GitHub Actions matrix。
 *
 *   node scripts/dsh-versions.mjs            # 打印人类可读清单
 *   node scripts/dsh-versions.mjs --github   # 追加 matrix=<json> 到 $GITHUB_OUTPUT
 *
 * 取 npm dist-tags 的 `latest` 与 `next`：
 *   - latest：当前稳定线，**必须通过**（blocking: true）；
 *   - next：预发布线，允许失败（blocking: false），用于提前发现破坏性变更；
 *   - 额外版本：由 `EXTRA_DSH_VERSIONS`（空格分隔）注入，走 workflow_dispatch。
 *
 * 只做「解析与编排」，不判断版本是否满足本包声明——那是 dsh 安装准入的职责，
 * 让真实报错原文（requirements 列表）出现在 CI 日志里，比自建 semver 判定更可信。
 */
import process from 'node:process'

const REGISTRY = 'https://registry.npmjs.org/-/package/@deepseek-ai/dsh/dist-tags'
const CHANNELS = [
  { tag: 'latest', blocking: true },
  { tag: 'next', blocking: false },
]

/** 读取 dist-tags；失败时抛出可读错误（CI 需要明确信号，不能静默空 matrix）。 */
async function fetchDistTags() {
  const response = await fetch(REGISTRY, {
    headers: { accept: 'application/json', 'user-agent': 'dsh-agent-dev-compat' },
  })
  if (!response.ok) {
    throw new Error(`读取 npm dist-tags 失败：HTTP ${response.status}`)
  }
  const tags = await response.json()
  if (tags === null || typeof tags !== 'object') {
    throw new Error('npm dist-tags 返回体不是对象')
  }
  return tags
}

const tags = await fetchDistTags()
const include = []
const seen = new Set()

for (const { tag, blocking } of CHANNELS) {
  const version = tags[tag]
  if (typeof version !== 'string' || version === '') {
    console.error(`[dsh-versions] 警告：dist-tag "${tag}" 不存在，跳过`)
    continue
  }
  if (seen.has(version)) continue
  seen.add(version)
  include.push({ target: tag, version, blocking })
}

for (const extra of (process.env.EXTRA_DSH_VERSIONS ?? '').split(/\s+/).filter(Boolean)) {
  if (seen.has(extra)) continue
  seen.add(extra)
  include.push({ target: 'manual', version: extra, blocking: true })
}

if (include.length === 0) {
  console.error('[dsh-versions] 没有可校验的 dsh 版本')
  process.exit(1)
}

const matrix = { include }
console.log('[dsh-versions] 待校验的 dsh 版本：')
for (const item of include) {
  console.log(`  - ${item.version}（来源 ${item.target}，${item.blocking ? '阻塞' : '仅提示'}）`)
}

if (process.argv.includes('--github')) {
  const output = process.env.GITHUB_OUTPUT
  if (output === undefined || output === '') {
    console.error('[dsh-versions] --github 需要 GITHUB_OUTPUT 环境变量')
    process.exit(2)
  }
  const { appendFileSync } = await import('node:fs')
  appendFileSync(output, `matrix=${JSON.stringify(matrix)}\n`)
  appendFileSync(output, `latest=${include[0]?.version ?? ''}\n`)
}
