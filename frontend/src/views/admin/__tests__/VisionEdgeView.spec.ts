import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import VisionEdgeView from '@/views/admin/VisionEdgeView.vue'

const { getStatus, getAllGroups, getCandidates, updateGroup, listKeys, showSuccess, showError } = vi.hoisted(() => ({
  getStatus: vi.fn(),
  getAllGroups: vi.fn(),
  getCandidates: vi.fn(),
  updateGroup: vi.fn(),
  listKeys: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/client', async () => {
  const actual = await vi.importActual<typeof import('@/api/client')>('@/api/client')
  return {
    ...actual,
    apiClient: { get: getStatus },
  }
})

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      getAll: getAllGroups,
      getModelAllowlistCandidates: getCandidates,
      update: updateGroup,
    },
  },
}))

vi.mock('@/api', () => ({
  keysAPI: { list: listKeys },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

vi.mock('@/components/layout/AppLayout.vue', () => ({
  default: { template: '<main><slot /></main>' },
}))

vi.mock('@/components/icons/Icon.vue', () => ({
  default: { template: '<span />' },
}))

function group(id: number, name: string, platform = 'openai') {
  return {
    id,
    name,
    description: null,
    platform,
    rate_multiplier: 1,
    is_exclusive: false,
    status: 'active',
    subscription_type: 'standard',
    daily_limit_usd: null,
    weekly_limit_usd: null,
    monthly_limit_usd: null,
    long_context_pricing_enabled: false,
    allow_image_generation: false,
    allow_batch_image_generation: false,
    image_rate_independent: false,
    image_rate_multiplier: 1,
    batch_image_discount_multiplier: 0.5,
    batch_image_hold_multiplier: 0.6,
    image_price_1k: null,
    image_price_2k: null,
    image_price_4k: null,
    video_rate_independent: false,
    video_rate_multiplier: 1,
    video_price_480p: null,
    video_price_720p: null,
    video_price_1080p: null,
    web_search_price_per_call: null,
    search_price_per_1k: null,
    audio_realtime_price_per_min: null,
    audio_tts_price_per_million_chars: null,
    audio_stt_price_per_hour: null,
    peak_rate_enabled: false,
    peak_start: '',
    peak_end: '',
    peak_rate_multiplier: 1,
    claude_code_only: false,
    fallback_group_id: null,
    fallback_group_id_on_invalid_request: null,
    require_oauth_only: false,
    require_privacy_set: false,
    created_at: '2026-09-24T00:00:00Z',
    updated_at: '2026-09-24T00:00:00Z',
    force_openai_fast: false,
    free_openai_fast: false,
    model_pricing: [],
    profit_control_enabled: false,
    profit_min_margin: 0,
    profit_safety_buffer: 0,
    model_routing: null,
    model_routing_enabled: false,
    mcp_xml_inject: false,
    model_allowlist: { enabled: true, models: ['existing-model'] },
    sort_order: 0,
  }
}

describe('VisionEdgeView workbench', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getStatus.mockResolvedValue({ data: { enabled: true, laya_enabled: true } })
    getAllGroups.mockResolvedValue([group(7, 'Main')])
    getCandidates.mockResolvedValue(['existing-model', 'laya-multilingual'])
    updateGroup.mockResolvedValue(group(7, 'Main'))
    listKeys.mockResolvedValue({
      items: [{ id: 11, name: 'admin-key', key: 'sk-test-1234567890', status: 'active' }],
      total: 1,
      page: 1,
      page_size: 100,
      pages: 1,
    })
  })

  it('imports a curl into editable fields and generates a gateway curl', async () => {
    const wrapper = mount(VisionEdgeView)
    await flushPromises()

    await wrapper.get('[data-testid="edge-endpoint-detect"]').trigger('click')
    await wrapper.get('[data-testid="edge-curl-input"]').setValue(`curl -X POST https://upstream.example/v1/systemone \\
  -H 'authorization: Bearer upstream-secret' \\
  -H 'content-type: application/json' \\
  -d '{"model":"laya-multilingual","state":"hello"}'`)
    await wrapper.findAll('button').find(button => button.text().includes('admin.vision.curl.parse'))!.trigger('click')

    expect((wrapper.get('[data-testid="edge-request-url"]').element as HTMLInputElement).value).toContain('/v1/systemone')
    expect((wrapper.get('[data-testid="edge-model-input"]').element as HTMLInputElement).value).toBe('laya-multilingual')
    expect(wrapper.text()).toContain('$SUB2API_KEY')
    expect(wrapper.text()).not.toContain('upstream-secret')
    expect(wrapper.get('[data-testid="edge-request-editor"]').text()).not.toContain('upstream-secret')
  })

  it('shows the request editor on first render', async () => {
    const wrapper = mount(VisionEdgeView)
    await flushPromises()

    const editor = wrapper.get('[data-testid="edge-request-detail"]')
    expect(editor.isVisible()).toBe(true)
    expect(editor.get('[data-testid="edge-request-editor"]').exists()).toBe(true)
  })

  it('sends the selected endpoint through the gateway with the chosen API key', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ detection: { label: 'person' } }), {
      status: 200,
      statusText: 'OK',
      headers: { 'content-type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)

    const wrapper = mount(VisionEdgeView)
    await flushPromises()

    await wrapper.get('[data-testid="edge-endpoint-detect"]').trigger('click')
    await flushPromises()

    const keySelect = wrapper.get('[data-testid="edge-api-key-select"]')
    expect((keySelect.element as HTMLSelectElement).value).toBe('11')

    await wrapper.findAll('button').find(button => button.text().includes('admin.vision.sendRequest'))!.trigger('click')
    await flushPromises()

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [calledUrl, calledInit] = fetchMock.mock.calls[0]
    expect(calledUrl).toContain('/vision/detect')
    expect((calledInit.headers as Headers).get('Authorization')).toBe('Bearer sk-test-1234567890')
    expect(wrapper.text()).toContain('person')
    expect(wrapper.text()).toContain('200')
  })

  it('merges the selected model into the group model allowlist', async () => {
    const wrapper = mount(VisionEdgeView)
    await flushPromises()

    await wrapper.get('[data-testid="edge-group-select"]').setValue('7')
    await flushPromises()
    await wrapper.get('[data-testid="edge-model-input"]').setValue('laya-multilingual')
    const checkbox = wrapper.findAll<HTMLInputElement>('input[type="checkbox"]').find(input => input.element.value === 'laya-multilingual')
    expect(checkbox).toBeTruthy()
    await checkbox!.setValue(true)
    await wrapper.findAll('button').find(button => button.text().includes('admin.vision.register.register'))!.trigger('click')
    await flushPromises()

    expect(updateGroup).toHaveBeenCalledWith(7, {
      model_allowlist: {
        enabled: true,
        models: ['existing-model', 'laya-multilingual'],
      },
    })
  })
})
