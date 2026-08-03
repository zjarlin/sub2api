<template>
  <BaseDialog
    :show="show"
    :title="t('keys.dailyUsageTitle', { name: apiKey?.name || '-' })"
    width="extra-wide"
    @close="emit('close')"
  >
    <div data-test="daily-usage-dialog" class="min-h-64">
      <div
        v-if="dailyUsage"
        class="mb-4 flex flex-col gap-2 border-b border-gray-200 pb-4 sm:flex-row sm:items-center sm:justify-between dark:border-dark-600"
      >
        <div class="text-sm text-gray-500 dark:text-gray-400">
          {{ dailyUsage.start_date }} - {{ dailyUsage.end_date }}
        </div>
        <div class="flex items-baseline gap-2">
          <span class="text-sm text-gray-500 dark:text-gray-400">
            {{ t('keys.dailyUsageMonthTotal') }}
          </span>
          <span data-test="daily-usage-total" class="text-lg font-semibold tabular-nums text-gray-900 dark:text-white">
            {{ formatUserCurrency(monthActualCost, 4) }}
          </span>
        </div>
      </div>

      <div v-if="loading" class="flex min-h-48 items-center justify-center text-gray-500 dark:text-gray-400">
        <Icon name="refresh" size="md" class="mr-2 animate-spin" />
        <span>{{ t('common.loading') }}</span>
      </div>

      <div v-else-if="errorMessage" class="flex min-h-48 flex-col items-center justify-center gap-3 text-center">
        <p class="text-sm text-red-600 dark:text-red-400">{{ errorMessage }}</p>
        <button type="button" class="btn btn-secondary" @click="loadDailyUsage">
          <Icon name="refresh" size="sm" class="mr-1.5" />
          {{ t('keys.dailyUsageRetry') }}
        </button>
      </div>

      <div v-else-if="dailyRows.length > 0" class="max-h-[60vh] overflow-auto">
        <table class="w-full min-w-[920px]">
          <thead class="sticky top-0 z-10 bg-gray-50 dark:bg-dark-800">
            <tr class="border-b border-gray-200 dark:border-dark-600">
              <th class="px-3 py-3 text-left text-xs font-semibold text-gray-500 dark:text-gray-400">
                {{ t('keys.dailyUsageDate') }}
              </th>
              <th class="px-3 py-3 text-right text-xs font-semibold text-gray-500 dark:text-gray-400">
                {{ t('keys.dailyUsageRequests') }}
              </th>
              <th class="px-3 py-3 text-right text-xs font-semibold text-gray-500 dark:text-gray-400">
                {{ t('keys.dailyUsageInputTokens') }}
              </th>
              <th class="px-3 py-3 text-right text-xs font-semibold text-gray-500 dark:text-gray-400">
                {{ t('keys.dailyUsageOutputTokens') }}
              </th>
              <th class="px-3 py-3 text-right text-xs font-semibold text-gray-500 dark:text-gray-400">
                {{ t('keys.dailyUsageCacheReadTokens') }}
              </th>
              <th class="px-3 py-3 text-right text-xs font-semibold text-gray-500 dark:text-gray-400">
                {{ t('keys.dailyUsageCacheWriteTokens') }}
              </th>
              <th class="px-3 py-3 text-right text-xs font-semibold text-gray-500 dark:text-gray-400">
                {{ t('keys.dailyUsageTotalTokens') }}
              </th>
              <th class="px-3 py-3 text-right text-xs font-semibold text-gray-500 dark:text-gray-400">
                {{ t('keys.dailyUsageActualCost') }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="row in dailyRows"
              :key="row.date"
              data-test="daily-usage-row"
              class="border-b border-gray-100 last:border-b-0 dark:border-dark-700"
            >
              <td class="whitespace-nowrap px-3 py-3 text-sm font-medium text-gray-900 dark:text-white">
                {{ row.date }}
              </td>
              <td class="px-3 py-3 text-right text-sm tabular-nums text-gray-700 dark:text-gray-300">
                {{ formatNumber(row.requests) }}
              </td>
              <td class="px-3 py-3 text-right text-sm tabular-nums text-gray-700 dark:text-gray-300">
                {{ formatNumber(row.input_tokens) }}
              </td>
              <td class="px-3 py-3 text-right text-sm tabular-nums text-gray-700 dark:text-gray-300">
                {{ formatNumber(row.output_tokens) }}
              </td>
              <td class="px-3 py-3 text-right text-sm tabular-nums text-gray-700 dark:text-gray-300">
                {{ formatNumber(row.cache_read_tokens) }}
              </td>
              <td class="px-3 py-3 text-right text-sm tabular-nums text-gray-700 dark:text-gray-300">
                {{ formatNumber(row.cache_write_tokens) }}
              </td>
              <td class="px-3 py-3 text-right text-sm tabular-nums text-gray-700 dark:text-gray-300">
                {{ formatNumber(row.total_tokens) }}
              </td>
              <td class="px-3 py-3 text-right text-sm font-medium tabular-nums text-gray-900 dark:text-white">
                {{ formatUserCurrency(row.actual_cost, 4) }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-else class="flex min-h-48 items-center justify-center text-sm text-gray-500 dark:text-gray-400">
        {{ t('keys.dailyUsageEmpty') }}
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { usageAPI } from '@/api'
import type { ApiKeyDailyUsagePoint, ApiKeyDailyUsageResponse } from '@/api/usage'
import type { ApiKey } from '@/types'
import { formatUserCurrency } from '@/utils/userCurrency'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  show: boolean
  apiKey: ApiKey | null
}>()

const emit = defineEmits<{
  (event: 'close'): void
}>()

const { t } = useI18n()
const loading = ref(false)
const errorMessage = ref('')
const dailyUsage = ref<ApiKeyDailyUsageResponse | null>(null)
let requestVersion = 0

const emptyDailyUsagePoint = (date: string): ApiKeyDailyUsagePoint => ({
  date,
  requests: 0,
  input_tokens: 0,
  output_tokens: 0,
  cache_read_tokens: 0,
  cache_write_tokens: 0,
  total_tokens: 0,
  cost: 0,
  actual_cost: 0,
})

const enumerateDates = (startDate: string, endDate: string): string[] => {
  const dates: string[] = []
  const cursor = new Date(`${startDate}T00:00:00Z`)
  const end = new Date(`${endDate}T00:00:00Z`)

  while (cursor <= end) {
    dates.push(cursor.toISOString().slice(0, 10))
    cursor.setUTCDate(cursor.getUTCDate() + 1)
  }
  return dates
}

const dailyRows = computed<ApiKeyDailyUsagePoint[]>(() => {
  if (!dailyUsage.value) {
    return []
  }

  const rowsByDate = new Map(dailyUsage.value.items.map((item) => [item.date, item]))
  return enumerateDates(dailyUsage.value.start_date, dailyUsage.value.end_date)
    .map((date) => rowsByDate.get(date) ?? emptyDailyUsagePoint(date))
    .reverse()
})

const monthActualCost = computed(() =>
  dailyRows.value.reduce((total, row) => total + row.actual_cost, 0)
)

const formatNumber = (value: number): string => value.toLocaleString()

const loadDailyUsage = async () => {
  if (!props.show || !props.apiKey) {
    return
  }

  const currentRequestVersion = ++requestVersion
  loading.value = true
  errorMessage.value = ''

  try {
    const response = await usageAPI.getMyApiKeyDailyUsage(props.apiKey.id, { period: 'month' })
    if (currentRequestVersion !== requestVersion) {
      return
    }
    dailyUsage.value = response
  } catch (error) {
    if (currentRequestVersion !== requestVersion) {
      return
    }
    const message = error instanceof Error ? error.message : ''
    errorMessage.value = message || t('keys.dailyUsageLoadFailed')
  } finally {
    if (currentRequestVersion === requestVersion) {
      loading.value = false
    }
  }
}

watch(
  () => [props.show, props.apiKey?.id] as const,
  ([show, apiKeyID]) => {
    if (show && apiKeyID) {
      void loadDailyUsage()
      return
    }

    requestVersion++
    loading.value = false
    errorMessage.value = ''
    dailyUsage.value = null
  },
  { immediate: true }
)

onUnmounted(() => {
  requestVersion++
})
</script>
