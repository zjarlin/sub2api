import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ReAuthAccountModal from '../ReAuthAccountModal.vue'
import type { Account } from '@/types'

const { clearError } = vi.hoisted(() => ({ clearError: vi.fn() }))
const { showSuccess, showError } = vi.hoisted(() => ({ showSuccess: vi.fn(), showError: vi.fn() }))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      clearError
    }
  }
}))

function account(platform: string): Account {
  return {
    id: 42,
    name: 'test account',
    platform,
    type: 'apikey',
    status: 'error',
    credentials: {}
  } as Account
}

describe('ReAuthAccountModal built-in adapter reauthorization', () => {
  beforeEach(() => {
    clearError.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
  })

  it.each(['workbuddy', 'traework', 'zcode'])('uses the built-in login flow for %s', async (platform) => {
    clearError.mockResolvedValue({ ...account(platform), status: 'active' })
    const wrapper = mount(ReAuthAccountModal, {
      props: { show: true, account: account(platform) },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /></div>' },
          BuiltinAdapterLogin: {
            emits: ['authorized'],
            template: '<button data-test="authorize" @click="$emit(\'authorized\')">authorize</button>'
          }
        }
      }
    })

    expect(wrapper.findComponent({ name: 'OAuthAuthorizationFlow' }).exists()).toBe(false)
    await wrapper.get('[data-test="authorize"]').trigger('click')
    await flushPromises()

    expect(clearError).toHaveBeenCalledWith(42)
    expect(showSuccess).toHaveBeenCalledWith('admin.accounts.reAuthorizedSuccess')
    expect(wrapper.emitted('reauthorized')).toEqual([[expect.objectContaining({ id: 42, status: 'active' })]])
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })
})
