import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ReAuthAccountModal from '../ReAuthAccountModal.vue'
import type { Account } from '@/types'

const { clearError, bindBuiltinAdapterAccount } = vi.hoisted(() => ({ clearError: vi.fn(), bindBuiltinAdapterAccount: vi.fn() }))
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
      clearError,
      bindBuiltinAdapterAccount
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
    bindBuiltinAdapterAccount.mockReset()
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
            template: '<button data-test="authorize" @click="$emit(\'authorized\', { status: \'completed\', account: { uid: \'saved-login\' } })">authorize</button>'
          }
        }
      }
    })

    expect(wrapper.findComponent({ name: 'OAuthAuthorizationFlow' }).exists()).toBe(false)
    await wrapper.get('[data-test="authorize"]').trigger('click')
    await flushPromises()

    expect(clearError).toHaveBeenCalledWith(42)
    if (platform === 'workbuddy' || platform === 'traework') {
      expect(bindBuiltinAdapterAccount).toHaveBeenCalledWith(42, 'saved-login')
      expect(bindBuiltinAdapterAccount.mock.invocationCallOrder[0]).toBeLessThan(clearError.mock.invocationCallOrder[0])
    } else {
      expect(bindBuiltinAdapterAccount).not.toHaveBeenCalled()
    }
    expect(showSuccess).toHaveBeenCalledWith('admin.accounts.reAuthorizedSuccess')
    expect(wrapper.emitted('reauthorized')).toEqual([[expect.objectContaining({ id: 42, status: 'active' })]])
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  it.each(['missing identity', 'binding failed'])('does not report success when %s', async (failure) => {
    bindBuiltinAdapterAccount.mockRejectedValue(new Error('binding failed'))
    const wrapper = mount(ReAuthAccountModal, {
      props: { show: true, account: account('workbuddy') },
      global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, BuiltinAdapterLogin: true } }
    })
    wrapper.findComponent({ name: 'BuiltinAdapterLogin' }).vm.$emit('authorized', {
      status: 'completed', account: failure === 'missing identity' ? {} : { uid: 'saved-login' }
    })
    await flushPromises()
    expect(clearError).not.toHaveBeenCalled()
    expect(showSuccess).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalled()
    expect(wrapper.emitted('close')).toBeUndefined()
    wrapper.unmount()
  })
})
