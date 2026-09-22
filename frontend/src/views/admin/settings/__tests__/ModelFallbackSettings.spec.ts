import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import ModelFallbackSettings from '../ModelFallbackSettings.vue'
import { getModelFallbackPolicy, getModelFallbackPreset, updateModelFallbackPolicy } from '@/api/admin/settings'

vi.mock('@/api/admin/settings', () => ({
  getModelFallbackPolicy: vi.fn(), getModelFallbackPreset: vi.fn(), updateModelFallbackPolicy: vi.fn()
}))

const policy = { enabled: true, tiers: [{ name: 'top', models: ['a', 'b'] }, { name: 'lower', models: ['c'] }] }
async function render() {
  const wrapper = mount(ModelFallbackSettings, { global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: {}, missingWarn: false, fallbackWarn: false })] } })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getModelFallbackPolicy).mockResolvedValue(structuredClone(policy))
  vi.mocked(getModelFallbackPreset).mockResolvedValue(structuredClone(policy))
  vi.mocked(updateModelFallbackPolicy).mockImplementation(async value => value)
})

describe('ModelFallbackSettings', () => {
  it('edits and reorders tiers then saves the ordered policy', async () => {
    const wrapper = await render()
    await wrapper.findAll('textarea')[0].setValue('a\nb\nnew-model')
    await wrapper.findAll('li')[1].find('button[aria-label="admin.settings.modelFallback.moveUp"]').trigger('click')
    await wrapper.find('.btn-primary').trigger('click')
    await flushPromises()
    expect(updateModelFallbackPolicy).toHaveBeenCalledWith({ enabled: true, tiers: [{ name: 'lower', models: ['c'] }, { name: 'top', models: ['a', 'b', 'new-model'] }] })
    expect(wrapper.text()).toContain('admin.settings.modelFallback.saved')
  })

  it('prevents duplicate models across tiers from being saved', async () => {
    const wrapper = await render()
    await wrapper.findAll('textarea')[1].setValue('a')
    expect(wrapper.find('.btn-primary').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('admin.settings.modelFallback.duplicate')
    expect(updateModelFallbackPolicy).not.toHaveBeenCalled()
  })

  it('loads a preset without persisting until save', async () => {
    const wrapper = await render()
    await wrapper.findAll('textarea')[0].setValue('edited')
    const button = wrapper.findAll('button').find(item => item.text() === 'admin.settings.modelFallback.preset')!
    await button.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('textarea')[0].element.value).toBe('a\nb')
    expect(updateModelFallbackPolicy).not.toHaveBeenCalled()
    await wrapper.find('#model-fallback-enabled').setValue(false)
    await wrapper.find('.btn-primary').trigger('click')
    expect(updateModelFallbackPolicy).toHaveBeenCalledWith({ ...policy, enabled: false })
  })

  it('keeps a failed load visible and supports retry', async () => {
    vi.mocked(getModelFallbackPolicy).mockRejectedValueOnce(new Error('offline'))
    const wrapper = await render()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.find('fieldset').exists()).toBe(false)
    await wrapper.find('button').trigger('click')
    await flushPromises()
    expect(wrapper.find('fieldset').exists()).toBe(true)
  })
})
