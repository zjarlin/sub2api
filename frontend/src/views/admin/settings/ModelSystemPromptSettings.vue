<template>
  <section class="card" data-testid="model-system-prompt-settings">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.modelSystemPrompts.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.modelSystemPrompts.description') }}</p>
    </div>
    <div class="space-y-4 p-6">
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <p v-if="loading" role="status" class="text-sm text-gray-500 dark:text-gray-400">{{ t('common.loading') }}</p>
      <fieldset v-if="loaded" :disabled="saving || loading" class="space-y-3">
        <div v-for="(entry, index) in entries" :key="index" class="space-y-2 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <div class="flex gap-2">
            <input v-model.trim="entry.model" class="input min-w-0 flex-1 font-mono" :aria-label="t('admin.settings.modelSystemPrompts.model', { index: index + 1 })" />
            <button type="button" class="btn btn-secondary" :aria-label="t('common.delete')" @click="entries.splice(index, 1)">{{ t('common.delete') }}</button>
          </div>
          <textarea v-model="entry.prompt" class="input w-full text-sm" rows="3" :aria-label="t('admin.settings.modelSystemPrompts.prompt', { index: index + 1 })" :placeholder="t('admin.settings.modelSystemPrompts.promptPlaceholder')" spellcheck="false" />
        </div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.modelSystemPrompts.hint') }}</p>
        <p v-if="validationError" role="alert" class="text-sm text-amber-700 dark:text-amber-300">{{ validationError }}</p>
        <div class="flex gap-2">
          <button type="button" class="btn btn-secondary" @click="entries.push({ model: '', prompt: '' })">{{ t('admin.settings.modelSystemPrompts.add') }}</button>
          <button type="button" class="btn btn-primary ml-auto" :disabled="!!validationError" @click="save">{{ saving ? t('common.saving') : t('common.save') }}</button>
        </div>
        <p v-if="saved" role="status" class="text-sm text-green-700 dark:text-green-400">{{ t('admin.settings.modelSystemPrompts.saved') }}</p>
      </fieldset>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getModelSystemPromptPolicy, updateModelSystemPromptPolicy, type ModelSystemPromptPolicy } from '@/api/admin/settings'
import { extractApiErrorMessage } from '@/utils/apiError'
const { t } = useI18n()
const entries = ref<Array<{ model: string; prompt: string }>>([])
const loading = ref(false); const saving = ref(false); const loaded = ref(false); const saved = ref(false); const error = ref('')
const policy = computed<ModelSystemPromptPolicy>(() => ({ entries: entries.value.map(e => ({ model: e.model.trim(), prompt: e.prompt.trim() })) }))
const validationError = computed(() => {
  const models = policy.value.entries.map(e => e.model)
  if (policy.value.entries.some(e => !e.model || !e.prompt)) return t('admin.settings.modelSystemPrompts.required')
  if (new Set(models).size !== models.length) return t('admin.settings.modelSystemPrompts.duplicate')
  return ''
})
function assign(value: ModelSystemPromptPolicy) { entries.value = value.entries.map(e => ({ model: e.model, prompt: e.prompt })) }
async function load() { loading.value = true; error.value = ''; try { assign(await getModelSystemPromptPolicy()); loaded.value = true } catch (e) { error.value = extractApiErrorMessage(e, t('common.error')) } finally { loading.value = false } }
async function save() { if (saving.value || validationError.value) return; saving.value = true; error.value = ''; saved.value = false; try { assign(await updateModelSystemPromptPolicy(policy.value)); saved.value = true } catch (e) { error.value = extractApiErrorMessage(e, t('common.error')) } finally { saving.value = false } }
onMounted(load)
</script>
