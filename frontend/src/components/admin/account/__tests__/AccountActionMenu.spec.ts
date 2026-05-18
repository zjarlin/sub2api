import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import AccountActionMenu from '../AccountActionMenu.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

describe('AccountActionMenu', () => {
  it('emits copy and close when copy account is clicked', async () => {
    const wrapper = mount(AccountActionMenu, {
      props: {
        show: true,
        position: { top: 20, left: 30 },
        account: {
          id: 1,
          name: 'demo',
          platform: 'openai',
          type: 'apikey',
          schedulable: true,
          status: 'active',
          rate_limit_reset_at: null,
          overload_until: null,
          temp_unschedulable_until: null,
          quota_limit: null,
          quota_daily_limit: null,
          quota_weekly_limit: null
        } as any
      },
      global: {
        stubs: {
          Icon: true,
          Teleport: true
        }
      }
    })

    const copyButton = wrapper.findAll('button').find(button => button.text().includes('admin.accounts.copyAccount'))
    expect(copyButton).toBeTruthy()

    await copyButton!.trigger('click')

    expect(wrapper.emitted('copy')).toHaveLength(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
})
