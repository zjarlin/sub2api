import { runModelTest } from '@/api/accountTest'
import { apiClient } from '../client'
import type {
  Account,
  AccountListItem,
  AccountPlatform,
  AccountType,
  CheckMixedChannelRequest,
  CheckMixedChannelResponse,
  CreateAccountRequest,
  PaginatedResponse,
  UpdateAccountRequest,
} from '@/types'

export interface UserSyncUpstreamModelsResult {
  models: string[]
  metadata?: Record<string, unknown>
  warnings?: { code: string; message: string }[]
}

export interface UserAccountFilters {
  platform?: string
  type?: string
  status?: string
  search?: string
  sort_by?: string
  sort_order?: 'asc' | 'desc'
}

export async function list(
  page = 1,
  pageSize = 20,
  filters?: UserAccountFilters,
): Promise<PaginatedResponse<Account>> {
  const { data } = await apiClient.get<PaginatedResponse<Account>>('/user/accounts', {
    params: {
      page,
      page_size: pageSize,
      ...filters,
    },
  })
  return data
}

export async function checkMixedChannelRisk(
  payload: CheckMixedChannelRequest,
): Promise<CheckMixedChannelResponse> {
  const { data } = await apiClient.post<CheckMixedChannelResponse>('/user/accounts/check-mixed-channel', payload)
  return data
}

export async function syncUpstreamModels(id: number): Promise<UserSyncUpstreamModelsResult> {
  const { data } = await apiClient.post<UserSyncUpstreamModelsResult>(`/user/accounts/${id}/models/sync-upstream`)
  return data
}

export async function getById(id: number): Promise<Account> {
  const { data } = await apiClient.get<Account>(`/user/accounts/${id}`)
  return data
}

export async function create(payload: CreateAccountRequest): Promise<Account> {
  const { data } = await apiClient.post<Account>('/user/accounts', payload)
  return data
}

export async function update(id: number, payload: UpdateAccountRequest): Promise<Account> {
  const { data } = await apiClient.put<Account>(`/user/accounts/${id}`, payload)
  return data
}

export async function deleteAccount(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/user/accounts/${id}`)
  return data
}

export async function testAccount(
  id: number,
  payload?: { model_id?: string; prompt?: string; mode?: string },
): Promise<void> {
  const result = await runModelTest(`/user/accounts/${id}/test`, payload || {}, new AbortController().signal)
  if (!result.success) {
    throw new Error(result.error || 'Account test failed')
  }
}

export const userAccountsAPI = {
  list,
  checkMixedChannelRisk,
  syncUpstreamModels,
  getById,
  create,
  update,
  deleteAccount,
  testAccount,
}

export type { AccountListItem }

export type { AccountPlatform, AccountType }

export default userAccountsAPI
