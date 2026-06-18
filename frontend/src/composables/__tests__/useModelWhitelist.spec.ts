import { describe, expect, it, vi } from 'vitest'

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn()
}))

import {
  buildModelMappingObject,
  fetchKiroDefaultMappings,
  getModelsByPlatform,
  getPresetMappingsByPlatform,
  splitModelMappingObject
} from '../useModelWhitelist'

describe('useModelWhitelist', () => {
  it('openai 模型列表包含 GPT-5.4 官方快照', () => {
    const models = getModelsByPlatform('openai')

    expect(models).toContain('gpt-5.4')
    expect(models).toContain('gpt-5.4-mini')
    expect(models).toContain('gpt-5.4-2026-03-05')
    expect(models).toContain('codex-auto-review')
  })

  it('openai 模型列表不再暴露已下线的 ChatGPT 登录 Codex 模型', () => {
    const models = getModelsByPlatform('openai')

    expect(models).not.toContain('gpt-5')
    expect(models).not.toContain('gpt-5.1')
    expect(models).not.toContain('gpt-5.1-codex')
    expect(models).not.toContain('gpt-5.1-codex-max')
    expect(models).not.toContain('gpt-5.1-codex-mini')
    expect(models).not.toContain('gpt-5.2-codex')
  })

  it('antigravity 模型列表包含图片模型兼容项', () => {
    const models = getModelsByPlatform('antigravity')

    expect(models).toContain('gemini-2.5-flash-image')
    expect(models).toContain('gemini-3.1-flash-image')
    expect(models).toContain('gemini-3-pro-image')
  })

  it('Claude 模型列表包含 Opus 4.8', () => {
    expect(getModelsByPlatform('claude')).toContain('claude-opus-4-8')
    expect(getModelsByPlatform('antigravity')).toContain('claude-opus-4-8')
  })

  it('gemini 模型列表包含原生生图模型', () => {
    const models = getModelsByPlatform('gemini')

    expect(models).toContain('gemini-2.5-flash-image')
    expect(models).toContain('gemini-3.1-flash-image')
    expect(models.indexOf('gemini-3.1-flash-image')).toBeLessThan(models.indexOf('gemini-2.0-flash'))
    expect(models.indexOf('gemini-2.5-flash-image')).toBeLessThan(models.indexOf('gemini-2.5-flash'))
  })

  it('deepseek 模型列表和预设映射支持 Codex 兼容入口', () => {
    const models = getModelsByPlatform('deepseek')
    const mappings = getPresetMappingsByPlatform('deepseek')

    expect(models).toContain('deepseek-v4-pro')
    expect(models).toContain('deepseek-v4-flash')
    expect(models).toContain('deepseek-chat')
    expect(models).toContain('deepseek-reasoner')
    expect(mappings.map(({ from, to }) => ({ from, to }))).toEqual(expect.arrayContaining([
      { from: 'gpt-5.4', to: 'deepseek-v4-pro' },
      { from: 'gpt-5.4', to: 'deepseek-v4-flash' },
      { from: 'deepseek-reasoner', to: 'deepseek-v4-flash' },
      { from: 'deepseek-chat', to: 'deepseek-v4-flash' }
    ]))
  })

  it('opencode 模型列表暴露 OpenCode Zen 免费模型并默认映射到免费模型', () => {
    const models = getModelsByPlatform('opencode')
    const mappings = getPresetMappingsByPlatform('opencode')

    expect(models).toEqual(expect.arrayContaining([
      'deepseek-v4-flash-free',
      'big-pickle'
    ]))
    expect(mappings.map(({ from, to }) => ({ from, to }))).toEqual([
      { from: 'deepseek-v4-flash-free', to: 'deepseek-v4-flash-free' },
      { from: 'big-pickle', to: 'big-pickle' },
      { from: 'gpt-*', to: 'deepseek-v4-flash-free' },
      { from: 'claude-*', to: 'deepseek-v4-flash-free' }
    ])
  })

  it('opencode-go 模型列表只覆盖官方 OpenAI 兼容端点模型', () => {
    const models = getModelsByPlatform('opencode-go')
    const mappings = getPresetMappingsByPlatform('opencode-go')

    expect(models).toEqual([
      'opencode-go/glm-5.1',
      'opencode-go/glm-5',
      'opencode-go/kimi-k2.7-code',
      'opencode-go/kimi-k2.6',
      'opencode-go/deepseek-v4-pro',
      'opencode-go/deepseek-v4-flash',
      'opencode-go/mimo-v2.5',
      'opencode-go/mimo-v2.5-pro'
    ])
    expect(mappings.map(({ from, to }) => ({ from, to }))).toEqual([
      { from: 'opencode-go/glm-5.1', to: 'glm-5.1' },
      { from: 'opencode-go/glm-5', to: 'glm-5' },
      { from: 'opencode-go/kimi-k2.7-code', to: 'kimi-k2.7' },
      { from: 'opencode-go/kimi-k2.6', to: 'kimi-k2.6' },
      { from: 'opencode-go/deepseek-v4-pro', to: 'deepseek-v4-pro' },
      { from: 'opencode-go/deepseek-v4-flash', to: 'deepseek-v4-flash' },
      { from: 'opencode-go/mimo-v2.5', to: 'mimo-v2.5' },
      { from: 'opencode-go/mimo-v2.5-pro', to: 'mimo-v2.5-pro' }
    ])
  })

  it('opencode-go-anthropic 只暴露官方 Anthropic Messages 端点模型', () => {
    const models = getModelsByPlatform('opencode-go-anthropic')
    const mappings = getPresetMappingsByPlatform('opencode-go-anthropic')

    expect(models).toEqual([
      'opencode-go/minimax-m3',
      'opencode-go/minimax-m2.7',
      'opencode-go/minimax-m2.5',
      'opencode-go/qwen3.7-max',
      'opencode-go/qwen3.7-plus',
      'opencode-go/qwen3.6-plus'
    ])
    expect(mappings.map(({ from, to }) => ({ from, to }))).toEqual([
      { from: 'opencode-go/minimax-m3', to: 'minimax-m3' },
      { from: 'opencode-go/minimax-m2.7', to: 'minimax-m2.7' },
      { from: 'opencode-go/minimax-m2.5', to: 'minimax-m2.5' },
      { from: 'opencode-go/qwen3.7-max', to: 'qwen3.7-max' },
      { from: 'opencode-go/qwen3.7-plus', to: 'qwen3.7-plus' },
      { from: 'opencode-go/qwen3.6-plus', to: 'qwen3.6-plus' },
      { from: 'claude-*', to: 'minimax-m3' }
    ])
  })

  it('doubao-web 模型列表和预设映射匹配网页逆向后端', () => {
    const models = getModelsByPlatform('doubao-web')
    const mappings = getPresetMappingsByPlatform('doubao-web')

    expect(models).toEqual([
      'doubao',
      'doubao:doubao',
      'doubao-pro',
      'doubao:doubao-pro'
    ])
    expect(mappings.map(({ from, to }) => ({ from, to }))).toEqual([
      { from: 'doubao', to: 'doubao' },
      { from: 'doubao:doubao', to: 'doubao' },
      { from: 'doubao-pro', to: 'doubao-pro' },
      { from: 'doubao:doubao-pro', to: 'doubao-pro' }
    ])
  })

  it('antigravity 模型列表会把新的 Gemini 图片模型排在前面', () => {
    const models = getModelsByPlatform('antigravity')

    expect(models.indexOf('gemini-3.1-flash-image')).toBeLessThan(models.indexOf('gemini-2.5-flash'))
    expect(models.indexOf('gemini-2.5-flash-image')).toBeLessThan(models.indexOf('gemini-2.5-flash-lite'))
  })

  it('kiro 模型列表不暴露旧的 -agentic / -chat 后缀', () => {
    const models = getModelsByPlatform('kiro')

    expect(models).toContain('claude-sonnet-4-6')
    expect(models).toContain('claude-sonnet-4-6-thinking')
    expect(models).not.toContain('claude-sonnet-4-6-chat')
    expect(models.every((model) => !model.endsWith('-agentic') && !model.endsWith('-chat'))).toBe(true)
  })

  it('kiro 模型列表只保留 Claude 模型', () => {
    const models = getModelsByPlatform('kiro')

    expect(models).toEqual([
      'claude-opus-4-7',
      'claude-opus-4-7-thinking',
      'claude-sonnet-4-6',
      'claude-sonnet-4-6-thinking',
      'claude-haiku-4-5-20251001',
      'claude-haiku-4-5-20251001-thinking'
    ])
    expect(models.every(model => model.startsWith('claude-'))).toBe(true)
    expect(models.some(model => model.endsWith('-agentic'))).toBe(false)
    expect(models.some(model => model.endsWith('-chat'))).toBe(false)
    expect(models).not.toContain('kiro-auto')
    expect(models).not.toContain('claude-opus-4-5')
    expect(models).not.toContain('claude-opus-4-6')
    expect(models).not.toContain('claude-sonnet-4-5')
    expect(models).not.toContain('claude-sonnet-4')
    expect(models).not.toContain('claude-3-5-sonnet-20241022')
    expect(models).not.toContain('claude-3-5-haiku-20241022')
    expect(models).not.toContain('claude-haiku-4-5')
    expect(models).not.toContain('gpt-4o')
    expect(models).not.toContain('gpt-4')
    expect(models).not.toContain('gpt-4-turbo')
    expect(models).not.toContain('gpt-3.5-turbo')
    expect(models).not.toContain('deepseek-3-2')
    expect(models).not.toContain('minimax-m2-1')
    expect(models).not.toContain('qwen3-coder-next')
  })

  it('claude 模型列表包含 dated 和 thinking 兼容别名', () => {
    const models = getModelsByPlatform('claude')

    expect(models).toContain('claude-opus-4-6-thinking')
    expect(models).toContain('claude-opus-4-5-20251101-thinking')
    expect(models).toContain('claude-sonnet-4-20250514-thinking')
    expect(models).toContain('claude-haiku-4-5-20251001-thinking')
  })

  it('whitelist 模式会忽略通配符条目', () => {
    const mapping = buildModelMappingObject('whitelist', ['claude-*', 'gemini-3.1-flash-image'], [])
    expect(mapping).toEqual({
      'gemini-3.1-flash-image': 'gemini-3.1-flash-image'
    })
  })

  it('whitelist 模式会保留 GPT-5.4 官方快照的精确映射', () => {
    const mapping = buildModelMappingObject('whitelist', ['gpt-5.4-2026-03-05'], [])

    expect(mapping).toEqual({
      'gpt-5.4-2026-03-05': 'gpt-5.4-2026-03-05'
    })
  })

  it('whitelist keeps GPT-5.4 mini exact mappings', () => {
    const mapping = buildModelMappingObject('whitelist', ['gpt-5.4-mini'], [])

    expect(mapping).toEqual({
      'gpt-5.4-mini': 'gpt-5.4-mini'
    })
  })

  it('kiro 预设映射只暴露 Claude 入口', () => {
    const mappings = getPresetMappingsByPlatform('kiro')
    const mappingTargets = mappings.map(item => item.to)

    expect(mappings.map(({ from, to }) => ({ from, to }))).toEqual([
      { from: 'claude-opus-4-7', to: 'claude-opus-4.7' },
      { from: 'claude-opus-4-7-thinking', to: 'claude-opus-4.7' },
      { from: 'claude-sonnet-4-6', to: 'claude-sonnet-4.6' },
      { from: 'claude-sonnet-4-6-thinking', to: 'claude-sonnet-4.6' },
      { from: 'claude-haiku-4-5-20251001', to: 'claude-haiku-4.5' },
      { from: 'claude-haiku-4-5-20251001-thinking', to: 'claude-haiku-4.5' }
    ])
    expect(mappings.every(item => item.from.startsWith('claude-'))).toBe(true)
    expect(mappingTargets.every(model => model.startsWith('claude-'))).toBe(true)
    expect(mappingTargets.some(model => model.endsWith('-agentic'))).toBe(false)
    expect(mappingTargets.some(model => model.endsWith('-chat'))).toBe(false)
    expect(mappingTargets).not.toContain('kiro-auto')
    expect(mappingTargets.some(model => model.startsWith('kiro-'))).toBe(false)
    expect(mappings.some(item => item.from === 'claude-opus-4-5')).toBe(false)
    expect(mappings.some(item => item.from === 'claude-sonnet-4-5')).toBe(false)
    expect(mappings.some(item => item.from === 'claude-sonnet-4')).toBe(false)
    expect(mappings.some(item => item.from === 'claude-3-5-sonnet-20241022')).toBe(false)
    expect(mappings.some(item => item.from === 'claude-3-5-haiku-20241022')).toBe(false)
    expect(mappings.some(item => item.from === 'claude-haiku-4-5')).toBe(false)
    expect(mappings.some(item => item.from === 'claude-opus-4-6')).toBe(false)
    expect(mappings.some(item => item.from === 'claude-opus-4-5-20251101')).toBe(false)
    expect(mappings.some(item => item.from === 'claude-sonnet-4-5-20250929')).toBe(false)
    expect(mappingTargets).not.toContain('gpt-4o')
    expect(mappingTargets).not.toContain('gpt-4')
    expect(mappingTargets).not.toContain('gpt-4-turbo')
    expect(mappingTargets).not.toContain('gpt-3.5-turbo')
    expect(mappingTargets).not.toContain('deepseek-3.2')
    expect(mappingTargets).not.toContain('minimax-m2.1')
    expect(mappingTargets).not.toContain('qwen3-coder-next')
  })

  it('kiro 默认映射会在前端填充所有可精确定价模型', async () => {
    const mappings = await fetchKiroDefaultMappings()

    expect(mappings).toEqual(expect.arrayContaining([
      { from: 'claude-opus-4-7', to: 'claude-opus-4.7' },
      { from: 'claude-opus-4-7-thinking', to: 'claude-opus-4.7' },
      { from: 'claude-sonnet-4-6', to: 'claude-sonnet-4.6' },
      { from: 'claude-sonnet-4-6-thinking', to: 'claude-sonnet-4.6' },
      { from: 'claude-haiku-4-5-20251001', to: 'claude-haiku-4.5' },
      { from: 'claude-haiku-4-5-20251001-thinking', to: 'claude-haiku-4.5' }
    ]))
    expect(mappings).toHaveLength(6)
    expect(mappings.every(item => !item.from.startsWith('kiro-'))).toBe(true)
    expect(mappings.every(item => !item.to.startsWith('kiro-'))).toBe(true)
    expect(mappings.every(item => !item.from.endsWith('-agentic'))).toBe(true)
    expect(mappings.every(item => !item.to.endsWith('-agentic'))).toBe(true)
    expect(mappings.every(item => !item.from.endsWith('-chat'))).toBe(true)
    expect(mappings.every(item => !item.to.endsWith('-chat'))).toBe(true)
    expect(mappings.every(item => item.from.startsWith('claude-'))).toBe(true)
    expect(mappings.every(item => item.to.startsWith('claude-'))).toBe(true)
    expect(mappings.some(item => item.from === 'claude-opus-4-6')).toBe(false)
    expect(mappings.some(item => item.from === 'claude-opus-4-5-20251101')).toBe(false)
    expect(mappings.some(item => item.from === 'claude-sonnet-4-5-20250929')).toBe(false)
    expect(mappings.some(item => item.to === 'claude-opus-4.7')).toBe(true)
  })

  it('combined 模式会同时保留白名单身份映射和模型映射', () => {
    const mapping = buildModelMappingObject(
      'combined',
      ['gpt-5.4', 'claude-*'],
      [
        { from: 'gpt-latest', to: 'gpt-5.4' },
        { from: 'gpt-5.4', to: 'gpt-5.4-mini' }
      ]
    )

    expect(mapping).toEqual({
      'gpt-5.4': 'gpt-5.4-mini',
      'gpt-latest': 'gpt-5.4'
    })
  })

  it('splitModelMappingObject 会把身份映射还原成白名单，其余保留为映射', () => {
    const parsed = splitModelMappingObject({
      'gpt-5.4': 'gpt-5.4',
      'gpt-latest': 'gpt-5.4',
      ' ': 'gpt-empty',
      broken: 123
    })

    expect(parsed).toEqual({
      allowedModels: ['gpt-5.4'],
      modelMappings: [{ from: 'gpt-latest', to: 'gpt-5.4' }]
    })
  })
})
