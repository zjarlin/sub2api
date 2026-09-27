<template>
  <div class="flex items-center gap-2">
    <label :for="id" class="text-sm font-medium text-gray-700 dark:text-gray-300">
      {{ t('keys.usageMonth') }}
    </label>
    <input
      :id="id"
      :value="modelValue"
      :data-test="id"
      type="month"
      min="0001-01"
      :max="currentMonth"
      class="input w-44"
      @change="onChange"
    />
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { formatDateLocalInput } from '@/utils/format'

const props = defineProps<{ id: string; modelValue: string }>()
const emit = defineEmits<{ (event: 'update:modelValue', month: string): void }>()
const { t } = useI18n()
const currentMonth = formatDateLocalInput(new Date()).slice(0, 7)

const onChange = (event: Event) => {
  const input = event.target as HTMLInputElement
  const month = input.value
  const valid = /^(?!0000)\d{4}-(0[1-9]|1[0-2])$/.test(month) && month <= currentMonth
  // 清空或输入无效月份时恢复已选值，避免控件与查询月份不一致。
  input.value = valid ? month : props.modelValue
  if (valid && month !== props.modelValue) {
    emit('update:modelValue', month)
  }
}
</script>
