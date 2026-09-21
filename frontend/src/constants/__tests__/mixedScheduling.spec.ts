import { describe, expect, it } from 'vitest'
import {
  MIXED_SCHEDULING_TARGETS,
  mixedSchedulingTargets,
  mixedSchedulingTargetsPlatform,
  supportsMixedScheduling,
} from '../platforms'

// 前端映射表需与后端 MixedSchedulingCompatibleTargets 保持一致。
describe('mixed scheduling mapping', () => {
  it('marks only capable platforms as supported', () => {
    expect(supportsMixedScheduling('antigravity')).toBe(true)
    expect(supportsMixedScheduling('traework')).toBe(true)
    expect(supportsMixedScheduling('workbuddy')).toBe(true)
    expect(supportsMixedScheduling('openai')).toBe(false)
    expect(supportsMixedScheduling('anthropic')).toBe(false)
    expect(supportsMixedScheduling(undefined)).toBe(false)
  })

  it('returns the compatible target groups', () => {
    expect(mixedSchedulingTargets('traework')).toEqual(['openai'])
    expect(mixedSchedulingTargets('workbuddy')).toEqual(['openai'])
    expect(mixedSchedulingTargets('antigravity')).toEqual(['anthropic', 'gemini'])
    expect(mixedSchedulingTargets('openai')).toEqual([])
  })

  it('reports whether a source platform can join a target group', () => {
    expect(mixedSchedulingTargetsPlatform('traework', 'openai')).toBe(true)
    expect(mixedSchedulingTargetsPlatform('workbuddy', 'openai')).toBe(true)
    expect(mixedSchedulingTargetsPlatform('traework', 'anthropic')).toBe(false)
    expect(mixedSchedulingTargetsPlatform('antigravity', 'gemini')).toBe(true)
    expect(mixedSchedulingTargetsPlatform('antigravity', 'openai')).toBe(false)
  })

  it('exposes the same target list as the constant', () => {
    expect(MIXED_SCHEDULING_TARGETS.traework).toEqual(['openai'])
    expect(MIXED_SCHEDULING_TARGETS.workbuddy).toEqual(['openai'])
  })
})
