import type { ResolveUpstreamKeyRateResponse } from '@/api/admin/accounts'

export interface UpstreamKeyRateForm {
  baseUrl: string
  email: string
  password: string
  apiKey: string
  loginPath: string
  keysPath: string
}

export const DEFAULT_UPSTREAM_KEY_RATE_LOGIN_PATH = '/api/v1/auth/login'
export const DEFAULT_UPSTREAM_KEY_RATE_KEYS_PATH = '/api/v1/keys'

export function createUpstreamKeyRateForm(): UpstreamKeyRateForm {
  return {
    baseUrl: '',
    email: '',
    password: '',
    apiKey: '',
    loginPath: DEFAULT_UPSTREAM_KEY_RATE_LOGIN_PATH,
    keysPath: DEFAULT_UPSTREAM_KEY_RATE_KEYS_PATH
  }
}

export function resetUpstreamKeyRateSecretFields(form: UpstreamKeyRateForm) {
  form.email = ''
  form.password = ''
  form.apiKey = ''
  form.loginPath = DEFAULT_UPSTREAM_KEY_RATE_LOGIN_PATH
  form.keysPath = DEFAULT_UPSTREAM_KEY_RATE_KEYS_PATH
}

export function deriveUpstreamConsoleBaseUrl(baseUrl: string): string {
  const trimmed = baseUrl.trim()
  if (!trimmed) return ''

  try {
    const parsed = new URL(trimmed)
    parsed.search = ''
    parsed.hash = ''
    parsed.pathname = parsed.pathname
      .replace(/\/+$/, '')
      .replace(/\/api\/v1$/i, '')
      .replace(/\/v1$/i, '')
    return parsed.toString().replace(/\/+$/, '')
  } catch {
    return trimmed
      .replace(/[?#].*$/, '')
      .replace(/\/+$/, '')
      .replace(/\/api\/v1$/i, '')
      .replace(/\/v1$/i, '')
  }
}

export function getFirstUpstreamAPIKey(value: string): string {
  return value
    .split(/[\s,;]+/)
    .map(item => item.trim())
    .find(Boolean) || ''
}

export function buildUpstreamRateSuccessParams(result: ResolveUpstreamKeyRateResponse): Record<string, string | number> {
  return {
    rate: result.rate_multiplier,
    group: result.group_name || (result.group_id ? `#${result.group_id}` : '-'),
    key: result.key_name || (result.key_id ? `#${result.key_id}` : '-')
  }
}
