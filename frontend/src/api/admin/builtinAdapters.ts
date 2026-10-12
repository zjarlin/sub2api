import { apiClient } from '../client'

export type BuiltinLoginPlatform = 'traework' | 'workbuddy' | 'vibex' | 'zcode' | 'deepseek_web' | 'madao' | 'arena' | 'cursor' | 'windsurf'
export interface BuiltinLoginSession {
  session_id: string
  auth_url?: string
  mode: 'callback' | 'poll'
  status: 'pending' | 'completed' | 'cancelled'
  expires_at: number
  /** 仅浏览器授权平台（Cursor / Windsurf）返回：授权完成后换得的账号凭据。 */
  api_key?: string
  account?: { uid: string; nickname?: string; model_id?: string; auto_relogin?: boolean }
}

const path = (platform: BuiltinLoginPlatform) => `/admin/builtin-adapters/${platform}/login-sessions`

export interface ZcodeLoginOptions {
  plan: 'coding-plan' | 'start-plan'
  provider: 'bigmodel' | 'zai'
}

export interface PasswordLoginOptions {
  email: string
  password: string
}

export interface DeepseekLoginOptions extends PasswordLoginOptions {
  auto_relogin: boolean
}

export type ArenaLoginOptions = PasswordLoginOptions

export async function startBuiltinLogin(platform: BuiltinLoginPlatform, signal?: AbortSignal, options?: ZcodeLoginOptions | PasswordLoginOptions | DeepseekLoginOptions) {
  const hasOptions = platform === 'zcode' || platform === 'arena' || platform === 'deepseek_web'
  const { data } = await apiClient.post<BuiltinLoginSession>(path(platform), hasOptions ? options ?? {} : {}, { signal, timeout: 50000 })
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

export interface BuiltinLoginInputEvent {
  type: 'click' | 'move' | 'wheel' | 'text' | 'key'
  x?: number
  y?: number
  delta_x?: number
  delta_y?: number
  text?: string
  key?: string
}

/** 把页面交互转发给适配器的隔离浏览器（截图式登录必需）。 */
export async function sendBuiltinLoginInput(platform: BuiltinLoginPlatform, id: string, event: BuiltinLoginInputEvent, signal?: AbortSignal) {
  await apiClient.post(`${path(platform)}/${id}/input`, event, { signal, timeout: 20000 })
}

export async function getBuiltinLoginView(platform: BuiltinLoginPlatform, id: string, signal?: AbortSignal) {
  const { data } = await apiClient.get<Blob>(`${path(platform)}/${id}/view`, {
    signal,
    timeout: 20000,
    responseType: 'blob'
  })
  return data
}

export interface DoubaoSessionProfiles {
  profiles: string[]
}

/** 返回已上传的豆包会话别名，不返回 Cookie 或快照内容。 */
export async function listDoubaoSessionProfiles(signal?: AbortSignal) {
  const { data } = await apiClient.get<DoubaoSessionProfiles>('/admin/builtin-adapters/doubao/profiles', { signal })
  return data
}

/** 将 Mac 导出的 session.json 通过管理端上传到内置适配器。 */
export async function uploadDoubaoSessionProfile(profile: string, session: File, signal?: AbortSignal) {
  const form = new FormData()
  form.append('profile', profile)
  form.append('session', session, session.name || 'session.json')
  const { data } = await apiClient.post<{ name: string }>('/admin/builtin-adapters/doubao/profiles', form, {
    headers: { 'Content-Type': 'multipart/form-data' },
    signal,
    timeout: 60000,
  })
  return data
}

/** 删除一个已上传的豆包会话快照。 */
export async function deleteDoubaoSessionProfile(profile: string, signal?: AbortSignal) {
  await apiClient.delete(`/admin/builtin-adapters/doubao/profiles/${encodeURIComponent(profile)}`, { signal })
}

export interface VibexUsage {
  wallet: { wallet_balance: number | null; min_balance_yuan: number | null }
  lite: { tokens: number | null; daily_token_limit: number | null; cost_yuan: number | null; daily_cost_limit_yuan: number | null }
}

export async function getVibexUsage(accountId: number, signal?: AbortSignal) {
  const { data } = await apiClient.get<VibexUsage>(`/admin/accounts/${accountId}/vibex-usage`, { signal, timeout: 50000 })
  return data
}
