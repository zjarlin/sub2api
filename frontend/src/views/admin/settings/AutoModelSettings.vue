<template>
  <section class="card" data-testid="auto-model-settings">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.autoModel.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.autoModel.description') }}</p>
    </div>
    <div class="space-y-4 p-6">
      <p v-if="loading" role="status">{{ t('common.loading') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <button v-if="!loaded && !loading" type="button" class="btn btn-secondary" @click="load">{{ t('admin.settings.modelFallback.reload') }}</button>
      <fieldset v-if="loaded" :disabled="saving || loading" class="space-y-3">
        <label for="auto-model-blacklist" class="block text-sm font-medium">{{ t('admin.settings.autoModel.blacklist') }}</label>
        <textarea id="auto-model-blacklist" v-model="blacklistText" class="input w-full font-mono text-sm" rows="4" spellcheck="false" aria-describedby="auto-model-blacklist-hint" />
        <p id="auto-model-blacklist-hint" class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.autoModel.hint') }}</p>
        <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
          <label class="flex items-center gap-2"><input v-model="verticalRouting.enabled" type="checkbox" data-testid="vertical-enabled" />{{ t('admin.settings.autoModel.verticalEnabled') }}</label>
          <div class="mt-3 grid gap-3 sm:grid-cols-2">
            <label class="block text-sm">{{ t('admin.settings.autoModel.confidence') }}<input v-model.number="verticalRouting.min_confidence" type="number" min="0.8" max="1" step="0.01" class="input mt-1 w-full" /></label>
            <label class="block text-sm">{{ t('admin.settings.autoModel.decisionTimeout') }}<input v-model.number="verticalRouting.timeout_ms" type="number" min="200" max="5000" step="100" class="input mt-1 w-full" /></label>
            <label class="block text-sm">{{ t('admin.settings.autoModel.imageModel') }}<input v-model.trim="verticalRouting.image_model" class="input mt-1 w-full" spellcheck="false" /></label>
            <label class="block text-sm">{{ t('admin.settings.autoModel.videoModel') }}<input v-model.trim="verticalRouting.video_model" class="input mt-1 w-full" spellcheck="false" /></label>
            <label class="block text-sm sm:col-span-2">{{ t('admin.settings.autoModel.imageFallbackModels') }}<textarea v-model="imageFallbackText" data-testid="image-fallback-models" class="input mt-1 w-full font-mono text-sm" rows="3" spellcheck="false" /></label>
          </div>
        </div>
        <p v-if="validationError" role="alert" class="text-sm text-amber-700 dark:text-amber-300">{{ validationError }}</p>
        <button type="button" class="btn btn-primary" :disabled="!!validationError" @click="save">{{ saving ? t('common.saving') : t('common.save') }}</button>
        <p v-if="saved" role="status" class="text-sm text-green-700 dark:text-green-400">{{ t('admin.settings.autoModel.saved') }}</p>
      </fieldset>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getAutoModelPolicy, updateAutoModelPolicy, type AutoModelPolicy } from '@/api/admin/settings'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const blacklistText = ref('')
const imageFallbackText = ref('')
const loading = ref(false)
const saving = ref(false)
const loaded = ref(false)
const saved = ref(false)
const error = ref('')
const defaultVerticalRouting = { enabled: true, min_confidence: 0.8, timeout_ms: 1500, image_model: '', video_model: '' }
const verticalRouting = ref({ ...defaultVerticalRouting })
const policy = computed<AutoModelPolicy>(() => ({
  blacklist: blacklistText.value.split(/[\s,，]+/).filter(Boolean),
  vertical_routing: { ...verticalRouting.value, image_fallback_models: imageFallbackText.value.split(/[\s,，]+/).filter(Boolean) },
}))
const validationError = computed(() => {
  const rules = policy.value.blacklist
  if (rules.length > 128 || new Set(rules.map(rule => rule.toLowerCase())).size !== rules.length
    || rules.some(rule => rule.length > 200 || /[?\[\]\\]/.test(rule) || rule.replace(/\*$/, '').includes('*'))) {
    return t('admin.settings.autoModel.invalidRules')
  }
  const vertical = verticalRouting.value
  const fallbacks = policy.value.vertical_routing?.image_fallback_models ?? []
  if (fallbacks.length > 4 || new Set(fallbacks).size !== fallbacks.length
    || fallbacks.some(model => model.length > 512 || /[\s*]/.test(model))) {
    return t('admin.settings.autoModel.invalidImageFallbacks')
  }
  if (!Number.isFinite(vertical.min_confidence) || vertical.min_confidence < 0.8 || vertical.min_confidence > 1
    || !Number.isInteger(vertical.timeout_ms) || vertical.timeout_ms < 200 || vertical.timeout_ms > 5000
    || [vertical.image_model, vertical.video_model].some(model => model.length > 512 || /[\s*]/.test(model))) {
    return t('admin.settings.autoModel.invalidVertical')
  }
  return ''
})

watch(blacklistText, () => { saved.value = false }, { flush: 'sync' })
watch(imageFallbackText, () => { saved.value = false }, { flush: 'sync' })
watch(verticalRouting, () => { saved.value = false }, { deep: true, flush: 'sync' })

async function load() {
  loading.value = true
  error.value = ''
  try {
    const result = await getAutoModelPolicy()
    blacklistText.value = result.blacklist.join('\n')
    verticalRouting.value = { ...defaultVerticalRouting, ...result.vertical_routing }
    imageFallbackText.value = (result.vertical_routing?.image_fallback_models ?? []).join('\n')
    loaded.value = true
  } catch (err) {
    error.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    loading.value = false
  }
}

async function save() {
  if (saving.value || validationError.value) {
    return
  }
  saving.value = true
  saved.value = false
  error.value = ''
  try {
    const result = await updateAutoModelPolicy(policy.value)
    blacklistText.value = result.blacklist.join('\n')
    verticalRouting.value = { ...defaultVerticalRouting, ...result.vertical_routing }
    imageFallbackText.value = (result.vertical_routing?.image_fallback_models ?? []).join('\n')
    saved.value = true
  } catch (err) {
    error.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
