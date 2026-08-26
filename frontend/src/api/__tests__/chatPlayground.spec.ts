import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  generateChatPlaygroundImage,
  isImageGenerationModel,
  listChatPlaygroundModels,
  streamChatCompletion,
} from '@/api/chatPlayground'

function createStreamResponse(chunks: string[]): Response {
  const encoder = new TextEncoder()
  let index = 0
  const stream = new ReadableStream<Uint8Array>({
    pull(controller) {
      if (index >= chunks.length) {
        controller.close()
        return
      }
      controller.enqueue(encoder.encode(chunks[index]))
      index += 1
    },
  })

  return new Response(stream, {
    status: 200,
    headers: { 'Content-Type': 'text/event-stream' },
  })
}

describe('chatPlayground API', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('使用所选 API Key 加载并排序模型', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: [
        { id: 'gpt-5.4', owned_by: 'openai' },
        { id: 'claude-sonnet-4-5', owned_by: 'anthropic' },
        { id: '' },
      ],
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)

    const models = await listChatPlaygroundModels('sk-user-key')

    expect(models.map((model) => model.id)).toEqual(['claude-sonnet-4-5', 'gpt-5.4'])
    expect(fetchMock).toHaveBeenCalledOnce()
    const [, request] = fetchMock.mock.calls[0]
    expect(request.headers.Authorization).toBe('Bearer sk-user-key')
  })

  it('识别网关支持的图片生成模型', () => {
    expect(isImageGenerationModel('gpt-image-2')).toBe(true)
    expect(isImageGenerationModel('grok-imagine-image-quality')).toBe(true)
    expect(isImageGenerationModel('gpt-5.6-sol')).toBe(false)
  })

  it('通过 Images API 生成并规范化 base64 图片', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: [{
        b64_json: 'aW1hZ2U=',
        revised_prompt: 'a friendly cat',
        output_format: 'webp',
      }],
      usage: { input_tokens: 3, output_tokens: 7 },
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)

    const result = await generateChatPlaygroundImage({
      apiKey: 'sk-user-key',
      model: 'gpt-image-2',
      prompt: 'draw a cat',
    })

    expect(result.images).toEqual([{
      url: 'data:image/webp;base64,aW1hZ2U=',
      revisedPrompt: 'a friendly cat',
    }])
    expect(result.usage?.total_tokens).toBe(10)
    const [url, request] = fetchMock.mock.calls[0]
    expect(url).toContain('/v1/images/generations')
    expect(JSON.parse(request.body)).toEqual({
      model: 'gpt-image-2',
      prompt: 'draw a cat',
      n: 1,
      response_format: 'b64_json',
    })
  })

  it('透传 Images keepalive 已提交状态码后的响应体错误', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: { message: 'image upstream unavailable' },
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })))

    await expect(generateChatPlaygroundImage({
      apiKey: 'sk-user-key',
      model: 'gpt-image-2',
      prompt: 'draw a cat',
    })).rejects.toThrow('image upstream unavailable')
  })

  it('上传多张参考图时通过 multipart Images Edits API 生成', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: [{ b64_json: 'ZWRpdGVk' }],
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)
    const firstReferenceImage = new File(['first'], 'first.png', { type: 'image/png' })
    const secondReferenceImage = new File(['second'], 'second.webp', { type: 'image/webp' })

    await generateChatPlaygroundImage({
      apiKey: 'sk-user-key',
      model: 'gpt-image-2',
      prompt: 'replace the background',
      referenceImages: [firstReferenceImage, secondReferenceImage],
    })

    const [url, request] = fetchMock.mock.calls[0]
    expect(url).toContain('/v1/images/edits')
    expect(request.headers).toEqual({ Authorization: 'Bearer sk-user-key' })
    expect(request.body).toBeInstanceOf(FormData)
    const formData = request.body as FormData
    expect(formData.get('model')).toBe('gpt-image-2')
    expect(formData.get('prompt')).toBe('replace the background')
    const uploadedImages = formData.getAll('image') as File[]
    expect(uploadedImages).toHaveLength(2)
    expect(uploadedImages.map((image) => image.name)).toEqual(['first.png', 'second.webp'])
    expect(uploadedImages.map((image) => image.type)).toEqual(['image/png', 'image/webp'])
    expect(uploadedImages.map((image) => image.size)).toEqual([
      firstReferenceImage.size,
      secondReferenceImage.size,
    ])
    expect(formData.get('input_fidelity')).toBe('high')
    expect(formData.get('response_format')).toBe('b64_json')
  })

  it('解析跨网络分片的 Chat Completions SSE', async () => {
    const response = createStreamResponse([
      'data: {"choices":[{"delta":{"content":"你',
      '好"},"finish_reason":null}]}\n\n',
      'data: {"choices":[{"delta":{"content":"！"},"finish_reason":"stop"}]}\n\n',
      'data: [DONE]\n\n',
    ])
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))
    const deltas: string[] = []

    const result = await streamChatCompletion({
      apiKey: 'sk-user-key',
      model: 'gpt-5.4',
      messages: [{ role: 'user', content: '你好' }],
      onDelta: (text) => deltas.push(text),
    })

    expect(deltas).toEqual(['你好', '！'])
    expect(result).toEqual({
      content: '你好！',
      finishReason: 'stop',
      usage: null,
    })
  })

  it('兼容忽略 stream 参数并返回普通 JSON 的上游', async () => {
    const response = new Response(JSON.stringify({
      choices: [{
        message: { content: [{ type: 'text', text: 'fallback' }] },
        finish_reason: 'stop',
      }],
      usage: { prompt_tokens: 2, completion_tokens: 3, total_tokens: 5 },
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))
    const deltas: string[] = []

    const result = await streamChatCompletion({
      apiKey: 'sk-user-key',
      model: 'grok-4',
      messages: [{ role: 'user', content: 'test' }],
      onDelta: (text) => deltas.push(text),
    })

    expect(deltas).toEqual(['fallback'])
    expect(result.usage?.total_tokens).toBe(5)
  })

  it('透传网关返回的错误消息', async () => {
    const response = new Response(JSON.stringify({
      error: { message: 'The selected API key has no quota' },
    }), {
      status: 429,
      headers: { 'Content-Type': 'application/json' },
    })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))

    await expect(listChatPlaygroundModels('sk-user-key')).rejects.toThrow(
      'The selected API key has no quota',
    )
  })

  it('不会把反向代理 HTML 错误页显示到聊天记录', async () => {
    const response = new Response('<html><body>Bad gateway</body></html>', {
      status: 502,
      headers: { 'Content-Type': 'text/html' },
    })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response))

    await expect(listChatPlaygroundModels('sk-user-key')).rejects.toThrow(
      'Gateway request failed with status 502',
    )
  })
})
