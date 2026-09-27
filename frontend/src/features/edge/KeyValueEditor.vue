<template>
  <div class="space-y-2" data-testid="edge-key-value-editor">
    <div
      v-for="(row, index) in rows"
      :key="`row-${index}`"
      class="grid grid-cols-[minmax(120px,0.8fr)_minmax(0,1.2fr)_auto] gap-2"
    >
      <input v-model.trim="row.name" class="input font-mono text-xs" spellcheck="false" :placeholder="namePlaceholder" />
      <input v-model="row.value" class="input font-mono text-xs" spellcheck="false" :placeholder="valuePlaceholder" />
      <button type="button" class="btn btn-secondary btn-icon h-10 w-10" :title="t('common.delete')" @click="rows.splice(index, 1)">
        <Icon name="trash" size="sm" />
      </button>
    </div>
    <p v-if="rows.length === 0" class="rounded-md border border-dashed border-gray-300 px-3 py-6 text-center text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400">
      {{ emptyText }}
    </p>
    <button type="button" class="btn btn-secondary btn-sm" @click="emit('add')">
      <Icon name="plus" size="xs" />
      {{ t('common.add') }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import Icon from '@/components/icons/Icon.vue'
import type { EdgeKeyValue } from './curl'

const { t } = useI18n()
withDefaults(defineProps<{
  namePlaceholder?: string
  valuePlaceholder?: string
  emptyText?: string
}>(), {
  namePlaceholder: 'Name',
  valuePlaceholder: 'Value',
  emptyText: 'No entries',
})

const rows = defineModel<EdgeKeyValue[]>({ required: true })
const emit = defineEmits<{ add: [] }>()
</script>
