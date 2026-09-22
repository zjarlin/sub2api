import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import AccountTableFilters from '../AccountTableFilters.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

const baseFilters = {
  platform: '',
  type: '',
  status: '',
  privacy_mode: '',
  group: '',
  rate_multiplier_min: '',
  rate_multiplier_max: ''
}

describe('AccountTableFilters rate multiplier range', () => {
  it('emits updated filters when the minimum multiplier changes', async () => {
    const wrapper = mount(AccountTableFilters, {
      props: { searchQuery: '', filters: { ...baseFilters }, groups: [] }
    })

    const inputs = wrapper.findAll('input[type="number"]')
    expect(inputs).toHaveLength(2)

    await inputs[0].setValue('0.5')

    const emitted = wrapper.emitted('update:filters')
    expect(emitted).toBeTruthy()
    expect(emitted!.at(-1)![0]).toMatchObject({ rate_multiplier_min: '0.5' })
  })

  it('emits updated filters when the maximum multiplier changes', async () => {
    const wrapper = mount(AccountTableFilters, {
      props: { searchQuery: '', filters: { ...baseFilters }, groups: [] }
    })

    const inputs = wrapper.findAll('input[type="number"]')
    await inputs[1].setValue('2')

    const emitted = wrapper.emitted('update:filters')
    expect(emitted).toBeTruthy()
    expect(emitted!.at(-1)![0]).toMatchObject({ rate_multiplier_max: '2' })
  })
})
