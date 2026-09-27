import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import type { ApiKey } from '@/types'
import ApiKeyDailyUsageDialog from '../ApiKeyDailyUsageDialog.vue'

const { getMyApiKeyDailyUsage } = vi.hoisted(() => ({
  getMyApiKeyDailyUsage: vi.fn(),
}))

vi.mock('@/api', () => ({
  usageAPI: {
    getMyApiKeyDailyUsage,
  },
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string>) => {
        if (key === 'keys.dailyUsageTitle') {
          return `${params?.name} - Daily Usage`
        }
        return key
      },
    }),
  }
})

const BaseDialogStub = {
  props: ['show', 'title'],
  emits: ['close'],
  template: '<div v-if="show"><h2>{{ title }}</h2><slot /></div>',
}

const IconStub = {
  props: ['name'],
  template: '<span>{{ name }}</span>',
}

const apiKey = {
  id: 7,
  name: 'team-key',
} as ApiKey

const mountDialog = () =>
  mount(ApiKeyDailyUsageDialog, {
    props: {
      show: true,
      apiKey,
      month: '2026-08',
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Icon: IconStub,
      },
    },
  })

describe('ApiKeyDailyUsageDialog', () => {
  beforeEach(() => {
    getMyApiKeyDailyUsage.mockReset()
    getMyApiKeyDailyUsage.mockResolvedValue({
      items: [
        {
          date: '2026-08-03',
          requests: 2,
          input_tokens: 10,
          output_tokens: 20,
          cache_read_tokens: 3,
          cache_write_tokens: 4,
          total_tokens: 37,
          cost: 0.6,
          actual_cost: 0.5,
        },
      ],
      days: 3,
      period: 'month',
      start_date: '2026-08-01',
      end_date: '2026-08-03',
    })
  })

  it('loads the selected API key using the selected month', async () => {
    const wrapper = mountDialog()
    await flushPromises()

    expect(getMyApiKeyDailyUsage).toHaveBeenCalledWith(7, { period: 'month', month: '2026-08' })
    expect(wrapper.text()).toContain('team-key - Daily Usage')
    expect(wrapper.get('[data-test="daily-usage-total"]').text()).toBe('¥0.5000')
  })

  it('shows every date in the month-to-date range including zero-usage days', async () => {
    const wrapper = mountDialog()
    await flushPromises()

    const rows = wrapper.findAll('[data-test="daily-usage-row"]')
    expect(rows).toHaveLength(3)
    expect(rows[0].text()).toContain('2026-08-03')
    expect(rows[1].text()).toContain('2026-08-02')
    expect(rows[2].text()).toContain('2026-08-01')
  })

  it('clears the previous month while loading and ignores late responses', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    let resolveOld!: (value: unknown) => void
    getMyApiKeyDailyUsage.mockReturnValueOnce(new Promise((resolve) => { resolveOld = resolve }))
    await wrapper.setProps({ month: '2024-02' })
    expect(wrapper.find('[data-test="daily-usage-total"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-test="daily-usage-row"]')).toHaveLength(0)

    getMyApiKeyDailyUsage.mockResolvedValueOnce({
      items: [], days: 31, period: 'month', start_date: '2024-12-01', end_date: '2024-12-31',
    })
    await wrapper.setProps({ month: '2024-12' })
    await flushPromises()
    resolveOld({ items: [], days: 29, period: 'month', start_date: '2024-02-01', end_date: '2024-02-29' })
    await flushPromises()
    expect(wrapper.findAll('[data-test="daily-usage-row"]')).toHaveLength(31)
    expect(wrapper.text()).toContain('2024-12-31')
    expect(wrapper.text()).not.toContain('2024-02-29')
    wrapper.unmount()
  })

  it('shows all 29 days of an empty leap-year February with a zero total', async () => {
    getMyApiKeyDailyUsage.mockResolvedValueOnce({
      items: [], days: 29, period: 'month', start_date: '2024-02-01', end_date: '2024-02-29',
    })
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.findAll('[data-test="daily-usage-row"]')).toHaveLength(29)
    expect(wrapper.get('[data-test="daily-usage-total"]').text()).toBe('¥0.0000')
    wrapper.unmount()
  })

  it('emits month selection to keep the list and dialog synchronized', async () => {
    const wrapper = mountDialog()
    await wrapper.get('[data-test="daily-usage-month"]').setValue('2024-02')
    expect(wrapper.emitted('update:month')).toEqual([['2024-02']])
    wrapper.unmount()
  })

  it('retries a failed month without displaying the old total', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    getMyApiKeyDailyUsage.mockRejectedValueOnce(new Error('Temporary failure'))
    await wrapper.setProps({ month: '2024-02' })
    await flushPromises()
    expect(wrapper.text()).toContain('Temporary failure')
    expect(wrapper.find('[data-test="daily-usage-total"]').exists()).toBe(false)
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(getMyApiKeyDailyUsage).toHaveBeenLastCalledWith(7, { period: 'month', month: '2024-02' })
    expect(wrapper.text()).not.toContain('Temporary failure')
    wrapper.unmount()
  })

})
