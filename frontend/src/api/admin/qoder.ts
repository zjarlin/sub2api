/**
 * Admin Qoder OAuth（设备流授权）API。
 */
import { apiClient } from '../client'

export interface QoderAuthURLResponse {
  auth_url: string
  session_id: string
}

export interface QoderTokenInfo {
  access_token: string
  refresh_token?: string
  expires_at?: number
  expires_in?: number
}

export interface QoderPollResponse {
  done: boolean
  token?: QoderTokenInfo
}

/** 生成 Qoder 设备授权链接。 */
export async function generateAuthURL(): Promise<QoderAuthURLResponse> {
  const { data } = await apiClient.post<QoderAuthURLResponse>('/admin/qoder/oauth/auth-url')
  return data
}

/** 轮询设备授权结果，done=false 表示用户尚未完成授权。 */
export async function pollToken(sessionId: string): Promise<QoderPollResponse> {
  const { data } = await apiClient.post<QoderPollResponse>('/admin/qoder/oauth/poll-token', {
    session_id: sessionId
  })
  return data
}

/** 使用 refresh_token 续期设备令牌。 */
export async function refreshToken(refreshToken: string): Promise<QoderTokenInfo> {
  const { data } = await apiClient.post<QoderTokenInfo>('/admin/qoder/oauth/refresh-token', {
    refresh_token: refreshToken
  })
  return data
}

const qoderAPI = { generateAuthURL, pollToken, refreshToken }
export default qoderAPI
