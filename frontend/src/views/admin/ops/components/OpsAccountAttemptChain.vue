<template>
  <section v-if="attempts.length" class="rounded-xl bg-gray-50 p-6 dark:bg-dark-900">
    <h3 class="text-sm font-bold text-gray-900 dark:text-white">{{ t('admin.ops.errorDetail.attemptChain.title') }}</h3>
    <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.ops.errorDetail.attemptChain.description') }}</p>
    <ol class="mt-4 space-y-3">
      <li v-for="(attempt, index) in attempts" :key="index">
        <details
          class="group overflow-hidden rounded-lg border shadow-sm"
          :class="accordionClass(attempt, index)"
          :open="attempt.isFinalFailure"
          :data-testid="`attempt-chain-accordion-${index}`"
        >
          <summary class="flex cursor-pointer list-none flex-wrap items-center gap-2 p-3 text-sm [&::-webkit-details-marker]:hidden">
            <Icon
              name="chevronRight"
              size="xs"
              :stroke-width="2"
              class="shrink-0 transition-transform group-open:rotate-90"
            />
            <span class="text-gray-500 dark:text-gray-400">{{ index + 1 }}.</span>
            <span class="break-all font-medium text-gray-900 dark:text-white">{{ attempt.modelFallback ? t('admin.ops.errorDetail.attemptChain.modelFallback') : attempt.name || t('common.unknown') }}</span>
            <span v-if="attempt.id" class="font-mono text-xs text-gray-500 dark:text-gray-400">#{{ attempt.id }}</span>
            <span v-if="!attempt.modelFallback" class="rounded bg-white/70 px-2 py-0.5 text-xs text-gray-700 ring-1 ring-inset ring-black/5 dark:bg-dark-800/70 dark:text-gray-200 dark:ring-white/10">
              {{ t(`admin.ops.errorDetail.attemptChain.${attempt.stage}`) }}<template v-if="attempt.status"> · {{ attempt.status }}</template>
            </span>
            <span
              v-if="attempt.isFinalFailure"
              class="rounded bg-red-100 px-2 py-0.5 text-xs font-medium text-red-800 dark:bg-red-500/20 dark:text-red-200"
            >
              {{ t('admin.ops.errorDetail.attemptChain.finalFailure') }}
            </span>
            <time v-if="attempt.at" class="ml-auto text-xs text-gray-500 dark:text-gray-400">{{ formatDateTime(new Date(attempt.at).toISOString()) }}</time>
          </summary>
          <div class="border-t border-current/10 px-3 pb-3 pt-2">
            <p v-if="attempt.dropped > 0" class="mb-2 text-xs text-amber-800 dark:text-amber-200">
              {{ t('admin.ops.errorDetail.attemptChain.dropped', { count: attempt.dropped }) }}
            </p>
            <p v-if="attempt.model" class="break-all font-mono text-xs text-gray-700 dark:text-gray-300"><template v-if="attempt.fromModel">{{ attempt.fromModel }} → </template>{{ attempt.model }}<span v-if="attempt.tier" class="ml-2">· {{ attempt.tier }}</span></p>
            <p v-if="attempt.message && !attempt.modelFallback" class="mt-2 whitespace-pre-wrap break-words text-sm text-gray-800 dark:text-gray-200">{{ attempt.message }}</p>
          </div>
        </details>
      </li>
      <li v-if="isSuccess" class="rounded-lg border border-emerald-200 bg-emerald-50 p-3 text-sm text-emerald-800 dark:border-emerald-500/30 dark:bg-emerald-950/30 dark:text-emerald-200" data-testid="attempt-chain-success">
        <div class="flex flex-wrap items-center gap-2">
          <span class="font-semibold">{{ t('admin.ops.errorDetail.attemptChain.finalSuccess') }}</span>
          <span v-if="Number(finalStatusCode) >= 200 && Number(finalStatusCode) < 400">{{ finalStatusCode }}</span>
          <span v-if="finalAccountName" class="break-all">{{ finalAccountName }}</span>
          <span v-if="finalAccountId" class="font-mono text-xs">#{{ finalAccountId }}</span>
        </div>
        <p v-if="finalModel" class="mt-1 break-all font-mono text-xs">{{ finalModel }}</p>
      </li>
    </ol>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'

const props = defineProps<{
  raw?: string
  finalStatusCode?: number | null
  finalSucceeded?: boolean
  finalAccountId?: number | null
  finalAccountName?: string
  finalModel?: string
}>()
const { t } = useI18n()
const isSuccess = computed(() => props.finalSucceeded || (Number(props.finalStatusCode) >= 200 && Number(props.finalStatusCode) < 400))

// 旧日志可能缺字段或包含空事件；顺序和重复账号均保留，原始载荷仍在详情中可查。
const attempts = computed(() => {
  let parsed: unknown
  try {
    parsed = JSON.parse(props.raw || '[]')
  } catch {
    return []
  }
  if (!Array.isArray(parsed)) return []
  const finalFailure = !isSuccess.value && Number(props.finalStatusCode || 0) >= 400
  const attempts = parsed.filter((value): value is Record<string, unknown> => value != null && typeof value === 'object' && !Array.isArray(value)).map(value => ({
    id: positiveNumber(value.account_id),
    modelFallback: value.kind === 'model_fallback',
    model: typeof value.model === 'string' ? value.model : '',
    fromModel: typeof value.from_model === 'string' ? value.from_model : '',
    tier: typeof value.model_tier === 'string' ? value.model_tier : '',
    name: typeof value.account_name === 'string' ? value.account_name : '',
    at: positiveNumber(value.at_unix_ms) <= 8640000000000000 ? positiveNumber(value.at_unix_ms) : 0,
    status: positiveNumber(value.upstream_status_code) || positiveNumber(value.status_code),
    message: typeof value.message === 'string' ? value.message : '',
    dropped: positiveNumber(value.dropped_earlier_attempts),
    stage: value.stage === 'routing' ? 'routing' : value.stage === 'account_auth' ? 'accountAuth' : 'upstream',
    isFinalFailure: false
  }))
  if (finalFailure && attempts.length > 0) {
    attempts[attempts.length - 1].isFinalFailure = true
  }
  return attempts
})

function accordionClass(attempt: { isFinalFailure: boolean }, _index: number): string {
  if (attempt.isFinalFailure) {
    return 'border-red-200 bg-red-50 text-red-950 dark:border-red-500/30 dark:bg-red-950/30 dark:text-red-50'
  }
  return 'border-amber-200 bg-amber-50 text-amber-950 dark:border-amber-500/30 dark:bg-amber-950/25 dark:text-amber-50'
}

function positiveNumber(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : 0
}
</script>
