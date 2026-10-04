<template>
  <div v-if="visible" class="space-y-1">
    <div class="flex flex-wrap items-center gap-1.5">
      <span
        class="text-[10px] font-medium leading-4"
        :class="platformTextClass(account.platform)"
        :title="t('admin.accounts.builtinCheckin.remaining')"
      >
        {{ summaryLabel }}
      </span>
    </div>
    <div class="flex flex-wrap items-center gap-1.5">
      <button
        type="button"
        class="inline-flex items-center gap-0.5 whitespace-nowrap rounded px-1.5 py-0.5 text-[10px] font-medium leading-4 text-blue-600 transition-colors hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-blue-400 dark:hover:bg-blue-900/30"
        :disabled="loading"
        :title="t('admin.accounts.builtinCheckin.title')"
        @click="emit('open')"
      >
        <svg class="h-2.5 w-2.5" :class="{ 'animate-spin': loading }" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
        </svg>
        {{ t('admin.accounts.builtinCheckin.title') }}
      </button>
      <button
        type="button"
        class="rounded px-1 py-0.5 text-[10px] text-gray-400 hover:text-gray-600 disabled:opacity-50 dark:hover:text-gray-200"
        :disabled="loading"
        :title="t('admin.accounts.builtinCheckin.refresh')"
        @click="load"
      >
        ↻
      </button>
    </div>
    <div v-if="error" class="truncate text-[10px] text-red-600 dark:text-red-400" :title="error">
      {{ error }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { platformTextClass } from '@/utils/platformColors'
import type { Account } from '@/types'

const props = defineProps<{ account: Account }>()
const emit = defineEmits<{ open: [] }>()
const { t } = useI18n()

// WorkBuddy 与 TRAE Work 走内置 sidecar，积分/签到由 sidecar 池提供。
const visible = computed(() => props.account.platform === 'workbuddy' || props.account.platform === 'traework')

const loading = ref(false)
const error = ref<string | null>(null)
const totalCredits = ref<number | null>(null)
const accountCount = ref(0)

const summaryLabel = computed(() => {
  if (error.value) return t('admin.accounts.builtinCheckin.remaining')
  if (totalCredits.value == null) return t('admin.accounts.builtinCheckin.remaining')
  return `${t('admin.accounts.builtinCheckin.totalCredits')} ${new Intl.NumberFormat().format(totalCredits.value)}`
})

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
  if (loading.value) return
  loading.value = true
  error.value = null
  try {
    const overview = await adminAPI.accounts.getBuiltinAdapterCheckins(props.account.id)
    totalCredits.value = overview.total_credits
    accountCount.value = overview.accounts.length
  } catch (e) {
    error.value = extractErrorMessage(e)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  if (visible.value) void load()
})

watch(
  () => props.account.id,
  () => {
    totalCredits.value = null
    accountCount.value = 0
    error.value = null
    if (visible.value) void load()
  }
)

defineExpose({ accountCount })
</script>
