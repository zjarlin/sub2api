import type { LocationQuery, LocationQueryRaw } from 'vue-router'

export const EDGE_SERVICES = [
  { key: 'translate', icon: 'globe', tone: 'teal', endpoints: ['translate', 'translate-providers'], title: 'translation', category: 'language', tags: ['Baidu', 'MyMemory', 'Hy-MT2'] },
  { key: 'vision', icon: 'eye', tone: 'blue', endpoints: ['detect', 'segment', 'pose', 'classify', 'ocr'], title: 'vision', category: 'vision', tags: ['YOLO', 'OCR', 'Volcengine'] },
  { key: 'tts', icon: 'chatBubble', tone: 'rose', endpoints: ['tts'], title: 'tts', category: 'media', tags: ['GPT-SoVITS', 'WAV', 'MP3'] },
  { key: 'dub', icon: 'cube', tone: 'violet', endpoints: ['dub'], title: 'dub', category: 'media', tags: ['ASR', 'TTS', 'Mixing'] },
  { key: 'generation', icon: 'sparkles', tone: 'amber', endpoints: ['generation'], title: 'generation', category: 'media', tags: ['Seedance', 'Ark'] },
  { key: 'laya', icon: 'cpu', tone: 'teal', endpoints: ['laya'], title: 'laya', category: 'decision', tags: ['Laya', 'System One'] },
  { key: 'jev', icon: 'bolt', tone: 'blue', endpoints: ['jev'], title: 'jev', category: 'decision', tags: ['TypeSafe', 'JEV'] },
] as const

export type EdgeService = typeof EDGE_SERVICES[number]
export type EdgeDetailTab = 'docs' | 'debug' | 'code' | 'context'
export const EDGE_ADAPTERS = ['baidu', 'tencent', 'youdao', 'mymemory', 'libretranslate', 'hymt', 'caiyun', 'google_web'] as const
export type EdgeAdapter = typeof EDGE_ADAPTERS[number]

export function edgeSelection(query: LocationQuery) {
  const service = EDGE_SERVICES.find(item => item.key === query.service)
  const tab = ['docs', 'debug', 'code', 'context'].includes(String(query.tab)) ? query.tab as EdgeDetailTab : 'docs'
  const endpoint = service?.endpoints.find(key => key === query.endpoint) ?? service?.endpoints[0]
  const adapter = EDGE_ADAPTERS.find(key => key === query.adapter) ?? 'baidu'
  return { service, tab, endpoint, adapter }
}

export function edgeQuery(query: LocationQuery, service?: EdgeService, tab: EdgeDetailTab = 'docs', endpoint?: string, adapter?: EdgeAdapter): LocationQueryRaw {
  const next: LocationQueryRaw = { ...query }
  for (const key of ['service', 'tab', 'endpoint', 'adapter']) delete next[key]
  if (!service) return next
  next.service = service.key
  if (tab !== 'docs') next.tab = tab
  if (endpoint && endpoint !== service.endpoints[0] && (service.endpoints as readonly string[]).includes(endpoint)) next.endpoint = endpoint
  if (service.key === 'translate' && adapter && adapter !== 'baidu') next.adapter = adapter
  return next
}
