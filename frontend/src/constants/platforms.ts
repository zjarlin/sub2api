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
  { value: 'workbuddy', label: 'WorkBuddy' }
] as const satisfies readonly PlatformOption<AccountPlatform>[]

/** Platforms that can own a group. */
export const GROUP_PLATFORM_OPTIONS = [
  ...CONCRETE_PLATFORM_OPTIONS,
  { value: 'composite', label: 'Composite' }
] as const satisfies readonly PlatformOption<GroupPlatform>[]

/**
 * 混合调度兼容目标：来源平台启用 extra.mixed_scheduling 后，可加入这些目标平台分组。
 * 与后端 MixedSchedulingCompatibleTargets 保持一致；新增平台只需扩展此表。
 */
export const MIXED_SCHEDULING_TARGETS: Partial<Record<AccountPlatform, GroupPlatform[]>> = {
  antigravity: ['anthropic', 'gemini'],
  traework: ['openai'],
  workbuddy: ['openai'],
}

/** 平台是否支持开启混合调度（可加入其他分组）。 */
export function supportsMixedScheduling(platform: string | undefined): boolean {
  return !!platform && platform in MIXED_SCHEDULING_TARGETS
}

/** 某平台可加入的目标分组平台列表。 */
export function mixedSchedulingTargets(platform: string | undefined): GroupPlatform[] {
  if (!platform) return []
  return MIXED_SCHEDULING_TARGETS[platform as AccountPlatform] ?? []
}

/** 来源平台启用混合调度后是否可加入目标平台分组。 */
export function mixedSchedulingTargetsPlatform(source: string | undefined, target: string | undefined): boolean {
  if (!source || !target) return false
  return mixedSchedulingTargets(source).includes(target as GroupPlatform)
}
