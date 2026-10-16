import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import BuiltinCheckinDialog from '../BuiltinCheckinDialog.vue'
import type { Account } from '@/types'

const { getBuiltinAdapterCheckins, bindBuiltinAdapterAccount } = vi.hoisted(() => ({
  getBuiltinAdapterCheckins: vi.fn(), bindBuiltinAdapterAccount: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getBuiltinAdapterCheckins, bindBuiltinAdapterAccount } } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const account = { id: 893, platform: 'workbuddy', type: 'apikey' } as Account
const accounts = [
  { uid: 'mine', nickname: 'My saved login', credits: 79.75, checkins: [] },
  { uid: 'other', nickname: 'Other login', credits: 23050, checkins: [] }
]
const unbound = { platform: 'workbuddy', fetched_at: 1, total_credits: 0, binding_required: true, accounts }

describe('BuiltinCheckinDialog existing login recovery', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    getBuiltinAdapterCheckins.mockResolvedValue(unbound)
  })

  it('links a saved login and then displays only that account without another login', async () => {
    const updated = { ...account, extra: { builtin_adapter_uid: 'mine' } }
    bindBuiltinAdapterAccount.mockResolvedValue(updated)
    const wrapper = mount(BuiltinCheckinDialog, {
      props: { show: true, account },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } }
    })
    await flushPromises()
    expect(wrapper.text()).toContain('admin.accounts.builtinCheckin.bindExistingHint')
    expect(wrapper.text()).toContain('My saved login')
    getBuiltinAdapterCheckins.mockResolvedValue({ ...unbound, binding_required: false, total_credits: 79.75, accounts: [accounts[0]] })
    const button = wrapper.findAll('button').find(b => b.text() === 'admin.accounts.builtinCheckin.bindAccount')!
    await button.trigger('click')
    await flushPromises()
    expect(bindBuiltinAdapterAccount).toHaveBeenCalledWith(893, 'mine')
    expect(wrapper.emitted('updated')).toEqual([[updated]])
    expect(wrapper.text()).toContain('79.75')
    expect(wrapper.text()).not.toContain('Other login')
    expect(wrapper.text()).not.toContain('admin.accounts.builtinCheckin.bindExistingHint')
    wrapper.unmount()
  })

  it('keeps failed saves visible and does not emit an account update', async () => {
    bindBuiltinAdapterAccount.mockRejectedValue(new Error('save failed'))
    const wrapper = mount(BuiltinCheckinDialog, {
      props: { show: true, account },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } }
    })
    await flushPromises()
    const button = wrapper.findAll('button').find(b => b.text() === 'admin.accounts.builtinCheckin.bindAccount')!
    await button.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('save failed')
    expect(wrapper.emitted('updated')).toBeUndefined()
    wrapper.unmount()
  })
})

describe('BuiltinCheckinDialog user scope', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    getBuiltinAdapterCheckins.mockResolvedValue({ ...unbound, binding_required: false, total_credits: 1450, accounts: [accounts[0]] })
  })

  it('reads the user-side sidecar endpoint and never offers pool binding', async () => {
    const wrapper = mount(BuiltinCheckinDialog, {
      props: { show: true, account, scope: 'user' },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } }
    })
    await flushPromises()
    expect(getBuiltinAdapterCheckins).toHaveBeenCalledWith(893, 'user')
    expect(wrapper.text()).toContain('1,450')
    wrapper.unmount()
  })

  it('keeps binding admin-only even if the payload asks for it', async () => {
    getBuiltinAdapterCheckins.mockResolvedValue(unbound)
    const wrapper = mount(BuiltinCheckinDialog, {
      props: { show: true, account, scope: 'user' },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } }
    })
    await flushPromises()
    expect(getBuiltinAdapterCheckins).toHaveBeenCalledWith(893, 'user')
    expect(wrapper.text()).not.toContain('admin.accounts.builtinCheckin.bindAccount')
    const button = wrapper.findAll('button').find(b => b.text() === 'admin.accounts.builtinCheckin.title')!
    await button.trigger('click')
    await flushPromises()
    expect(bindBuiltinAdapterAccount).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
