import { afterEach, describe, expect, it } from 'vitest'
import { computed } from 'vue'
import { CONCRETE_PLATFORM_OPTIONS, GROUP_PLATFORM_OPTIONS } from '@/constants/platforms'
import {
  BUILTIN_PLATFORM_CATALOG,
  compositePrecedencePlatformIds,
  listPlatformIds,
  platformDisplayName,
  resetPlatformCatalog,
  setPlatformCatalog
} from '@/constants/platformCatalog'
import { platformLabel } from '@/utils/platformColors'
import { normalizePlatformQuotasMap, sanitizePlatformQuotasMap } from '@/api/admin/settings'
import { platformQuotaPlatforms } from '@/api/admin/users'

// 改为读取平台清单之前的平台字面量：上游平台 + 本仓库（fork）登记的适配器平台，
// 顺序与后端 domain/platforms.go 的展示顺序一致。选项、展示名与额度平台列表现在
// 都从清单派生，这里保留字面量作为「清单未漏登记上游/ fork 平台」的等价基准。
const concretePlatforms = [
  'anthropic',
  'openai',
  'gemini',
  'antigravity',
  'grok',
  'kimi',
  'zhipu',
  'deepseek',
  'deepseek_web',
  'madao',
  'arena',
  'minimax',
  'cursor',
  'windsurf',
  'teleagent',
  'opencode_go',
  'kilo',
  'typesafe',
  'command_code',
  'cline',
  'doubao',
  'traework',
  'workbuddy',
  'vibex',
  'zcode',
  'qoder',
  'systemone',
  'model3d',
  'v0',
  'laya',
  'jev'
]

// 组合分组回退顺序：固定前缀 anthropic、gemini、openai，其余按展示顺序。
const compositePrecedencePlatforms = [
  'anthropic', 'gemini', 'openai',
  'antigravity', 'grok', 'kimi', 'zhipu', 'deepseek', 'deepseek_web', 'madao', 'arena',
  'minimax', 'cursor', 'windsurf', 'teleagent', 'opencode_go', 'kilo', 'typesafe',
  'command_code', 'cline', 'doubao', 'traework', 'workbuddy', 'vibex', 'zcode', 'qoder',
  'systemone', 'model3d', 'v0', 'laya', 'jev'
]

// 展示名同样来自清单（平台清单的 display_name 即后端 domain/platforms.go 的 DisplayName）。
const concretePlatformLabels = [
  'Anthropic', 'OpenAI', 'Gemini', 'Antigravity', 'Grok',
  'Kimi', 'Zhipu GLM', 'DeepSeek', 'DeepSeek Web', 'CodeArts (Madao)', 'Arena',
  'MiniMax', 'Cursor', 'Windsurf', 'TeleAgent', 'OpenCode', 'Kilo',
  'TypeSafe / Jev', 'Command Code', 'Cline',
  'Doubao', 'TRAE Work', 'WorkBuddy', 'VibeX', 'ZCode', 'Qoder',
  'System One', '3D (Meshy / Tripo / Hi3D)', 'v0 (Vercel)', 'Laya', 'Jev'
]

describe('platform option catalogs', () => {
  it('exposes every concrete account platform', () => {
    expect(CONCRETE_PLATFORM_OPTIONS.map((option) => option.value)).toEqual(concretePlatforms)
  })

  it('adds composite for group-backed filters', () => {
    expect(GROUP_PLATFORM_OPTIONS.map((option) => option.value)).toEqual([
      ...concretePlatforms,
      'composite'
    ])
  })
})

describe('platform catalog with a newly registered platform', () => {
  const withNewProvider = {
    platforms: [
      ...BUILTIN_PLATFORM_CATALOG.platforms,
      { id: 'acme_router', display_name: 'Acme Router', gateway: 'openai', cn_provider: false }
    ],
    composite_precedence: [...BUILTIN_PLATFORM_CATALOG.composite_precedence, 'acme_router']
  }

  afterEach(() => {
    resetPlatformCatalog()
  })

  it('keeps the built-in catalog equal to the legacy literals', () => {
    expect(listPlatformIds()).toEqual(concretePlatforms)
    expect(compositePrecedencePlatformIds()).toEqual(compositePrecedencePlatforms)
    expect(CONCRETE_PLATFORM_OPTIONS.map((option) => option.label)).toEqual(concretePlatformLabels)
    expect(platformLabel('zhipu')).toBe('Zhipu GLM')
    expect(platformLabel('composite')).toBe('Composite')
    expect(platformLabel('')).toBe('API')
    // fork 的决策平台已登记进后端清单，展示名同样来自清单。
    expect(platformLabel('systemone')).toBe('System One')
    expect(platformDisplayName('model3d')).toBe('3D (Meshy / Tripo / Hi3D)')
  })

  it('adds newly registered platforms to options, labels and quota lists reactively', () => {
    const optionValues = computed(() => CONCRETE_PLATFORM_OPTIONS.map((option) => option.value))
    expect(optionValues.value).not.toContain('acme_router')

    setPlatformCatalog(withNewProvider)

    expect(optionValues.value).toEqual([...concretePlatforms, 'acme_router'])
    expect(GROUP_PLATFORM_OPTIONS.map((option) => option.value)).toEqual([
      ...concretePlatforms,
      'acme_router',
      'composite'
    ])
    expect(platformLabel('acme_router')).toBe('Acme Router')
    expect(platformDisplayName('acme_router')).toBe('Acme Router')
    expect(platformLabel('unregistered')).toBe('unregistered')
    expect(platformQuotaPlatforms()).toContain('acme_router')
    expect(Object.keys(normalizePlatformQuotasMap())).toEqual([...concretePlatforms, 'acme_router'])
    expect(sanitizePlatformQuotasMap({ acme_router: { daily: 5, weekly: -1, monthly: null } }).acme_router).toEqual({
      daily: 5,
      weekly: null,
      monthly: null
    })
  })

  it('restores the built-in catalog on reset', () => {
    setPlatformCatalog(withNewProvider)
    resetPlatformCatalog()
    expect(CONCRETE_PLATFORM_OPTIONS.map((option) => option.value)).toEqual(concretePlatforms)
  })
})
