<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import type { ExhaustionPolicy } from './policy'

const props = defineProps<{
  modelValue: ExhaustionPolicy
  models: string[]
  groups: { id: number; name: string }[]
}>()
const emit = defineEmits<{ 'update:modelValue': [value: ExhaustionPolicy] }>()
const { t } = useI18n()
const selected = ref('')
const dragged = ref<number | null>(null)
const available = computed(() => props.models.filter(id => !props.modelValue.models.includes(id)))
const invalid = computed(() => props.modelValue.enabled && (props.modelValue.models.length === 0 ||
  props.modelValue.models.some(id => !props.models.includes(id)) ||
  props.modelValue.group_ids.some(id => !props.groups.some(group => group.id === id))))
function update(patch: Partial<ExhaustionPolicy>) {
  emit('update:modelValue', { ...props.modelValue, ...patch })
}
function add() {
  if (!available.value.includes(selected.value) || props.modelValue.models.length >= 16) return
  update({ models: [...props.modelValue.models, selected.value] })
  selected.value = ''
}
function move(from: number, to: number) {
  if (to < 0 || to >= props.modelValue.models.length || from === to) return
  const models = [...props.modelValue.models]
  const [model] = models.splice(from, 1)
  models.splice(to, 0, model)
  update({ models })
}
function drop(index: number) {
  if (dragged.value !== null) move(dragged.value, index)
  dragged.value = null
}
function toggleGroup(id: number) {
  const ids = props.modelValue.group_ids
  update({ group_ids: ids.includes(id) ? ids.filter(value => value !== id) : [...ids, id] })
}
</script>

<template>
  <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600" data-testid="exhaustion-policy">
    <div class="flex items-center gap-2">
      <label class="flex cursor-pointer items-center gap-2 font-medium">
        <input type="checkbox" :checked="modelValue.enabled" @change="update({ enabled: ($event.target as HTMLInputElement).checked })" />
        {{ t('admin.exhaustion.title') }}
      </label>
      <HelpTooltip trigger="click" width-class="w-80">
        <template #trigger><button type="button" class="text-gray-400" :aria-label="t('admin.exhaustion.helpLabel')">?</button></template>
        {{ t('admin.exhaustion.help') }}
      </HelpTooltip>
    </div>
    <template v-if="modelValue.enabled">
      <p v-if="invalid" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ t('admin.exhaustion.invalid') }}</p>
      <ol class="space-y-2">
        <li v-for="(model, index) in modelValue.models" :key="model" class="flex items-center gap-2" draggable="true"
          @dragstart="dragged = index" @dragend="dragged = null" @dragover.prevent @drop.prevent="drop(index)">
          <span class="text-sm text-gray-400">{{ index + 1 }}.</span>
          <span class="min-w-0 flex-1 break-all text-sm">{{ model }}</span>
          <button type="button" class="btn btn-secondary px-2" :disabled="index === 0" :aria-label="t('admin.exhaustion.up', { model })" @click="move(index, index - 1)">↑</button>
          <button type="button" class="btn btn-secondary px-2" :disabled="index === modelValue.models.length - 1" :aria-label="t('admin.exhaustion.down', { model })" @click="move(index, index + 1)">↓</button>
          <button type="button" class="btn btn-secondary px-2" :aria-label="t('admin.exhaustion.remove', { model })" @click="update({ models: modelValue.models.filter(id => id !== model) })">×</button>
        </li>
      </ol>
      <div class="flex gap-2">
        <select v-model="selected" class="input min-w-0 flex-1" :aria-label="t('admin.exhaustion.selectModel')">
          <option value="">{{ t('admin.exhaustion.selectModel') }}</option>
          <option v-for="model in available" :key="model" :value="model">{{ model }}</option>
        </select>
        <button type="button" class="btn btn-secondary" :disabled="!selected || modelValue.models.length >= 16" @click="add">{{ t('admin.exhaustion.add') }}</button>
      </div>
      <fieldset class="space-y-2">
        <legend class="mb-2 text-sm font-medium">{{ t('admin.exhaustion.groups') }}</legend>
        <label class="flex items-center gap-2 text-sm">
          <input type="checkbox" :checked="modelValue.group_ids.length === 0" @change="update({ group_ids: [] })" />
          {{ t('admin.exhaustion.allGroups') }}
        </label>
        <label v-for="group in groups" :key="group.id" class="inline-flex items-center gap-2 pr-4 text-sm">
          <input type="checkbox" :checked="modelValue.group_ids.includes(group.id)" @change="toggleGroup(group.id)" />{{ group.name }}
        </label>
      </fieldset>
    </template>
  </section>
</template>
