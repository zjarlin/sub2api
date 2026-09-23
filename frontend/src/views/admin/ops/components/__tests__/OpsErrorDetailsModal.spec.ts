import { flushPromises, shallowMount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OpsErrorDetailsModal from '../OpsErrorDetailsModal.vue'

const mocks = vi.hoisted(() => ({
  listUpstreamErrors: vi.fn(),
  listRequestErrors: vi.fn(),
}))

vi.mock('@/api/admin/ops', () => ({ opsAPI: mocks }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

describe('OpsErrorDetailsModal outcome scope', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.listUpstreamErrors.mockResolvedValue({ items: [], total: 0 })
    mocks.listRequestErrors.mockResolvedValue({ items: [], total: 0 })
  })

  it('loads recovered requests without imposing an upstream phase or error-only filter', async () => {
    const wrapper = shallowMount(OpsErrorDetailsModal, {
      props: { show: false, timeRange: '1h', errorType: 'upstream', recovered: true },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } },
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(mocks.listUpstreamErrors).toHaveBeenCalledWith(expect.objectContaining({ view: 'recovered' }))
    expect(mocks.listUpstreamErrors.mock.calls[0][0]).not.toHaveProperty('phase')
    expect(mocks.listRequestErrors).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.ops.recoveredSuccessHint')
    expect(wrapper.findComponent({ name: 'OpsErrorLogTable' }).props('recovered')).toBe(true)

    await wrapper.setProps({ show: false, recovered: false, errorType: 'request' })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(mocks.listRequestErrors).toHaveBeenCalledWith(expect.objectContaining({ view: 'errors' }))
    wrapper.unmount()
  })
})
