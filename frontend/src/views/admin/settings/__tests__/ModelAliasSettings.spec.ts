import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import ModelAliasSettings from '../ModelAliasSettings.vue'
import { getModelAliasPolicy, updateModelAliasPolicy } from '@/api/admin/settings'

vi.mock('@/api/admin/settings', () => ({ getModelAliasPolicy: vi.fn(), updateModelAliasPolicy: vi.fn() }))
const policy = { groups: [
  { canonical: 'ds', aliases: [] },
  { canonical: 'deepseek-flash', parent: 'ds', aliases: ['deepseek/deepseek-v4.1-flash-1017'], routes: [
    { model: 'deepseek/deepseek-v4.1-flash-1017', priority: 0, base_urls: ['https://api.cline.bot/api'] }
  ] }
] }
async function render() {
  const wrapper = mount(ModelAliasSettings, { global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: {}, missingWarn: false, fallbackWarn: false })] } })
  await flushPromises()
  return wrapper
}
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getModelAliasPolicy).mockResolvedValue(structuredClone(policy))
  vi.mocked(updateModelAliasPolicy).mockImplementation(async value => value)
})
describe('ModelAliasSettings tree', () => {
  it('saves empty roots, parents, priorities and base URL constraints', async () => {
    const wrapper = await render()
    expect(wrapper.find('.btn-primary').attributes('disabled')).toBeUndefined()
    await wrapper.find('input[type="number"]').setValue(3)
    await wrapper.find('.btn-primary').trigger('click')
    await flushPromises()
    const saved = vi.mocked(updateModelAliasPolicy).mock.calls[0][0]
    expect(saved.groups[0]).toMatchObject({ canonical: 'ds', aliases: [] })
    expect(saved.groups[1]).toMatchObject({ parent: 'ds', routes: [{ ...policy.groups[1].routes![0], priority: 3 }] })
  })
  it('prevents cycles and routes outside their alias group', async () => {
    const wrapper = await render()
    await wrapper.findAll('select')[0].setValue('deepseek-flash')
    expect(wrapper.find('.btn-primary').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('admin.settings.modelAliases.invalidTree')
    await wrapper.findAll('select')[0].setValue('')
    const routeInput = wrapper.findAll('input').find(input => input.element.value === 'deepseek/deepseek-v4.1-flash-1017')!
    await routeInput.setValue('outside-group')
    expect(wrapper.text()).toContain('admin.settings.modelAliases.invalidRoute')
    expect(updateModelAliasPolicy).not.toHaveBeenCalled()
  })
})
