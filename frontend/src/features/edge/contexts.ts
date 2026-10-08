export interface EdgeAccountContext {
  id: number
  name: string
  base_url: string
  api_key_set: boolean
  api_key_editable: boolean
  status: string
  schedulable: boolean
  models: string[]
  group_ids: number[] | null
}

export interface EdgeServiceContext {
  adapter: string
  source: 'deployment' | 'account'
  enabled: boolean
  configured: boolean
  error?: string
  fields: Array<{ key: string; value: string | number | boolean }>
  accounts: EdgeAccountContext[]
}

export type EdgeContexts = Partial<Record<string, EdgeServiceContext>>
