import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import VisionFallbackSettings from '../VisionFallbackSettings.vue'
import { getVisionFallbackPolicy, updateVisionFallbackPolicy } from '@/api/admin/settings'

vi.mock('@/api/admin/settings', () => ({ getVisionFallbackPolicy: vi.fn(), updateVisionFallbackPolicy: vi.fn() }))
const policy = { enabled: true, models: ['preferred', 'backup'], allow_unlisted_models: true, candidate_timeout_seconds: 60, timeout_seconds: 120 }
async function render() {
  const wrapper = mount(VisionFallbackSettings, { global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: {}, missingWarn: false, fallbackWarn: false })] } })
  await flushPromises()
  return wrapper
}
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getVisionFallbackPolicy).mockResolvedValue(structuredClone(policy))
  vi.mocked(updateVisionFallbackPolicy).mockImplementation(async value => value)
})
describe('VisionFallbackSettings', () => {
  it('persists exact model order, allowed scope and both budgets', async () => {
    const wrapper = await render()
    await wrapper.get('textarea').setValue('backup\npreferred\nthird')
    await wrapper.get('#vision-fallback-unlisted').setValue(false)
    await wrapper.get('#vision-fallback-candidate-timeout').setValue(11)
    await wrapper.get('#vision-fallback-timeout').setValue(25)
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    expect(updateVisionFallbackPolicy).toHaveBeenCalledWith({ ...policy, models: ['backup', 'preferred', 'third'], allow_unlisted_models: false, candidate_timeout_seconds: 11, timeout_seconds: 25 })
    expect(wrapper.text()).toContain('visionFallback.saved')
    await wrapper.get('#vision-fallback-enabled').setValue(false)
    expect(wrapper.text()).not.toContain('visionFallback.saved')
  })
  it('rejects duplicate models, missing candidates and invalid budgets', async () => {
    const wrapper = await render()
    for (const models of ['a\na', 'a*', 'a b']) {
      await wrapper.get('textarea').setValue(models)
      expect(wrapper.get('.btn-primary').attributes('disabled')).toBeDefined()
    }
    await wrapper.get('textarea').setValue('')
    await wrapper.get('#vision-fallback-unlisted').setValue(false)
    expect(wrapper.text()).toContain('visionFallback.required')
    await wrapper.get('#vision-fallback-enabled').setValue(false)
    expect(wrapper.get('.btn-primary').attributes('disabled')).toBeUndefined()
    await wrapper.get('#vision-fallback-timeout').setValue(0)
    expect(wrapper.text()).toContain('visionFallback.invalidTimeout')
    expect(updateVisionFallbackPolicy).not.toHaveBeenCalled()
  })
  it('allows automatic selection with an empty preferred list', async () => {
    const wrapper = await render()
    await wrapper.get('textarea').setValue('')
    await wrapper.get('.btn-primary').trigger('click')
    expect(updateVisionFallbackPolicy).toHaveBeenCalledWith({ ...policy, models: [] })
  })
  it('retains edits on save failure and supports retry', async () => {
    vi.mocked(updateVisionFallbackPolicy).mockRejectedValueOnce(new Error('offline'))
    const wrapper = await render()
    await wrapper.get('textarea').setValue('changed')
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.get('textarea').element.value).toBe('changed')
    expect(wrapper.text()).not.toContain('visionFallback.saved')
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('visionFallback.saved')
  })
  it('does not offer a save after a failed load', async () => {
    vi.mocked(getVisionFallbackPolicy).mockRejectedValueOnce(new Error('offline'))
    const wrapper = await render()
    expect(wrapper.find('fieldset').exists()).toBe(false)
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.find('fieldset').exists()).toBe(true)
  })
})
