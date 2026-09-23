import { buildApiUrl } from '@/api/client'
import { ADMIN_UI_REQUEST_HEADER } from '@/api/adminUIRequest'

export interface ModelTestEvent {
  type: string
  text?: string
  model?: string
  success?: boolean
  error?: string
  image_url?: string
  audio_url?: string
  video_url?: string
  mime_type?: string
}

export interface ModelTestResult {
  success: boolean
  error?: string
}

// 统一请求超时与用户取消，避免批量任务卡在单个上游。
export async function runModelTest(...[path, payload, signal, onEvent]: Parameters<typeof readModelTest>): Promise<ModelTestResult> {
  signal.throwIfAborted()
  const controller = new AbortController()
  const abort = () => controller.abort(signal.reason)
  signal.addEventListener('abort', abort, { once: true })
  const timer = setTimeout(() => controller.abort(new Error('Account test timed out')), 120000)
  try {
    return await readModelTest(path, payload, controller.signal, onEvent)
  } finally {
    clearTimeout(timer)
    signal.removeEventListener('abort', abort)
  }
}

// 只有明确的结束事件才表示测试完成；网络错误或断流不得当作模型失败删除。
async function readModelTest(
  path: string,
  payload: { model_id?: string; prompt?: string; mode?: string; image_data_url?: string; audio_data_url?: string },
  signal: AbortSignal,
  onEvent?: (event: ModelTestEvent) => void
): Promise<ModelTestResult> {
  const response = await fetch(buildApiUrl(path), {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${localStorage.getItem('auth_token')}`,
      'Content-Type': 'application/json',
      [ADMIN_UI_REQUEST_HEADER]: '1'
    },
    body: JSON.stringify(payload),
    signal
  })
  if (!response.ok) {
    throw new Error(`HTTP ${response.status}`)
  }
  const reader = response.body?.getReader()
  if (!reader) {
    throw new Error('Incomplete test stream')
  }
  const decoder = new TextDecoder()
  let buffer = ''
  let result: ModelTestResult | undefined
  const parse = (line: string) => {
    if (!line.startsWith('data:')) {
      return
    }
    const data = line.slice(5).trim()
    if (!data || data === '[DONE]') {
      return
    }
    const event = JSON.parse(data) as ModelTestEvent
    onEvent?.(event)
    if (event.type === 'error' || event.type === 'test_complete') {
      result = { success: event.type === 'test_complete' && event.success === true, error: event.error }
    }
  }
  try {
    while (true) {
      const { done, value } = await reader.read()
      buffer += done ? decoder.decode() : decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''
      for (const line of lines) {
        parse(line)
        if (result) {
          break
        }
      }
      // 完成事件已经给出业务结果，不再等待上游关闭连接。
      if (result) {
        break
      }
      if (done) {
        parse(buffer)
        break
      }
    }
  } finally {
    if (result) {
      void reader.cancel?.().catch(() => undefined)
    }
    reader.releaseLock?.()
  }
  signal.throwIfAborted()
  if (!result) {
    throw new Error('Incomplete test stream')
  }
  return result
}

