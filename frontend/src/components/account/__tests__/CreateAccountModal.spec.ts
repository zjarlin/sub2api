import { describe, expect, it, vi, beforeEach } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const { createAccountMock, checkMixedChannelRiskMock } = vi.hoisted(() => ({
  createAccountMock: vi.fn(),
  checkMixedChannelRiskMock: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    isSimpleMode: true
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      create: createAccountMock,
      checkMixedChannelRisk: checkMixedChannelRiskMock
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({})
    },
    tlsFingerprintProfiles: {
      list: vi.fn().mockResolvedValue([])
    }
  }
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn()
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

import CreateAccountModal from '../CreateAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: {
    show: {
      type: Boolean,
      default: false
    }
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const PassthroughModelStub = defineComponent({
  name: 'PassthroughModelStub',
  props: {
    modelValue: {
      default: null
    }
  },
  emits: ['update:modelValue'],
  template: '<div><slot /></div>'
})

const IconStub = defineComponent({
  name: 'Icon',
  template: '<span />'
})

describe('CreateAccountModal', () => {
  beforeEach(() => {
    createAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    createAccountMock.mockResolvedValue({})
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
  })

  it('creates OpenCode Go as an Anthropic Messages API key account', async () => {
    const wrapper = mount(CreateAccountModal, {
      props: {
        show: true,
        proxies: [],
        groups: []
      },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          ConfirmDialog: true,
          Icon: IconStub,
          ProxySelector: PassthroughModelStub,
          ProxyAdBanner: true,
          GroupSelector: PassthroughModelStub,
          DelimitedTagInput: PassthroughModelStub,
          ModelWhitelistSelector: PassthroughModelStub,
          ModelMappingBulkImporter: PassthroughModelStub,
          QuotaLimitCard: true,
          OAuthAuthorizationFlow: true,
          Select: PassthroughModelStub
        }
      }
    })

    await wrapper.find('[data-tour="account-form-name"]').setValue('opencode-go-minimax')
    const openCodeButton = wrapper
      .findAll('button')
      .find(button => button.text().includes('OpenCode Go'))
    expect(openCodeButton).toBeTruthy()
    await openCodeButton!.trigger('click')

    await wrapper.find('textarea[placeholder="sk-..."]').setValue('sk-test-opencode')
    await wrapper.find('form#create-account-form').trigger('submit')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock).toHaveBeenCalledWith(expect.objectContaining({
      name: 'opencode-go-minimax',
      platform: 'anthropic',
      type: 'apikey',
      credentials: expect.objectContaining({
        api_key: 'sk-test-opencode',
        base_url: 'https://opencode.ai/zen/go',
        model_mapping: expect.objectContaining({
          'opencode-go/minimax-m3': 'minimax-m3',
          'opencode-go/qwen3.7-max': 'qwen3.7-max',
          'claude-*': 'minimax-m3'
        })
      }),
      extra: expect.objectContaining({
        anthropic_passthrough: true
      })
    }))
  })
})
