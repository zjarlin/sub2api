<template>
  <AppLayout>
    <div class="space-y-6">
      <div v-if="loading" class="flex items-center justify-center py-12"><LoadingSpinner /></div>
      <template v-else-if="stats">
        <div class="card flex flex-wrap items-center gap-3 p-4">
          <label for="dashboard-month" class="text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t('dashboard.monthFilter') }}
          </label>
          <input
            id="dashboard-month"
            v-model="selectedMonth"
            data-test="dashboard-month"
            type="month"
            :max="currentMonth"
            class="rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-700 focus:border-primary-500 focus:outline-none focus:ring-2 focus:ring-primary-500/30 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300"
            @change="handleMonthChange"
          />
          <span class="text-sm text-gray-500 dark:text-gray-400">{{ selectedMonthLabel }}</span>
          <div class="ml-auto flex items-center gap-2">
            <span class="text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('dashboard.granularity') }}:</span>
            <div class="w-28">
              <Select
                v-model="granularity"
                :options="[
                  { value: 'day', label: t('dashboard.day') },
                  { value: 'hour', label: t('dashboard.hour') },
                ]"
                @change="loadCharts"
              />
            </div>
          </div>
          <button type="button" class="btn btn-secondary" :disabled="loading || loadingCharts" @click="refreshAll">
            <Icon name="refresh" size="sm" class="mr-1.5" />
            {{ t('common.refresh') }}
          </button>
        </div>
        <UserDashboardStats
          :stats="stats"
          :period-stats="periodStats"
          :period-label="selectedMonthLabel"
          :balance="user?.balance || 0"
          :is-simple="authStore.isSimpleMode"
          :platform-quotas="platformQuotas"
        />
        <UserDashboardCharts :loading="loadingCharts" :trend="trendData" :models="modelStats" />
        <div class="grid grid-cols-1 gap-6 lg:grid-cols-3">
          <div class="lg:col-span-2"><UserDashboardRecentUsage :data="recentUsage" :loading="loadingUsage" :period-label="selectedMonthLabel" /></div>
          <div class="lg:col-span-1"><UserDashboardQuickActions /></div>
        </div>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { usageAPI, type UserDashboardStats as UserStatsType } from '@/api/usage'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Select from '@/components/common/Select.vue'
import UserDashboardStats from '@/components/user/dashboard/UserDashboardStats.vue'
import UserDashboardCharts from '@/components/user/dashboard/UserDashboardCharts.vue'
import UserDashboardRecentUsage from '@/components/user/dashboard/UserDashboardRecentUsage.vue'
import UserDashboardQuickActions from '@/components/user/dashboard/UserDashboardQuickActions.vue'
import type { UsageLog, TrendDataPoint, ModelStat, PlatformQuotaItem, UsageStatsResponse } from '@/types'
import { getMyPlatformQuotas } from '@/api/user'
import { formatDateLocalInput } from '@/utils/format'
import Icon from '@/components/icons/Icon.vue'

const authStore = useAuthStore()
const { t } = useI18n()
const user = computed(() => authStore.user)
const stats = ref<UserStatsType | null>(null)
const loading = ref(false)
const loadingUsage = ref(false)
const loadingCharts = ref(false)
const periodStats = ref<UsageStatsResponse | null>(null)
const trendData = ref<TrendDataPoint[]>([])
const modelStats = ref<ModelStat[]>([])
const recentUsage = ref<UsageLog[]>([])
const platformQuotas = ref<PlatformQuotaItem[] | null>(null)

const formatMonth = (date: Date): string => `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}`
const currentMonth = formatMonth(new Date())
const today = formatDateLocalInput(new Date())
const selectedMonth = ref(currentMonth)
const monthBounds = (month: string): { start: string; end: string } => {
  const [year, monthNumber] = month.split('-').map(Number)
  const lastDay = new Date(year, monthNumber, 0).getDate()
  const monthEnd = `${month}-${String(lastDay).padStart(2, '0')}`
  return { start: `${month}-01`, end: month === currentMonth ? today : monthEnd }
}
const selectedMonthLabel = computed(() => {
  const [year, month] = selectedMonth.value.split('-')
  return t('dashboard.selectedMonth', { year, month: Number(month) })
})
const initialMonthBounds = monthBounds(selectedMonth.value)
const startDate = ref(initialMonthBounds.start)
const endDate = ref(initialMonthBounds.end)
const granularity = ref('day')

const loadStats = async () => {
  loading.value = true
  try {
    await authStore.refreshUser()
    const bounds = monthBounds(selectedMonth.value)
    const [dashboardStats, selectedStats] = await Promise.all([
      usageAPI.getDashboardStats(),
      usageAPI.getStatsByDateRange(bounds.start, bounds.end),
    ])
    stats.value = dashboardStats
    periodStats.value = selectedStats
  } catch (error) {
    console.error('Failed to load dashboard stats:', error)
  } finally {
    loading.value = false
  }
}

const loadCharts = async () => {
  loadingCharts.value = true
  try {
    const [trend, models] = await Promise.all([
      usageAPI.getDashboardTrend({
        start_date: startDate.value,
        end_date: endDate.value,
        granularity: granularity.value as 'day' | 'hour',
      }),
      usageAPI.getDashboardModels({ start_date: startDate.value, end_date: endDate.value }),
    ])
    trendData.value = trend.trend || []
    modelStats.value = models.models || []
  } catch (error) {
    console.error('Failed to load charts:', error)
  } finally {
    loadingCharts.value = false
  }
}

const loadRecent = async () => {
  loadingUsage.value = true
  try {
    const response = await usageAPI.getByDateRange(startDate.value, endDate.value)
    recentUsage.value = response.items.slice(0, 5)
  } catch (error) {
    console.error('Failed to load recent usage:', error)
  } finally {
    loadingUsage.value = false
  }
}

const loadPlatformQuotas = async () => {
  try {
    const data = await getMyPlatformQuotas()
    platformQuotas.value = data.platform_quotas ?? []
  } catch (error) {
    console.warn('Failed to load platform quotas:', error)
    platformQuotas.value = []
  }
}

const refreshAll = () => {
  void loadStats()
  void loadCharts()
  void loadRecent()
  void loadPlatformQuotas()
}
const handleMonthChange = () => {
  if (!selectedMonth.value) selectedMonth.value = currentMonth
  const bounds = monthBounds(selectedMonth.value)
  startDate.value = bounds.start
  endDate.value = bounds.end
  refreshAll()
}

onMounted(refreshAll)
</script>
