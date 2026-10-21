<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.tempUnschedulable.statusTitle')"
    width="normal"
    @close="handleClose"
  >
    <div class="space-y-4">
      <div v-if="loading" class="flex items-center justify-center py-8">
        <svg class="h-6 w-6 animate-spin text-gray-400" fill="none" viewBox="0 0 24 24">
          <circle
            class="opacity-25"
            cx="12"
            cy="12"
            r="10"
            stroke="currentColor"
            stroke-width="4"
          ></circle>
          <path
            class="opacity-75"
            fill="currentColor"
            d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
          ></path>
        </svg>
      </div>

      <div v-else-if="!isActive" class="rounded-lg border border-gray-200 p-4 text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">
        {{ t('admin.accounts.tempUnschedulable.notActive') }}
      </div>

      <div v-else class="space-y-4">
        <div class="rounded-lg border border-emerald-200 bg-emerald-50 p-3 text-sm text-emerald-800 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-300">
          {{ t('admin.accounts.recoverStateHint') }}
        </div>

        <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
          <p class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.tempUnschedulable.accountName') }}
          </p>
          <p class="mt-1 text-sm font-medium text-gray-900 dark:text-gray-100">
            {{ account?.name || '-' }}
          </p>
        </div>

        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.tempUnschedulable.triggeredAt') }}
            </p>
            <p class="mt-1 text-sm font-medium text-gray-900 dark:text-gray-100">
              {{ triggeredAtText }}
            </p>
          </div>
          <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.tempUnschedulable.until') }}
            </p>
            <p class="mt-1 text-sm font-medium text-gray-900 dark:text-gray-100">
              {{ untilText }}
            </p>
          </div>
          <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.tempUnschedulable.remaining') }}
            </p>
            <p class="mt-1 text-sm font-medium text-gray-900 dark:text-gray-100">
              {{ remainingText }}
            </p>
          </div>
          <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.tempUnschedulable.errorCode') }}
            </p>
            <p class="mt-1 text-sm font-medium text-gray-900 dark:text-gray-100">
              {{ state?.status_code || '-' }}
            </p>
          </div>
          <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.tempUnschedulable.matchedKeyword') }}
            </p>
            <p class="mt-1 text-sm font-medium text-gray-900 dark:text-gray-100">
              {{ state?.matched_keyword || '-' }}
            </p>
          </div>
          <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.tempUnschedulable.ruleOrder') }}
            </p>
            <p class="mt-1 text-sm font-medium text-gray-900 dark:text-gray-100">
              {{ ruleIndexDisplay }}
            </p>
          </div>
        </div>

        <div class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <p class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.tempUnschedulable.errorMessage') }}
          </p>
          <div class="mt-2 rounded bg-gray-50 p-2 text-xs text-gray-700 dark:bg-dark-700 dark:text-gray-300">
            {{ state?.error_message || '-' }}
          </div>
        </div>

        <div
          v-if="hasThresholdEvidence"
          class="rounded-lg border border-blue-200 bg-blue-50 p-3 text-sm text-blue-800 dark:border-blue-500/30 dark:bg-blue-500/10 dark:text-blue-300"
          data-testid="temp-unsched-trigger-evidence"
        >
          {{ triggerEvidenceText }}
        </div>

        <!-- 手动指定恢复日期：额度用尽的账号在日期前不参与调度，到期后自动回到候选池。 -->
        <div class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
          <p class="text-sm font-medium text-gray-900 dark:text-gray-100">
            {{ t('admin.accounts.tempUnschedulable.recoveryDate') }}
          </p>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.tempUnschedulable.recoveryDateHint') }}
          </p>
          <div class="mt-3 flex flex-wrap items-center gap-2">
            <input
              v-model="recoveryInput"
              type="datetime-local"
              class="input w-auto"
              :aria-label="t('admin.accounts.tempUnschedulable.recoveryDate')"
              data-testid="quota-recovery-input"
            />
            <button
              type="button"
              class="btn btn-primary btn-sm"
              :disabled="savingDate || !recoveryInput"
              @click="handleSaveRecoveryDate"
            >
              {{ savingDate ? t('admin.accounts.tempUnschedulable.savingRecoveryDate') : t('admin.accounts.tempUnschedulable.saveRecoveryDate') }}
            </button>
          </div>
          <p v-if="recoveryDateHint" class="mt-2 text-xs text-gray-500 dark:text-gray-400">
            {{ recoveryDateHint }}
          </p>
          <p class="mt-2 text-xs text-amber-600 dark:text-amber-400">
            {{ t('admin.accounts.tempUnschedulable.recoveryDateTimezoneHint') }}
          </p>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" @click="handleClose">
          {{ t('common.close') }}
        </button>
        <button
          type="button"
          class="btn btn-primary"
          :disabled="!isActive || resetting"
          @click="handleReset"
        >
          <svg
            v-if="resetting"
            class="-ml-1 mr-2 h-4 w-4 animate-spin"
            fill="none"
            viewBox="0 0 24 24"
          >
            <circle
              class="opacity-25"
              cx="12"
              cy="12"
              r="10"
              stroke="currentColor"
              stroke-width="4"
            ></circle>
            <path
              class="opacity-75"
              fill="currentColor"
              d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
            ></path>
          </svg>
          {{ t('admin.accounts.recoverState') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { Account, TempUnschedulableStatus } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { formatDateTime, formatDateTimeLocalInput, parseDateTimeLocalInput } from '@/utils/format'

const props = defineProps<{
  show: boolean
  account: Account | null
}>()

const emit = defineEmits<{
  close: []
  reset: [account: Account]
}>()

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const resetting = ref(false)
const savingDate = ref(false)
const recoveryInput = ref('')

// 默认给出「明天此刻」，让额度用尽账号有一个明确的可提交值，同时保留编辑。
const defaultRecoveryInput = () => formatDateTimeLocalInput(Math.floor(Date.now() / 1000) + 24 * 3600)
const resetRecoveryInput = (seed: string | null = null) => {
  recoveryInput.value = seed || defaultRecoveryInput()
}
const status = ref<TempUnschedulableStatus | null>(null)
let requestVersion = 0

const state = computed(() => status.value?.state || null)

// 已有恢复日期时回填到本地时间控件，避免管理员重复推算时区。
const recoveryDateHint = computed(() => {
  if (!state.value?.until_unix) return ''
  return t('admin.accounts.status.tempUnschedulableUntil', {
    time: formatDateTime(new Date(state.value.until_unix * 1000))
  })
})

const isActive = computed(() => {
  if (!status.value?.active || !state.value) return false
  return state.value.until_unix * 1000 > Date.now()
})

const ruleIndexDisplay = computed(() => {
  if (!state.value || !state.value.matched_keyword || state.value.rule_index < 0) return '-'
  return state.value.rule_index + 1
})

const hasThresholdEvidence = computed(() => (state.value?.trigger_count || 0) > 1)

const triggerEvidenceText = computed(() => {
  const count = state.value?.trigger_count || 0
  const threshold = state.value?.trigger_threshold || 0
  const minutes = state.value?.trigger_window_minutes || 0
  if (threshold > 0 && minutes > 0) {
    return t('admin.accounts.tempUnschedulable.multipleErrorTrigger', { count, threshold, minutes })
  }
  if (threshold > 0) {
    return t('admin.accounts.tempUnschedulable.multipleErrorTriggerNoWindow', { count, threshold })
  }
  if (minutes > 0) {
    return t('admin.accounts.tempUnschedulable.multipleErrorCountInWindow', { count, minutes })
  }
  return t('admin.accounts.tempUnschedulable.multipleErrorCount', { count })
})

const triggeredAtText = computed(() => {
  if (!state.value?.triggered_at_unix) return '-'
  return formatDateTime(new Date(state.value.triggered_at_unix * 1000))
})

const untilText = computed(() => {
  if (!state.value?.until_unix) return '-'
  return formatDateTime(new Date(state.value.until_unix * 1000))
})

const remainingText = computed(() => {
  if (!state.value) return '-'
  const remainingMs = state.value.until_unix * 1000 - Date.now()
  if (remainingMs <= 0) {
    return t('admin.accounts.tempUnschedulable.expired')
  }
  const minutes = Math.ceil(remainingMs / 60000)
  if (minutes < 60) {
    return t('admin.accounts.tempUnschedulable.remainingMinutes', { minutes })
  }
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  if (rest === 0) {
    return t('admin.accounts.tempUnschedulable.remainingHours', { hours })
  }
  return t('admin.accounts.tempUnschedulable.remainingHoursMinutes', { hours, minutes: rest })
})

const loadStatus = async () => {
  if (!props.account) return
  const version = ++requestVersion
  status.value = null
  loading.value = true
  try {
    const result = await adminAPI.accounts.getTempUnschedulableStatus(props.account.id)
    if (version !== requestVersion) return
    status.value = result
    const untilUnix = result.state?.until_unix
    if (untilUnix && untilUnix * 1000 > Date.now()) {
      // 已有恢复日期时以真实值回填，避免管理员重复推算时区。
      resetRecoveryInput(formatDateTimeLocalInput(untilUnix))
    }
  } catch (error: any) {
    if (version !== requestVersion) return
    appStore.showError(error?.message || t('admin.accounts.tempUnschedulable.failedToLoad'))
    status.value = null
  } finally {
    if (version === requestVersion) loading.value = false
  }
}

const handleClose = () => {
  emit('close')
}

const handleSaveRecoveryDate = async () => {
  if (!props.account) return
  const timestamp = parseDateTimeLocalInput(recoveryInput.value)
  if (!timestamp) {
    appStore.showError(t('admin.accounts.tempUnschedulable.recoveryDateInvalid'))
    return
  }
  if (timestamp * 1000 <= Date.now()) {
    appStore.showError(t('admin.accounts.tempUnschedulable.recoveryDateMustBeFuture'))
    return
  }
  savingDate.value = true
  try {
    // 控件是本地时间，提交前转成带时区的 UTC，避免服务端按各自时区解释。
    const until = new Date(timestamp * 1000).toISOString()
    const updated = await adminAPI.accounts.setQuotaRecoveryDate(props.account.id, until)
    appStore.showSuccess(t('admin.accounts.tempUnschedulable.recoveryDateSaved'))
    emit('reset', updated)
    handleClose()
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.tempUnschedulable.recoveryDateSaveFailed'))
  } finally {
    savingDate.value = false
  }
}

const handleReset = async () => {
  if (!props.account) return
  resetting.value = true
  try {
    const updated = await adminAPI.accounts.recoverState(props.account.id)
    appStore.showSuccess(t('admin.accounts.recoverStateSuccess'))
    emit('reset', updated)
    handleClose()
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.recoverStateFailed'))
  } finally {
    resetting.value = false
  }
}

watch(
  () => [props.show, props.account?.id],
  ([visible], _, onCleanup) => {
    onCleanup(() => { requestVersion++ })
    if (visible && props.account) {
      resetRecoveryInput()
      loadStatus()
      return
    }
    status.value = null
  }
)
</script>
