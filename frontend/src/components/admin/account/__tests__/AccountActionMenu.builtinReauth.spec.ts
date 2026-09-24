import { afterEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, mount } from '@vue/test-utils'
import AccountActionMenu from '../AccountActionMenu.vue'
import type { Account } from '@/types'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

enableAutoUnmount(afterEach)

function mountMenu(account: Partial<Account>) {
  return mount(AccountActionMenu, {
    props: {
      show: true,
      account: account as Account,
      anchorRect: new DOMRect(20, 20, 24, 24)
    },
    global: { stubs: { Icon: true } }
  })
}

describe('AccountActionMenu built-in adapter reauthorization', () => {
  it.each(['workbuddy', 'traework', 'zcode'])('shows reauthorize but not refresh-token for %s API key accounts', (platform) => {
    const wrapper = mountMenu({ id: 1, platform, type: 'apikey', status: 'active' })
    const text = document.body.textContent || ''
    expect(text).toContain('admin.accounts.reAuthorize')
    expect(text).not.toContain('admin.accounts.refreshToken')
    wrapper.unmount()
  })

  it('keeps reauthorize hidden for an ordinary API key account', () => {
    const wrapper = mountMenu({ id: 2, platform: 'openai', type: 'apikey', status: 'active' })
    expect(document.body.textContent || '').not.toContain('admin.accounts.reAuthorize')
    wrapper.unmount()
  })

  it('emits reauth for a built-in adapter account', async () => {
    const account = { id: 3, platform: 'workbuddy', type: 'apikey', status: 'active' } as Account
    const wrapper = mountMenu(account)
    const button = Array.from(document.body.querySelectorAll('button'))
      .find(item => item.textContent?.includes('admin.accounts.reAuthorize'))!

    button.click()
    expect(wrapper.emitted('reauth')).toEqual([[account]])
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })
})
