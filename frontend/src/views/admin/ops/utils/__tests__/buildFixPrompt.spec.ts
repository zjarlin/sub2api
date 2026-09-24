import { describe, expect, it } from 'vitest'
import { formatAttemptChain } from '../buildFixPrompt'

describe('formatAttemptChain', () => {
  it('preserves helper attribution, model and shared budget exhaustion in copied logs', () => {
    const raw = JSON.stringify([
      { stage: 'vision_helper', account_id: 831, model: 'gpt-5.6-luna', image_index: 8, image_count: 8, reason: 'vision_total_timeout' },
      { request_role: 'text', account_id: 837, model: 'kimi-k3' }
    ])
    const lines = formatAttemptChain(raw).split('\n')
    expect(lines[0]).toContain('用途=视觉辅助')
    expect(lines[0]).toContain('模型=gpt-5.6-luna')
    expect(lines[0]).toContain('图片=8/8')
    expect(lines[0]).toContain('整轮视觉预算耗尽')
    expect(lines[0]).not.toContain('kimi-k3')
    expect(lines[1]).toContain('用途=文本主请求')
    expect(lines[1]).toContain('模型=kimi-k3')
    expect(formatAttemptChain(raw, false)).toContain('role=vision helper')
    expect(formatAttemptChain(raw, false)).toContain('role=text primary request')
  })
})
