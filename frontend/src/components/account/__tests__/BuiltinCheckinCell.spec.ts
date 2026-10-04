import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import BuiltinCheckinCell from '../BuiltinCheckinCell.vue'
import type { Account } from '@/types'

const { getBuiltinAdapterCheckins } = vi.hoisted(() => ({
  getBuiltinAdapterCheckins: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: { getBuiltinAdapterCheckins }
  }
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

const overview = {
  platform: 'workbuddy',
  fetched_at: 1,
  total_credits: 2250,
  accounts: [
    { uid: 'u1', nickname: 'n1', credits: 1450, checkins: [] },
    { uid: 'u2', nickname: 'n2', credits: 800, checkins: [] }
  ]
}

const account = { id: 7, platform: 'workbuddy', type: 'apikey' } as Account

describe('BuiltinCheckinCell', () => {
  beforeEach(() => {
    getBuiltinAdapterCheckins.mockReset()
    getBuiltinAdapterCheckins.mockResolvedValue(overview)
  })

  it('loads the sidecar summary on mount for workbuddy/traework', async () => {
    const wrapper = mount(BuiltinCheckinCell, { props: { account } })
    await flushPromises()
    expect(getBuiltinAdapterCheckins).toHaveBeenCalledWith(account.id)
    expect(wrapper.text()).toContain('2,250')
  })

  it('does not fetch for unrelated platforms', async () => {
    const wrapper = mount(BuiltinCheckinCell, {
      props: { account: { id: 9, platform: 'openai' } as Account }
    })
    await flushPromises()
    expect(getBuiltinAdapterCheckins).not.toHaveBeenCalled()
    expect(wrapper.text()).toBe('')
  })

  it('opens the detail dialog via the button', async () => {
    const wrapper = mount(BuiltinCheckinCell, { props: { account } })
    await flushPromises()
    await wrapper.get('button').trigger('click')
    expect(wrapper.emitted('open')).toBeTruthy()
  })
})
