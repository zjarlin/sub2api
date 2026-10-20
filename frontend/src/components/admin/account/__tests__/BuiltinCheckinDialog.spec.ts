import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import BuiltinCheckinDialog from '../BuiltinCheckinDialog.vue'
import type { Account } from '@/types'

const { getBuiltinAdapterCheckins, bindBuiltinAdapterAccount, runBuiltinAdapterCheckin, runBuiltinAdapterPlatformCheckin } = vi.hoisted(() => ({
  getBuiltinAdapterCheckins: vi.fn(),
  bindBuiltinAdapterAccount: vi.fn(),
  runBuiltinAdapterCheckin: vi.fn(),
  runBuiltinAdapterPlatformCheckin: vi.fn()
}))
vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getBuiltinAdapterCheckins,
      bindBuiltinAdapterAccount,
      runBuiltinAdapterCheckin,
      runBuiltinAdapterPlatformCheckin
    }
  }
}))
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

  it('triggers a manual check-in and renders the refreshed balance', async () => {
    getBuiltinAdapterCheckins.mockResolvedValue({
      ...unbound, binding_required: false, total_credits: 1450, accounts: [accounts[0]]
    })
    runBuiltinAdapterCheckin.mockResolvedValue({
      ...unbound,
      binding_required: false,
      total_credits: 1550,
      accounts: [{
        ...accounts[0],
        credits: 1550,
        checkin: { at: '2026-10-18T09:00:00Z', status: 'ok', credits: 1550, delta: 100 },
        checkins: [{ at: '2026-10-18T09:00:00Z', status: 'ok', credits: 1550, delta: 100 }]
      }]
    })
    const wrapper = mount(BuiltinCheckinDialog, {
      props: { show: true, account, scope: 'user' },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } }
    })
    await flushPromises()
    const button = wrapper.findAll('button').find(b => b.text() === 'admin.accounts.builtinCheckin.checkinNow')!
    await button.trigger('click')
    await flushPromises()
    expect(runBuiltinAdapterCheckin).toHaveBeenCalledWith(893, 'user')
    expect(wrapper.text()).toContain('admin.accounts.builtinCheckin.checkinSuccess')
    expect(wrapper.text()).toContain('1,550')
    wrapper.unmount()
  })

  it('does not offer check-in for accounts that still need linking', async () => {
    getBuiltinAdapterCheckins.mockResolvedValue(unbound)
    const wrapper = mount(BuiltinCheckinDialog, {
      props: { show: true, account, scope: 'user' },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } }
    })
    await flushPromises()
    expect(wrapper.findAll('button').some(b => b.text() === 'admin.accounts.builtinCheckin.checkinNow')).toBe(false)
    wrapper.unmount()
  })

  it('offers pool-wide check-in to admins only and reports the batch result', async () => {
    const pool = [
      { ...accounts[0], checkin: { at: '2026-10-18T09:00:00Z', status: 'ok', credits: 1550, delta: 100 }, checkins: [] },
      { ...accounts[1], checkin: { at: '2026-10-18T09:00:00Z', status: 'already', credits: 23050, delta: 0 }, checkins: [] }
    ]
    getBuiltinAdapterCheckins.mockResolvedValue({ ...unbound, binding_required: false, total_credits: 24600, accounts: pool })
    runBuiltinAdapterPlatformCheckin.mockResolvedValue({ ...unbound, binding_required: false, total_credits: 24600, accounts: pool })

    const admin = mount(BuiltinCheckinDialog, {
      props: { show: true, account },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } }
    })
    await flushPromises()
    const all = admin.findAll('button').find(b => b.text() === 'admin.accounts.builtinCheckin.checkinAll')!
    expect(all).toBeTruthy()
    await all.trigger('click')
    await flushPromises()
    expect(runBuiltinAdapterPlatformCheckin).toHaveBeenCalledWith('workbuddy')
    expect(admin.text()).toContain('admin.accounts.builtinCheckin.checkinAllDone')
    admin.unmount()

    // 用户侧不能整池签到：否则任何登录用户都能打光共享账号池的上游配额。
    const user = mount(BuiltinCheckinDialog, {
      props: { show: true, account, scope: 'user' },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } }
    })
    await flushPromises()
    expect(user.findAll('button').some(b => b.text() === 'admin.accounts.builtinCheckin.checkinAll')).toBe(false)
    user.unmount()
  })

  it('shows the claimed amount of today\'s successful check-in and blocks re-check-in', async () => {
    const now = new Date()
    const today = new Date(now.getTime() + 8 * 60 * 60 * 1000).toISOString().slice(0, 10)
    getBuiltinAdapterCheckins.mockResolvedValue({
      ...unbound, binding_required: false, total_credits: 1550,
      accounts: [{
        ...accounts[0],
        credits: 1550,
        // 当天先签到成功（+100），随后手动又点了一次得到 already(delta 0)。
        checkins: [
          { at: `${today}T09:00:00+08:00`, status: 'ok', credits: 1550, delta: 100 },
          { at: `${today}T09:30:00+08:00`, status: 'already', credits: 1550, delta: 0 }
        ],
        checkin: { at: `${today}T09:30:00+08:00`, status: 'already', credits: 1550, delta: 0 }
      }]
    })
    const wrapper = mount(BuiltinCheckinDialog, {
      props: { show: true, account, scope: 'user' },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' } } }
    })
    await flushPromises()
    // 「本次领取」要显示当天真正领到的数值，而不是最近一条 already 的 0。
    expect(wrapper.text()).toContain('+100')
    // 当天已签到：不再给出可点的「签到」，改为「今日已签」。
    const buttons = wrapper.findAll('button')
    expect(buttons.some(b => b.text() === 'admin.accounts.builtinCheckin.checkinDone')).toBe(true)
    const done = buttons.find(b => b.text() === 'admin.accounts.builtinCheckin.checkinDone')!
    expect(done.attributes('disabled')).toBeDefined()
    await done.trigger('click')
    await flushPromises()
    expect(runBuiltinAdapterCheckin).not.toHaveBeenCalled()
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
