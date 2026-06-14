import { describe, expect, it } from 'vitest'

import {
  getOpenAIVendorPreset,
  inferOpenAIVendorPreset,
  listOpenAIVendorPresets
} from '../useOpenAIVendorPreset'

describe('useOpenAIVendorPreset', () => {
  it('exposes DeepSeek with OpenAI-compatible chat/completions defaults', () => {
    const preset = getOpenAIVendorPreset('deepseek')

    expect(listOpenAIVendorPresets().map(item => item.id)).toContain('deepseek')
    expect(preset.baseUrl).toBe('https://api.deepseek.com')
    expect(preset.authHeader).toBe('authorization')
    expect(preset.authScheme).toBe('bearer')
    expect(preset.modelPlatforms).toEqual(['deepseek'])
    expect(preset.presetPlatform).toBe('deepseek')
  })

  it('infers DeepSeek from the official API host', () => {
    expect(inferOpenAIVendorPreset({
      baseUrl: 'https://api.deepseek.com',
      authHeader: 'Authorization',
      authScheme: 'Bearer'
    })).toBe('deepseek')
  })
})
