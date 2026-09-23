<template>
  <div class="space-y-2">
    <button type="button" class="btn btn-secondary" :disabled="disabled || running || models.length === 0" @click="start">
      <Icon :name="running ? 'refresh' : 'beaker'" size="sm" :class="{ 'animate-spin': running }" />
      {{ running ? t('admin.accounts.batchTestingModelsProgress', { done, total }) : t('admin.accounts.batchTestModelsAndPrune') }}
    </button>
    <button v-if="running" type="button" class="btn btn-secondary ml-2" @click="cancel">
      {{ t('admin.accounts.cancelTest') }}
    </button>
    <p v-if="running && currentModel" role="status" class="text-sm text-gray-500 dark:text-gray-400">
      {{ t('admin.accounts.testingCurrentModel', { model: currentModel }) }}
    </p>
    <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.batchTestModelsPruneHint') }}</p>
    <div v-if="lines.length" role="log" aria-live="polite" class="max-h-40 overflow-auto rounded-lg bg-gray-900 p-3 text-xs text-gray-100">
      <p v-for="(line, index) in lines" :key="index">{{ line }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import Icon from '@/components/icons/Icon.vue'
import type { Account, ClaudeModel } from '@/types'
import { runModelTest } from '@/api/accountTest'
import { successfulModelMapping } from './modelMapping'

const props = defineProps<{ account: Account; models: ClaudeModel[]; disabled: boolean; show: boolean }>()
const emit = defineEmits<{ updated: [account: Account]; running: [value: boolean] }>()
const { t } = useI18n()
const running = ref(false)
const currentModel = ref('')
const done = ref(0)
const total = ref(0)
const lines = ref<string[]>([])
let controller: AbortController | undefined
const cancel = () => controller?.abort()
watch(() => [props.show, props.account.id], cancel)
onBeforeUnmount(cancel)

async function start() {
  if (running.value || props.disabled) {
    return
  }
  const accountID = props.account.id
  const models = [...new Set(props.models.map(model => model.id))].filter(id => !id.includes('*'))
  if (!models.length) {
    return
  }
  const run = new AbortController()
  controller = run
  running.value = true
  emit('running', true)
  done.value = 0
  total.value = models.length
  lines.value = []
  const passed: string[] = []
  try {
    for (const model of models) {
      run.signal.throwIfAborted()
      currentModel.value = model
      const result = await runModelTest(`/admin/accounts/${accountID}/test`, {
        model_id: model,
        prompt: model.includes('image') ? t('admin.accounts.imagePromptDefault') : '',
        mode: 'default'
      }, run.signal)
      run.signal.throwIfAborted()
      done.value++
      if (result.success) {
        passed.push(model)
      }
      lines.value.push(t(result.success ? 'admin.accounts.batchTestModelPassed' : 'admin.accounts.batchTestModelFailed', {
        model, error: result.error || t('admin.accounts.testFailed')
      }))
    }
    if (!passed.length) {
      lines.value.push(t('admin.accounts.batchTestModelsNoSuccess'))
      return
    }
    // 当前接口替换非敏感凭证，先读取最新配置以保留 Base URL、协议和其他设置。
    const latest = await adminAPI.accounts.getById(accountID)
    run.signal.throwIfAborted()
    const existing = (latest.credentials?.model_mapping || {}) as Record<string, unknown>
    const updated = await adminAPI.accounts.update(accountID, {
      credentials: {
        ...latest.credentials,
        model_mapping: successfulModelMapping(existing, models, passed)
      }
    })
    emit('updated', updated)
    lines.value.push(t('admin.accounts.batchTestModelsPruned', { count: passed.length }))
  } catch (error) {
    if (run.signal.aborted) {
      lines.value.push(t('admin.accounts.testCancelledPreserved'))
    } else {
      lines.value.push(t('admin.accounts.batchTestModelsStopped', { error: error instanceof Error ? error.message : String(error) }))
    }
  } finally {
    running.value = false
    emit('running', false)
    controller = undefined
  }
}
</script>
