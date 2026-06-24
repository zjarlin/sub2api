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

  it('exposes OpenCode Zen with free model preset defaults', () => {
    const preset = getOpenAIVendorPreset('opencode')

    expect(listOpenAIVendorPresets().map(item => item.id)).toContain('opencode')
    expect(preset.baseUrl).toBe('https://opencode.ai/zen/v1')
    expect(preset.authHeader).toBe('authorization')
    expect(preset.authScheme).toBe('bearer')
    expect(preset.modelPlatforms).toEqual(['opencode'])
    expect(preset.presetPlatform).toBe('opencode')
  })

  it('exposes OpenCode Go as the official Go API preset', () => {
    const preset = getOpenAIVendorPreset('opencode-go')

    expect(listOpenAIVendorPresets().map(item => item.id)).toContain('opencode-go')
    expect(preset.baseUrl).toBe('https://opencode.ai/zen/go/v1')
    expect(preset.authHeader).toBe('authorization')
    expect(preset.authScheme).toBe('bearer')
    expect(preset.modelPlatforms).toEqual(['opencode-go'])
    expect(preset.presetPlatform).toBe('opencode-go')
  })

  it('infers OpenCode from the current OpenCode Go API host', () => {
    expect(inferOpenAIVendorPreset({
      baseUrl: 'https://opencode.ai/zen/go/v1',
      authHeader: 'Authorization',
      authScheme: 'Bearer'
    })).toBe('opencode-go')
  })

  it('infers OpenCode Go from the local OpenCode serve host', () => {
    expect(inferOpenAIVendorPreset({
      baseUrl: 'http://host.docker.internal:4096',
      authHeader: 'Authorization',
      authScheme: 'Bearer'
    })).toBe('opencode-go')
  })

  it('infers OpenCode Zen from the Zen API host', () => {
    expect(inferOpenAIVendorPreset({
      baseUrl: 'https://opencode.ai/zen/v1',
      authHeader: 'Authorization',
      authScheme: 'Bearer'
    })).toBe('opencode')
  })

  it('exposes Doubao Web reverse preset backed by sessionid', () => {
    const preset = getOpenAIVendorPreset('doubao-web')

    expect(listOpenAIVendorPresets().map(item => item.id)).toContain('doubao-web')
    expect(preset.baseUrl).toBe('https://www.doubao.com')
    expect(preset.apiKeyPlaceholder).toBe('sessionid')
    expect(preset.modelPlatforms).toEqual(['doubao-web'])
    expect(preset.presetPlatform).toBe('doubao-web')
  })

  it('infers Doubao Web from doubao.com', () => {
    expect(inferOpenAIVendorPreset({
      baseUrl: 'https://www.doubao.com',
      authHeader: 'Authorization',
      authScheme: 'Bearer'
    })).toBe('doubao-web')
  })

  it('exposes Seedance with ModelArk video task defaults', () => {
    const preset = getOpenAIVendorPreset('seedance')

    expect(listOpenAIVendorPresets().map(item => item.id)).toContain('seedance')
    expect(preset.baseUrl).toBe('https://ark.ap-southeast.bytepluses.com/api/v3')
    expect(preset.authHeader).toBe('authorization')
    expect(preset.authScheme).toBe('bearer')
    expect(preset.modelPlatforms).toEqual(['seedance'])
    expect(preset.presetPlatform).toBe('seedance')
  })

  it('exposes ChatGPT-Web2API as a local OpenAI-compatible preset', () => {
    const preset = getOpenAIVendorPreset('chatgpt-web2api')

    expect(listOpenAIVendorPresets().map(item => item.id)).toContain('chatgpt-web2api')
    expect(preset.baseUrl).toBe('http://127.0.0.1:8080/v1')
    expect(preset.authHeader).toBe('authorization')
    expect(preset.authScheme).toBe('bearer')
    expect(preset.modelPlatforms).toEqual(['chatgpt-web2api'])
    expect(preset.presetPlatform).toBe('chatgpt-web2api')
  })

  it('infers ChatGPT-Web2API from its default local port', () => {
    expect(inferOpenAIVendorPreset({
      baseUrl: 'http://localhost:8080/v1',
      authHeader: 'Authorization',
      authScheme: 'Bearer'
    })).toBe('chatgpt-web2api')
  })
})
