import { apiClient } from '../client'

export type BuiltinLoginPlatform = 'traework' | 'workbuddy'
export interface BuiltinLoginSession {
  session_id: string
  auth_url?: string
  mode: 'callback' | 'poll'
  status: 'pending' | 'completed' | 'cancelled'
  expires_at: number
  account?: { uid: string; nickname?: string }
}

const path = (platform: BuiltinLoginPlatform) => `/admin/builtin-adapters/${platform}/login-sessions`

export async function startBuiltinLogin(platform: BuiltinLoginPlatform, signal?: AbortSignal) {
  const { data } = await apiClient.post<BuiltinLoginSession>(path(platform), {}, { signal, timeout: 50000 })
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
