import { describe, expect, it } from 'vitest'
import { serializeModelMappings } from '@/utils/accountFormBulk'

describe('serializeModelMappings', () => {
  it('normalizes mappings into bulk-import friendly lines', () => {
    expect(serializeModelMappings([
      { from: ' gpt-5.4 ', to: ' deepseek-v4-pro[1m] ' },
      { from: 'claude-opus-4-7', to: 'deepseek-v4-pro[1m]' },
      { from: 'gpt-5.4', to: 'gpt-5.4-latest' },
      { from: '', to: 'ignored' },
      { from: 'ignored', to: '' }
    ])).toBe([
      'gpt-5.4 => gpt-5.4-latest',
      'claude-opus-4-7 => deepseek-v4-pro[1m]'
    ].join('\n'))
  })
})
