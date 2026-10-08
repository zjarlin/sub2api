import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import EdgeServiceContext from '../EdgeServiceContext.vue'
import { EDGE_SERVICES } from '../catalog'
import type { EdgeServiceContext as Context } from '../contexts'

const { put } = vi.hoisted(() => ({ put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { put } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/components/icons/Icon.vue', () => ({ default: { template: '<span />' } }))

const context: Context = {
  adapter: 'JEV', source: 'account', enabled: true, configured: true, fields: [],
  accounts: [{ id: 7, name: 'JEV account', base_url: 'https://api.example/v1', api_key_set: true, api_key_editable: true, status: 'active', schedulable: true, models: ['typesafe/jev'], group_ids: [1] }],
}
function mountContext(value = context, key = 'jev') {
  return mount(EdgeServiceContext, { props: { service: EDGE_SERVICES.find(s => s.key === key)!, context: value, loading: false, error: '' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
}

describe('EdgeServiceContext', () => {
  beforeEach(() => { vi.clearAllMocks(); put.mockResolvedValue({ data: context.accounts[0] }) })

  it('shows configured credentials without recovering plaintext, and saves only the selected account', async () => {
    const wrapper = mountContext()
    expect((wrapper.get('[data-testid="edge-account-key-7"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.get('[data-testid="edge-account-url-7"]').attributes()).toHaveProperty('readonly')
    expect(wrapper.get('[data-testid="edge-account-save-7"]').attributes()).toHaveProperty('disabled')
    await wrapper.get('[data-testid="edge-account-key-7"]').setValue('replacement')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(put).toHaveBeenCalledWith('/admin/vision/contexts/jev/accounts/7', { base_url: 'https://api.example/v1', api_key: 'replacement' })
    expect((wrapper.get('[data-testid="edge-account-key-7"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.emitted('saved')).toHaveLength(1)
  })

  it('retains a new key after a failed save and reports the failure', async () => {
    put.mockRejectedValue(new Error('unavailable'))
    const wrapper = mountContext()
    await wrapper.get('[data-testid="edge-account-key-7"]').setValue('replacement')
    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').exists()).toBe(true)
    expect((wrapper.get('[data-testid="edge-account-key-7"]').element as HTMLInputElement).value).toBe('replacement')
    expect(wrapper.emitted('saved')).toBeUndefined()
  })

  it('shows actual deployment fields and an explicit missing Ark account state', () => {
    const deployment = mountContext({ adapter: 'GPT-SoVITS', source: 'deployment', enabled: true, configured: true, fields: [{ key: 'base_url', value: 'http://inference:9880' }], accounts: [] }, 'tts')
    expect(deployment.get('[data-testid="edge-context-base_url"]').text()).toBe('http://inference:9880')
    expect(deployment.find('input').exists()).toBe(false)
    const missing = mountContext({ ...context, accounts: [], enabled: false, configured: false }, 'generation')
    expect(missing.text()).toContain('admin.vision.runtime.arkRequired')
    expect(missing.text()).not.toContain('admin.vision.runtime.ready')
  })
})
