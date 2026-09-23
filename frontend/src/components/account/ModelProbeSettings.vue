<template>
  <section class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600" data-testid="model-probe-settings">
    <div class="flex items-center justify-between gap-4">
      <div>
        <label class="input-label mb-0">{{ t('admin.accounts.modelProbe.title') }}</label>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.modelProbe.hint') }}</p>
      </div>
      <Toggle :model-value="modelValue.enabled" :aria-label="t('admin.accounts.modelProbe.title')"
        data-testid="model-probe-enabled" @update:model-value="update({ enabled: $event })" />
    </div>
    <label v-if="modelValue.enabled" class="block">
      <span class="input-label">{{ t('admin.accounts.modelProbe.interval') }}</span>
      <input type="number" min="24" max="8760" step="1" required class="input w-full"
        data-testid="model-probe-interval" :value="modelValue.intervalHours"
        @input="update({ intervalHours: Number(($event.target as HTMLInputElement).value) })" />
    </label>
    <p class="input-hint">{{ t('admin.accounts.modelProbe.gptExcluded') }}</p>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import type { ModelProbePolicy } from './modelProbePolicy'

const props = defineProps<{ modelValue: ModelProbePolicy }>()
const emit = defineEmits<{ 'update:modelValue': [value: ModelProbePolicy] }>()
const { t } = useI18n()
const update = (patch: Partial<ModelProbePolicy>) => emit('update:modelValue', { ...props.modelValue, ...patch })
</script>
