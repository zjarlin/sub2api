import { describe, expect, it } from 'vitest'
import {
  MIXED_SCHEDULING_TARGETS,
  mixedSchedulingTargets,
  mixedSchedulingTargetsPlatform,
  requiresMixedSchedulingOptIn,
  supportsMixedScheduling,
  usesAutomaticMixedScheduling,
} from '../platforms'

describe('mixed scheduling mapping', () => {
  const automaticOpenAIPlatforms = [
    'grok',
    'kimi',
    'zhipu',
    'deepseek',
    'minimax',
    'opencode_go',
    'doubao',
    'traework',
    'workbuddy',
    'zcode',
  ] as const

  it('marks compatible platforms as supported', () => {
    expect(supportsMixedScheduling('antigravity')).toBe(true)
    for (const platform of automaticOpenAIPlatforms) {
      expect(supportsMixedScheduling(platform)).toBe(true)
    }
    expect(supportsMixedScheduling('openai')).toBe(false)
    expect(supportsMixedScheduling('anthropic')).toBe(false)
    expect(supportsMixedScheduling(undefined)).toBe(false)
  })

  it('returns the compatible target groups', () => {
    for (const platform of automaticOpenAIPlatforms) {
      expect(mixedSchedulingTargets(platform)).toEqual(['openai'])
    }
    expect(mixedSchedulingTargets('antigravity')).toEqual(['anthropic', 'gemini'])
    expect(mixedSchedulingTargets('openai')).toEqual([])
  })

  it('distinguishes automatic compatibility from explicit opt-in', () => {
    for (const platform of automaticOpenAIPlatforms) {
      expect(usesAutomaticMixedScheduling(platform)).toBe(true)
      expect(requiresMixedSchedulingOptIn(platform)).toBe(false)
    }
    expect(usesAutomaticMixedScheduling('antigravity')).toBe(false)
    expect(requiresMixedSchedulingOptIn('antigravity')).toBe(true)
    expect(usesAutomaticMixedScheduling('openai')).toBe(false)
    expect(requiresMixedSchedulingOptIn('openai')).toBe(false)
  })

  it('reports whether a source platform can join a target group', () => {
    expect(mixedSchedulingTargetsPlatform('traework', 'openai')).toBe(true)
    expect(mixedSchedulingTargetsPlatform('workbuddy', 'openai')).toBe(true)
    expect(mixedSchedulingTargetsPlatform('traework', 'anthropic')).toBe(false)
    expect(mixedSchedulingTargetsPlatform('antigravity', 'gemini')).toBe(true)
    expect(mixedSchedulingTargetsPlatform('antigravity', 'openai')).toBe(false)
  })

  it('exposes the same target list as the constant', () => {
    for (const platform of automaticOpenAIPlatforms) {
      expect(MIXED_SCHEDULING_TARGETS[platform]).toEqual(['openai'])
    }
  })
})
