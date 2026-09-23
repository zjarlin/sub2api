<template>
  <section class="card" data-testid="vision-fallback-settings">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.visionFallback.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.visionFallback.description') }}</p>
    </div>
    <div class="space-y-4 p-6">
      <p v-if="loading" role="status" class="text-sm text-gray-500 dark:text-gray-400">{{ t('common.loading') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <button v-if="!loaded && !loading" type="button" class="btn btn-secondary" @click="load">{{ t('admin.settings.modelFallback.reload') }}</button>
      <fieldset v-if="loaded" :disabled="loading || saving" class="space-y-4">
        <div class="flex items-center justify-between gap-4">
          <label for="vision-fallback-enabled" class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.settings.visionFallback.enabled') }}</label>
          <input id="vision-fallback-enabled" v-model="enabled" type="checkbox" class="h-4 w-4 accent-primary-600" />
        </div>
        <div>
          <label for="vision-fallback-models" class="mb-2 block text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.settings.visionFallback.models') }}</label>
          <textarea id="vision-fallback-models" v-model="models" rows="4" spellcheck="false" class="input w-full font-mono text-sm" aria-describedby="vision-fallback-models-hint" />
          <p id="vision-fallback-models-hint" class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.visionFallback.modelsHint') }}</p>
        </div>
        <div class="flex items-center justify-between gap-4">
          <label for="vision-fallback-unlisted" class="text-sm text-gray-900 dark:text-white">{{ t('admin.settings.visionFallback.allowUnlisted') }}</label>
          <input id="vision-fallback-unlisted" v-model="allowUnlisted" type="checkbox" class="h-4 w-4 accent-primary-600" />
        </div>
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label for="vision-fallback-candidate-timeout" class="mb-2 block text-sm text-gray-900 dark:text-white">{{ t('admin.settings.visionFallback.candidateTimeout') }}</label>
            <input id="vision-fallback-candidate-timeout" v-model.number="candidateTimeout" class="input w-full" type="number" min="1" step="1" />
          </div>
          <div>
            <label for="vision-fallback-timeout" class="mb-2 block text-sm text-gray-900 dark:text-white">{{ t('admin.settings.visionFallback.totalTimeout') }}</label>
            <input id="vision-fallback-timeout" v-model.number="totalTimeout" class="input w-full" type="number" min="1" step="1" />
          </div>
        </div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.visionFallback.scope') }}</p>
        <p v-if="validationError" role="alert" class="text-sm text-amber-700 dark:text-amber-300">{{ validationError }}</p>
        <div class="flex justify-end">
          <button type="button" class="btn btn-primary" :disabled="!!validationError" @click="save">{{ saving ? t('common.saving') : t('common.save') }}</button>
        </div>
        <p v-if="saved" role="status" class="text-sm text-green-700 dark:text-green-400">{{ t('admin.settings.visionFallback.saved') }}</p>
      </fieldset>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getVisionFallbackPolicy, updateVisionFallbackPolicy, type VisionFallbackPolicy } from '@/api/admin/settings'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const enabled = ref(false)
const models = ref('')
const allowUnlisted = ref(false)
const candidateTimeout = ref<number | string>(60)
const totalTimeout = ref<number | string>(120)
const loading = ref(false)
const saving = ref(false)
const loaded = ref(false)
const saved = ref(false)
const error = ref('')
const policy = computed<VisionFallbackPolicy>(() => ({
  enabled: enabled.value,
  models: models.value.split('\n').map(model => model.trim()).filter(Boolean),
  allow_unlisted_models: allowUnlisted.value,
  candidate_timeout_seconds: Number(candidateTimeout.value),
  timeout_seconds: Number(totalTimeout.value)
}))
const validationError = computed(() => {
  const value = policy.value
  if (value.enabled && !value.allow_unlisted_models && !value.models.length) return t('admin.settings.visionFallback.required')
  if (new Set(value.models).size !== value.models.length || value.models.length > 64 || value.models.some(model => model.length > 200 || /[\s*]/.test(model))) return t('admin.settings.visionFallback.invalidModels')
  if ([value.candidate_timeout_seconds, value.timeout_seconds].some(seconds => !Number.isSafeInteger(seconds) || seconds <= 0 || seconds > 9223372036)) return t('admin.settings.visionFallback.invalidTimeout')
  return ''
})
watch(policy, () => { saved.value = false }, { flush: 'sync' })

function assign(value: VisionFallbackPolicy) {
  enabled.value = value.enabled
  models.value = value.models.join('\n')
  allowUnlisted.value = value.allow_unlisted_models
  candidateTimeout.value = value.candidate_timeout_seconds
  totalTimeout.value = value.timeout_seconds
}
async function load() {
  loading.value = true
  error.value = ''
  try {
    assign(await getVisionFallbackPolicy())
    loaded.value = true
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
    assign(await updateVisionFallbackPolicy(policy.value))
    saved.value = true
  } catch (err) {
    error.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    saving.value = false
  }
}
onMounted(load)
</script>
