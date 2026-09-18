import { afterEach, describe, expect, it, vi } from 'vitest'
import { runModelTest } from '@/api/accountTest'
import { successfulModelMapping } from '../modelMapping'

vi.mock('@/api/client', () => ({ buildApiUrl: (path: string) => `/api/v1${path}` }))
afterEach(() => vi.unstubAllGlobals())
function response(data: string) {
  const bytes = new TextEncoder().encode(data)
  return new Response(new ReadableStream({ start(controller) {
    for (const byte of bytes) { controller.enqueue(new Uint8Array([byte])) }
    controller.close()
  } }))
}
describe('账号测试业务结果', () => {
  it('处理分片 UTF-8 和没有结尾换行的失败事件', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response('data: {"type":"error","error":"模型不可用"}')))
    const result = await runModelTest('/user/accounts/1/test', {}, new AbortController().signal)
    expect(result).toEqual({ success: false, error: '模型不可用' })
  })
  it('HTTP 200 的未完成流不能触发清理', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response('data: {"type":"content","text":"hello"}\n')))
    await expect(runModelTest('/admin/accounts/1/test', {}, new AbortController().signal)).rejects.toThrow('Incomplete')
  })
  it('成功必须有明确的完成事件', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response('data: {"type":"test_complete","success":true}\n\n')))
    await expect(runModelTest('/admin/accounts/1/test', {}, new AbortController().signal)).resolves.toEqual({ success: true })
  })
  it('收到完成事件后立即返回，不等待仍保持连接的响应流', async () => {
    const cancel = vi.fn()
    const stream = new ReadableStream({
      start(controller) {
        controller.enqueue(new TextEncoder().encode('data: {"type":"test_complete","success":true}\n\n'))
      },
      cancel
    })
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(stream)))
    await expect(runModelTest('/admin/accounts/1/test', {}, new AbortController().signal)).resolves.toEqual({ success: true })
    expect(cancel).toHaveBeenCalledOnce()
  })
  it('超时会中止请求并返回错误', async () => {
    vi.useFakeTimers()
    vi.stubGlobal('fetch', vi.fn((_url, options) => new Promise((_resolve, reject) => {
      options.signal.addEventListener('abort', () => reject(options.signal.reason))
    })))
    try {
      const result = expect(runModelTest('/admin/accounts/1/test', {}, new AbortController().signal)).rejects.toThrow('timed out')
      await vi.advanceTimersByTimeAsync(120000)
      await result
    } finally {
      vi.useRealTimers()
    }
  })
  it('保留别名目标和未测试项，删除已确认失败项', () => {
    expect(successfulModelMapping({ alias: 'real-model', bad: 'bad', 'future-*': 'other' }, ['alias', 'bad'], ['alias']))
      .toEqual({ alias: 'real-model', 'future-*': 'other' })
  })
})
