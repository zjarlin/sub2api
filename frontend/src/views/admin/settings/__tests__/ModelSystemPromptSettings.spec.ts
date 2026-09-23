import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import ModelSystemPromptSettings from '../ModelSystemPromptSettings.vue'
import { getModelSystemPromptPolicy, updateModelSystemPromptPolicy } from '@/api/admin/settings'

vi.mock('@/api/admin/settings', () => ({
  getModelSystemPromptPolicy: vi.fn(), updateModelSystemPromptPolicy: vi.fn()
}))

const policy = { entries: [{ model: 'deepseek-v4-pro', prompt: '中文回答' }] }
async function render() {
  const wrapper = mount(ModelSystemPromptSettings, { global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: {}, missingWarn: false, fallbackWarn: false })] } })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getModelSystemPromptPolicy).mockResolvedValue(structuredClone(policy))
  vi.mocked(updateModelSystemPromptPolicy).mockImplementation(async value => value)
})

describe('ModelSystemPromptSettings', () => {
  it('loads, edits and saves model prompts', async () => {
    const wrapper = await render()
    expect(wrapper.findAll('textarea')[0].element.value).toBe('中文回答')
    await wrapper.findAll('input')[0].setValue('deepseek-v4-pro')
    await wrapper.findAll('textarea')[0].setValue('始终使用简体中文')
    await wrapper.find('.btn-primary').trigger('click')
    await flushPromises()
    expect(updateModelSystemPromptPolicy).toHaveBeenCalledWith({ entries: [{ model: 'deepseek-v4-pro', prompt: '始终使用简体中文' }] })
    expect(wrapper.text()).toContain('admin.settings.modelSystemPrompts.saved')
  })

  it('blocks duplicate model IDs', async () => {
    const wrapper = await render()
    await wrapper.findAll('button').find(b => b.text() === 'admin.settings.modelSystemPrompts.add')!.trigger('click')
    await wrapper.findAll('input')[1].setValue('deepseek-v4-pro')
    await wrapper.findAll('textarea')[1].setValue('other')
    expect(wrapper.find('.btn-primary').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('admin.settings.modelSystemPrompts.duplicate')
    expect(updateModelSystemPromptPolicy).not.toHaveBeenCalled()
  })
})
