import { afterEach, describe, expect, it, vi } from 'vitest'
import {
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
