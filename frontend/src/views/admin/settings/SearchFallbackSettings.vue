<template>
  <section class="card" data-testid="search-fallback-settings">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.searchFallback.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.searchFallback.description') }}</p>
    </div>
    <div class="space-y-4 p-6">
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <p v-if="loading" role="status">{{ t('common.loading') }}</p>
      <button v-if="!policy && !loading" type="button" class="btn btn-secondary" @click="load">{{ t('admin.settings.modelFallback.reload') }}</button>
      <fieldset v-if="policy" :disabled="saving || probing" class="space-y-4">
        <label class="flex items-center justify-between text-sm text-gray-900 dark:text-white"><span>{{ t('admin.settings.searchFallback.enabled') }}</span><input v-model="policy.enabled" type="checkbox" class="h-4 w-4 accent-primary-600" /></label>
        <label class="flex items-center justify-between text-sm text-gray-900 dark:text-white"><span>{{ t('admin.settings.searchFallback.verifiedOnly') }}</span><input v-model="policy.require_verified" type="checkbox" class="h-4 w-4 accent-primary-600" /></label>
        <label class="block text-sm text-gray-900 dark:text-white">{{ t('admin.settings.searchFallback.models') }}<textarea v-model="models" class="input mt-2 w-full font-mono" rows="3" spellcheck="false" /></label>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="block text-sm text-gray-900 dark:text-white">{{ t('admin.settings.visionFallback.candidateTimeout') }}<input v-model.number="policy.candidate_timeout_seconds" class="input mt-2 w-full" type="number" min="1" max="120" /></label>
          <label class="block text-sm text-gray-900 dark:text-white">{{ t('admin.settings.visionFallback.totalTimeout') }}<input v-model.number="policy.timeout_seconds" class="input mt-2 w-full" type="number" min="1" max="300" /></label>
        </div>
        <div class="flex justify-end"><button type="button" class="btn btn-primary" :disabled="!valid" @click="save">{{ saving ? t('common.saving') : t('common.save') }}</button></div>
      </fieldset>
      <p v-if="saved" role="status" class="text-sm text-green-700 dark:text-green-400">{{ t('admin.settings.searchFallback.saved') }}</p>
      <div v-if="policy" class="flex flex-wrap items-end gap-3">
        <label class="text-sm text-gray-900 dark:text-white">{{ t('admin.settings.searchFallback.group') }}<select v-model="groupId" :disabled="probing" class="input mt-2 block"><option :value="0">{{ t('admin.settings.searchFallback.chooseGroup') }}</option><option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }}</option></select></label>
        <button type="button" class="btn btn-secondary" :disabled="!groupId || probing || saving || !valid" @click="probe">{{ t('admin.settings.searchFallback.probe') }}</button>
        <button v-if="probing" type="button" class="btn btn-secondary" @click="cancelProbe">{{ t('common.cancel') }}</button>
        <p v-if="probing" role="status" class="text-sm text-gray-500">{{ completed }} / {{ total }} · {{ currentModel }}</p>
      </div>
      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.searchFallback.probeHint') }}</p>
      <div v-if="policy?.probe_results.length" class="overflow-x-auto">
        <table class="w-full text-left text-sm text-gray-700 dark:text-gray-300">
          <thead><tr class="border-b dark:border-dark-700"><th class="p-2">{{ t('admin.settings.searchFallback.model') }}</th><th class="p-2">{{ t('admin.settings.searchFallback.account') }}</th><th class="p-2">{{ t('admin.settings.searchFallback.result') }}</th><th class="p-2">{{ t('admin.settings.searchFallback.checked') }}</th><th class="p-2">{{ t('admin.settings.searchFallback.sources') }}</th></tr></thead>
          <tbody><tr v-for="result in policy.probe_results" :key="`${result.account_id}:${result.model}`" class="border-b dark:border-dark-700"><td class="p-2"><div class="font-mono">{{ result.model }}</div><div v-if="result.actual_model && result.actual_model !== result.model" class="text-xs text-gray-500">→ {{ result.actual_model }}</div></td><td class="p-2">{{ result.account_name }}</td><td class="p-2"><span :class="result.status === 'supported' ? 'text-green-700 dark:text-green-400' : 'text-amber-700 dark:text-amber-300'">{{ t(`admin.settings.searchFallback.${result.status}`) }}</span><p class="max-w-xs text-xs text-gray-500">{{ result.message }}</p></td><td class="whitespace-nowrap p-2">{{ new Date(result.checked_at).toLocaleString() }}</td><td class="p-2"><a v-for="url in safeSources(result.source_urls)" :key="url" :href="url" target="_blank" rel="noopener noreferrer" class="block max-w-xs truncate text-primary-600 hover:underline">{{ url }}</a></td></tr></tbody>
        </table>
      </div>
    </div>
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getSearchFallbackPolicy, updateSearchFallbackPolicy, getSearchProbeCandidates, probeSearchCapability, type SearchFallbackPolicy } from '@/api/admin/settings'
import { list as listGroups } from '@/api/admin/groups'
import { extractApiErrorMessage } from '@/utils/apiError'
const { t } = useI18n()
const policy = ref<SearchFallbackPolicy>()
const models = ref('')
const groupId = ref(0)
const groups = ref<{ id: number; name: string }[]>([])
const loading = ref(false), saving = ref(false), saved = ref(false), probing = ref(false)
const error = ref(''), currentModel = ref(''), completed = ref(0), total = ref(0)
let probeController: AbortController | undefined
const modelList = computed(() => models.value.split('\n').map(model => model.trim()).filter(Boolean))
const valid = computed(() => policy.value && modelList.value.length <= 256 && new Set(modelList.value).size === modelList.value.length && modelList.value.every(model => model.length <= 200 && !/[\s*]/.test(model)) && Number.isInteger(policy.value.candidate_timeout_seconds) && policy.value.candidate_timeout_seconds >= 1 && policy.value.candidate_timeout_seconds <= 120 && Number.isInteger(policy.value.timeout_seconds) && policy.value.timeout_seconds >= 1 && policy.value.timeout_seconds <= 300)
watch([policy, models], () => { saved.value = false }, { deep: true, flush: 'sync' })
function safeSources(urls: string[]) { return urls.filter(url => /^https?:\/\//i.test(url)) }
async function load() {
  loading.value = true; error.value = ''
  try { policy.value = await getSearchFallbackPolicy(); models.value = policy.value.models.join('\n'); groups.value = (await listGroups(1, 100)).items }
  catch (err) { error.value = extractApiErrorMessage(err, t('common.error')) }
  finally { loading.value = false }
}
async function save() {
  if (!policy.value || !valid.value || saving.value) return
  saving.value = true; error.value = ''; saved.value = false
  try { policy.value = await updateSearchFallbackPolicy({ ...policy.value, models: modelList.value }); saved.value = true }
  catch (err) { error.value = extractApiErrorMessage(err, t('common.error')) }
  finally { saving.value = false }
}
function cancelProbe() { probeController?.abort() }
async function probe() {
  if (!policy.value || !groupId.value || !valid.value || probing.value) return
  probing.value = true; error.value = ''; completed.value = 0; total.value = 0
  probeController = new AbortController()
  try {
    policy.value = await updateSearchFallbackPolicy({ ...policy.value, models: modelList.value })
    const candidates = await getSearchProbeCandidates(groupId.value)
    total.value = candidates.length
    for (const candidate of candidates) {
      if (probeController.signal.aborted) break
      currentModel.value = `${candidate.account_name} · ${candidate.model}`
      await probeSearchCapability(groupId.value, candidate, probeController.signal)
      completed.value++
      policy.value = await getSearchFallbackPolicy()
    }
    if (!probeController.signal.aborted && candidates.length) {
      policy.value.require_verified = true
      policy.value.models = [...new Set(policy.value.probe_results.filter(result => result.status === 'supported').map(result => result.model))]
      policy.value = await updateSearchFallbackPolicy(policy.value)
      models.value = policy.value.models.join('\n')
      saved.value = true
    }
  } catch (err) { if (!probeController.signal.aborted) error.value = extractApiErrorMessage(err, t('common.error')) }
  finally { probing.value = false; currentModel.value = '' }
}
onMounted(load)
onBeforeUnmount(cancelProbe)
</script>
