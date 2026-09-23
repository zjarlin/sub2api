import type { OpsDashboardOverview, OpsErrorDetail } from '@/api/admin/ops'
import { formatNumber } from '@/utils/format'

export interface FixPromptContext {
  locale: string
  timeRangeLabel: string
  platformLabel: string
  groupLabel: string
}

const STAGE_LABEL_ZH: Record<string, string> = {
  routing: '路由',
  account_auth: '账号认证',
  upstream: '上游'
}

function collapseText(text: string | null | undefined, max: number): string {
  if (!text) return ''
  const collapsed = String(text).replace(/\s+/g, ' ').trim()
  return collapsed.length > max ? `${collapsed.slice(0, max)}...` : collapsed
}

function fmtPct(v: number | null | undefined, digits = 2): string {
  return v == null || !Number.isFinite(v) ? '-' : `${(v * 100).toFixed(digits)}%`
}

function fmtNum(v: number | null | undefined): string {
  return v == null ? '-' : formatNumber(v)
}

function fmtMs(v: number | null | undefined): string {
  return v == null ? '-' : `${Math.round(v)} ms`
}

/**
 * 解析调用链（错误详情的 upstream_errors JSON 数组），输出可读文本。
 * 事件结构参考 OpsAccountAttemptChain：account_id/account_name/kind/model/from_model/model_tier/
 * at_unix_ms/upstream_status_code/status_code/message/dropped_earlier_attempts/stage。
 */
export function formatAttemptChain(raw?: string | null, isZh = true): string {
  if (!raw) return ''
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return ''
  }
  if (!Array.isArray(parsed)) return ''
  const lines: string[] = []
  for (const entry of parsed) {
    if (!entry || typeof entry !== 'object') continue
    const e = entry as Record<string, unknown>
    if (e.kind === 'model_fallback') {
      const from = typeof e.from_model === 'string' ? e.from_model : '?'
      const to = typeof e.model === 'string' ? e.model : '?'
      const tier = typeof e.model_tier === 'string' ? ` (${e.model_tier})` : ''
      lines.push(isZh ? `- 切换模型：${from} → ${to}${tier}` : `- model fallback: ${from} -> ${to}${tier}`)
      continue
    }
    const name = typeof e.account_name === 'string' ? e.account_name : ''
    const id = typeof e.account_id === 'number' && e.account_id > 0 ? ` #${e.account_id}` : ''
    const stageRaw = typeof e.stage === 'string' ? e.stage : ''
    const stage = isZh ? STAGE_LABEL_ZH[stageRaw] ?? stageRaw : stageRaw
    const model = typeof e.model === 'string' ? e.model : ''
    const status =
      typeof e.upstream_status_code === 'number' && e.upstream_status_code > 0
        ? e.upstream_status_code
        : typeof e.status_code === 'number' && e.status_code > 0
          ? e.status_code
          : undefined
    const message = collapseText(typeof e.message === 'string' ? e.message : '', 160)
    const parts = [
      isZh ? `账号 ${name || '未知'}${id}` : `account ${name || 'unknown'}${id}`,
      stage ? `${isZh ? '阶段' : 'stage'}=${stage}` : '',
      model ? `${isZh ? '模型' : 'model'}=${model}` : '',
      status != null ? `${isZh ? '状态' : 'status'}=${status}` : '',
      message
    ].filter(Boolean)
    lines.push(`- ${parts.join(' | ')}`)
  }
  return lines.join('\n')
}

/**
 * 生成「根据报错日志修复代码缺陷」提示词。
 * 包含监控概览（可选）+ 每条错误的完整明细：状态码/阶段/归属/端点/消息/错误体/上游状态码/
 * 上游错误信息/上游错误详情/调用链。
 */
export function buildErrorFixPrompt(
  ov: OpsDashboardOverview | null,
  details: OpsErrorDetail[],
  ctx: FixPromptContext
): string {
  const isZh = ctx.locale === 'zh'
  const lines: string[] = []

  if (isZh) {
    lines.push(
      '你是资深后端工程师。请根据以下 API 网关的监控数据与报错日志，定位并修复系统中的代码缺陷。',
      '',
      '【监控概览】'
    )
    if (ov) {
      lines.push(
        `- 统计窗口：${ctx.timeRangeLabel}`,
        `- 筛选：平台=${ctx.platformLabel}，分组=${ctx.groupLabel}`,
        `- 请求总数：${fmtNum(ov.request_count_total)}`,
        `- SLA（排除业务限制）：${fmtPct(ov.sla, 3)}`,
        `- 请求错误率：${fmtPct(ov.error_rate)}`,
        `- 错误数（SLA范围）：${fmtNum(ov.error_count_sla)}`,
        `- 业务限制数：${fmtNum(ov.business_limited_count)}`,
        `- 上游错误率：${fmtPct(ov.upstream_error_rate)}`,
        `- 上游错误数（排除429/529）：${fmtNum(ov.upstream_error_count_excl_429_529)}`,
        `- 上游 429/529 次数：${fmtNum((ov.upstream_429_count ?? 0) + (ov.upstream_529_count ?? 0))}`,
        `- 请求时长 P99：${fmtMs(ov.duration?.p99_ms)}`,
        ''
      )
    } else {
      lines.push('- 监控概览数据不可用。', '')
    }

    if (details.length === 0) {
      lines.push('【报错日志】当前筛选条件下暂无错误日志，请补充报错日志后再分析。', '')
    } else {
      lines.push(`【报错日志与调用链（共 ${details.length} 条）】`, '')
      details.forEach((d, i) => {
        lines.push(
          `■ 错误 #${i + 1} [${d.created_at}]`,
          `- HTTP ${d.status_code} | 阶段=${d.phase}/${d.type} | 归属=${d.error_owner} | 来源=${d.error_source} | 模型=${d.model || '-'}`,
          `- 端点：${d.inbound_endpoint || '-'} → ${d.upstream_endpoint || '-'}`,
          `- 消息：${collapseText(d.message, 200) || '-'}`,
          `- 错误体：${collapseText(d.error_body, 400) || '-'}`
        )
        if (d.upstream_status_code != null) lines.push(`- 上游状态码：${d.upstream_status_code}`)
        if (d.upstream_error_message) lines.push(`- 上游错误信息：${collapseText(d.upstream_error_message, 200)}`)
        if (d.upstream_error_detail) lines.push(`- 上游错误详情：${collapseText(d.upstream_error_detail, 400)}`)
        const chain = formatAttemptChain(d.upstream_errors, true)
        lines.push(chain ? `- 调用链：\n${chain}` : '- 调用链：无')
        lines.push('')
      })
    }

    lines.push(
      '【要求】',
      '1. 先按错误类型归类，归纳根因，指出最可能出问题的代码位置（文件/函数/逻辑分支）。',
      '2. 区分平台自身缺陷、上游提供商问题与客户端问题；只针对平台代码缺陷给出修复方案。',
      '3. 给出可落地的修复建议，关键改动附代码补丁（diff 形式）。',
      '4. 遵循项目现有架构与代码风格，避免无关改动；修复后说明验证方式。'
    )
  } else {
    lines.push(
      'You are a senior backend engineer. Based on the API gateway monitoring data and error logs below, locate and fix the code defects in the system.',
      '',
      '## Monitoring overview'
    )
    if (ov) {
      lines.push(
        `- Window: ${ctx.timeRangeLabel}`,
        `- Filters: platform=${ctx.platformLabel}, group=${ctx.groupLabel}`,
        `- Total requests: ${fmtNum(ov.request_count_total)}`,
        `- SLA (excl business limits): ${fmtPct(ov.sla, 3)}`,
        `- Request error rate: ${fmtPct(ov.error_rate)}`,
        `- Error count (SLA scope): ${fmtNum(ov.error_count_sla)}`,
        `- Business limited count: ${fmtNum(ov.business_limited_count)}`,
        `- Upstream error rate: ${fmtPct(ov.upstream_error_rate)}`,
        `- Upstream error count (excl 429/529): ${fmtNum(ov.upstream_error_count_excl_429_529)}`,
        `- Upstream 429/529 count: ${fmtNum((ov.upstream_429_count ?? 0) + (ov.upstream_529_count ?? 0))}`,
        `- Request duration P99: ${fmtMs(ov.duration?.p99_ms)}`,
        ''
      )
    } else {
      lines.push('- Monitoring overview unavailable.', '')
    }

    if (details.length === 0) {
      lines.push('## Error logs', 'No error logs for the current filters. Append them before analysis.', '')
    } else {
      lines.push(`## Error logs & call chains (${details.length})`, '')
      details.forEach((d, i) => {
        lines.push(
          `### Error #${i + 1} [${d.created_at}]`,
          `- HTTP ${d.status_code} | phase=${d.phase}/${d.type} | owner=${d.error_owner} | source=${d.error_source} | model=${d.model || '-'}`,
          `- Endpoints: ${d.inbound_endpoint || '-'} -> ${d.upstream_endpoint || '-'}`,
          `- Message: ${collapseText(d.message, 200) || '-'}`,
          `- Error body: ${collapseText(d.error_body, 400) || '-'}`
        )
        if (d.upstream_status_code != null) lines.push(`- Upstream status: ${d.upstream_status_code}`)
        if (d.upstream_error_message) lines.push(`- Upstream error message: ${collapseText(d.upstream_error_message, 200)}`)
        if (d.upstream_error_detail) lines.push(`- Upstream error detail: ${collapseText(d.upstream_error_detail, 400)}`)
        const chain = formatAttemptChain(d.upstream_errors, false)
        lines.push(chain ? `- Call chain:\n${chain}` : '- Call chain: none')
        lines.push('')
      })
    }

    lines.push(
      '## Requirements',
      '1. Group the errors by type, summarize the root causes, and point out the most likely defective code locations (file/function/logic branch).',
      '2. Distinguish platform defects, upstream provider issues, and client issues; only propose fixes for platform code defects.',
      '3. Provide actionable fixes with code patches (diff) for key changes.',
      '4. Follow the project architecture and code style; avoid unrelated changes; describe how to verify the fix.'
    )
  }

  return lines.join('\n')
}
