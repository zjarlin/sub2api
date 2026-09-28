import { apiClient } from '../client'

export type BuiltinLoginPlatform = 'traework' | 'workbuddy' | 'vibex' | 'zcode' | 'deepseek_web'
export interface BuiltinLoginSession {
  session_id: string
  auth_url?: string
  mode: 'callback' | 'poll'
  status: 'pending' | 'completed' | 'cancelled'
  expires_at: number
  account?: { uid: string; nickname?: string }
}

const path = (platform: BuiltinLoginPlatform) => `/admin/builtin-adapters/${platform}/login-sessions`

export interface ZcodeLoginOptions {
  plan: 'coding-plan' | 'start-plan'
  provider: 'bigmodel' | 'zai'
}

export async function startBuiltinLogin(platform: BuiltinLoginPlatform, signal?: AbortSignal, options?: ZcodeLoginOptions) {
  const { data } = await apiClient.post<BuiltinLoginSession>(path(platform), platform === 'zcode' ? options ?? {} : {}, { signal, timeout: 50000 })
  return data
}

export async function completeBuiltinLogin(platform: BuiltinLoginPlatform, session: BuiltinLoginSession, callback: string, signal?: AbortSignal) {
  const action = session.mode === 'callback' ? 'callback' : 'poll'
  const { data } = await apiClient.post<BuiltinLoginSession>(`${path(platform)}/${session.session_id}/${action}`, { callback_url: callback }, { signal, timeout: 50000 })
  return data
}

export async function cancelBuiltinLogin(platform: BuiltinLoginPlatform, id: string) {
  await apiClient.delete(`${path(platform)}/${id}`)
}

export interface VibexUsage {
  wallet: { wallet_balance: number | null; min_balance_yuan: number | null }
  lite: { tokens: number | null; daily_token_limit: number | null; cost_yuan: number | null; daily_cost_limit_yuan: number | null }
}

export async function getVibexUsage(accountId: number, signal?: AbortSignal) {
  const { data } = await apiClient.get<VibexUsage>(`/admin/accounts/${accountId}/vibex-usage`, { signal, timeout: 50000 })
  return data
}
