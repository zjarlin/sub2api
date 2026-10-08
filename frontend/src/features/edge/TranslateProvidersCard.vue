<template>
  <section class="min-w-0 space-y-5 py-2" data-testid="translate-providers">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300"><input v-model="form.enabled" type="checkbox" :disabled="!loaded" data-testid="translate-enabled" />{{ t('admin.vision.context.serviceEnabled') }}</label>
      <div class="flex items-center gap-2">
        <button type="button" class="btn btn-icon btn-secondary" :title="t('common.refresh')" :aria-label="t('common.refresh')" :disabled="loading || saving" @click="load"><Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" /></button>
        <button type="button" class="btn btn-primary btn-sm" :disabled="!loaded || saving" data-testid="translate-save" @click="save"><Icon name="check" size="xs" />{{ saving ? t('common.saving') : t('common.save') }}</button>
      </div>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <p v-if="success" role="status" class="text-sm text-emerald-700 dark:text-emerald-400">{{ success }}</p>
    <p v-if="loading" class="py-8 text-sm text-gray-500">{{ t('common.loading') }}</p>
    <div v-else-if="loaded" class="grid min-w-0 gap-6 lg:grid-cols-[210px_minmax(0,1fr)]">
      <nav class="flex gap-1 overflow-x-auto border-b border-gray-200 pb-3 lg:flex-col lg:border-b-0 lg:border-r lg:pb-0 lg:pr-4 dark:border-dark-700" :aria-label="t('admin.vision.catalog.adapters')">
        <button v-for="provider in providers" :key="provider.key" type="button" class="edge-adapter-option flex shrink-0 items-center justify-between gap-3 rounded-md px-3 py-2.5 text-left text-sm" :aria-current="adapter === provider.key ? 'page' : undefined" :data-testid="`translate-adapter-${provider.key}`" @click="emit('select', provider.key)">
          <span>{{ provider.title }}<small class="mt-1 block text-[11px] text-gray-400">{{ t('admin.vision.runtime.' + providerState(provider.key)) }}</small></span>
          <span class="h-1.5 w-1.5 shrink-0 rounded-full" :class="providerState(provider.key) === 'ready' ? 'bg-emerald-500' : 'bg-gray-300'" />
        </button>
      </nav>
      <div class="min-w-0 space-y-6">
        <div class="flex flex-wrap items-start justify-between gap-3 border-b border-gray-200 pb-4 dark:border-dark-700">
          <div><h3 class="text-base font-semibold text-gray-950 dark:text-white">{{ selected.title }}</h3><p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.vision.context.' + adapter + 'Hint') }}</p></div>
          <label class="flex items-center gap-2 text-sm text-gray-600 dark:text-gray-300"><input v-model="form[adapter].enabled" type="checkbox" :data-testid="`translate-provider-${adapter}`" />{{ t('admin.vision.context.adapterEnabled') }}</label>
        </div>
        <div class="grid gap-x-6 gap-y-4 xl:grid-cols-2">
          <label v-for="field in selected.fields" :key="field.key" class="min-w-0 space-y-2">
            <span class="flex items-center justify-between gap-2 text-xs font-medium text-gray-700 dark:text-gray-300"><span>{{ field.label }}</span><span v-if="field.secret && saved[adapter]?.[field.key + '_set']" class="text-emerald-700 dark:text-emerald-400">{{ t('admin.vision.context.configured') }}</span></span>
            <div class="relative">
              <input v-model.trim="form[adapter][field.key]" class="input w-full text-sm" :class="field.secret ? 'pr-10' : ''" :type="field.secret && !revealed[adapter + field.key] ? 'password' : field.type || 'text'" :placeholder="field.secret && saved[adapter]?.[field.key + '_set'] ? t('admin.vision.context.keepSecret') : field.placeholder || field.label" :data-testid="`translate-${adapter}-${field.key}`" :disabled="!form[adapter].enabled || saving" autocomplete="off" spellcheck="false" />
              <button v-if="field.secret" type="button" class="absolute right-2 top-2 p-1 text-gray-400 hover:text-gray-600" :aria-label="t('admin.vision.context.toggleSecret')" :title="t('admin.vision.context.toggleSecret')" @click="revealed[adapter + field.key] = !revealed[adapter + field.key]"><Icon :name="revealed[adapter + field.key] ? 'eyeOff' : 'eye'" size="sm" /></button>
            </div>
            <code class="block break-all text-[11px] text-gray-400 dark:text-gray-500">{{ field.env }}</code>
          </label>
          <p v-if="!selected.fields.length" class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.vision.context.noCredentials') }}</p>
        </div>
        <div class="grid gap-4 border-t border-gray-200 pt-5 sm:grid-cols-2 dark:border-dark-700">
          <label class="space-y-2"><span class="block text-xs font-medium text-gray-700 dark:text-gray-300">{{ t('admin.vision.context.defaultAdapter') }}</span><select :value="defaultAdapter" class="input w-full text-sm" :disabled="!enabledProviders.length || saving" data-testid="translate-default-provider" @change="changeDefault"><option v-for="provider in enabledProviders" :key="provider.key" :value="provider.key">{{ provider.title }}</option></select></label>
          <div class="space-y-2"><span class="block text-xs font-medium text-gray-700 dark:text-gray-300">{{ t('admin.vision.context.selector') }}</span><code class="block rounded-md border border-gray-200 bg-gray-50 px-3 py-2.5 text-xs dark:border-dark-700 dark:bg-dark-900">"provider": "{{ adapter }}"</code></div>
        </div>
        <p class="text-xs leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.vision.context.secretPolicy') }}</p>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { apiClient } from '@/api/client'
import { extractApiErrorMessage } from '@/utils/apiError'
import { EDGE_ADAPTERS, type EdgeAdapter } from './catalog'

interface Context { enabled: boolean; [key: string]: string | boolean }
type Settings = { enabled: boolean; priority: EdgeAdapter[] } & Record<EdgeAdapter, Context>
interface Field { key: string; label: string; env: string; secret?: boolean; placeholder?: string; type?: string }
interface Provider { key: EdgeAdapter; title: string; fields: Field[] }
const props = defineProps<{ adapter: EdgeAdapter }>()
const emit = defineEmits<{ saved: []; select: [adapter: EdgeAdapter] }>()
const { t } = useI18n()
const loading = ref(true)
const loaded = ref(false)
const saving = ref(false)
const error = ref('')
const success = ref('')
const saved = ref<Partial<Record<EdgeAdapter, Context>>>({})
const revealed = reactive<Record<string, boolean>>({})
const providers: Provider[] = [
  { key: 'baidu', title: 'Baidu', fields: [{ key: 'app_id', label: 'APP_ID', env: 'TRANSLATE_BAIDU_APP_ID' }, { key: 'secret', label: 'SECURITY_KEY / Secret', env: 'TRANSLATE_BAIDU_SECRET', secret: true }] },
  { key: 'tencent', title: 'Tencent Cloud', fields: [{ key: 'secret_id', label: 'SecretId', env: 'TRANSLATE_TENCENT_SECRET_ID' }, { key: 'secret_key', label: 'SecretKey', env: 'TRANSLATE_TENCENT_SECRET_KEY', secret: true }, { key: 'region', label: 'Region', env: 'TRANSLATE_TENCENT_REGION', placeholder: 'ap-guangzhou' }] },
  { key: 'youdao', title: 'Youdao', fields: [{ key: 'app_key', label: 'AppKey', env: 'TRANSLATE_YOUDAO_APP_KEY' }, { key: 'app_secret', label: 'AppSecret', env: 'TRANSLATE_YOUDAO_APP_SECRET', secret: true }] },
  { key: 'mymemory', title: 'MyMemory', fields: [{ key: 'email', label: 'Email', env: 'TRANSLATE_MYMEMORY_EMAIL', type: 'email' }, { key: 'api_key', label: 'API Key', env: 'TRANSLATE_MYMEMORY_API_KEY', secret: true }] },
  { key: 'libretranslate', title: 'LibreTranslate', fields: [{ key: 'base_url', label: 'Base URL', env: 'TRANSLATE_LIBRETRANSLATE_URL', type: 'url' }, { key: 'api_key', label: 'API Key', env: 'TRANSLATE_LIBRETRANSLATE_API_KEY', secret: true }] },
  { key: 'hymt', title: 'Hy-MT2', fields: [{ key: 'base_url', label: 'Base URL', env: 'TRANSLATE_HYMT_URL', type: 'url' }, { key: 'api_key', label: 'API Key', env: 'TRANSLATE_HYMT_API_KEY', secret: true }] },
  { key: 'caiyun', title: 'Caiyun', fields: [{ key: 'token', label: 'Token', env: 'TRANSLATE_CAIYUN_TOKEN', secret: true }] },
  { key: 'google_web', title: 'Google Web', fields: [{ key: 'proxy_url', label: 'Proxy URL', env: 'TRANSLATE_GOOGLE_WEB_PROXY_URL', type: 'url' }] },
]
const form = reactive({ enabled: false, priority: [...EDGE_ADAPTERS], ...Object.fromEntries(EDGE_ADAPTERS.map(key => [key, { enabled: false }])) } as Settings)
const selected = computed(() => providers.find(provider => provider.key === props.adapter)!)
const enabledProviders = computed(() => providers.filter(provider => form[provider.key].enabled))
const defaultAdapter = computed(() => form.priority.find(key => form[key]?.enabled))

function providerState(key: EdgeAdapter) {
  const context = saved.value[key]
  const required: Partial<Record<EdgeAdapter, string[]>> = {
    baidu: ['app_id', 'secret_set'], tencent: ['secret_id', 'secret_key_set'], youdao: ['app_key', 'app_secret_set'],
    libretranslate: ['base_url'], hymt: ['base_url'],
  }
  if ((required[key] || []).some(field => !context?.[field])) return 'missing'
  return context?.enabled ? 'ready' : 'inactive'
}

function hydrate(data: Settings) {
  saved.value = data
  form.enabled = data.enabled
  form.priority = [...data.priority]
  for (const provider of providers) {
    const context = data[provider.key]
    form[provider.key] = { enabled: !!context?.enabled }
    for (const field of provider.fields) form[provider.key][field.key] = field.secret ? '' : String(context?.[field.key] ?? '')
  }
  for (const key of Object.keys(revealed)) delete revealed[key]
}

function changeDefault(event: Event) {
  const key = (event.target as HTMLSelectElement).value as EdgeAdapter
  form.priority = [key, ...form.priority.filter(value => value !== key)]
}

async function load() {
  loading.value = true
  loaded.value = false
  error.value = ''
  success.value = ''
  try {
    const { data } = await apiClient.get<Settings>('/admin/settings/translate/providers')
    hydrate(data)
    loaded.value = true
  } catch (cause) { error.value = extractApiErrorMessage(cause, t('admin.vision.context.loadFailed')) }
  finally { loading.value = false }
}

async function save() {
  if (!loaded.value || saving.value) return
  saving.value = true
  error.value = ''
  success.value = ''
  try {
    const payload = { enabled: form.enabled, priority: [...form.priority], ...Object.fromEntries(EDGE_ADAPTERS.map(key => [key, { ...form[key] }])) }
    const { data } = await apiClient.put<Settings>('/admin/settings/translate/providers', payload)
    hydrate(data)
    success.value = t('admin.vision.context.saved')
    emit('saved')
  } catch (cause) { error.value = extractApiErrorMessage(cause, t('admin.vision.context.saveFailed')) }
  finally { saving.value = false }
}

onMounted(load)
</script>
