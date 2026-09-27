import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import VisionEdgeView from '@/views/admin/VisionEdgeView.vue'

enableAutoUnmount(afterEach)

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
    URL.createObjectURL = vi.fn().mockReturnValue('blob:media-result')
    URL.revokeObjectURL = vi.fn()
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
    expect(wrapper.get('[data-testid="edge-request-detail"]').text()).not.toContain('upstream-secret')
  })

  it('shows the Postman-style workspace on first render', async () => {
    const wrapper = mount(VisionEdgeView)
    await flushPromises()

    const editor = wrapper.get('[data-testid="edge-request-detail"]')
    expect(editor.isVisible()).toBe(true)
    expect(editor.get('[data-testid="edge-request-url"]').exists()).toBe(true)
    expect(editor.find('[data-testid="edge-request-body"]').exists()).toBe(true)
    expect(editor.find('[data-testid="edge-send-request"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('admin.vision.workbench.query')
    expect(wrapper.text()).toContain('admin.vision.workbench.headers')
    expect(wrapper.text()).toContain('admin.vision.workbench.body')
  })

  it('sends the Volcengine envelope through the gateway with the chosen API key', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ResponseMetadata: { RequestId: 'req-1' }, Result: { detections: [] } }), {
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
    expect((wrapper.get('[data-testid="edge-request-url"]').element as HTMLInputElement).value).toContain('/vision/volcengine/detect')
    expect((wrapper.get('[data-testid="edge-request-body"]').element as HTMLTextAreaElement).value).toContain('"Action": "Detect"')

    await wrapper.get('[data-testid="edge-send-request"]').trigger('click')
    await flushPromises()

    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [calledUrl, calledInit] = fetchMock.mock.calls[0]
    expect(calledUrl).toContain('/vision/volcengine/detect')
    expect((calledInit.headers as Headers).get('Authorization')).toBe('Bearer sk-test-1234567890')
    expect((calledInit.headers as Headers).get('Content-Type')).toBe('application/json')
    expect(calledInit.body).toContain('"Version": "2022-08-31"')
    expect(wrapper.text()).toContain('req-1')
    expect(wrapper.text()).toContain('200')
  })

  it('switches the Volcengine Action and endpoint with the selected vision operation', async () => {
    const wrapper = mount(VisionEdgeView)
    await flushPromises()

    await wrapper.get('[data-testid="edge-endpoint-classify"]').trigger('click')
    await flushPromises()

    expect((wrapper.get('[data-testid="edge-request-url"]').element as HTMLInputElement).value).toContain('/vision/volcengine/classify')
    expect((wrapper.get('[data-testid="edge-request-body"]').element as HTMLTextAreaElement).value).toContain('"Action": "Classify"')
    expect(wrapper.text()).toContain('cURL')
    expect(wrapper.text()).toContain('JavaScript')
    expect(wrapper.text()).toContain('Python')
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

  it('uploads an actual video and timeline JSON, then previews the authenticated result', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ task_id: 'dub_test', status: 'succeeded', output: { path: '/media/tasks/dub_test/content' } }), { headers: { 'content-type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(new Uint8Array([0, 255, 128, 10]), { headers: { 'content-type': 'video/mp4' } }))
    vi.stubGlobal('fetch', fetchMock)
    const wrapper = mount(VisionEdgeView)
    await flushPromises()
    await wrapper.get('[data-testid="edge-endpoint-dub"]').trigger('click')
    const input = wrapper.get('[data-testid="dub-file"]')
    Object.defineProperty(input.element, 'files', { value: [new File(['video'], 'input.mp4', { type: 'video/mp4' })] })
    await input.trigger('change')
    await wrapper.get('[data-testid="dub-mode-timeline"]').trigger('click')
    await wrapper.get('[data-testid="dub-start"]').setValue('0.5')
    await wrapper.get('[data-testid="dub-end"]').setValue('3')
    await wrapper.get('[data-testid="dub-text"]').setValue('你好 & hello')
    await wrapper.get('[data-testid="edge-send-request"]').trigger('click')
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    const body = fetchMock.mock.calls[0][1].body as FormData
    expect(body.get('video')).toBeInstanceOf(File)
    expect((body.get('video') as File).name).toBe('input.mp4')
    expect(JSON.parse(body.get('options') as string)).toEqual({ mode: 'timeline', language: 'zh', keep_original_audio: false, segments: [{ start: 0.5, end: 3, text: '你好 & hello' }] })
    expect(fetchMock.mock.calls[0][1].headers.has('Content-Type')).toBe(false)
    expect(fetchMock.mock.calls[1][0]).toContain('/media/tasks/dub_test/content')
    expect(fetchMock.mock.calls[1][1].headers.get('Authorization')).toBe('Bearer sk-test-1234567890')
    expect(wrapper.get('[data-testid="edge-media-result"] video').exists()).toBe(true)
    expect(wrapper.get('[data-testid="edge-media-download"]').attributes('download')).toBe('result.mp4')
    expect(wrapper.text()).toContain('4 B')
    await wrapper.get('[data-testid="edge-endpoint-tts"]').trigger('click')
    expect(URL.revokeObjectURL).toHaveBeenCalled()
    expect(wrapper.find('[data-testid="edge-media-result"]').exists()).toBe(false)
  })

  it('requires a video and validates the timeline before sending', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    const wrapper = mount(VisionEdgeView)
    await flushPromises()
    await wrapper.get('[data-testid="edge-endpoint-dub"]').trigger('click')
    await wrapper.get('[data-testid="edge-send-request"]').trigger('click')
    expect(wrapper.text()).toContain('admin.vision.dubbing.chooseVideo')
    await wrapper.get('[data-testid="dub-mode-timeline"]').trigger('click')
    await wrapper.get('[data-testid="dub-start"]').setValue('4')
    await wrapper.get('[data-testid="dub-end"]').setValue('1')
    expect(wrapper.text()).toContain('admin.vision.dubbing.invalidSegments')
    expect(fetchMock).not.toHaveBeenCalled()
    await wrapper.findAll('button').find(button => button.text() === 'admin.vision.mediaResult.docs')!.trigger('click')
    expect(wrapper.find('[data-testid="dubbing-docs"]').exists()).toBe(true)
  })

  it('plays TTS bytes instead of rendering them as text', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(new Uint8Array([82, 73, 70, 70, 255, 128]), { headers: { 'content-type': 'audio/wav' } })))
    const wrapper = mount(VisionEdgeView)
    await flushPromises()
    await wrapper.get('[data-testid="edge-endpoint-tts"]').trigger('click')
    await wrapper.get('[data-testid="edge-send-request"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('audio').attributes('src')).toBe('blob:media-result')
    expect(wrapper.text()).toContain('6 B')
    expect(wrapper.get('[data-testid="edge-media-download"]').attributes('download')).toBe('result.wav')
  })

  it.each(['failed', 'blocked'])('shows %s task errors even with HTTP 200', async (status) => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ task_id: 'dub_failed', status, error: 'speech is too long' })))
    vi.stubGlobal('fetch', fetchMock)
    const wrapper = mount(VisionEdgeView)
    await flushPromises()
    await wrapper.get('[data-testid="edge-send-request"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('speech is too long')
    expect(wrapper.find('[data-testid="edge-media-download"]').exists()).toBe(false)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('does not send the key to a foreign request or task URL', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: 'succeeded', output: { path: 'https://other.example/content' } })))
    vi.stubGlobal('fetch', fetchMock)
    const wrapper = mount(VisionEdgeView)
    await flushPromises()
    await wrapper.get('[data-testid="edge-request-url"]').setValue('https://other.example/tts')
    await wrapper.get('[data-testid="edge-send-request"]').trigger('click')
    expect(fetchMock).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="edge-endpoint-tts"]').trigger('click')
    await wrapper.get('[data-testid="edge-send-request"]').trigger('click')
    await flushPromises()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(wrapper.text()).toContain('admin.vision.mediaResult.invalidOutput')
  })

  it('ignores a response after switching endpoints', async () => {
    let resolve!: (response: Response) => void
    vi.stubGlobal('fetch', vi.fn().mockReturnValue(new Promise<Response>(done => { resolve = done })))
    const wrapper = mount(VisionEdgeView)
    await flushPromises()
    await wrapper.get('[data-testid="edge-send-request"]').trigger('click')
    await wrapper.get('[data-testid="edge-endpoint-dub"]').trigger('click')
    resolve(new Response('old-result'))
    await flushPromises()
    expect(wrapper.text()).not.toContain('old-result')
  })
})
