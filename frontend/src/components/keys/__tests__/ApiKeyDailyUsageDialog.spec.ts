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
          return `${params?.name} - Daily Usage This Month`
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

  it('loads the selected API key using the current-month period', async () => {
    const wrapper = mountDialog()
    await flushPromises()

    expect(getMyApiKeyDailyUsage).toHaveBeenCalledWith(7, { period: 'month' })
    expect(wrapper.text()).toContain('team-key - Daily Usage This Month')
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
})
