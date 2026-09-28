<template>
  <section class="space-y-2 border-t border-gray-200 pt-3 text-sm dark:border-dark-600" data-testid="vibex-usage">
    <div class="flex items-center justify-between gap-2">
      <h3 class="font-medium">{{ t('admin.accounts.vibex.usage') }}</h3>
      <button type="button" class="btn btn-secondary h-8 w-8 p-1" :title="t('common.refresh')" :aria-label="t('common.refresh')" :disabled="loading" @click="refresh">
        <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
      </button>
    </div>
    <dl v-if="usage" class="grid grid-cols-2 gap-x-3 gap-y-1">
      <dt>{{ t('admin.accounts.vibex.balance') }}</dt><dd>{{ number(usage.wallet.wallet_balance) }}</dd>
      <dt>{{ t('admin.accounts.vibex.liteTokens') }}</dt><dd>{{ number(usage.lite.tokens) }} / {{ number(usage.lite.daily_token_limit) }}</dd>
      <dt>{{ t('admin.accounts.vibex.liteCost') }}</dt><dd>{{ number(usage.lite.cost_yuan) }} / {{ number(usage.lite.daily_cost_limit_yuan) }}</dd>
    </dl>
    <p v-else-if="!error" class="text-gray-500">{{ t('admin.accounts.vibex.notLoaded') }}</p>
    <p v-if="error" role="alert" class="text-red-600 dark:text-red-400">{{ error }}</p>
  </section>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { getVibexUsage, type VibexUsage } from '@/api/admin/builtinAdapters'

const props = defineProps<{ accountId: number }>()
const { t } = useI18n()
const usage = ref<VibexUsage | null>(null)
const loading = ref(false)
const error = ref('')
let controller: AbortController | undefined

function number(value: number | null) {
  return value == null ? t('common.unknown') : value.toLocaleString(undefined, { maximumFractionDigits: 2 })
}

async function refresh() {
  if (loading.value) return
  loading.value = true
  error.value = ''
  controller = new AbortController()
  try {
    usage.value = await getVibexUsage(props.accountId, controller.signal)
  } catch (err) {
    if (!controller.signal.aborted) error.value = (err as { message?: string }).message || t('admin.accounts.vibex.usageFailed')
  } finally {
    loading.value = false
  }
}

onBeforeUnmount(() => controller?.abort())
</script>
