import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const {
  copyToClipboard,
  showError,
  showSuccess,
  showInfo,
  showWarning,
  syncUpstreamModels,
  syncUpstreamModelsPreview,
  getModelAliasPolicy
} = vi.hoisted(() => ({
  copyToClipboard: vi.fn().mockResolvedValue(true),
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showInfo: vi.fn(),
  showWarning: vi.fn(),
  syncUpstreamModels: vi.fn(),
  syncUpstreamModelsPreview: vi.fn(),
  getModelAliasPolicy: vi.fn()
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => (key === 'common.copy' ? '复制' : key)
    })
  }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
    showInfo,
    showWarning
  })
}))

vi.mock('@/api/admin/accounts', () => ({
  accountsAPI: {
    syncUpstreamModels,
    syncUpstreamModelsPreview
  }
}))

vi.mock('@/api/admin/settings', () => ({
  getModelAliasPolicy
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard
  })
}))

import ModelWhitelistSelector from '../ModelWhitelistSelector.vue'

function mountSelector(props: Record<string, unknown> = {}) {
  return mount(ModelWhitelistSelector, {
    props: {
      modelValue: [],
      enableAliasMapping: true,
      platform: 'openai',
      ...props,
    },
    global: {
      stubs: {
        ModelIcon: true
      }
    }
  })
}

function findModelRow(wrapper: ReturnType<typeof mountSelector>, modelId: string) {
  const row = wrapper
    .findAll('[data-testid="model-option"]')
    .find(candidate => candidate.text().includes(modelId))

  if (!row) {
    throw new Error(`Model row not found: ${modelId}`)
  }

  return row
}

describe('ModelWhitelistSelector', () => {
  beforeEach(() => {
    copyToClipboard.mockClear()
    showError.mockReset()
    showSuccess.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    syncUpstreamModels.mockReset()
    syncUpstreamModelsPreview.mockReset()
    getModelAliasPolicy.mockReset()
  })

  it('hides alias import for consumers that cannot handle mappings', () => {
    const wrapper = mountSelector({ enableAliasMapping: false })
    expect(wrapper.find('[data-testid="sync-model-aliases"]').exists()).toBe(false)
  })

  it('syncs Arena session aliases and exposes them in the picker', async () => {
    syncUpstreamModels.mockResolvedValue({ models: ['arena-session'] })
    const wrapper = mountSelector({ platform: 'arena', accountId: 46 })
    expect(wrapper.text()).toContain('admin.accounts.arena.modelsHint')
    expect(wrapper.text()).not.toContain('admin.accounts.fillRelatedModels')
    const button = wrapper.findAll('button').find(item => item.text() === 'admin.accounts.syncUpstreamModels')!
    await button.trigger('click')
    await flushPromises()
    expect(syncUpstreamModels).toHaveBeenCalledWith(46)
    expect(wrapper.emitted('update:modelValue')).toEqual([[['arena-session']]])
    await wrapper.get('div.cursor-pointer').trigger('click')
    expect(findModelRow(wrapper, 'arena-session').exists()).toBe(true)
  })

  it('explains an empty Arena catalog without inventing models', async () => {
    syncUpstreamModels.mockResolvedValue({ models: [] })
    const wrapper = mountSelector({ platform: 'arena', accountId: 46 })
    const button = wrapper.findAll('button').find(item => item.text() === 'admin.accounts.syncUpstreamModels')!
    await button.trigger('click')
    await flushPromises()
    expect(showInfo).toHaveBeenCalledWith('admin.accounts.arena.modelsEmpty')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('copies a model ID without selecting the model', async () => {
    const wrapper = mountSelector()
    await wrapper.get('div.cursor-pointer').trigger('click')

    const row = findModelRow(wrapper, 'gpt-5.6-sol')

    const copyButton = row.get('[data-testid="copy-model-id"]')
    expect(copyButton.attributes('aria-label')).toBe('复制 gpt-5.6-sol')

    await copyButton.trigger('click')
    await flushPromises()

    expect(copyToClipboard).toHaveBeenCalledWith('gpt-5.6-sol')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('copies an already selected model ID without removing it', async () => {
    const wrapper = mountSelector({ modelValue: ['gpt-5.6-sol'] })

    await wrapper.get('[data-testid="copy-selected-model-id"]').trigger('click')
    await flushPromises()

    expect(copyToClipboard).toHaveBeenCalledWith('gpt-5.6-sol')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.find('[data-testid="model-option"]').exists()).toBe(false)
  })

  it('maps all synonyms to a selected provider ID even when the synonyms are not in the picker', async () => {
    getModelAliasPolicy.mockResolvedValue({
      groups: [
        { canonical: 'deepseek-v4.1-flash', aliases: ['cline-pass/deepseek-v4.1-flash', 'DeepSeek-V4.1-Flash'] },
        { canonical: 'gpt-5.6', aliases: ['gpt-latest'] }
      ]
    })
    const wrapper = mountSelector({ modelValue: ['cline-pass/deepseek-v4.1-flash'] })
    await wrapper.get('[data-testid="sync-model-aliases"]').trigger('click')
    await flushPromises()

    expect(wrapper.emitted('global-aliases-mapped')).toEqual([[[
      { from: 'deepseek-v4.1-flash', to: 'cline-pass/deepseek-v4.1-flash' },
      { from: 'DeepSeek-V4.1-Flash', to: 'cline-pass/deepseek-v4.1-flash' }
    ]]])
  })

  it('reports no matches without changing models or adding mappings', async () => {
    getModelAliasPolicy.mockResolvedValue({ groups: [{ canonical: 'gpt-5.6', aliases: ['gpt-latest'] }] })
    const wrapper = mountSelector({ modelValue: ['unrelated-model'] })
    await wrapper.get('[data-testid="sync-model-aliases"]').trigger('click')
    await flushPromises()

    expect(showInfo).toHaveBeenCalledWith('admin.accounts.syncModelAliasesEmpty')
    expect(wrapper.emitted('global-aliases-mapped')).toBeUndefined()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('handles alias policy failures and re-enables the action', async () => {
    getModelAliasPolicy.mockRejectedValue(new Error('request failed'))
    const wrapper = mountSelector()
    await wrapper.get('[data-testid="sync-model-aliases"]').trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.accounts.syncModelAliasesError')
    expect(wrapper.get('[data-testid="sync-model-aliases"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.emitted('global-aliases-mapped')).toBeUndefined()
  })

  it('ignores alias responses belonging to a previous account', async () => {
    let resolvePolicy!: (value: { groups: Array<{ canonical: string; aliases: string[] }> }) => void
    getModelAliasPolicy.mockImplementation(() => new Promise(resolve => { resolvePolicy = resolve }))
    const wrapper = mountSelector({ accountId: 1, modelValue: ['gpt-5.6'] })
    await wrapper.get('[data-testid="sync-model-aliases"]').trigger('click')
    await wrapper.setProps({ accountId: 2 })
    resolvePolicy({ groups: [{ canonical: 'gpt-5.6', aliases: ['gpt-latest'] }] })
    await flushPromises()

    expect(wrapper.emitted('global-aliases-mapped')).toBeUndefined()
    expect(showSuccess).not.toHaveBeenCalled()
  })

  it('reads global aliases and maps an alias to the synced upstream model ID', async () => {
    syncUpstreamModels.mockResolvedValue({ models: ['provider/gpt-5.6'] })
    getModelAliasPolicy.mockResolvedValue({
      groups: [{ canonical: 'gpt-5.6', aliases: ['provider/gpt-5.6'] }]
    })
    const wrapper = mountSelector({ accountId: 46 })

    const syncButton = wrapper
      .findAll('button')
      .find(button => button.text() === 'admin.accounts.syncUpstreamModels')
    await syncButton?.trigger('click')
    await flushPromises()

    const aliasButton = wrapper
      .findAll('button')
      .find(button => button.text() === 'admin.accounts.syncModelAliases')
    expect(aliasButton).toBeDefined()
    await aliasButton?.trigger('click')
    await flushPromises()

    expect(getModelAliasPolicy).toHaveBeenCalledOnce()
    expect(wrapper.emitted('global-aliases-mapped')).toEqual([
      [[{ from: 'gpt-5.6', to: 'provider/gpt-5.6' }]]
    ])
  })

  it('keeps the existing model selection behavior', async () => {
    const wrapper = mountSelector()
    await wrapper.get('div.cursor-pointer').trigger('click')

    const row = findModelRow(wrapper, 'gpt-5.6-sol')
    await row.get('[data-testid="select-model"]').trigger('click')

    expect(wrapper.emitted('update:modelValue')).toEqual([[['gpt-5.6-sol']]])
    expect(copyToClipboard).not.toHaveBeenCalled()
  })

  it('warns when model IDs sync but capability metadata is incomplete', async () => {
    syncUpstreamModels.mockResolvedValue({
      models: ['x-preview-f-free'],
      warnings: [
        {
          code: 'upstream_model_metadata_incomplete',
          message: 'Model IDs were synced, but capability metadata could not be updated.'
        }
      ]
    })
    const wrapper = mount(ModelWhitelistSelector, {
      props: {
        modelValue: [],
        platform: 'openai',
        accountId: 46
      },
      global: {
        stubs: {
          ModelIcon: true
        }
      }
    })

    const syncButton = wrapper
      .findAll('button')
      .find(button => button.text() === 'admin.accounts.syncUpstreamModels')
    expect(syncButton).toBeDefined()
    await syncButton!.trigger('click')
    await flushPromises()

    expect(wrapper.emitted('update:modelValue')).toEqual([[['x-preview-f-free']]])
    expect(showWarning).toHaveBeenCalledWith('admin.accounts.syncUpstreamModelsMetadataIncomplete')
    expect(showSuccess).not.toHaveBeenCalled()
  })

  it('shows success and a partial warning when some capabilities were saved', async () => {
    syncUpstreamModels.mockResolvedValue({
      models: ['gpt-6-astra', 'gpt-image-2'],
      warnings: [
        {
          code: 'upstream_model_metadata_partial',
          message: 'Some model capabilities were saved; remaining models are still incomplete.'
        }
      ]
    })
    const wrapper = mount(ModelWhitelistSelector, {
      props: {
        modelValue: [],
        platform: 'openai',
        accountId: 46
      },
      global: {
        stubs: {
          ModelIcon: true
        }
      }
    })

    const syncButton = wrapper
      .findAll('button')
      .find(button => button.text() === 'admin.accounts.syncUpstreamModels')
    expect(syncButton).toBeDefined()
    await syncButton!.trigger('click')
    await flushPromises()

    expect(wrapper.emitted('update:modelValue')).toEqual([[['gpt-6-astra', 'gpt-image-2']]])
    expect(showSuccess).toHaveBeenCalledWith('admin.accounts.syncUpstreamModelsSuccess')
    expect(showWarning).toHaveBeenCalledWith('admin.accounts.syncUpstreamModelsMetadataPartial')
  })

  it('reports a successful preview so account creation can persist metadata', async () => {
    syncUpstreamModelsPreview.mockResolvedValue({
      models: ['x-preview-f-free'],
      metadata: {
        'x-preview-f-free': {
          id: 'x-preview-f-free',
          reasoning: true,
          supported_reasoning_levels: ['low', 'high', 'max'],
        },
      },
    })
    const wrapper = mountSelector({
      syncCredentials: {
        platform: 'openai',
        type: 'apikey',
        base_url: 'https://opencode.ai/zen/v1',
        api_key: 'test-key',
      },
    })
    const syncButton = wrapper
      .findAll('button')
      .find(button => button.text() === 'admin.accounts.syncUpstreamModels')

    expect(syncButton).toBeDefined()
    await syncButton?.trigger('click')
    await flushPromises()

    expect(syncUpstreamModelsPreview).toHaveBeenCalledOnce()
    expect(wrapper.emitted('upstream-synced')).toEqual([[]])
    expect(wrapper.emitted('update:modelValue')).toEqual([[['x-preview-f-free']]])
  })

  it('shows the upstream sync button for OpenCode Go create-account credentials', () => {
    const wrapper = mountSelector({
      platform: 'opencode_go',
      syncCredentials: {
        platform: 'opencode_go',
        type: 'apikey',
        base_url: 'https://opencode.ai/zen/go/v1',
        api_key: 'sk-test',
      },
    })
    const syncButton = wrapper
      .findAll('button')
      .find(button => button.text() === 'admin.accounts.syncUpstreamModels')

    expect(syncButton).toBeDefined()
    expect(syncButton?.exists()).toBe(true)
  })
})
