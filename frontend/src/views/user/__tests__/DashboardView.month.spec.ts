import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import DashboardView from '../DashboardView.vue'

const mocks = vi.hoisted(() => ({
  refreshUser: vi.fn(),
  getDashboardStats: vi.fn(),
  getStatsByDateRange: vi.fn(),
  getDashboardTrend: vi.fn(),
  getDashboardModels: vi.fn(),
  getByDateRange: vi.fn(),
  getMyPlatformQuotas: vi.fn(),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: { balance: 100 },
    isSimpleMode: false,
    refreshUser: mocks.refreshUser,
  }),
}))

vi.mock('@/api/usage', () => ({
  usageAPI: {
    getDashboardStats: mocks.getDashboardStats,
    getStatsByDateRange: mocks.getStatsByDateRange,
    getDashboardTrend: mocks.getDashboardTrend,
    getDashboardModels: mocks.getDashboardModels,
    getByDateRange: mocks.getByDateRange,
  },
}))

vi.mock('@/api/user', () => ({
  getMyPlatformQuotas: mocks.getMyPlatformQuotas,
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (key === 'dashboard.selectedMonth') return `${params?.year}年${params?.month}月`
        return key
      },
    }),
  }
})

const dashboardStats = {
  total_api_keys: 1,
  active_api_keys: 1,
  today_requests: 0,
  today_tokens: 0,
  today_cost: 0,
  today_actual_cost: 0,
}

const periodStats = {
  total_requests: 12,
  total_input_tokens: 100,
  total_output_tokens: 20,
  total_cache_tokens: 30,
  total_cache_read_tokens: 30,
  total_cache_creation_tokens: 0,
  total_tokens: 150,
  total_cost: 1,
  total_actual_cost: 0.3,
  average_duration_ms: 100,
}

describe('user DashboardView month filter', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 8, 3, 12))
    vi.clearAllMocks()
    mocks.refreshUser.mockResolvedValue(undefined)
    mocks.getDashboardStats.mockResolvedValue(dashboardStats)
    mocks.getStatsByDateRange.mockResolvedValue(periodStats)
    mocks.getDashboardTrend.mockResolvedValue({ trend: [] })
    mocks.getDashboardModels.mockResolvedValue({ models: [] })
    mocks.getByDateRange.mockResolvedValue({ items: [] })
    mocks.getMyPlatformQuotas.mockResolvedValue({ platform_quotas: [] })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('loads the current month to date and switches every dashboard query to a historical month', async () => {
    const wrapper = mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<main><slot /></main>' },
          LoadingSpinner: true,
          UserDashboardStats: {
            props: ['periodStats', 'periodLabel'],
            template: '<div data-test="period-summary">{{ periodLabel }}:{{ periodStats.total_tokens }}</div>',
          },
          UserDashboardCharts: true,
          UserDashboardRecentUsage: true,
          UserDashboardQuickActions: true,
          Icon: true,
        },
      },
    })
    await flushPromises()

    expect(mocks.getStatsByDateRange).toHaveBeenCalledWith('2026-09-01', '2026-09-03')
    expect(wrapper.get('[data-test="period-summary"]').text()).toBe('2026年9月:150')

    vi.clearAllMocks()
    await wrapper.get('[data-test="dashboard-month"]').setValue('2026-08')
    await flushPromises()

    expect(mocks.getStatsByDateRange).toHaveBeenCalledWith('2026-08-01', '2026-08-31')
    expect(mocks.getDashboardTrend).toHaveBeenCalledWith({
      start_date: '2026-08-01',
      end_date: '2026-08-31',
      granularity: 'day',
    })
    expect(mocks.getDashboardModels).toHaveBeenCalledWith({
      start_date: '2026-08-01',
      end_date: '2026-08-31',
    })
    expect(mocks.getByDateRange).toHaveBeenCalledWith('2026-08-01', '2026-08-31')
    expect(wrapper.get('[data-test="period-summary"]').text()).toBe('2026年8月:150')
  })
})
