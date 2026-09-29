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
const loading = ref(false)
const saving = ref(false)
const loaded = ref(false)
const saved = ref(false)
const error = ref('')
const policy = computed<AutoModelPolicy>(() => ({
  blacklist: blacklistText.value.split(/[\s,，]+/).filter(Boolean),
}))
const validationError = computed(() => {
  const rules = policy.value.blacklist
  if (rules.length > 128 || new Set(rules.map(rule => rule.toLowerCase())).size !== rules.length
    || rules.some(rule => rule.length > 200 || /[?\[\]\\]/.test(rule) || rule.replace(/\*$/, '').includes('*'))) {
    return t('admin.settings.autoModel.invalidRules')
  }
  return ''
})

watch(blacklistText, () => { saved.value = false }, { flush: 'sync' })

async function load() {
  loading.value = true
  error.value = ''
  try {
    const result = await getAutoModelPolicy()
    blacklistText.value = result.blacklist.join('\n')
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
    saved.value = true
  } catch (err) {
    error.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
