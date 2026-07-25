import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { ApiKey } from '@/types'
import ChatPlaygroundView from '../ChatPlaygroundView.vue'

const {
  listKeys,
  listModels,
  generateImage,
  streamCompletion,
  showError,
  copyToClipboard,
} = vi.hoisted(() => ({
  listKeys: vi.fn(),
  listModels: vi.fn(),
  generateImage: vi.fn(),
  streamCompletion: vi.fn(),
  showError: vi.fn(),
  copyToClipboard: vi.fn(),
}))

vi.mock('@/api/keys', () => ({
  keysAPI: {
    list: listKeys,
  },
}))

vi.mock('@/api/chatPlayground', () => ({
  generateChatPlaygroundImage: generateImage,
  isImageGenerationModel: (model: string) => model.startsWith('gpt-image-'),
  listChatPlaygroundModels: listModels,
  streamChatCompletion: streamCompletion,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => (
        key === 'chatPlayground.tokens' ? `${params?.count} tokens` : key
      ),
    }),
  }
})

const AppLayoutStub = {
  template: '<div><slot /></div>',
}

const IconStub = {
  props: ['name'],
  template: '<span>{{ name }}</span>',
}

const SelectStub = {
  props: ['id', 'modelValue', 'options', 'disabled'],
  emits: ['update:modelValue'],
  methods: {
    emitSelection(event: Event) {
      const target = event.target as HTMLSelectElement
      const option = this.options.find((item: { value: string | number }) => (
        String(item.value) === target.value
      ))
      this.$emit('update:modelValue', option?.value ?? null)
    },
  },
  template: `
    <select :id="id" :value="modelValue" :disabled="disabled" @change="emitSelection">
      <option v-for="option in options" :key="option.value" :value="option.value">
        {{ option.label }}
      </option>
    </select>
  `,
}

function createApiKey(): ApiKey {
  return {
    id: 7,
    user_id: 1,
    key: 'sk-private-chat-key',
    name: 'chat-key',
    group_id: null,
    status: 'active',
    ip_whitelist: [],
    ip_blacklist: [],
    last_used_at: null,
    last_used_ip: null,
    quota: 0,
    quota_used: 0,
    expires_at: null,
    created_at: '2026-07-24T00:00:00Z',
    updated_at: '2026-07-24T00:00:00Z',
    current_concurrency: 0,
    rate_limit_5h: 0,
    rate_limit_1d: 0,
    rate_limit_7d: 0,
    usage_5h: 0,
    usage_1d: 0,
    usage_7d: 0,
    window_5h_start: null,
    window_1d_start: null,
    window_7d_start: null,
    reset_5h_at: null,
    reset_1d_at: null,
    reset_7d_at: null,
  }
}

async function mountView() {
  const wrapper = mount(ChatPlaygroundView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        Icon: IconStub,
        Select: SelectStub,
        RouterLink: {
          template: '<a><slot /></a>',
        },
      },
    },
  })
  await flushPromises()
  return wrapper
}

describe('ChatPlaygroundView', () => {
  beforeEach(() => {
    localStorage.clear()
    listKeys.mockReset()
    listModels.mockReset()
    generateImage.mockReset()
    streamCompletion.mockReset()
    showError.mockReset()
    copyToClipboard.mockReset()

    listKeys.mockResolvedValue({
      items: [createApiKey()],
      total: 1,
      page: 1,
      page_size: 100,
      pages: 1,
    })
    listModels.mockResolvedValue([
      { id: 'gpt-5.4', owned_by: 'openai' },
      { id: 'grok-4', owned_by: 'xai' },
    ])
    generateImage.mockResolvedValue({
      images: [{ url: 'data:image/png;base64,aW1hZ2U=', revisedPrompt: 'a friendly cat' }],
      usage: { total_tokens: 9 },
    })
    streamCompletion.mockImplementation(async (options) => {
      options.onDelta('streamed ')
      options.onDelta('response')
      return {
        content: 'streamed response',
        finishReason: 'stop',
        usage: { total_tokens: 12 },
      }
    })
  })

  it('使用当前用户的 Key 加载可用模型且不持久化密钥明文', async () => {
    const wrapper = await mountView()

    expect(listModels).toHaveBeenCalledWith('sk-private-chat-key', expect.any(AbortSignal))
    expect(wrapper.get('#chat-model').text()).toContain('gpt-5.4')
    expect(localStorage.getItem('sub2api-chat-api-key-id')).toBe('7')
    expect(Object.values(localStorage)).not.toContain('sk-private-chat-key')
  })

  it('发送消息并逐段展示模型回复', async () => {
    const wrapper = await mountView()
    await wrapper.get('.chat-composer__input').setValue('hello model')
    await wrapper.get('.chat-composer').trigger('submit')
    await flushPromises()

    expect(streamCompletion).toHaveBeenCalledWith(expect.objectContaining({
      apiKey: 'sk-private-chat-key',
      model: 'gpt-5.4',
      messages: [{ role: 'user', content: 'hello model' }],
    }))
    expect(wrapper.text()).toContain('hello model')
    expect(wrapper.text()).toContain('streamed response')
    expect(wrapper.text()).toContain('12 tokens')
  })

  it('重试失败请求时不重复发送失败轮次', async () => {
    streamCompletion.mockRejectedValueOnce(new Error('upstream unavailable'))
    const wrapper = await mountView()

    await wrapper.get('.chat-composer__input').setValue('same prompt')
    await wrapper.get('.chat-composer').trigger('submit')
    await flushPromises()

    await wrapper.get('.chat-composer__input').setValue('same prompt')
    await wrapper.get('.chat-composer').trigger('submit')
    await flushPromises()

    expect(streamCompletion).toHaveBeenNthCalledWith(2, expect.objectContaining({
      messages: [{ role: 'user', content: 'same prompt' }],
    }))
  })

  it('图片模型使用 Images API 并展示生成结果', async () => {
    listModels.mockResolvedValue([{ id: 'gpt-image-2', owned_by: 'openai' }])
    const wrapper = await mountView()

    await wrapper.get('.chat-composer__input').setValue('draw a cat')
    await wrapper.get('.chat-composer').trigger('submit')
    await flushPromises()

    expect(generateImage).toHaveBeenCalledWith(expect.objectContaining({
      apiKey: 'sk-private-chat-key',
      model: 'gpt-image-2',
      prompt: 'draw a cat',
    }))
    expect(streamCompletion).not.toHaveBeenCalled()
    expect(wrapper.get('.chat-message__image-link img').attributes('src')).toBe(
      'data:image/png;base64,aW1hZ2U=',
    )
    const downloadLink = wrapper.get('.chat-message__download')
    expect(downloadLink.attributes('href')).toBe('data:image/png;base64,aW1hZ2U=')
    expect(downloadLink.attributes('download')).toMatch(/^generated-image-\d+\.png$/)
    expect(wrapper.text()).toContain('a friendly cat')
    expect(wrapper.text()).toContain('9 tokens')
  })
})
