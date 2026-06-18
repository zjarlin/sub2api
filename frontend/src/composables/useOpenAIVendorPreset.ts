export type OpenAIVendorPresetId =
  | 'openai'
  | 'deepseek'
  | 'openrouter'
  | 'opencode'
  | 'opencode-go'
  | 'doubao-web'
  | 'gemini'
  | 'mimo'
  | 'ollama'
  | 'trae'
  | 'openai-local-proxy'
  | 'custom'

export interface OpenAIVendorPreset {
  id: OpenAIVendorPresetId
  labelKey: string
  baseUrl: string
  authHeader: string
  authScheme: string
  apiKeyPlaceholder: string
  modelPlatforms: string[]
  presetPlatform: string
  baseUrlHintKey: string
  apiKeyHintKey: string
}

const OPENAI_VENDOR_PRESETS: Record<OpenAIVendorPresetId, OpenAIVendorPreset> = {
  openai: {
    id: 'openai',
    labelKey: 'admin.accounts.openai.vendorOptions.openai',
    baseUrl: 'https://api.openai.com',
    authHeader: 'authorization',
    authScheme: 'bearer',
    apiKeyPlaceholder: 'sk-proj-...',
    modelPlatforms: ['openai'],
    presetPlatform: 'openai',
    baseUrlHintKey: 'admin.accounts.openai.baseUrlHint',
    apiKeyHintKey: 'admin.accounts.openai.apiKeyHint'
  },
  deepseek: {
    id: 'deepseek',
    labelKey: 'admin.accounts.openai.vendorOptions.deepseek',
    baseUrl: 'https://api.deepseek.com',
    authHeader: 'authorization',
    authScheme: 'bearer',
    apiKeyPlaceholder: 'sk-...',
    modelPlatforms: ['deepseek'],
    presetPlatform: 'deepseek',
    baseUrlHintKey: 'admin.accounts.openai.deepseekBaseUrlHint',
    apiKeyHintKey: 'admin.accounts.openai.deepseekApiKeyHint'
  },
  openrouter: {
    id: 'openrouter',
    labelKey: 'admin.accounts.openai.vendorOptions.openrouter',
    baseUrl: 'https://openrouter.ai/api/v1',
    authHeader: 'authorization',
    authScheme: 'bearer',
    apiKeyPlaceholder: 'sk-or-v1-...',
    modelPlatforms: ['openrouter'],
    presetPlatform: 'openrouter',
    baseUrlHintKey: 'admin.accounts.openai.openrouterBaseUrlHint',
    apiKeyHintKey: 'admin.accounts.openai.openrouterApiKeyHint'
  },
  opencode: {
    id: 'opencode',
    labelKey: 'admin.accounts.openai.vendorOptions.opencode',
    baseUrl: 'https://opencode.ai/zen/v1',
    authHeader: 'authorization',
    authScheme: 'bearer',
    apiKeyPlaceholder: 'sk-...',
    modelPlatforms: ['opencode'],
    presetPlatform: 'opencode',
    baseUrlHintKey: 'admin.accounts.openai.opencodeBaseUrlHint',
    apiKeyHintKey: 'admin.accounts.openai.opencodeApiKeyHint'
  },
  'opencode-go': {
    id: 'opencode-go',
    labelKey: 'admin.accounts.openai.vendorOptions.opencodeGo',
    baseUrl: 'https://opencode.ai/zen/go/v1',
    authHeader: 'authorization',
    authScheme: 'bearer',
    apiKeyPlaceholder: 'sk-...',
    modelPlatforms: ['opencode-go'],
    presetPlatform: 'opencode-go',
    baseUrlHintKey: 'admin.accounts.openai.opencodeGoBaseUrlHint',
    apiKeyHintKey: 'admin.accounts.openai.opencodeGoApiKeyHint'
  },
  'doubao-web': {
    id: 'doubao-web',
    labelKey: 'admin.accounts.openai.vendorOptions.doubaoWeb',
    baseUrl: 'https://www.doubao.com',
    authHeader: 'authorization',
    authScheme: 'bearer',
    apiKeyPlaceholder: 'sessionid',
    modelPlatforms: ['doubao-web'],
    presetPlatform: 'doubao-web',
    baseUrlHintKey: 'admin.accounts.openai.doubaoWebBaseUrlHint',
    apiKeyHintKey: 'admin.accounts.openai.doubaoWebApiKeyHint'
  },
  gemini: {
    id: 'gemini',
    labelKey: 'admin.accounts.openai.vendorOptions.gemini',
    baseUrl: 'https://generativelanguage.googleapis.com/v1beta/openai/',
    authHeader: 'authorization',
    authScheme: 'bearer',
    apiKeyPlaceholder: 'AIza...',
    modelPlatforms: ['gemini'],
    presetPlatform: 'gemini',
    baseUrlHintKey: 'admin.accounts.openai.geminiBaseUrlHint',
    apiKeyHintKey: 'admin.accounts.openai.geminiApiKeyHint'
  },
  mimo: {
    id: 'mimo',
    labelKey: 'admin.accounts.openai.vendorOptions.mimo',
    baseUrl: 'https://token-plan-cn.xiaomimimo.com/v1',
    authHeader: 'api-key',
    authScheme: 'raw',
    apiKeyPlaceholder: 'mimo-live-key',
    modelPlatforms: ['mimo'],
    presetPlatform: 'mimo',
    baseUrlHintKey: 'admin.accounts.openai.mimoBaseUrlHint',
    apiKeyHintKey: 'admin.accounts.openai.mimoApiKeyHint'
  },
  ollama: {
    id: 'ollama',
    labelKey: 'admin.accounts.openai.vendorOptions.ollama',
    baseUrl: 'http://127.0.0.1:11434/v1',
    authHeader: 'authorization',
    authScheme: 'bearer',
    apiKeyPlaceholder: 'optional-local-token',
    modelPlatforms: ['ollama'],
    presetPlatform: 'ollama',
    baseUrlHintKey: 'admin.accounts.openai.ollamaBaseUrlHint',
    apiKeyHintKey: 'admin.accounts.openai.ollamaApiKeyHint'
  },
  trae: {
    id: 'trae',
    labelKey: 'admin.accounts.openai.vendorOptions.trae',
    baseUrl: 'http://127.0.0.1:17080/v1',
    authHeader: 'authorization',
    authScheme: 'bearer',
    apiKeyPlaceholder: 'trae-local-key',
    modelPlatforms: ['trae'],
    presetPlatform: 'trae',
    baseUrlHintKey: 'admin.accounts.openai.traeBaseUrlHint',
    apiKeyHintKey: 'admin.accounts.openai.traeApiKeyHint'
  },
  'openai-local-proxy': {
    id: 'openai-local-proxy',
    labelKey: 'admin.accounts.openai.vendorOptions.openaiLocalProxy',
    baseUrl: 'http://127.0.0.1:18081/v1',
    authHeader: 'authorization',
    authScheme: 'bearer',
    apiKeyPlaceholder: 'optional-proxy-auth-token',
    modelPlatforms: ['openai-local-proxy'],
    presetPlatform: 'openai-local-proxy',
    baseUrlHintKey: 'admin.accounts.openai.openaiLocalProxyBaseUrlHint',
    apiKeyHintKey: 'admin.accounts.openai.openaiLocalProxyApiKeyHint'
  },
  custom: {
    id: 'custom',
    labelKey: 'admin.accounts.openai.vendorOptions.custom',
    baseUrl: 'https://your-compatible-endpoint.example.com/v1',
    authHeader: 'authorization',
    authScheme: 'bearer',
    apiKeyPlaceholder: 'your-api-key',
    modelPlatforms: ['openai'],
    presetPlatform: 'openai',
    baseUrlHintKey: 'admin.accounts.openai.customBaseUrlHint',
    apiKeyHintKey: 'admin.accounts.openai.customApiKeyHint'
  }
}

const PRESET_ORDER: OpenAIVendorPresetId[] = [
  'openai',
  'deepseek',
  'openrouter',
  'opencode',
  'opencode-go',
  'doubao-web',
  'gemini',
  'mimo',
  'ollama',
  'trae',
  'openai-local-proxy',
  'custom'
]

function normalizeBaseUrl(value: string | null | undefined) {
  return (value || '').trim().replace(/\/+$/, '').toLowerCase()
}

function normalizeHeader(value: string | null | undefined) {
  return (value || '').trim().toLowerCase()
}

function normalizeScheme(value: string | null | undefined) {
  return (value || '').trim().toLowerCase()
}

export function listOpenAIVendorPresets(): OpenAIVendorPreset[] {
  return PRESET_ORDER.map(id => OPENAI_VENDOR_PRESETS[id])
}

export function getOpenAIVendorPreset(id?: string | null): OpenAIVendorPreset {
  if (!id) {
    return OPENAI_VENDOR_PRESETS.openai
  }
  return OPENAI_VENDOR_PRESETS[id as OpenAIVendorPresetId] || OPENAI_VENDOR_PRESETS.custom
}

export function getOpenAIVendorModelPlatforms(id?: string | null): string[] {
  return [...getOpenAIVendorPreset(id).modelPlatforms]
}

export function getOpenAIVendorPresetPlatform(id?: string | null): string {
  return getOpenAIVendorPreset(id).presetPlatform
}

export function inferOpenAIVendorPreset(input: {
  vendor?: string | null
  baseUrl?: string | null
  authHeader?: string | null
  authScheme?: string | null
}): OpenAIVendorPresetId {
  const explicitVendor = input.vendor?.trim().toLowerCase() as OpenAIVendorPresetId | undefined
  if (explicitVendor && OPENAI_VENDOR_PRESETS[explicitVendor]) {
    return explicitVendor
  }

  const baseUrl = normalizeBaseUrl(input.baseUrl)
  const authHeader = normalizeHeader(input.authHeader)
  const authScheme = normalizeScheme(input.authScheme)

  if (baseUrl.includes('generativelanguage.googleapis.com')) {
    return 'gemini'
  }
  if (baseUrl.includes('deepseek.com')) {
    return 'deepseek'
  }
  if (baseUrl.includes('openrouter.ai')) {
    return 'openrouter'
  }
  if (baseUrl.includes('opencode.ai/zen/go')) {
    return 'opencode-go'
  }
  if (baseUrl.includes('host.docker.internal:4096') || baseUrl.includes('127.0.0.1:4096') || baseUrl.includes('localhost:4096')) {
    return 'opencode-go'
  }
  if (baseUrl.includes('opencode.ai')) {
    return 'opencode'
  }
  if (baseUrl.includes('doubao.com')) {
    return 'doubao-web'
  }
  if (baseUrl.includes('xiaomimimo.com')) {
    return 'mimo'
  }
  if (baseUrl.includes(':11434') || baseUrl.includes('ollama')) {
    return 'ollama'
  }
  if (baseUrl.includes(':17080') || baseUrl.includes('/trae')) {
    return 'trae'
  }
  if (baseUrl.includes(':18081') || baseUrl.includes('openai-local-proxy')) {
    return 'openai-local-proxy'
  }
  if (baseUrl.includes('api.openai.com')) {
    return 'openai'
  }
  if (authHeader === 'authorization' && authScheme === 'bearer') {
    return 'custom'
  }
  return 'custom'
}
