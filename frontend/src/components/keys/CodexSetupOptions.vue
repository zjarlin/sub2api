<template>
  <div class="space-y-3">
    <div class="flex flex-wrap gap-1" role="radiogroup" :aria-label="t('docs.setup.client')">
      <button
        v-for="client in ['desktop', 'cli'] as const"
        :key="client"
        type="button"
        role="radio"
        :aria-checked="model.client === client"
        :data-testid="`setup-client-${client}`"
        class="rounded-md border px-3 py-2 text-sm focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500"
        :class="model.client === client ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-950 dark:text-primary-200' : 'border-gray-300 dark:border-dark-700'"
        @click="model = { ...model, client, installDir: '' }"
      >
        {{ t(`docs.setup.${client}`) }}
      </button>
    </div>
    <div class="grid gap-3 sm:grid-cols-2">
      <label v-if="model.client === 'cli'" class="flex flex-col justify-end gap-1 text-sm">
        <span>{{ t('docs.setup.installDir') }}</span>
        <input v-model="model.installDir" data-testid="setup-install-dir" :placeholder="windows ? 'D:\\Codex\\app' : '/opt/codex'" class="input w-full font-mono" autocomplete="off" spellcheck="false" />
      </label>
      <label class="flex flex-col justify-end gap-1 text-sm">
        <span>{{ t('docs.setup.codexHome') }}</span>
        <input v-model="model.codexHome" data-testid="setup-codex-home" :placeholder="windows ? 'D:\\Codex\\data' : '/mnt/data/codex'" class="input w-full font-mono" autocomplete="off" spellcheck="false" />
      </label>
    </div>
    <p v-if="windows && model.client === 'desktop'" class="text-xs leading-5 text-gray-500 dark:text-dark-300">{{ t('docs.setup.windowsStore') }}</p>
    <p v-if="model.codexHome.trim()" class="text-xs leading-5 text-gray-500 dark:text-dark-300">{{ t(windows ? 'docs.setup.homeWindows' : 'docs.setup.homeUnix') }}</p>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { CodexSetupOptions } from '@/utils/codexSetup'

defineProps<{ windows: boolean }>()
const model = defineModel<CodexSetupOptions>({ required: true })
const { t } = useI18n()
</script>
