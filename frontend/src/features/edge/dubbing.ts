export interface DubbingSegment { start: number; end: number; text: string }
export interface DubbingOptions {
  mode: 'auto' | 'timeline'
  language: string
  keep_original_audio: boolean
  segments?: DubbingSegment[]
}

export function defaultDubbingOptions(): DubbingOptions {
  return { mode: 'auto', language: 'zh', keep_original_audio: false }
}

export function dubbingOptionsError(value: unknown, duration = 0): string {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return 'invalidOptions'
  const options = value as Record<string, unknown>
  if (Object.keys(options).some(key => !['mode', 'language', 'keep_original_audio', 'segments'].includes(key))) return 'invalidOptions'
  if (!['auto', 'timeline'].includes(options.mode as string)
      || !['zh', 'en', 'ja', 'ko', 'yue'].includes(options.language as string)
      || typeof options.keep_original_audio !== 'boolean') return 'invalidOptions'
  if (options.mode === 'auto') return options.segments === undefined ? '' : 'invalidOptions'
  if (!Array.isArray(options.segments) || options.segments.length < 1 || options.segments.length > 500) return 'invalidSegments'
  let previousEnd = 0
  for (const segment of options.segments) {
    if (!segment || typeof segment !== 'object' || Object.keys(segment).sort().join(',') !== 'end,start,text') return 'invalidSegments'
    const { start, end, text } = segment
    if (typeof start !== 'number' || typeof end !== 'number' || !Number.isFinite(start) || !Number.isFinite(end)
        || start < previousEnd || end <= start || (duration > 0 && end > duration)) return 'invalidSegments'
    if (typeof text !== 'string' || !text.trim() || text.length > 2000) return 'invalidSegments'
    previousEnd = end
  }
  return ''
}

export function videoFileError(file: File | null): string {
  if (!file) return 'chooseVideo'
  if (!file.size || file.size > 512 * 1024 * 1024) return 'fileSizeError'
  if (!file.type.startsWith('video/') && !/\.(mp4|mov|mkv|webm|avi|m4v|flv|ts)$/i.test(file.name)) return 'fileTypeError'
  return ''
}
