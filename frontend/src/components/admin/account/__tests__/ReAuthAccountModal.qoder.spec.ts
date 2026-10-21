import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ReAuthAccountModal from '../ReAuthAccountModal.vue'
import type { Account } from '@/types'

const {
  generateAuthURL,
  pollToken,
  applyOAuthCredentials
} = vi.hoisted(() => ({
  generateAuthURL: vi.fn(),
  pollToken: vi.fn(),
  applyOAuthCredentials: vi.fn()
}))
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
    accounts: { applyOAuthCredentials },
    qoder: { generateAuthURL, pollToken }
  }
}))

function qoderAccount(): Account {
  return {
    id: 855,
    name: 'zjarlin_qoder',
    platform: 'qoder',
    type: 'oauth',
    status: 'active',
    credentials: { access_token: 'old-token', model_mapping: { 'qwen3.8-max': 'qmodel_38max' } }
  } as Account
}

function mountModal() {
  return mount(ReAuthAccountModal, {
    props: { show: true, account: qoderAccount() },
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        OAuthAuthorizationFlow: true
      }
    }
  })
}

describe('ReAuthAccountModal Qoder device-flow reauthorization', () => {
  beforeEach(() => {
    generateAuthURL.mockReset()
    pollToken.mockReset()
    applyOAuthCredentials.mockReset()
    showSuccess.mockReset()
    showError.mockReset()
    vi.useFakeTimers()
  })

  it('shows the Qoder device flow instead of the Claude OAuth flow', () => {
    const wrapper = mountModal()
    expect(wrapper.findComponent({ name: 'OAuthAuthorizationFlow' }).exists()).toBe(false)
    expect(wrapper.get('[data-testid="qoder-reauth-action"]').text()).toBe(
      'admin.accounts.qoder.openAuthPage'
    )
    wrapper.unmount()
    vi.useRealTimers()
  })

  it('persists the device access token on the existing account', async () => {
    generateAuthURL.mockResolvedValue({ auth_url: 'https://qoder.com/device/selectAccounts?x=1', session_id: 'sess-1' })
    pollToken.mockResolvedValue({
      done: true,
      token: { access_token: 'new-device-token', refresh_token: 'rt-1', expires_at: 1793000000 }
    })
    applyOAuthCredentials.mockResolvedValue({ ...qoderAccount(), status: 'active', credentials: {} })
    const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)

    const wrapper = mountModal()
    await wrapper.get('[data-testid="qoder-reauth-action"]').trigger('click')
    await flushPromises()

    expect(generateAuthURL).toHaveBeenCalledTimes(1)
    expect(openSpy).toHaveBeenCalledWith('https://qoder.com/device/selectAccounts?x=1', '_blank', 'noopener,noreferrer')

    await vi.advanceTimersByTimeAsync(1600)
    await flushPromises()

    expect(pollToken).toHaveBeenCalledWith('sess-1')
    expect(applyOAuthCredentials).toHaveBeenCalledTimes(1)
    const [id, payload] = applyOAuthCredentials.mock.calls[0]
    expect(id).toBe(855)
    expect(payload.type).toBe('oauth')
    expect(payload.credentials).toMatchObject({
      access_token: 'new-device-token',
      refresh_token: 'rt-1',
      api_protocol: 'chat_completions'
    })
    expect(showSuccess).toHaveBeenCalledWith('admin.accounts.reAuthorizedSuccess')
    expect(wrapper.emitted('reauthorized')).toEqual([[expect.objectContaining({ id: 855 })]])
    expect(wrapper.emitted('close')).toHaveLength(1)

    openSpy.mockRestore()
    wrapper.unmount()
    vi.useRealTimers()
  })

  it('reports an expired authorization session without persisting credentials', async () => {
    generateAuthURL.mockResolvedValue({ auth_url: 'https://qoder.com/device/selectAccounts?x=2', session_id: 'sess-2' })
    pollToken.mockRejectedValue({ response: { data: { code: 'QODER_OAUTH_SESSION_EXPIRED', detail: 'expired' } } })
    const openSpy = vi.spyOn(window, 'open').mockImplementation(() => null)

    const wrapper = mountModal()
    await wrapper.get('[data-testid="qoder-reauth-action"]').trigger('click')
    await flushPromises()
    await vi.advanceTimersByTimeAsync(1600)
    await flushPromises()

    expect(applyOAuthCredentials).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith('admin.accounts.qoder.authorizationExpired')
    expect(wrapper.emitted('reauthorized')).toBeUndefined()

    openSpy.mockRestore()
    wrapper.unmount()
    vi.useRealTimers()
  })
})
