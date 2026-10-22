import type {
  Account,
  CheckMixedChannelRequest,
  CheckMixedChannelResponse,
  CodexSessionImportRequest,
  CodexSessionImportResult,
  CreateAccountRequest,
  GrokMediaEligibilityMode,
  GrokMediaEligibilityState,
  OpenAICodexPATCreateRequest,
  UpdateAccountRequest,
} from '@/types'

/**
 * 账号弹窗实际使用的最小 API 契约。
 *
 * 管理员面板传入 `adminAPI.accounts`，普通用户「我的账号」页面传入
 * `userAccountsAPI`。两者共享账号 CRUD 与混用渠道检查；仅在管理端可用的
 * 高级能力（Codex 会话导入、PAT、OAuth 兑换、Grok 媒体资格等）声明为可选，
 * 调用方已用 `typeof … === 'function'` 兜底。
 *
 * 注意：组件的 prop 名必须是 `accountApi`，父级用 kebab `:account-api` 传入。
 * 若写成 `accountAPI`，Vue 会把 `account-api` 驼峰化成 `accountApi`，永远绑不上，
 * 从而静默回退到管理员 API（普通用户将收到 403）。
 */
export interface UpstreamModelSyncOutcome {
  models: string[]
  metadata?: Record<string, unknown>
  warnings?: { code: string; message: string }[]
}

export interface UpstreamModelSyncPreviewParams {
  platform: string
  type: string
  base_url?: string
  api_key: string
  model_mapping?: Record<string, string>
}

/**
 * `ModelWhitelistSelector` 实际消费的最小同步契约。父级以 kebab `:account-api`
 * 传入账号弹窗/编辑器的 API 对象（管理员 `adminAPI.accounts` 或用户
 * `userAccountsAPI`）；不传时组件回退到管理员 API 以保持管理端既有行为。
 */
export interface UpstreamCatalogModel {
  id: string
  display_name?: string
  /** 上游目录是否对该账号套餐启用；缺省表示未知（旧快照）。 */
  enable?: boolean
  free?: boolean
  default?: boolean
}

export interface UpstreamModelSyncApi {
  syncUpstreamModels?(id: number): Promise<UpstreamModelSyncOutcome>
  syncUpstreamModelsPreview?(params: UpstreamModelSyncPreviewParams): Promise<UpstreamModelSyncOutcome>
  /** 账号的真实模型目录（与测试弹窗同一接口）；未声明时调用方回退平台预设。 */
  getAvailableModels?(id: number): Promise<UpstreamCatalogModel[]>
}

export interface AccountModalApi {
  create(payload: CreateAccountRequest): Promise<Account>
  update(id: number, payload: UpdateAccountRequest): Promise<Account>
  checkMixedChannelRisk(payload: CheckMixedChannelRequest): Promise<CheckMixedChannelResponse>
  syncUpstreamModels?(id: number): Promise<UpstreamModelSyncOutcome>
  syncUpstreamModelsPreview?(params: UpstreamModelSyncPreviewParams): Promise<UpstreamModelSyncOutcome>
  probeUpstreamBilling?(id: number): Promise<unknown>
  importCodexSession?(payload: CodexSessionImportRequest): Promise<CodexSessionImportResult>
  createOpenAICodexPAT?(payload: OpenAICodexPATCreateRequest): Promise<Account>
  exchangeCode?(
    endpoint: string,
    data: { session_id: string; code: string; state?: string; proxy_id?: number },
  ): Promise<Record<string, unknown>>
  getGrokMediaEligibility?(id: number): Promise<GrokMediaEligibilityState>
  updateGrokMediaEligibility?(id: number, mode: GrokMediaEligibilityMode): Promise<GrokMediaEligibilityState>
}
