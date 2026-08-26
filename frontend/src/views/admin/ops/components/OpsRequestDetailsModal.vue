<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import { useClipboard } from '@/composables/useClipboard'
import { useAppStore } from '@/stores'
import { adminAPI } from '@/api/admin'
import { opsAPI, type OpsRequestDetailsParams, type OpsRequestDetail } from '@/api/admin/ops'
import { parseTimeRangeMinutes, formatDateTime } from '../utils/opsFormatters'

const TEMP_UNSCHED_DURATION_MINUTES = 10

export interface OpsRequestDetailsPreset {
  title: string
  kind?: OpsRequestDetailsParams['kind']
  sort?: OpsRequestDetailsParams['sort']
  min_duration_ms?: number
  max_duration_ms?: number
}

interface Props {
  modelValue: boolean
  timeRange: string
  preset: OpsRequestDetailsPreset
  platform?: string
  groupId?: number | null
}

const props = defineProps<Props>()
const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'openErrorDetail', errorId: number): void
}>()

const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const loading = ref(false)
const items = ref<OpsRequestDetail[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(10)
const tempDisableLoadingKeys = ref<Set<string>>(new Set())

const close = () => emit('update:modelValue', false)

const rangeLabel = computed(() => {
  const minutes = parseTimeRangeMinutes(props.timeRange)
  if (minutes >= 60) return t('admin.ops.requestDetails.rangeHours', { n: Math.round(minutes / 60) })
  return t('admin.ops.requestDetails.rangeMinutes', { n: minutes })
})

function buildTimeParams(): Pick<OpsRequestDetailsParams, 'start_time' | 'end_time'> {
  const minutes = parseTimeRangeMinutes(props.timeRange)
  const endTime = new Date()
  const startTime = new Date(endTime.getTime() - minutes * 60 * 1000)
  return {
    start_time: startTime.toISOString(),
    end_time: endTime.toISOString()
  }
}

const fetchData = async () => {
  if (!props.modelValue) return
  loading.value = true
  try {
    const params: OpsRequestDetailsParams = {
      ...buildTimeParams(),
      page: page.value,
      page_size: pageSize.value,
      kind: props.preset.kind ?? 'all',
      sort: props.preset.sort ?? 'created_at_desc'
    }

    const platform = (props.platform || '').trim()
    if (platform) params.platform = platform
    if (typeof props.groupId === 'number' && props.groupId > 0) params.group_id = props.groupId

    if (typeof props.preset.min_duration_ms === 'number') params.min_duration_ms = props.preset.min_duration_ms
    if (typeof props.preset.max_duration_ms === 'number') params.max_duration_ms = props.preset.max_duration_ms

    const res = await opsAPI.listRequestDetails(params)
    items.value = res.items || []
    total.value = res.total || 0
  } catch (e: any) {
    console.error('[OpsRequestDetailsModal] Failed to fetch request details', e)
    appStore.showError(e?.message || t('admin.ops.requestDetails.failedToLoad'))
    items.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      page.value = 1
      pageSize.value = 10
      fetchData()
    }
  }
)

watch(
  () => [
    props.timeRange,
    props.platform,
    props.groupId,
    props.preset.kind,
    props.preset.sort,
    props.preset.min_duration_ms,
    props.preset.max_duration_ms
  ],
  () => {
    if (!props.modelValue) return
    page.value = 1
    fetchData()
  }
)

function handlePageChange(next: number) {
  page.value = next
  fetchData()
}

function handlePageSizeChange(next: number) {
  pageSize.value = next
  page.value = 1
  fetchData()
}

async function handleCopyRequestId(requestId: string) {
  const ok = await copyToClipboard(requestId, t('admin.ops.requestDetails.requestIdCopied'))
  if (ok) return
  // `useClipboard` already shows toast on failure; this keeps UX consistent with older ops modal.
  appStore.showWarning(t('admin.ops.requestDetails.copyFailed'))
}

function displayScheduledAccount(row: OpsRequestDetail): string {
  const scheduledName = String(row.scheduled_account_name || '').trim()
  if (scheduledName) return scheduledName
  if (row.scheduled_account_id != null) return String(row.scheduled_account_id)
  return '-'
}

function displayLoginAccount(row: OpsRequestDetail): string {
  const userAccount = String(row.user_account || '').trim()
  if (userAccount) return userAccount
  const username = String(row.username || '').trim()
  if (username) return username
  const email = String(row.user_email || '').trim()
  if (email) return email
  if (row.user_id != null) return String(row.user_id)
  return '-'
}

function loginAccountTitle(row: OpsRequestDetail): string {
  const parts = [
    String(row.username || '').trim(),
    String(row.user_email || '').trim()
  ].filter(Boolean)
  return parts.length > 0 ? parts.join(' / ') : displayLoginAccount(row)
}

function canTempDisableScheduledAccount(row: OpsRequestDetail): boolean {
  return row.kind === 'error' && row.scheduled_account_id != null
}

function tempDisableKey(row: OpsRequestDetail, idx: number): string {
  return `${row.scheduled_account_id || 0}:${row.error_id || row.request_id || idx}`
}

function isTempDisableLoading(row: OpsRequestDetail, idx: number): boolean {
  return tempDisableLoadingKeys.value.has(tempDisableKey(row, idx))
}

function setTempDisableLoading(row: OpsRequestDetail, idx: number, loading: boolean) {
  const key = tempDisableKey(row, idx)
  const next = new Set(tempDisableLoadingKeys.value)
  if (loading) next.add(key)
  else next.delete(key)
  tempDisableLoadingKeys.value = next
}

function buildTempDisableReason(row: OpsRequestDetail): string {
  const parts = ['Ops request details error']
  if (row.error_id) parts.push(`#${row.error_id}`)
  if (row.request_id) parts.push(`request ${row.request_id}`)
  if (row.status_code) parts.push(`status ${row.status_code}`)
  const message = String(row.message || '').trim()
  if (message) parts.push(message.length > 160 ? `${message.substring(0, 160)}...` : message)
  return parts.join(': ')
}

async function handleTempDisableScheduledAccount(row: OpsRequestDetail, idx: number) {
  const accountId = row.scheduled_account_id
  if (accountId == null || isTempDisableLoading(row, idx)) return
  setTempDisableLoading(row, idx, true)
  try {
    await adminAPI.accounts.setTempUnschedulable(accountId, {
      duration_minutes: TEMP_UNSCHED_DURATION_MINUTES,
      status_code: row.status_code ?? undefined,
      reason: buildTempDisableReason(row)
    })
    appStore.showSuccess(t('admin.ops.requestDetails.tempDisableSuccess', {
      id: accountId,
      minutes: TEMP_UNSCHED_DURATION_MINUTES
    }))
  } catch (error: any) {
    console.error('[OpsRequestDetailsModal] Failed to temp-disable scheduled account', error)
    appStore.showError(error?.message || t('admin.ops.requestDetails.tempDisableFailed'))
  } finally {
    setTempDisableLoading(row, idx, false)
  }
}

function openErrorDetail(errorId: number | null | undefined) {
  if (!errorId) return
  close()
  emit('openErrorDetail', errorId)
}

const kindBadgeClass = (kind: string) => {
  if (kind === 'error') return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  return 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
}
</script>

<template>
  <BaseDialog :show="modelValue" :title="props.preset.title || t('admin.ops.requestDetails.title')" width="full" @close="close">
    <template #default>
      <div class="flex h-full min-h-0 flex-col">
        <div class="mb-4 flex flex-shrink-0 items-center justify-between">
          <div class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.ops.requestDetails.rangeLabel', { range: rangeLabel }) }}
          </div>
          <button
            type="button"
            class="btn btn-secondary btn-sm"
            @click="fetchData"
          >
            {{ t('common.refresh') }}
          </button>
        </div>

        <!-- Loading -->
        <div v-if="loading" class="flex flex-1 items-center justify-center py-16">
          <div class="flex flex-col items-center gap-3">
            <svg class="h-8 w-8 animate-spin text-blue-500" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
              <path
                class="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
              ></path>
            </svg>
            <span class="text-sm font-medium text-gray-500 dark:text-gray-400">{{ t('common.loading') }}</span>
          </div>
        </div>

        <!-- Table -->
        <div v-else class="flex min-h-0 flex-1 flex-col">
          <div v-if="items.length === 0" class="rounded-xl border border-dashed border-gray-200 p-10 text-center dark:border-dark-700">
            <div class="text-sm font-medium text-gray-600 dark:text-gray-300">{{ t('admin.ops.requestDetails.empty') }}</div>
            <div class="mt-1 text-xs text-gray-400">{{ t('admin.ops.requestDetails.emptyHint') }}</div>
          </div>

          <div v-else class="flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border border-gray-200 dark:border-dark-700">
            <div class="min-h-0 flex-1 overflow-auto">
              <table class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
                <thead class="sticky top-0 z-10 bg-gray-50 dark:bg-dark-900">
                <tr>
                  <th class="px-4 py-3 text-left text-[11px] font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">
                    {{ t('admin.ops.requestDetails.table.time') }}
                  </th>
                  <th class="px-4 py-3 text-left text-[11px] font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">
                    {{ t('admin.ops.requestDetails.table.kind') }}
                  </th>
                  <th class="px-4 py-3 text-left text-[11px] font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">
                    {{ t('admin.ops.requestDetails.table.platform') }}
                  </th>
                  <th class="px-4 py-3 text-left text-[11px] font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">
                    {{ t('admin.ops.requestDetails.table.model') }}
                  </th>
                  <th class="px-4 py-3 text-left text-[11px] font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">
                    {{ t('admin.ops.requestDetails.table.loginAccount') }}
                  </th>
                  <th class="px-4 py-3 text-left text-[11px] font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">
                    {{ t('admin.ops.requestDetails.table.scheduledAccount') }}
                  </th>
                  <th class="px-4 py-3 text-left text-[11px] font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">
                    {{ t('admin.ops.requestDetails.table.duration') }}
                  </th>
                  <th class="px-4 py-3 text-left text-[11px] font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">
                    {{ t('admin.ops.requestDetails.table.status') }}
                  </th>
                  <th class="px-4 py-3 text-left text-[11px] font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">
                    {{ t('admin.ops.requestDetails.table.requestId') }}
                  </th>
                  <th class="px-4 py-3 text-right text-[11px] font-bold uppercase tracking-wider text-gray-500 dark:text-gray-400">
                    {{ t('admin.ops.requestDetails.table.actions') }}
                  </th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-800">
                <tr v-for="(row, idx) in items" :key="idx" class="hover:bg-gray-50 dark:hover:bg-dark-700/50">
                  <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-600 dark:text-gray-300">
                    {{ formatDateTime(row.created_at) }}
                  </td>
                  <td class="whitespace-nowrap px-4 py-3">
                    <span class="rounded-full px-2 py-1 text-[10px] font-bold" :class="kindBadgeClass(row.kind)">
                      {{ row.kind === 'error' ? t('admin.ops.requestDetails.kind.error') : t('admin.ops.requestDetails.kind.success') }}
                    </span>
                  </td>
                  <td class="whitespace-nowrap px-4 py-3 text-xs font-medium text-gray-700 dark:text-gray-200">
                    {{ (row.platform || 'unknown').toUpperCase() }}
                  </td>
                  <td class="max-w-[240px] truncate px-4 py-3 text-xs text-gray-600 dark:text-gray-300" :title="row.model || ''">
                    {{ row.model || '-' }}
                  </td>
                  <td class="max-w-[220px] truncate px-4 py-3 text-xs font-medium text-gray-700 dark:text-gray-200" :title="loginAccountTitle(row)">
                    {{ displayLoginAccount(row) }}
                  </td>
                  <td class="px-4 py-3 text-xs font-medium text-gray-700 dark:text-gray-200">
                    <div class="flex max-w-[260px] items-center gap-2">
                      <span class="min-w-0 flex-1 truncate" :title="displayScheduledAccount(row)">
                        {{ displayScheduledAccount(row) }}
                      </span>
                      <button
                        v-if="canTempDisableScheduledAccount(row)"
                        type="button"
                        class="shrink-0 rounded bg-amber-50 px-2 py-1 text-[10px] font-bold text-amber-700 hover:bg-amber-100 disabled:cursor-not-allowed disabled:opacity-60 dark:bg-amber-900/20 dark:text-amber-300 dark:hover:bg-amber-900/30"
                        :disabled="isTempDisableLoading(row, idx)"
                        @click="handleTempDisableScheduledAccount(row, idx)"
                      >
                        {{ isTempDisableLoading(row, idx) ? t('admin.ops.requestDetails.tempDisableLoading') : t('admin.ops.requestDetails.tempDisable') }}
                      </button>
                    </div>
                  </td>
                  <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-600 dark:text-gray-300">
                    {{ typeof row.duration_ms === 'number' ? `${row.duration_ms} ms` : '-' }}
                  </td>
                  <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-600 dark:text-gray-300">
                    {{ row.status_code ?? '-' }}
                  </td>
                  <td class="px-4 py-3">
                    <div v-if="row.request_id" class="flex items-center gap-2">
                      <span class="max-w-[220px] truncate font-mono text-[11px] text-gray-700 dark:text-gray-200" :title="row.request_id">
                        {{ row.request_id }}
                      </span>
                      <button
                        class="rounded-md bg-gray-100 px-2 py-1 text-[10px] font-bold text-gray-600 hover:bg-gray-200 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600"
                        @click="handleCopyRequestId(row.request_id)"
                      >
                        {{ t('admin.ops.requestDetails.copy') }}
                      </button>
                    </div>
                    <span v-else class="text-xs text-gray-400">-</span>
                  </td>
                  <td class="whitespace-nowrap px-4 py-3 text-right">
                    <button
                      v-if="row.kind === 'error' && row.error_id"
                      class="rounded-lg bg-red-50 px-3 py-1.5 text-xs font-bold text-red-600 hover:bg-red-100 dark:bg-red-900/20 dark:text-red-300 dark:hover:bg-red-900/30"
                      @click="openErrorDetail(row.error_id)"
                    >
                      {{ t('admin.ops.requestDetails.viewError') }}
                    </button>
                    <span v-else class="text-xs text-gray-400">-</span>
                  </td>
                </tr>
              </tbody>
            </table>
            </div>

            <Pagination
              :total="total"
              :page="page"
              :page-size="pageSize"
              @update:page="handlePageChange"
              @update:pageSize="handlePageSizeChange"
            />
          </div>
        </div>
      </div>
    </template>
  </BaseDialog>
</template>
