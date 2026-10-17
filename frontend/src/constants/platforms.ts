import { reactive, watchSyncEffect } from 'vue'
import type { AccountPlatform, GroupPlatform } from '@/types'
import { listPlatforms } from './platformCatalog'

// type 别名（而非 interface）以便赋给 Select 组件的 Record<string, unknown>[] 选项类型。
export type PlatformOption<T extends string = string> = {
  value: T
  label: string
}

/**
 * Concrete upstream platforms supported by accounts and request routing.
 * Derived from the platform catalog (backend platform list, see platformCatalog.ts),
 * so newly registered providers show up in every selector without frontend
 * changes. Reactive so that tests replacing the catalog see the update.
 */
export const CONCRETE_PLATFORM_OPTIONS: PlatformOption<AccountPlatform>[] = reactive([])

/** Platforms that can own a group. */
export const GROUP_PLATFORM_OPTIONS: PlatformOption<GroupPlatform>[] = reactive([])

watchSyncEffect(() => {
  const concrete = listPlatforms().map(spec => ({ value: spec.id, label: spec.display_name }))
  CONCRETE_PLATFORM_OPTIONS.splice(0, CONCRETE_PLATFORM_OPTIONS.length, ...concrete)
  GROUP_PLATFORM_OPTIONS.splice(0, GROUP_PLATFORM_OPTIONS.length, ...concrete, {
    value: 'composite',
    label: 'Composite'
  })
})

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
  deepseek_web: ['openai'],
  madao: ['openai'],
  minimax: ['openai'],
  opencode_go: ['openai'],
  kilo: ['openai'],
  doubao: ['openai'],
  traework: ['openai'],
  workbuddy: ['openai'],
  vibex: ['openai'],
  zcode: ['openai'],
  qoder: ['openai'],
  cursor: ['openai'],
  windsurf: ['openai'],
  teleagent: ['openai'],
  systemone: ['openai'],
  // 历史账号读取兼容；新建账号统一使用 systemone。
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
