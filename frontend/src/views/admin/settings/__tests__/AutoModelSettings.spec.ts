import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import AutoModelSettings from '../AutoModelSettings.vue'
import { getAutoModelPolicy, updateAutoModelPolicy } from '@/api/admin/settings'

vi.mock('@/api/admin/settings', () => ({
  getAutoModelPolicy: vi.fn(), updateAutoModelPolicy: vi.fn(),
}))

async function render() {
  const wrapper = mount(AutoModelSettings, {
    global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: {}, missingWarn: false, fallbackWarn: false })] },
  })
  await flushPromises()
  return wrapper
}

beforeEach(() => {
  vi.resetAllMocks()
  vi.mocked(getAutoModelPolicy).mockResolvedValue({ blacklist: ['doubao*'] })
  vi.mocked(updateAutoModelPolicy).mockImplementation(async value => value)
})

describe('AutoModelSettings', () => {
  it('保存所有模型适用的垂直路由开关和媒体模型', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="vertical-enabled"]').setValue(false)
    const fields = wrapper.findAll('input:not([type="checkbox"])')
    await fields[2]!.setValue('gpt-image-2')
    await fields[3]!.setValue('grok-imagine-video')
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    expect(updateAutoModelPolicy).toHaveBeenLastCalledWith(expect.objectContaining({
      vertical_routing: { enabled: false, min_confidence: 0.8, timeout_ms: 1500, image_model: 'gpt-image-2', video_model: 'grok-imagine-video' },
    }))
  })
  it('展示默认黑名单并保存修改，允许显式清空', async () => {
    const wrapper = await render()
    expect(wrapper.get('textarea').element.value).toBe('doubao*')
    await wrapper.get('textarea').setValue('doubao*\ngpt-5.5')
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    expect(updateAutoModelPolicy).toHaveBeenLastCalledWith(expect.objectContaining({ blacklist: ['doubao*', 'gpt-5.5'] }))
    expect(wrapper.text()).toContain('admin.settings.autoModel.saved')
    await wrapper.get('textarea').setValue('')
    expect(wrapper.text()).not.toContain('admin.settings.autoModel.saved')
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    expect(updateAutoModelPolicy).toHaveBeenLastCalledWith(expect.objectContaining({ blacklist: [] }))
  })

  it.each(['doubao*\nDOUBAO*', '*doubao*', 'model?', 'a'.repeat(201)])('拒绝无效规则 %s', async value => {
    const wrapper = await render()
    await wrapper.get('textarea').setValue(value)
    expect(wrapper.get('.btn-primary').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).toContain('admin.settings.autoModel.invalidRules')
    expect(updateAutoModelPolicy).not.toHaveBeenCalled()
  })

  it('读取失败时显示错误并支持重试', async () => {
    vi.mocked(getAutoModelPolicy).mockRejectedValueOnce(new Error('offline'))
    const wrapper = await render()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.find('textarea').exists()).toBe(false)
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.get('textarea').element.value).toBe('doubao*')
  })

  it('保存失败时保留编辑内容并显示错误', async () => {
    vi.mocked(updateAutoModelPolicy).mockRejectedValueOnce(new Error('offline'))
    const wrapper = await render()
    await wrapper.get('textarea').setValue('doubao*\nprivate*')
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.get('textarea').element.value).toBe('doubao*\nprivate*')
    expect(wrapper.text()).not.toContain('admin.settings.autoModel.saved')
  })
})
