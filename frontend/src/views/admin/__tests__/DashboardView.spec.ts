import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

import type { DashboardStats } from '@/types'
import type { AdminUsageStatsResponse } from '@/api/admin/usage'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import DashboardView from '../DashboardView.vue'

const { getSnapshotV2, getUserUsageTrend, getUserSpendingRanking, getUsageStats, showError } = vi.hoisted(() => ({
  getSnapshotV2: vi.fn(),
  getUserUsageTrend: vi.fn(),
  getUserSpendingRanking: vi.fn(),
  getUsageStats: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    dashboard: {
      getSnapshotV2,
      getUserUsageTrend,
      getUserSpendingRanking
    },
    usage: { getStats: getUsageStats }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError
  })
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (key === 'dashboard.selectedMonth') {
          return `${params?.year}年${params?.month}月`
        }
        if (key === 'dashboard.periodRequests') {
          return `${params?.period}请求`
        }
        if (key === 'dashboard.periodTokens') {
          return `${params?.period} Token`
        }
        return key
      }
    })
  }
})

const createDashboardStats = (): DashboardStats => ({
  total_users: 0,
  today_new_users: 0,
  active_users: 0,
  hourly_active_users: 0,
  stats_updated_at: '',
  stats_stale: false,
  total_api_keys: 0,
  active_api_keys: 0,
  total_accounts: 0,
  normal_accounts: 0,
  error_accounts: 0,
  ratelimit_accounts: 0,
  overload_accounts: 0,
  total_requests: 0,
  total_input_tokens: 0,
  total_output_tokens: 0,
  total_cache_creation_tokens: 0,
  total_cache_read_tokens: 0,
  total_tokens: 0,
  total_cost: 0,
  total_actual_cost: 0,
  today_requests: 0,
  today_input_tokens: 0,
  today_output_tokens: 0,
  today_cache_creation_tokens: 0,
  today_cache_read_tokens: 0,
  today_tokens: 0,
  today_cost: 0,
  today_actual_cost: 0,
  average_duration_ms: 0,
  uptime: 0,
  rpm: 0,
  tpm: 0
})

const createPeriodStats = (requests = 12): AdminUsageStatsResponse => ({
  total_requests: requests,
  total_input_tokens: 1000,
  total_output_tokens: 200,
  total_cache_tokens: 300,
  total_cache_creation_tokens: 100,
  total_cache_read_tokens: 200,
  total_tokens: 1500,
  total_cost: 10,
  total_actual_cost: 3,
  total_account_cost: 2,
  average_duration_ms: 1250
})

const mountDashboard = () => mount(DashboardView, {
  global: {
    stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      LoadingSpinner: true,
      Icon: true,
      DateRangePicker: true,
      Select: true,
      ModelDistributionChart: true,
      TokenUsageTrend: true,
      Line: true
    }
  }
})

const expectRangeQueries = (start: string, end: string, granularity = 'day') => {
  expect(getUsageStats).toHaveBeenLastCalledWith({ start_date: start, end_date: end })
  expect(getSnapshotV2).toHaveBeenLastCalledWith(expect.objectContaining({
    start_date: start,
    end_date: end,
    granularity
  }))
  expect(getUserUsageTrend).toHaveBeenLastCalledWith({
    start_date: start,
    end_date: end,
    granularity,
    limit: 12
  })
  expect(getUserSpendingRanking).toHaveBeenLastCalledWith({
    start_date: start,
    end_date: end,
    limit: 12
  })
}

describe('admin DashboardView', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 8, 3, 12))
    vi.resetAllMocks()
    setActivePinia(createPinia())

    getSnapshotV2.mockResolvedValue({
      stats: createDashboardStats(),
      trend: [],
      models: []
    })
    getUsageStats.mockResolvedValue(createPeriodStats())
    getUserUsageTrend.mockResolvedValue({ trend: [] })
    getUserSpendingRanking.mockResolvedValue({ ranking: [] })
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('defaults to month-to-date and renders system-wide period totals independently of today and lifetime stats', async () => {
    const wrapper = mountDashboard()
    await flushPromises()

    expectRangeQueries('2026-09-01', '2026-09-03')
    expect(wrapper.get('[data-test="dashboard-month"]').attributes('max')).toBe('2026-09')
    expect(wrapper.text()).toContain('2026年9月请求')
    expect(wrapper.get('[data-test="period-requests"]').text()).toBe('12')
    expect(wrapper.get('[data-test="period-tokens"]').text()).toBe('1.50K')
    expect(wrapper.get('[data-test="period-costs"]').text()).toBe('$3.00 / $2.00 / $10.00')
    expect(wrapper.get('[data-test="period-response"]').text()).toBe('1.25s')
  })

  it.each([
    ['2026-08', '2026-08-31'],
    ['2025-02', '2025-02-28'],
    ['2024-02', '2024-02-29'],
    ['2025-12', '2025-12-31']
  ])('updates all queries for historical month %s, including the full last day', async (month, end) => {
    const wrapper = mountDashboard()
    await flushPromises()
    getUsageStats.mockResolvedValueOnce(createPeriodStats(34))

    await wrapper.get('[data-test="dashboard-month"]').setValue(month)
    await flushPromises()

    expectRangeQueries(`${month}-01`, end)
    expect(wrapper.get('[data-test="period-requests"]').text()).toBe('34')
    expect(wrapper.findComponent(DateRangePicker).props()).toMatchObject({
      startDate: `${month}-01`, endDate: end
    })
  })

  it('keeps custom date ranges working and clears the month label', async () => {
    const wrapper = mountDashboard()
    await flushPromises()

    wrapper.findComponent(DateRangePicker).vm.$emit('change', {
      startDate: '2026-09-02', endDate: '2026-09-03', preset: 'last24Hours'
    })
    await flushPromises()

    expectRangeQueries('2026-09-02', '2026-09-03', 'hour')
    expect((wrapper.get('[data-test="dashboard-month"]').element as HTMLInputElement).value).toBe('')
    expect(wrapper.text()).toContain('2026-09-02 ~ 2026-09-03请求')
    expect(wrapper.text()).not.toContain('2026年9月请求')
  })

  it('restores the current month when the month input is cleared and refreshes through the current day', async () => {
    const wrapper = mountDashboard()
    await flushPromises()
    await wrapper.get('[data-test="dashboard-month"]').setValue('2026-08')
    await flushPromises()
    await wrapper.get('[data-test="dashboard-month"]').setValue('')
    await flushPromises()
    expectRangeQueries('2026-09-01', '2026-09-03')

    vi.setSystemTime(new Date(2026, 8, 4, 12))
    await wrapper.get('[data-test="dashboard-refresh"]').trigger('click')
    await flushPromises()
    expectRangeQueries('2026-09-01', '2026-09-04')
  })

  it('does not show or restore another month totals while requests are pending or arrive out of order', async () => {
    const wrapper = mountDashboard()
    await flushPromises()
    let resolveOlder!: (stats: AdminUsageStatsResponse) => void
    getUsageStats.mockReturnValueOnce(new Promise<AdminUsageStatsResponse>((resolve) => {
      resolveOlder = resolve
    }))

    await wrapper.get('[data-test="dashboard-month"]').setValue('2026-08')
    expect(wrapper.get('[data-test="period-requests"]').text()).toBe('—')
    expect(wrapper.find('[data-test="period-costs"]').exists()).toBe(false)

    getUsageStats.mockResolvedValueOnce(createPeriodStats(56))
    await wrapper.get('[data-test="dashboard-month"]').setValue('2026-07')
    await flushPromises()
    resolveOlder(createPeriodStats(34))
    await flushPromises()

    expect(wrapper.text()).toContain('2026年7月请求')
    expect(wrapper.get('[data-test="period-requests"]').text()).toBe('56')
  })

  it('shows an error and leaves totals unavailable if the selected month cannot be loaded', async () => {
    const wrapper = mountDashboard()
    await flushPromises()
    vi.spyOn(console, 'error').mockImplementation(() => {})
    getUsageStats.mockRejectedValueOnce(new Error('stats unavailable'))
    await wrapper.get('[data-test="dashboard-month"]').setValue('2026-08')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.dashboard.failedToLoad')
    expect(wrapper.get('[data-test="period-requests"]').text()).toBe('—')
    expect(wrapper.get('[data-test="period-tokens"]').text()).toBe('—')
    expect(wrapper.find('[data-test="period-costs"]').exists()).toBe(false)
  })
})
