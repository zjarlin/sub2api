import { buildGatewayUrl } from './url'

export type ChatPlaygroundRole = 'system' | 'user' | 'assistant'

export interface ChatPlaygroundMessage {
  role: ChatPlaygroundRole
  content: string
}

export interface ChatPlaygroundModel {
  id: string
  owned_by?: string
}

export interface ChatPlaygroundUsage {
  prompt_tokens?: number
  completion_tokens?: number
  input_tokens?: number
  output_tokens?: number
  total_tokens?: number
}

export interface ChatPlaygroundImage {
  url: string
  revisedPrompt: string
}

export interface GenerateChatPlaygroundImageResult {
  images: ChatPlaygroundImage[]
  usage: ChatPlaygroundUsage | null
}

export interface GenerateChatPlaygroundImageOptions {
  apiKey: string
  model: string
  prompt: string
  signal?: AbortSignal
}

export interface StreamChatCompletionResult {
  content: string
  finishReason: string | null
  usage: ChatPlaygroundUsage | null
}

export interface StreamChatCompletionOptions {
  apiKey: string
  model: string
  messages: ChatPlaygroundMessage[]
  signal?: AbortSignal
  onDelta: (text: string) => void
}

interface GatewayErrorBody {
  error?: {
    message?: string
  }
  message?: string
}

interface ChatCompletionChunk {
  choices?: Array<{
    delta?: {
      content?: unknown
    }
    message?: {
      content?: unknown
    }
    finish_reason?: string | null
  }>
  usage?: ChatPlaygroundUsage | null
  error?: {
    message?: string
  }
}

interface ImageGenerationResponse {
  data?: Array<{
    b64_json?: unknown
    url?: unknown
    revised_prompt?: unknown
    output_format?: unknown
  }>
  usage?: ChatPlaygroundUsage | null
  error?: {
    message?: string
  }
}

export function isImageGenerationModel(model: string): boolean {
  const normalized = model.trim().toLowerCase()
  return normalized.startsWith('gpt-image-')
    || normalized === 'grok-imagine'
    || normalized === 'grok-imagine-edit'
    || normalized.startsWith('grok-imagine-image')
}

function extractContentText(content: unknown): string {
  if (typeof content === 'string') {
    return content
  }
  if (!Array.isArray(content)) {
    return ''
  }
  return content
    .map((part) => {
      if (!part || typeof part !== 'object') {
        return ''
      }
      const value = (part as { text?: unknown }).text
      return typeof value === 'string' ? value : ''
    })
    .join('')
}

function extractGatewayErrorMessage(text: string, status: number): string {
  if (!text.trim()) {
    return `Gateway request failed with status ${status}`
  }
  if (/^\s*</.test(text)) {
    return `Gateway request failed with status ${status}`
  }

  try {
    const payload = JSON.parse(text) as GatewayErrorBody
    const message = payload.error?.message || payload.message
    return message?.trim() || text.trim()
  } catch {
    return text.trim()
  }
}

async function assertGatewayResponse(response: Response): Promise<void> {
  if (response.ok) {
    return
  }
  const body = await response.text()
  throw new Error(extractGatewayErrorMessage(body, response.status))
}

export async function listChatPlaygroundModels(
  apiKey: string,
  signal?: AbortSignal,
): Promise<ChatPlaygroundModel[]> {
  const response = await fetch(buildGatewayUrl('/v1/models'), {
    headers: {
      Authorization: `Bearer ${apiKey}`,
    },
    signal,
  })
  await assertGatewayResponse(response)

  const payload = await response.json() as { data?: ChatPlaygroundModel[] }
  if (!Array.isArray(payload.data)) {
    throw new Error('Gateway returned an invalid model list')
  }

  return payload.data
    .filter((model) => typeof model.id === 'string' && model.id.trim())
    .sort((left, right) => left.id.localeCompare(right.id))
}

function normalizeImageMimeType(outputFormat: unknown): string {
  if (typeof outputFormat !== 'string') {
    return 'image/png'
  }
  switch (outputFormat.trim().toLowerCase()) {
    case 'jpeg':
    case 'jpg':
      return 'image/jpeg'
    case 'webp':
      return 'image/webp'
    default:
      return 'image/png'
  }
}

function normalizeGeneratedImage(
  item: NonNullable<ImageGenerationResponse['data']>[number],
): ChatPlaygroundImage | null {
  const revisedPrompt = typeof item.revised_prompt === 'string' ? item.revised_prompt.trim() : ''
  if (typeof item.b64_json === 'string' && item.b64_json.trim()) {
    const base64 = item.b64_json.trim()
    const mimeType = normalizeImageMimeType(item.output_format)
    return {
      url: base64.startsWith('data:') ? base64 : `data:${mimeType};base64,${base64}`,
      revisedPrompt,
    }
  }
  if (typeof item.url !== 'string') {
    return null
  }
  const url = item.url.trim()
  if (!/^(https?:\/\/|\/)/i.test(url)) {
    return null
  }
  return { url, revisedPrompt }
}

export async function generateChatPlaygroundImage(
  options: GenerateChatPlaygroundImageOptions,
): Promise<GenerateChatPlaygroundImageResult> {
  const response = await fetch(buildGatewayUrl('/v1/images/generations'), {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${options.apiKey}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      model: options.model,
      prompt: options.prompt,
      n: 1,
      response_format: 'b64_json',
    }),
    signal: options.signal,
  })
  await assertGatewayResponse(response)

  const payload = await response.json() as ImageGenerationResponse
  if (payload.error?.message) {
    throw new Error(payload.error.message)
  }
  const images = (payload.data || [])
    .map(normalizeGeneratedImage)
    .filter((image): image is ChatPlaygroundImage => image !== null)
  if (images.length === 0) {
    throw new Error('Gateway returned no generated images')
  }

  const usage = payload.usage || null
  if (usage && usage.total_tokens === undefined) {
    usage.total_tokens = (usage.input_tokens || 0) + (usage.output_tokens || 0)
  }
  return { images, usage }
}

function parseChatCompletionChunk(
  chunk: ChatCompletionChunk,
  current: StreamChatCompletionResult,
  onDelta: (text: string) => void,
): void {
  if (chunk.error?.message) {
    throw new Error(chunk.error.message)
  }

  const choice = chunk.choices?.[0]
  const delta = extractContentText(choice?.delta?.content)
  if (delta) {
    current.content += delta
    onDelta(delta)
  }
  if (choice?.finish_reason) {
    current.finishReason = choice.finish_reason
  }
  if (chunk.usage) {
    current.usage = chunk.usage
  }
}

function parseSSEFrame(
  frame: string,
  current: StreamChatCompletionResult,
  onDelta: (text: string) => void,
): boolean {
  const data = frame
    .split('\n')
    .filter((line) => line.startsWith('data:'))
    .map((line) => line.slice(5).trimStart())
    .join('\n')
    .trim()

  if (!data) {
    return false
  }
  if (data === '[DONE]') {
    return true
  }

  const chunk = JSON.parse(data) as ChatCompletionChunk
  parseChatCompletionChunk(chunk, current, onDelta)
  return false
}

async function consumeSSE(
  response: Response,
  current: StreamChatCompletionResult,
  onDelta: (text: string) => void,
): Promise<void> {
  const reader = response.body?.getReader()
  if (!reader) {
    throw new Error('Gateway returned an empty response stream')
  }

  const decoder = new TextDecoder()
  let buffer = ''
  let completed = false

  while (!completed) {
    const { done, value } = await reader.read()
    buffer += decoder.decode(value, { stream: !done }).replace(/\r\n/g, '\n')

    let boundary = buffer.indexOf('\n\n')
    while (boundary >= 0) {
      const frame = buffer.slice(0, boundary)
      buffer = buffer.slice(boundary + 2)
      completed = parseSSEFrame(frame, current, onDelta)
      if (completed) {
        break
      }
      boundary = buffer.indexOf('\n\n')
    }

    if (done) {
      if (!completed && buffer.trim()) {
        parseSSEFrame(buffer, current, onDelta)
      }
      break
    }
  }
}

async function consumeJSON(
  response: Response,
  current: StreamChatCompletionResult,
  onDelta: (text: string) => void,
): Promise<void> {
  const chunk = await response.json() as ChatCompletionChunk
  if (chunk.error?.message) {
    throw new Error(chunk.error.message)
  }

  const choice = chunk.choices?.[0]
  const content = extractContentText(choice?.message?.content)
  if (content) {
    current.content = content
    onDelta(content)
  }
  current.finishReason = choice?.finish_reason ?? null
  current.usage = chunk.usage ?? null
}

export async function streamChatCompletion(
  options: StreamChatCompletionOptions,
): Promise<StreamChatCompletionResult> {
  const response = await fetch(buildGatewayUrl('/v1/chat/completions'), {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${options.apiKey}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      model: options.model,
      messages: options.messages,
      stream: true,
    }),
    signal: options.signal,
  })
  await assertGatewayResponse(response)

  const result: StreamChatCompletionResult = {
    content: '',
    finishReason: null,
    usage: null,
  }
  const contentType = response.headers.get('content-type') || ''

  if (contentType.includes('text/event-stream')) {
    await consumeSSE(response, result, options.onDelta)
  } else {
    await consumeJSON(response, result, options.onDelta)
  }

  return result
}
