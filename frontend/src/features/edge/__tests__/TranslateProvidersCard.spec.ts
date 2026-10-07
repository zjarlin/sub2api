import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import TranslateProvidersCard from '../TranslateProvidersCard.vue'
import { EDGE_ADAPTERS } from '../catalog'

enableAutoUnmount(afterEach)
const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, put } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

function settings() {
  return { enabled: true, priority: [...EDGE_ADAPTERS], ...Object.fromEntries(EDGE_ADAPTERS.map(key => [key, { enabled: false }])), baidu: { enabled: true, app_id: 'app-test', secret_set: true }, hymt: { enabled: true, base_url: 'http://private:8000', api_key_set: true } }
}

describe('translation adapter contexts', () => {
  beforeEach(() => { vi.clearAllMocks(); get.mockResolvedValue({ data: settings() }); put.mockResolvedValue({ data: settings() }) })
  it('loads masked Baidu credentials and keeps blank secrets when saving', async () => {
    const wrapper = mount(TranslateProvidersCard, { props: { adapter: 'baidu' } })
    await flushPromises()
    expect(wrapper.findAll('[data-testid^="translate-adapter-"]')).toHaveLength(8)
    expect((wrapper.get('[data-testid="translate-baidu-app_id"]').element as HTMLInputElement).value).toBe('app-test')
    expect((wrapper.get('[data-testid="translate-baidu-secret"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.get('[data-testid="translate-baidu-secret"]').attributes('type')).toBe('password')
    await wrapper.get('[data-testid="translate-save"]').trigger('click')
    await flushPromises()
    expect(put.mock.calls[0][1].baidu.secret).toBe('')
    expect(wrapper.emitted('saved')).toHaveLength(1)
  })
  it('changes the context without losing the current credential draft', async () => {
    const wrapper = mount(TranslateProvidersCard, { props: { adapter: 'baidu' } })
    await flushPromises()
    await wrapper.get('[data-testid="translate-baidu-secret"]').setValue('new-test-secret')
    await wrapper.get('[data-testid="translate-adapter-hymt"]').trigger('click')
    expect(wrapper.emitted('select')?.[0]).toEqual(['hymt'])
    await wrapper.setProps({ adapter: 'hymt' })
    expect(wrapper.find('[data-testid="translate-baidu-secret"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="translate-hymt-base_url"]').exists()).toBe(true)
    await wrapper.get('[data-testid="translate-default-provider"]').setValue('hymt')
    await wrapper.get('[data-testid="translate-save"]').trigger('click')
    expect(put.mock.calls[0][1].priority[0]).toBe('hymt')
    expect(put.mock.calls[0][1].baidu.secret).toBe('new-test-secret')
    await flushPromises()
    await wrapper.setProps({ adapter: 'baidu' })
    expect((wrapper.get('[data-testid="translate-baidu-secret"]').element as HTMLInputElement).value).toBe('')
  })
  it('keeps save disabled after a failed load', async () => {
    get.mockRejectedValue(new Error('failed'))
    const wrapper = mount(TranslateProvidersCard, { props: { adapter: 'baidu' } })
    await flushPromises()
    expect(wrapper.get('[data-testid="translate-save"]').attributes('disabled')).toBeDefined()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(put).not.toHaveBeenCalled()
  })
})
