import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import GroupSelector from '../GroupSelector.vue'

const authState = { isSimpleMode: false }

vi.mock('@/stores', () => ({ useAuthStore: () => authState }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const groups = [
  { id: 1, name: 'Basic', platform: 'anthropic', status: 'active' },
  { id: 2, name: 'Composite', platform: 'composite', status: 'active' }
] as any

const mountSelector = (modelValue: number[] = []) => mount(GroupSelector, {
  props: { modelValue, groups },
  global: { stubs: { GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' }, Icon: true } }
})

describe('GroupSelector simple-mode binding policy', () => {
  beforeEach(() => { authState.isSimpleMode = false })

  it('hides composite groups in simple mode and preserves basic groups', () => {
    authState.isSimpleMode = true
    const wrapper = mountSelector()
    expect(wrapper.text()).toContain('Basic')
    expect(wrapper.text()).not.toContain('Composite')
  })

  it('keeps composite groups available in advanced mode', () => {
    const wrapper = mountSelector()
    expect(wrapper.text()).toContain('Composite')
  })

  it('cleans hidden historical composite IDs while preserving visible selections', () => {
    authState.isSimpleMode = true
    const wrapper = mountSelector([1, 2])
    expect(wrapper.emitted('update:modelValue')).toEqual([[[1]]])
  })

  it('keeps the default selector independent of group selection in simple mode', async () => {
    authState.isSimpleMode = true
    const wrapper = mountSelector([1])
    await wrapper.setProps({ showDefaultSelector: true, defaultGroupId: null })

    const checkboxes = wrapper.findAll('input[type="checkbox"]')
    expect(checkboxes).toHaveLength(2)
    await checkboxes[1].setValue(true)

    expect(wrapper.emitted('update:defaultGroupId')).toEqual([[1]])
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })
})

describe('GroupSelector mixed-scheduling platform filter', () => {
  beforeEach(() => { authState.isSimpleMode = false })

  const mixedGroups = [
    { id: 10, name: 'Codex', platform: 'openai', status: 'active' },
    { id: 11, name: 'Claude', platform: 'anthropic', status: 'active' },
    { id: 12, name: 'Combined', platform: 'composite', status: 'active' }
  ] as any

  const mountMixed = (props: Record<string, unknown>) => mount(GroupSelector, {
    props: { modelValue: [], groups: mixedGroups, ...props },
    global: { stubs: { GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' }, Icon: true } }
  })

  it('hides openai groups from traework accounts without mixed scheduling', () => {
    const wrapper = mountMixed({ platform: 'traework', mixedScheduling: false })
    expect(wrapper.text()).not.toContain('Codex')
    expect(wrapper.text()).toContain('Combined')
  })

  it('shows openai groups to traework accounts once mixed scheduling is enabled', () => {
    const wrapper = mountMixed({ platform: 'traework', mixedScheduling: true })
    expect(wrapper.text()).toContain('Codex')
    expect(wrapper.text()).toContain('Combined')
    expect(wrapper.text()).not.toContain('Claude')
  })

  it('shows openai groups to workbuddy accounts once mixed scheduling is enabled', () => {
    const wrapper = mountMixed({ platform: 'workbuddy', mixedScheduling: true })
    expect(wrapper.text()).toContain('Codex')
  })

  it('never shows anthropic groups to traework accounts', () => {
    const wrapper = mountMixed({ platform: 'traework', mixedScheduling: true })
    expect(wrapper.text()).not.toContain('Claude')
  })
})
