import { onBeforeUnmount, ref } from 'vue'

interface EdgeResponse {
  status: number
  statusText: string
  durationMs: number
  size: number
  body: string
  ok: boolean
}
interface MediaResult { url: string; type: string; name: string; size: number }

export function useEdgeResponse(origin: string, t: (key: string) => string) {
  const sending = ref(false)
  const response = ref<EdgeResponse | null>(null)
  const media = ref<MediaResult | null>(null)
  const sendError = ref('')
  const loadingMedia = ref(false)
  const taskID = ref('')
  let controller: AbortController | undefined

  function reset() {
    controller?.abort()
    controller = undefined
    if (media.value) URL.revokeObjectURL(media.value.url)
    media.value = null
    response.value = null
    sendError.value = ''
    sending.value = false
    loadingMedia.value = false
  }
  onBeforeUnmount(reset)

  function setMedia(blob: Blob, type: string) {
    if (media.value) URL.revokeObjectURL(media.value.url)
    const extension = /mp4/.test(type) ? 'mp4' : /mpeg|mp3/.test(type) ? 'mp3' : /ogg/.test(type) ? 'ogg' : /wav/.test(type) ? 'wav' : /webm/.test(type) ? 'webm' : 'bin'
    media.value = { url: URL.createObjectURL(blob), type, name: `result.${extension}`, size: blob.size }
  }

  async function execute(urlText: string, init: RequestInit) {
    reset()
    const active = new AbortController()
    controller = active
    sending.value = true
    const started = performance.now()
    try {
      const url = new URL(urlText, origin)
      if (url.origin !== origin) throw new Error(t('admin.vision.mediaResult.sameOrigin'))
      const result = await fetch(url.toString(), { ...init, signal: active.signal })
      const blob = await result.blob()
      if (active.signal.aborted) return
      const type = result.headers.get('content-type') ?? ''
      const binary = result.ok && /^(audio|video)\/|application\/octet-stream/i.test(type)
      response.value = {
        status: result.status, statusText: result.statusText,
        durationMs: Math.round(performance.now() - started), size: blob.size, body: '', ok: result.ok,
      }
      if (binary) {
        setMedia(blob, type)
        return
      }
      const text = await blob.text()
      if (active.signal.aborted) return
      response.value.body = text
      let task: Record<string, unknown>
      try { task = JSON.parse(text) } catch { return }
      if (!task || typeof task !== 'object') return
      response.value.body = JSON.stringify(task, null, 2)
      if (typeof task.task_id === 'string') taskID.value = task.task_id
      if (['failed', 'blocked', 'cancelled'].includes(String(task.status))) {
        response.value.ok = false
        sendError.value = typeof task.error === 'string' ? task.error : t('admin.vision.mediaResult.taskFailed')
        return
      }
      const output = task.output as { path?: unknown } | undefined
      if (result.ok && task.status === 'succeeded' && output?.path) {
        if (typeof output.path !== 'string' || !/^\/media\/tasks\/[a-zA-Z0-9_-]+\/content$/.test(output.path)) {
          throw new Error(t('admin.vision.mediaResult.invalidOutput'))
        }
        loadingMedia.value = true
        const headers = new Headers()
        const auth = new Headers(init.headers).get('Authorization')
        if (auth) headers.set('Authorization', auth)
        const content = await fetch(new URL(output.path, origin).toString(), { headers, signal: active.signal })
        if (!content.ok) throw new Error((await content.text()).slice(0, 2000))
        const contentType = content.headers.get('content-type') ?? ''
        if (!/^(audio|video)\//i.test(contentType)) throw new Error(t('admin.vision.mediaResult.invalidOutput'))
        const mediaBlob = await content.blob()
        if (!active.signal.aborted) setMedia(mediaBlob, contentType)
      }
    } catch (cause) {
      if (!active.signal.aborted) sendError.value = cause instanceof Error ? cause.message : t('admin.vision.requestFailed')
    } finally {
      if (controller === active) {
        sending.value = false
        loadingMedia.value = false
      }
    }
  }
  return { sending, response, media, sendError, loadingMedia, taskID, execute, reset }
}
