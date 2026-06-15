import { apiClient } from '../client'
import type { Account, AccountPlatform, AccountType, CreateAccountRequest, PaginatedResponse, UpdateAccountRequest } from '@/types'

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
  await apiClient.post(`/user/accounts/${id}/test`, payload || {})
}

export const userAccountsAPI = {
  list,
  getById,
  create,
  update,
  deleteAccount,
  testAccount,
}

export type { AccountPlatform, AccountType }

export default userAccountsAPI
