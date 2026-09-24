<template>
  <AppLayout>
    <div class="mx-auto min-w-0 max-w-7xl space-y-6 overflow-x-hidden pb-10">
      <header class="card overflow-hidden">
        <div class="grid gap-0 lg:grid-cols-[minmax(0,1.25fr)_minmax(320px,0.75fr)]">
          <div class="border-b-2 border-black p-6 dark:border-white lg:border-b-0 lg:border-r-2">
            <div class="flex flex-wrap items-center gap-2">
              <span class="badge badge-primary">EDGE / DECISION / VISION</span>
              <span :class="status?.enabled ? 'badge badge-success' : 'badge badge-gray'">
                {{ status?.enabled ? t('admin.vision.statusEnabled') : t('admin.vision.statusDisabled') }}
              </span>
            </div>
            <h1 class="mt-4 text-3xl font-black tracking-tight text-gray-950 dark:text-white">{{ t('admin.vision.title') }}</h1>
            <p class="mt-2 max-w-3xl text-sm leading-6 text-gray-700 dark:text-gray-300">{{ t('admin.vision.description') }}</p>
            <div class="mt-5 flex flex-wrap gap-2">
              <button type="button" class="btn btn-secondary" :disabled="loading" @click="loadStatus">
                <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
                {{ t('common.refresh') }}
              </button>
              <button type="button" class="btn btn-primary" @click="usePreset('vision')">
                <Icon name="eye" size="sm" />
                {{ t('admin.vision.presets.vision') }}
              </button>
              <button type="button" class="btn btn-secondary" @click="usePreset('jev')">
                <Icon name="brain" size="sm" />
                {{ t('admin.vision.presets.jev') }}
              </button>
              <button type="button" class="btn btn-secondary" @click="usePreset('laya')">
                <Icon name="cpu" size="sm" />
                {{ t('admin.vision.presets.laya') }}
              </button>
            </div>
          </div>
          <div class="grid content-start gap-3 bg-black/5 p-6 dark:bg-white/5">
            <div>
              <span class="input-label">{{ t('admin.vision.gatewayBase') }}</span>
              <code class="mt-1 block break-all rounded-md border-2 border-black bg-white px-3 py-2 text-xs text-gray-900 dark:border-white dark:bg-dark-900 dark:text-gray-100">{{ baseUrl }}</code>
            </div>
            <div class="grid grid-cols-2 gap-3">
              <div class="rounded-md border-2 border-black bg-white p-3 dark:border-white dark:bg-dark-900">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.vision.visualStatus') }}</span>
                <strong class="mt-1 block text-lg">{{ status?.enabled ? t('admin.vision.statusEnabled') : t('admin.vision.statusDisabled') }}</strong>
              </div>
              <div class="rounded-md border-2 border-black bg-white p-3 dark:border-white dark:bg-dark-900">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.vision.layaStatus') }}</span>
                <strong class="mt-1 block text-lg">{{ status?.laya_enabled ? t('admin.vision.statusEnabled') : t('admin.vision.statusDisabled') }}</strong>
              </div>
            </div>
            <p v-if="error" role="alert" class="rounded-md border-2 border-red-700 bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/40 dark:text-red-200">{{ error }}</p>
            <p v-else-if="status && !status.enabled" class="text-xs leading-5 text-gray-600 dark:text-gray-400">{{ t('admin.vision.disabledHint') }}</p>
          </div>
        </div>
      </header>

      <section class="card">
        <div class="card-header flex flex-wrap items-center justify-between gap-3">
          <div>
            <span class="text-xs font-black uppercase text-primary-700 dark:text-primary-300">01 / IMPORT</span>
            <h2 class="mt-1 text-xl font-bold text-gray-950 dark:text-white">{{ t('admin.vision.curl.title') }}</h2>
            <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.vision.curl.description') }}</p>
          </div>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="!curlInput.trim()" @click="curlInput = ''">
            <Icon name="trash" size="xs" />
            {{ t('common.clear') }}
          </button>
        </div>
        <div class="card-body space-y-4">
          <textarea
            v-model="curlInput"
            rows="7"
            class="input w-full whitespace-pre font-mono text-xs leading-5"
            data-testid="edge-curl-input"
            spellcheck="false"
            :placeholder="t('admin.vision.curl.placeholder')"
          />
          <div class="flex flex-wrap items-center gap-3">
            <button type="button" class="btn btn-primary" :disabled="!curlInput.trim()" @click="importCurl">
              <Icon name="terminal" size="sm" />
              {{ t('admin.vision.curl.parse') }}
            </button>
            <span v-if="parseError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ parseError }}</span>
            <span v-else-if="parsedFromCurl" class="text-sm text-emerald-700 dark:text-emerald-300">{{ t('admin.vision.curl.parsed') }}</span>
          </div>
          <p v-if="parseWarnings.length" class="rounded-md border-2 border-amber-600 bg-amber-50 p-3 text-xs text-amber-900 dark:bg-amber-950/30 dark:text-amber-200">
            {{ t('admin.vision.curl.warnings', { count: parseWarnings.length }) }}
          </p>
        </div>
      </section>

      <div class="grid gap-6 xl:grid-cols-[minmax(0,1.35fr)_minmax(360px,0.65fr)]">
        <section class="card min-w-0">
          <div class="card-header">
            <span class="text-xs font-black uppercase text-primary-700 dark:text-primary-300">02 / REQUEST</span>
            <h2 class="mt-1 text-xl font-bold text-gray-950 dark:text-white">{{ t('admin.vision.request.title') }}</h2>
            <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.vision.request.description') }}</p>
          </div>
          <div class="card-body">
            <EdgeRequestEditor
              v-model:method="request.method"
              v-model:url="request.url"
              v-model:headers="request.headers"
              v-model:query="request.query"
              v-model:body="request.body"
              v-model:body-mode="request.bodyMode"
            />
          </div>
        </section>

        <section class="card min-w-0">
          <div class="card-header">
            <span class="text-xs font-black uppercase text-primary-700 dark:text-primary-300">03 / REGISTER</span>
            <h2 class="mt-1 text-xl font-bold text-gray-950 dark:text-white">{{ t('admin.vision.register.title') }}</h2>
            <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.vision.register.description') }}</p>
          </div>
          <div class="card-body space-y-5">
            <div>
              <label for="edge-group" class="input-label">{{ t('admin.vision.register.group') }}</label>
              <select id="edge-group" v-model.number="selectedGroupID" class="input" data-testid="edge-group-select" @change="loadGroupModels">
                <option :value="0">{{ t('admin.vision.register.selectGroup') }}</option>
                <option v-for="group in groups" :key="group.id" :value="group.id">
                  {{ group.name }} · {{ group.platform }} · #{{ group.id }}
                </option>
              </select>
            </div>

            <div class="grid gap-3 sm:grid-cols-2">
              <label class="space-y-1.5">
                <span class="input-label">{{ t('admin.vision.register.model') }}</span>
                <input v-model.trim="selectedModel" class="input font-mono text-xs" data-testid="edge-model-input" spellcheck="false" :placeholder="t('admin.vision.register.modelPlaceholder')" />
              </label>
              <label class="space-y-1.5">
                <span class="input-label">{{ t('admin.vision.register.gatewayPath') }}</span>
                <select v-model="gatewayPath" class="input font-mono text-xs">
                  <option value="">{{ t('admin.vision.register.keepImportedPath') }}</option>
                  <option value="/v1/chat/completions">/v1/chat/completions</option>
                  <option value="/v1/responses">/v1/responses</option>
                  <option value="/v1/systemone">/v1/systemone</option>
                  <option value="/vision/detect">/vision/detect</option>
                </select>
              </label>
            </div>

            <div class="flex flex-wrap gap-2">
              <button type="button" class="btn btn-secondary btn-sm" :disabled="!request.model" @click="selectedModel = request.model">
                {{ t('admin.vision.register.useImportedModel') }}
              </button>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="!selectedModel || loadingModels" @click="loadGroupModels">
                <Icon name="refresh" size="xs" :class="loadingModels ? 'animate-spin' : ''" />
                {{ t('admin.vision.register.loadCandidates') }}
              </button>
            </div>

            <div class="rounded-md border-2 border-black bg-gray-50 p-3 dark:border-white dark:bg-dark-900">
              <div class="flex items-center justify-between gap-3">
                <span class="text-xs font-bold uppercase text-gray-700 dark:text-gray-300">{{ t('admin.vision.register.availableModels') }}</span>
                <span class="text-xs text-gray-500 dark:text-gray-400">{{ candidateModels.length }}</span>
              </div>
              <div v-if="loadingModels" class="mt-3 text-sm text-gray-500">{{ t('common.loading') }}</div>
              <p v-else-if="!selectedGroupID" class="mt-3 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.vision.register.chooseGroupFirst') }}</p>
              <p v-else-if="candidateModels.length === 0" class="mt-3 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.vision.register.noCandidates') }}</p>
              <div v-else class="mt-3 max-h-56 space-y-2 overflow-y-auto pr-1">
                <label
                  v-for="model in candidateModels"
                  :key="model"
                  class="flex items-center gap-2 rounded border border-gray-200 bg-white px-2.5 py-2 text-xs dark:border-dark-600 dark:bg-dark-800"
                  :title="t('admin.vision.register.selectCandidateHint')"
                  @click="selectCandidateModel(model)"
                >
                  <input v-model="selectedCandidateModels" type="checkbox" class="h-4 w-4 accent-primary-600" :value="model" />
                  <code class="min-w-0 flex-1 break-all">{{ model }}</code>
                </label>
              </div>
            </div>

            <label class="flex items-start gap-2 text-xs text-gray-600 dark:text-gray-400">
              <input v-model="replaceAllowlist" type="checkbox" class="mt-0.5 h-4 w-4 accent-primary-600" />
              <span>{{ t('admin.vision.register.replaceHint') }}</span>
            </label>
            <p v-if="registerError" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ registerError }}</p>
            <p v-if="registerSuccess" role="status" class="text-sm text-emerald-700 dark:text-emerald-300">{{ registerSuccess }}</p>

            <div class="flex flex-wrap gap-2">
              <button type="button" class="btn btn-primary" :disabled="!canRegister || registering" @click="registerModels">
                <Icon name="plus" size="sm" />
                {{ registering ? t('common.saving') : t('admin.vision.register.register') }}
              </button>
              <button type="button" class="btn btn-secondary" :disabled="!generatedCurl" @click="copyGeneratedCurl">
                <Icon name="document" size="sm" />
                {{ t('admin.vision.register.copyCurl') }}
              </button>
            </div>
          </div>
        </section>
      </div>

      <section class="card min-w-0">
        <div class="card-header flex flex-wrap items-center justify-between gap-3">
          <div>
            <span class="text-xs font-black uppercase text-primary-700 dark:text-primary-300">04 / OUTPUT</span>
            <h2 class="mt-1 text-xl font-bold text-gray-950 dark:text-white">{{ t('admin.vision.output.title') }}</h2>
            <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.vision.output.description') }}</p>
          </div>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="!generatedCurl" @click="copyGeneratedCurl">
            <Icon name="document" size="xs" />
            {{ t('common.copy') }}
          </button>
        </div>
        <div class="card-body">
          <pre class="max-h-[420px] overflow-auto rounded-md border-2 border-black bg-gray-950 p-4 text-xs leading-5 text-gray-100 dark:border-white"><code>{{ generatedCurl || t('admin.vision.output.empty') }}</code></pre>
        </div>
      </section>

      <div class="grid gap-6 lg:grid-cols-2">
        <section class="card">
          <div class="card-header">
            <h2 class="text-lg font-bold text-gray-950 dark:text-white">{{ t('admin.vision.endpoints') }}</h2>
          </div>
          <div class="overflow-x-auto">
            <table class="table">
              <thead>
                <tr>
                  <th>{{ t('admin.vision.colEndpoint') }}</th>
                  <th>{{ t('admin.vision.colMethod') }}</th>
                  <th>{{ t('admin.vision.colDescription') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="endpoint in endpoints" :key="endpoint.path">
                  <td><code class="text-xs text-primary-700 dark:text-primary-300">{{ endpoint.path }}</code></td>
                  <td><span class="badge badge-primary">{{ endpoint.method }}</span></td>
                  <td>{{ endpoint.description }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>

        <section class="card">
          <div class="card-header">
            <h2 class="text-lg font-bold text-gray-950 dark:text-white">{{ t('admin.vision.billingTitle') }}</h2>
          </div>
          <div class="card-body space-y-3 text-sm text-gray-700 dark:text-gray-300">
            <p>{{ t('admin.vision.billingDesc') }}</p>
            <ul class="list-disc space-y-1 pl-5">
              <li>{{ t('admin.vision.billingPoint1') }}</li>
              <li>{{ t('admin.vision.billingPoint2') }}</li>
            </ul>
            <div class="border-t-2 border-black pt-3 dark:border-white">
              <p><strong>{{ t('admin.vision.jevTitle') }}</strong>: {{ t('admin.vision.jevBilling') }}</p>
              <p class="mt-2"><strong>{{ t('admin.vision.layaTitle') }}</strong>: {{ t('admin.vision.layaBilling') }}</p>
            </div>
          </div>
        </section>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { apiClient, buildGatewayUrl } from '@/api/client'
import { adminAPI } from '@/api/admin'
import { useClipboard } from '@/composables/useClipboard'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { AdminGroup } from '@/types'
import EdgeRequestEditor from '@/features/edge/EdgeRequestEditor.vue'
import {
  applyGatewayPath,
  buildEdgeCurl,
  parseEdgeCurl,
  replaceBodyModel,
  sanitizeHeaders,
  type EdgeBodyMode,
  type EdgeKeyValue,
  type EdgeRequestMethod,
} from '@/features/edge/curl'

interface VisionStatus {
  enabled: boolean
  laya_enabled: boolean
}

type Preset = 'vision' | 'jev' | 'laya'

const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const loading = ref(true)
const error = ref('')
const status = ref<VisionStatus | null>(null)
const baseUrl = new URL(buildGatewayUrl('/v1/systemone')).origin
const groups = ref<AdminGroup[]>([])
const selectedGroupID = ref(0)
const selectedModel = ref('')
const selectedCandidateModels = ref<string[]>([])
const candidateModels = ref<string[]>([])
const loadingModels = ref(false)
const registering = ref(false)
const registerError = ref('')
const registerSuccess = ref('')
const replaceAllowlist = ref(false)
const gatewayPath = ref('/v1/systemone')
const curlInput = ref('')
const parsedFromCurl = ref(false)
const parseError = ref('')
const parseWarnings = ref<string[]>([])

const request = reactive<{
  method: EdgeRequestMethod
  url: string
  headers: EdgeKeyValue[]
  query: EdgeKeyValue[]
  body: string
  bodyMode: EdgeBodyMode
  model: string
}>({
  method: 'POST',
  url: buildGatewayUrl('/v1/systemone'),
  headers: [{ name: 'Content-Type', value: 'application/json' }],
  query: [],
  body: JSON.stringify({
    model: 'laya',
    state: 'Payments failed for three days.',
    questions: {
      urgent: { type: 'noul', instructions: 'Does this need urgent attention?' },
    },
  }, null, 2),
  bodyMode: 'json',
  model: 'laya',
})

const endpoints = computed(() => [
  { path: '/vision/detect', method: 'POST', description: t('admin.vision.endpointDetect') },
  { path: '/vision/segment', method: 'POST', description: t('admin.vision.endpointSegment') },
  { path: '/vision/pose', method: 'POST', description: t('admin.vision.endpointPose') },
  { path: '/vision/classify', method: 'POST', description: t('admin.vision.endpointClassify') },
  { path: '/vision/ocr', method: 'POST', description: t('admin.vision.endpointOcr') },
])

const generatedCurl = computed(() => buildEdgeCurl({
  gatewayOrigin: baseUrl,
  method: request.method,
  url: gatewayPath.value ? applyGatewayPath(request.url, baseUrl, gatewayPath.value) : request.url,
  headers: sanitizeHeaders(request.headers, '$SUB2API_KEY'),
  query: request.query,
  body: request.body,
  bodyMode: request.bodyMode,
  apiKeyPlaceholder: '$SUB2API_KEY',
}))

const canRegister = computed(() => selectedGroupID.value > 0
  && selectedModel.value.trim() !== ''
  && candidateModels.value.length > 0)

function presetRequest(preset: Preset) {
  if (preset === 'vision') {
    gatewayPath.value = '/vision/detect'
    return {
      method: 'POST' as EdgeRequestMethod,
      url: buildGatewayUrl('/vision/detect'),
      headers: [] as EdgeKeyValue[],
      query: [] as EdgeKeyValue[],
      body: 'image=@photo.jpg&confidence_threshold=0.5',
      bodyMode: 'form' as EdgeBodyMode,
      model: '',
    }
  }
  const model = preset === 'jev' ? 'typesafe/jev' : 'laya'
  gatewayPath.value = '/v1/systemone'
  return {
    method: 'POST' as EdgeRequestMethod,
    url: buildGatewayUrl('/v1/systemone'),
    headers: [{ name: 'Content-Type', value: 'application/json' }],
    query: [] as EdgeKeyValue[],
    body: JSON.stringify({
      model,
      state: 'Payments failed for three days.',
      questions: {
        urgent: { type: 'noul', instructions: 'Does this need urgent attention?' },
      },
    }, null, 2),
    bodyMode: 'json' as EdgeBodyMode,
    model,
  }
}

function applyRequest(value: ReturnType<typeof presetRequest>) {
  request.method = value.method
  request.url = value.url
  request.headers = value.headers
  request.query = value.query
  request.body = value.body
  request.bodyMode = value.bodyMode
  request.model = value.model
  selectedModel.value = value.model
}

function usePreset(preset: Preset) {
  applyRequest(presetRequest(preset))
}

function importCurl() {
  parseError.value = ''
  registerError.value = ''
  registerSuccess.value = ''
  try {
    const parsed = parseEdgeCurl(curlInput.value)
    request.method = parsed.method
    request.url = parsed.url
    request.headers = sanitizeHeaders(parsed.headers)
    request.query = parsed.query
    request.body = parsed.body
    request.bodyMode = parsed.bodyMode
    request.model = parsed.model
    selectedModel.value = parsed.model
    parseWarnings.value = parsed.warnings
    parsedFromCurl.value = true
    gatewayPath.value = gatewayPathForUrl(parsed.url)
  } catch (cause) {
    parsedFromCurl.value = false
    parseWarnings.value = []
    parseError.value = cause instanceof Error ? cause.message : t('admin.vision.curl.parseFailed')
  }
}

function gatewayPathForUrl(urlText: string): string {
  try {
    const path = new URL(urlText).pathname
    if (path.startsWith('/v1/')) return path
    if (path.startsWith('/vision/')) return path
  } catch {
    return ''
  }
  return ''
}

async function loadStatus() {
  loading.value = true
  error.value = ''
  try {
    const { data } = await apiClient.get<VisionStatus>('/admin/vision/status')
    status.value = data
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, t('admin.vision.statusError'))
  } finally {
    loading.value = false
  }
}

async function loadGroups() {
  try {
    groups.value = await adminAPI.groups.getAll()
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, t('admin.vision.statusError'))
  }
}

async function loadGroupModels() {
  registerError.value = ''
  registerSuccess.value = ''
  candidateModels.value = []
  selectedCandidateModels.value = []
  if (!selectedGroupID.value) return
  const group = groups.value.find(item => item.id === selectedGroupID.value)
  if (!group) return
  loadingModels.value = true
  try {
    candidateModels.value = await adminAPI.groups.getModelAllowlistCandidates(group.id, group.platform)
    if (selectedModel.value && !selectedCandidateModels.value.includes(selectedModel.value)) {
      selectedCandidateModels.value = [selectedModel.value]
    }
  } catch (cause) {
    registerError.value = extractApiErrorMessage(cause, t('admin.vision.register.loadFailed'))
  } finally {
    loadingModels.value = false
  }
}

async function registerModels() {
  if (!canRegister.value || registering.value) return
  const group = groups.value.find(item => item.id === selectedGroupID.value)
  if (!group) return
  registering.value = true
  registerError.value = ''
  registerSuccess.value = ''
  try {
    const candidates = candidateModels.value
    const existing = group.model_allowlist?.models ?? []
    const selected = new Set<string>(replaceAllowlist.value
      ? selectedCandidateModels.value
      : [...existing, ...selectedCandidateModels.value])
    selected.add(selectedModel.value.trim())
    const models = candidates.filter(model => selected.has(model))
    for (const model of selected) {
      if (!models.includes(model)) models.push(model)
    }
    await adminAPI.groups.update(group.id, {
      model_allowlist: { enabled: true, models },
    })
    group.model_allowlist = { enabled: true, models }
    registerSuccess.value = t('admin.vision.register.saved', { count: models.length })
  } catch (cause) {
    registerError.value = extractApiErrorMessage(cause, t('admin.vision.register.saveFailed'))
  } finally {
    registering.value = false
  }
}

function selectCandidateModel(model: string) {
  selectedModel.value = model
  request.model = model
  if (request.bodyMode === 'json') {
    request.body = replaceBodyModel(request.body, model)
  }
}

async function copyGeneratedCurl() {
  await copyToClipboard(generatedCurl.value, t('common.copiedToClipboard'))
}

onMounted(async () => {
  await Promise.all([loadStatus(), loadGroups()])
})
</script>
