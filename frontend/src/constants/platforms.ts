import type { AccountPlatform, GroupPlatform } from '@/types'

export interface PlatformOption<T extends string = string> {
  value: T
  label: string
}

/**
 * Concrete upstream platforms supported by accounts and request routing.
 * Keep platform selectors derived from this catalog so newly added providers
 * do not silently disappear from list filters.
 */
export const CONCRETE_PLATFORM_OPTIONS = [
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'antigravity', label: 'Antigravity' },
  { value: 'grok', label: 'Grok' },
  { value: 'kimi', label: 'Kimi' },
  { value: 'zhipu', label: 'Zhipu GLM' },
  { value: 'deepseek', label: 'DeepSeek' },
  { value: 'minimax', label: 'MiniMax' },
  { value: 'opencode_go', label: 'OpenCode' },
  { value: 'doubao', label: 'Doubao' },
  { value: 'traework', label: 'TRAE Work' },
  { value: 'workbuddy', label: 'WorkBuddy' },
  { value: 'zcode', label: 'ZCode' },
  { value: 'laya', label: 'Laya' },
  { value: 'jev', label: 'JEV' }
] as const satisfies readonly PlatformOption<AccountPlatform>[]

/** Platforms that can own a group. */
export const GROUP_PLATFORM_OPTIONS = [
  ...CONCRETE_PLATFORM_OPTIONS,
  { value: 'composite', label: 'Composite' }
] as const satisfies readonly PlatformOption<GroupPlatform>[]

/**
 * 跨平台调度兼容目标。
 * OpenAI 兼容来源可直接选择 OpenAI/Codex 分组；其他来源需显式启用混合调度。
 */
export const MIXED_SCHEDULING_TARGETS: Partial<Record<AccountPlatform, GroupPlatform[]>> = {
  antigravity: ['anthropic', 'gemini'],
  grok: ['openai'],
  kimi: ['openai'],
  zhipu: ['openai'],
  deepseek: ['openai'],
  minimax: ['openai'],
  opencode_go: ['openai'],
  doubao: ['openai'],
  traework: ['openai'],
  workbuddy: ['openai'],
  zcode: ['openai'],
  laya: ['openai'],
  jev: ['openai'],
}

/** 平台是否支持跨平台兼容分组。 */
export function supportsMixedScheduling(platform: string | undefined): boolean {
  return !!platform && platform in MIXED_SCHEDULING_TARGETS
}

/** 平台是否按 OpenAI 兼容协议直接提供 OpenAI/Codex 分组选择。 */
export function usesAutomaticMixedScheduling(platform: string | undefined): boolean {
  return mixedSchedulingTargets(platform).includes('openai')
}

/** 平台是否需要用户显式开启混合调度。 */
export function requiresMixedSchedulingOptIn(platform: string | undefined): boolean {
  return supportsMixedScheduling(platform) && !usesAutomaticMixedScheduling(platform)
}

/** 某平台可加入的目标分组平台列表。 */
export function mixedSchedulingTargets(platform: string | undefined): GroupPlatform[] {
  if (!platform) return []
  return MIXED_SCHEDULING_TARGETS[platform as AccountPlatform] ?? []
}

/** 来源平台是否兼容目标平台分组。 */
export function mixedSchedulingTargetsPlatform(source: string | undefined, target: string | undefined): boolean {
  if (!source || !target) return false
  return mixedSchedulingTargets(source).includes(target as GroupPlatform)
}
