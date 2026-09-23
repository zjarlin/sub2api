<template>
  <section class="card" data-testid="model-fallback-settings">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.modelFallback.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.modelFallback.description') }}</p>
    </div>
    <div class="space-y-4 p-6">
      <p v-if="loading" role="status" class="text-sm text-gray-500 dark:text-gray-400">{{ t('common.loading') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <button v-if="!loaded && !loading" type="button" class="btn btn-secondary" @click="load">{{ t('admin.settings.modelFallback.reload') }}</button>
      <fieldset v-if="loaded" :disabled="saving || loading" class="space-y-4">
        <div class="flex items-center justify-between gap-4">
          <label for="model-fallback-enabled" class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.settings.modelFallback.enabled') }}</label>
          <input id="model-fallback-enabled" v-model="enabled" type="checkbox" class="h-4 w-4 accent-primary-600" />
        </div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.modelFallback.scope') }}</p>
        <ol class="space-y-3">
          <li v-for="(tier, index) in tiers" :key="index" class="space-y-3 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-sm font-semibold text-gray-700 dark:text-gray-200">{{ index + 1 }}</span>
              <input v-model="tier.name" :aria-label="t('admin.settings.modelFallback.tierName', { index: index + 1 })" class="input min-w-0 flex-1" maxlength="80" />
              <button type="button" class="btn btn-secondary px-2" :disabled="index === 0" :aria-label="t('admin.settings.modelFallback.moveUp')" @click="move(index, -1)">↑</button>
              <button type="button" class="btn btn-secondary px-2" :disabled="index === tiers.length - 1" :aria-label="t('admin.settings.modelFallback.moveDown')" @click="move(index, 1)">↓</button>
              <button type="button" class="btn btn-secondary" @click="tiers.splice(index, 1)">{{ t('common.delete') }}</button>
            </div>
            <textarea v-model="tier.models" :aria-label="t('admin.settings.modelFallback.models', { index: index + 1 })" class="input w-full font-mono text-sm" rows="2" spellcheck="false" />
          </li>
        </ol>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.modelFallback.modelsHint') }}</p>
        <p v-if="validationError" role="alert" class="text-sm text-amber-700 dark:text-amber-300">{{ validationError }}</p>
        <div class="flex flex-wrap gap-2">
          <button type="button" class="btn btn-secondary" :disabled="tiers.length >= 12" @click="tiers.push({ name: '', models: '' })">{{ t('admin.settings.modelFallback.addTier') }}</button>
          <button type="button" class="btn btn-secondary" @click="preset">{{ t('admin.settings.modelFallback.preset') }}</button>
          <button type="button" class="btn btn-primary ml-auto" :disabled="!!validationError" @click="save">{{ saving ? t('common.saving') : t('common.save') }}</button>
        </div>
        <p v-if="saved" role="status" class="text-sm text-green-700 dark:text-green-400">{{ t('admin.settings.modelFallback.saved') }}</p>
      </fieldset>
      <p class="text-xs leading-relaxed text-gray-500 dark:text-gray-400">
        {{ t('admin.settings.modelFallback.reference') }}
        <a href="https://artificialanalysis.ai/leaderboards/models" target="_blank" rel="noopener noreferrer" class="text-primary-600 underline dark:text-primary-400">Artificial Analysis · 2026-09-20</a>
      </p>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getModelFallbackPolicy, getModelFallbackPreset, updateModelFallbackPolicy, type ModelFallbackPolicy } from '@/api/admin/settings'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const enabled = ref(false)
const tiers = ref<Array<{ name: string; models: string }>>([])
const loading = ref(false)
const saving = ref(false)
const loaded = ref(false)
const saved = ref(false)
const error = ref('')
const policy = computed<ModelFallbackPolicy>(() => ({
  enabled: enabled.value,
  tiers: tiers.value.map(tier => ({ name: tier.name.trim(), models: tier.models.split(/[\s,，]+/).filter(Boolean) }))
}))
const validationError = computed(() => {
  const rows = policy.value.tiers
  const models = rows.flatMap(tier => tier.models)
  if ((enabled.value && !rows.length) || rows.some(tier => !tier.name || !tier.models.length)) return t('admin.settings.modelFallback.required')
  if (new Set(rows.map(tier => tier.name)).size !== rows.length || new Set(models).size !== models.length) return t('admin.settings.modelFallback.duplicate')
  if (models.length > 64 || models.some(model => model.length > 200 || model.includes('*'))) return t('admin.settings.modelFallback.invalidModels')
  return ''
})

watch(policy, () => { saved.value = false }, { flush: 'sync' })

function assign(value: ModelFallbackPolicy) {
  enabled.value = value.enabled
  tiers.value = value.tiers.map(tier => ({ name: tier.name, models: tier.models.join('\n') }))
}
function move(index: number, delta: number) {
  const next = index + delta
  const tier = tiers.value[index]
  if (!tier || next < 0 || next >= tiers.value.length) return
  tiers.value.splice(index, 1)
  tiers.value.splice(next, 0, tier)
}
async function load() {
  loading.value = true
  error.value = ''
  try {
    assign(await getModelFallbackPolicy())
    loaded.value = true
  } catch (err) {
    error.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    loading.value = false
  }
}
async function preset() {
  loading.value = true
  error.value = ''
  saved.value = false
  try {
    assign(await getModelFallbackPreset())
  } catch (err) {
    error.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    loading.value = false
  }
}
async function save() {
  if (saving.value || validationError.value) return
  saving.value = true
  saved.value = false
  error.value = ''
  try {
    assign(await updateModelFallbackPolicy(policy.value))
    saved.value = true
  } catch (err) {
    error.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    saving.value = false
  }
}
onMounted(load)
</script>
