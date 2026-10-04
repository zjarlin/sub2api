<template>
  <BaseDialog :show="show" :title="t('admin.accounts.builtinCheckin.panelTitle')" width="wide" @close="emit('close')">
    <div class="space-y-4">
      <!-- 汇总行：剩余合计 + 账号数 + 刷新 -->
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="flex items-baseline gap-3">
          <div>
            <div class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.builtinCheckin.totalCredits') }}
            </div>
            <div class="text-2xl font-bold text-gray-900 dark:text-white">
              {{ formatCredits(totalCredits) }}
            </div>
          </div>
          <div class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.builtinCheckin.accountCount', { n: overview?.accounts?.length || 0 }) }}
          </div>
        </div>
        <button
          type="button"
          class="inline-flex items-center gap-1 rounded px-2 py-1 text-xs font-medium text-blue-600 transition-colors hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-blue-400 dark:hover:bg-blue-900/30"
          :disabled="loading"
          @click="load"
        >
          <svg class="h-3.5 w-3.5" :class="{ 'animate-spin': loading }" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          {{ t('admin.accounts.builtinCheckin.refresh') }}
        </button>
      </div>

      <div v-if="loading" class="py-8 text-center text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.accounts.builtinCheckin.loading') }}
      </div>

      <div v-else-if="error" class="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-800/40 dark:bg-red-900/20 dark:text-red-300">
        {{ error }}
      </div>

      <div v-else-if="!overview || overview.accounts.length === 0" class="py-8 text-center text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.accounts.builtinCheckin.emptyAccounts') }}
      </div>

      <template v-else>
        <!-- 账号列表 -->
        <div class="overflow-hidden rounded-lg border border-gray-200 dark:border-dark-600">
          <table class="w-full text-sm">
            <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-700 dark:text-gray-400">
              <tr>
                <th class="px-3 py-2 text-left font-medium">{{ t('admin.accounts.builtinCheckin.col.uid') }}</th>
                <th class="px-3 py-2 text-right font-medium">{{ t('admin.accounts.builtinCheckin.col.credits') }}</th>
                <th class="px-3 py-2 text-left font-medium">{{ t('admin.accounts.builtinCheckin.col.lastStatus') }}</th>
                <th class="px-3 py-2 text-right font-medium">{{ t('admin.accounts.builtinCheckin.col.lastDelta') }}</th>
                <th class="px-3 py-2 text-left font-medium">{{ t('admin.accounts.builtinCheckin.col.lastAt') }}</th>
                <th class="px-3 py-2 text-right font-medium">{{ t('admin.accounts.builtinCheckin.col.actions') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-600">
              <tr
                v-for="acc in overview.accounts"
                :key="acc.uid"
                class="cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-700"
                :class="{ 'bg-primary-50/60 dark:bg-primary-900/20': selectedUid === acc.uid }"
                @click="selectAccount(acc.uid)"
              >
                <td class="px-3 py-2">
                  <div class="font-medium text-gray-900 dark:text-gray-100">{{ acc.nickname || shortUid(acc.uid) }}</div>
                  <div v-if="acc.nickname" class="text-[10px] text-gray-400">{{ shortUid(acc.uid) }}</div>
                </td>
                <td class="px-3 py-2 text-right tabular-nums text-gray-900 dark:text-gray-100">{{ formatCredits(acc.credits) }}</td>
                <td class="px-3 py-2">
                  <span v-if="acc.checkin" :class="['inline-flex rounded px-1.5 py-0.5 text-[10px] font-medium', statusClass(acc.checkin.status)]">
                    {{ statusLabel(acc.checkin.status) }}
                  </span>
                  <span v-else class="text-xs text-gray-400">{{ t('admin.accounts.builtinCheckin.notCheckedIn') }}</span>
                </td>
                <td class="px-3 py-2 text-right tabular-nums">
                  <span v-if="acc.checkin && acc.checkin.delta > 0" class="text-emerald-600 dark:text-emerald-400">+{{ acc.checkin.delta }}</span>
                  <span v-else-if="acc.checkin" class="text-gray-400">0</span>
                  <span v-else class="text-gray-300">-</span>
                </td>
                <td class="px-3 py-2 text-xs text-gray-500 dark:text-gray-400">{{ acc.checkin ? formatTime(acc.checkin.at) : '-' }}</td>
                <td class="px-3 py-2 text-right">
                  <button
                    type="button"
                    class="rounded px-2 py-0.5 text-xs font-medium text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/30"
                    @click.stop="selectAccount(acc.uid)"
                  >
                    {{ t('admin.accounts.builtinCheckin.title') }}
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- 选中账号的历史 -->
        <div v-if="selectedAccount" class="rounded-lg border border-gray-200 dark:border-dark-600">
          <div class="flex items-center justify-between border-b border-gray-200 px-3 py-2 dark:border-dark-600">
            <div class="text-sm font-medium text-gray-800 dark:text-gray-100">
              {{ t('admin.accounts.builtinCheckin.historyTitle', { name: selectedAccount.nickname || shortUid(selectedAccount.uid) }) }}
            </div>
            <button type="button" class="text-xs text-gray-400 hover:text-gray-600" @click="selectedUid = ''">×</button>
          </div>
          <div v-if="selectedAccount.checkins.length === 0" class="px-3 py-6 text-center text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.builtinCheckin.historyEmpty') }}
          </div>
          <table v-else class="w-full text-sm">
            <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-700 dark:text-gray-400">
              <tr>
                <th class="px-3 py-2 text-left font-medium">{{ t('admin.accounts.builtinCheckin.colTime') }}</th>
                <th class="px-3 py-2 text-left font-medium">{{ t('admin.accounts.builtinCheckin.colStatus') }}</th>
                <th class="px-3 py-2 text-right font-medium">{{ t('admin.accounts.builtinCheckin.colDelta') }}</th>
                <th class="px-3 py-2 text-right font-medium">{{ t('admin.accounts.builtinCheckin.colRemain') }}</th>
                <th class="px-3 py-2 text-left font-medium">{{ t('admin.accounts.builtinCheckin.colDetail') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-600">
              <tr v-for="(rec, i) in reversedHistory" :key="i">
                <td class="px-3 py-2 text-xs text-gray-500 dark:text-gray-400">{{ formatTime(rec.at) }}</td>
                <td class="px-3 py-2">
                  <span :class="['inline-flex rounded px-1.5 py-0.5 text-[10px] font-medium', statusClass(rec.status)]">
                    {{ statusLabel(rec.status) }}
                  </span>
                </td>
                <td class="px-3 py-2 text-right tabular-nums">
                  <span v-if="rec.delta > 0" class="text-emerald-600 dark:text-emerald-400">+{{ rec.delta }}</span>
                  <span v-else class="text-gray-400">0</span>
                </td>
                <td class="px-3 py-2 text-right tabular-nums text-gray-700 dark:text-gray-200">{{ rec.credits }}</td>
                <td class="max-w-[16rem] truncate px-3 py-2 text-xs text-gray-500 dark:text-gray-400" :title="rec.detail">{{ rec.detail || '-' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api/admin'
import type { BuiltinAdapterCheckinOverview } from '@/api/admin/accounts'
import type { Account } from '@/types'

const props = defineProps<{
  show: boolean
  account: Account | null
}>()

const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()

const loading = ref(false)
const error = ref<string | null>(null)
const overview = ref<BuiltinAdapterCheckinOverview | null>(null)
const selectedUid = ref('')

const totalCredits = computed(() => overview.value?.total_credits ?? 0)
const selectedAccount = computed(() =>
  overview.value?.accounts.find((a) => a.uid === selectedUid.value) || null
)
const reversedHistory = computed(() => {
  const list = selectedAccount.value?.checkins || []
  return [...list].reverse()
})

const shortUid = (uid: string) => (uid.length > 10 ? `${uid.slice(0, 10)}…` : uid)
const formatCredits = (v: number) => new Intl.NumberFormat().format(v)
const formatTime = (iso: string) => {
  if (!iso) return '-'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString()
}

const statusLabel = (status: string) => {
  const key = `admin.accounts.builtinCheckin.status.${status}`
  const translated = t(key)
  return translated === key ? status : translated
}
const statusClass = (status: string) => {
  switch (status) {
    case 'ok':
      return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
    case 'already':
      return 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
    case 'fail':
      return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
    default:
      return 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300'
  }
}

const selectAccount = (uid: string) => {
  selectedUid.value = selectedUid.value === uid ? '' : uid
}

const extractErrorMessage = (e: unknown): string => {
  const err = e as {
    message?: string
    reason?: string
    response?: { data?: { message?: string; error?: string; detail?: string } }
  }
  return (
    err?.response?.data?.detail ||
    err?.message ||
    err?.reason ||
    err?.response?.data?.message ||
    err?.response?.data?.error ||
    t('admin.accounts.builtinCheckin.loadFailed')
  )
}

const load = async () => {
  if (!props.account) return
  loading.value = true
  error.value = null
  try {
    overview.value = await adminAPI.accounts.getBuiltinAdapterCheckins(props.account.id)
    if (selectedUid.value && !overview.value.accounts.some((a) => a.uid === selectedUid.value)) {
      selectedUid.value = ''
    }
  } catch (e) {
    error.value = extractErrorMessage(e)
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.show, props.account?.id],
  ([show]) => {
    if (show) {
      selectedUid.value = ''
      overview.value = null
      void load()
    }
  }
)
</script>
