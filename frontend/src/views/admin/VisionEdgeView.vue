<template>
  <AppLayout>
    <div class="mx-auto min-w-0 max-w-7xl space-y-6 overflow-x-hidden pb-10">
      <header class="overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
        <div class="grid gap-0 lg:grid-cols-[minmax(0,1.25fr)_minmax(320px,0.75fr)]">
          <div class="border-b border-gray-200 p-6 dark:border-dark-700 lg:border-b-0 lg:border-r">
            <div class="flex flex-wrap items-center gap-2">
              <span class="badge badge-primary">EDGE / VISION / MEDIA / DECISION</span>
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
              <button type="button" class="btn btn-primary" @click="openPreset('vision')">
                <Icon name="eye" size="sm" />
                {{ t('admin.vision.presets.vision') }}
              </button>
              <button type="button" class="btn btn-secondary" @click="openPreset('jev')">
                <Icon name="brain" size="sm" />
                {{ t('admin.vision.presets.jev') }}
              </button>
              <button type="button" class="btn btn-secondary" @click="openPreset('laya')">
                <Icon name="cpu" size="sm" />
                {{ t('admin.vision.presets.laya') }}
              </button>
            </div>
          </div>
          <div class="grid content-start gap-3 bg-gray-50 p-6 dark:bg-dark-950/40">
            <div>
              <span class="input-label">{{ t('admin.vision.gatewayBase') }}</span>
              <code class="mt-1 block break-all rounded-lg border border-gray-200 bg-white px-3 py-2 text-xs text-gray-900 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-100">{{ baseUrl }}</code>
            </div>
            <div class="grid grid-cols-3 gap-2">
              <div class="rounded-lg border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-900">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.vision.visualStatus') }}</span>
                <strong class="mt-1 block text-sm">{{ status?.enabled ? t('admin.vision.statusEnabled') : t('admin.vision.statusDisabled') }}</strong>
              </div>
              <div class="rounded-lg border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-900">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.vision.layaStatus') }}</span>
                <strong class="mt-1 block text-sm">{{ status?.laya_enabled ? t('admin.vision.statusEnabled') : t('admin.vision.statusDisabled') }}</strong>
              </div>
              <div class="rounded-lg border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-900">
                <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.vision.mediaStatus') }}</span>
                <strong class="mt-1 block text-sm">{{ status?.media_enabled ? t('admin.vision.statusEnabled') : t('admin.vision.statusDisabled') }}</strong>
              </div>
            </div>
            <p v-if="error" role="alert" class="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-200">{{ error }}</p>
            <p v-else-if="status && !status.enabled" class="text-xs leading-5 text-gray-600 dark:text-gray-400">{{ t('admin.vision.disabledHint') }}</p>
          </div>
        </div>
      </header>

      <section class="card min-w-0">
        <div class="card-header flex flex-wrap items-end justify-between gap-3">
          <div>
            <span class="text-xs font-black uppercase text-primary-700 dark:text-primary-300">01 / MATRIX</span>
            <h2 class="mt-1 text-xl font-bold text-gray-950 dark:text-white">{{ t('admin.vision.endpointMatrix') }}</h2>
            <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.vision.endpointMatrixDescription') }}</p>
          </div>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="loadStatus">
            <Icon name="refresh" size="xs" :class="loading ? 'animate-spin' : ''" />
            {{ t('common.refresh') }}
          </button>
        </div>
        <div class="card-body space-y-8">
          <div v-for="group in endpointGroups" :key="group.key" class="space-y-3">
            <div class="flex flex-wrap items-end justify-between gap-2 border-b border-gray-200 pb-2 dark:border-dark-700">
              <div>
                <h3 class="text-sm font-black uppercase tracking-wide text-gray-950 dark:text-white">{{ group.title }}</h3>
                <p class="text-xs text-gray-500 dark:text-gray-400">{{ group.description }}</p>
              </div>
              <span class="font-mono text-xs text-gray-400">{{ group.endpoints.length }} endpoints</span>
            </div>
            <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
              <button v-for="endpoint in group.endpoints" :key="endpoint.key" type="button" class="group flex min-h-40 min-w-0 flex-col justify-between rounded-lg border-2 p-4 text-left transition-colors" :class="[endpointEnabled(endpoint) ? 'border-gray-200 bg-white hover:border-primary-500 dark:border-dark-700 dark:bg-dark-900 dark:hover:border-primary-500' : 'border-gray-200 bg-gray-50 text-gray-500 dark:border-dark-700 dark:bg-dark-900/60 dark:text-gray-400', selectedEndpointKey === endpoint.key ? 'ring-2 ring-primary-500 ring-offset-2 dark:ring-offset-dark-950' : '']" :data-testid="`edge-endpoint-${endpoint.key}`" @click="openEndpoint(endpoint)">
                <span class="flex w-full items-start justify-between gap-3">
                  <span class="flex min-w-0 flex-col gap-2">
                    <span class="badge badge-primary w-fit">{{ endpoint.method }}</span>
                    <code class="break-all text-sm font-bold text-gray-950 dark:text-white">{{ endpoint.path }}</code>
                  </span>
                  <span class="h-3 w-3 shrink-0 rounded-full" :class="endpointEnabled(endpoint) ? 'bg-emerald-500' : 'bg-gray-400'" />
                </span>
                <span class="mt-4 flex min-w-0 flex-col gap-2">
                  <span class="text-sm leading-5 text-gray-700 dark:text-gray-300">{{ endpoint.description }}</span>
                  <span class="flex items-center justify-between gap-2 text-xs font-semibold text-primary-700 dark:text-primary-300">
                    {{ endpointStatusLabel(endpoint) }}
                    <Icon name="arrowRight" size="sm" class="transition-transform group-hover:translate-x-0.5" />
                  </span>
                </span>
              </button>
            </div>
          </div>
        </div>
      </section>

      <section v-if="detailOpen" class="card min-w-0" data-testid="edge-request-detail">
        <div class="card-header flex flex-wrap items-center justify-between gap-3">
          <div class="min-w-0">
            <span class="text-xs font-black uppercase text-primary-700 dark:text-primary-300">02 / REQUEST</span>
            <div class="mt-1 flex flex-wrap items-center gap-2">
              <span class="badge badge-primary">{{ selectedEndpoint.method }}</span>
              <code class="break-all text-lg font-black text-gray-950 dark:text-white">{{ selectedEndpoint.path }}</code>
            </div>
            <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.vision.detailDescription') }}</p>
          </div>
          <button type="button" class="btn btn-secondary btn-icon" :title="t('admin.vision.closeDetail')" @click="closeDetail">
            <Icon name="x" size="sm" />
          </button>
        </div>
        <div class="card-body space-y-5">
          <div class="grid gap-5 xl:grid-cols-[minmax(0,1fr)_360px]">
            <section class="min-w-0 space-y-4">
              <div class="flex flex-wrap items-center gap-2">
                <button type="button" class="btn btn-secondary btn-sm" @click="usePreset(selectedEndpoint.preset)">
                  <Icon name="refresh" size="xs" />
                  {{ t('admin.vision.exampleRequest') }}
                </button>
                <button type="button" class="btn btn-primary btn-sm" :disabled="sending || !selectedAPIKey" @click="sendRequest">
                  <Icon name="play" size="xs" />
                  {{ sending ? t('admin.vision.sending') : t('admin.vision.sendRequest') }}
                </button>
                <button type="button" class="btn btn-secondary btn-sm" :disabled="!generatedCurl" @click="copyGeneratedCurl">
                  <Icon name="document" size="xs" />
                  {{ t('common.copy') }}
                </button>
              </div>
              <div class="grid gap-3 sm:grid-cols-[130px_minmax(0,1fr)]">
                <label class="space-y-1.5">
                  <span class="input-label">{{ t('admin.vision.workbench.method') }}</span>
                  <select v-model="request.method" class="input font-mono" data-testid="edge-request-method">
                    <option v-for="method in ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']" :key="method" :value="method">{{ method }}</option>
                  </select>
                </label>
                <label class="space-y-1.5">
                  <span class="input-label">{{ t('admin.vision.workbench.url') }}</span>
                  <input v-model.trim="request.url" class="input font-mono text-xs" data-testid="edge-request-url" spellcheck="false" />
                </label>
              </div>
              <EdgeRequestEditor
                v-model:method="request.method"
                v-model:url="request.url"
                v-model:headers="request.headers"
                v-model:query="request.query"
                v-model:body="request.body"
                v-model:body-mode="request.bodyMode"
              />
            </section>
            <aside class="min-w-0 space-y-4">
              <section class="rounded-lg border border-gray-200 p-4 dark:border-dark-700">
                <h3 class="text-sm font-bold text-gray-950 dark:text-white">{{ t('admin.vision.apiKeyLabel') }}</h3>
                <select v-model.number="selectedAPIKeyID" class="input mt-2 w-full text-xs" data-testid="edge-api-key-select" :disabled="loadingKeys || apiKeys.length === 0">
                  <option :value="0">{{ loadingKeys ? t('common.loading') : t('admin.vision.selectApiKey') }}</option>
                  <option v-for="apiKey in apiKeys" :key="apiKey.id" :value="apiKey.id">{{ apiKey.name }} · {{ apiKey.key.slice(0, 8) }}...{{ apiKey.key.slice(-4) }}</option>
                </select>
                <p v-if="!loadingKeys && apiKeys.length === 0" class="mt-2 text-xs text-amber-700 dark:text-amber-300">{{ t('admin.vision.noApiKeys') }}</p>
              </section>
              <section class="rounded-lg border border-gray-800 bg-gray-950 p-4 text-gray-100">
                <div class="flex items-center justify-between gap-3">
                  <div>
                    <span class="text-xs font-black uppercase text-primary-300">03 / RESPONSE</span>
                    <h3 class="mt-1 text-sm font-bold">{{ t('admin.vision.responseTitle') }}</h3>
                  </div>
                  <Icon :name="response?.ok ? 'check' : response || sendError ? 'exclamationCircle' : 'terminal'" size="sm" :class="response?.ok ? 'text-emerald-400' : response || sendError ? 'text-red-400' : 'text-gray-400'" />
                </div>
                <div v-if="response" class="mt-3 grid grid-cols-3 gap-2 text-xs">
                  <div class="rounded bg-white/5 p-2"><span class="block text-gray-400">{{ t('admin.vision.responseStatus') }}</span><strong>{{ response.status }} {{ response.statusText }}</strong></div>
                  <div class="rounded bg-white/5 p-2"><span class="block text-gray-400">{{ t('admin.vision.responseDuration') }}</span><strong>{{ response.durationMs }} ms</strong></div>
                  <div class="rounded bg-white/5 p-2"><span class="block text-gray-400">{{ t('admin.vision.responseSize') }}</span><strong>{{ response.size }} B</strong></div>
                </div>
                <p v-if="sendError" role="alert" class="mt-3 rounded border border-red-800 bg-red-950/50 p-3 text-xs text-red-200">{{ sendError }}</p>
                <pre v-else-if="response" class="mt-3 max-h-[520px] overflow-auto whitespace-pre-wrap break-words rounded bg-black/40 p-3 text-xs leading-5">{{ response.body }}</pre>
                <p v-else class="mt-3 text-xs leading-5 text-gray-400">{{ t('admin.vision.responseEmpty') }}</p>
              </section>
              <section v-if="response && Object.keys(response.headers).length" class="rounded-lg border border-gray-200 p-4 dark:border-dark-700">
                <h3 class="text-sm font-bold text-gray-950 dark:text-white">{{ t('admin.vision.responseHeaders') }}</h3>
                <dl class="mt-2 space-y-1 text-xs">
                  <div v-for="(value, name) in response.headers" :key="name" class="grid grid-cols-[minmax(90px,0.8fr)_minmax(0,1fr)] gap-2">
                    <dt class="break-all font-mono text-gray-500 dark:text-gray-400">{{ name }}</dt>
                    <dd class="break-all font-mono text-gray-800 dark:text-gray-200">{{ value }}</dd>
                  </div>
                </dl>
              </section>
            </aside>
          </div>
          <details class="rounded-lg border border-gray-200 p-4 dark:border-dark-700">
            <summary class="cursor-pointer text-sm font-bold text-gray-950 dark:text-white">{{ t('admin.vision.curl.title') }}</summary>
            <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.vision.curl.description') }}</p>
            <textarea v-model="curlInput" rows="4" class="input mt-3 w-full whitespace-pre font-mono text-xs" data-testid="edge-curl-input" spellcheck="false" :placeholder="t('admin.vision.curl.placeholder')" />
            <div class="mt-2 flex flex-wrap items-center gap-2">
              <button type="button" class="btn btn-secondary btn-sm" :disabled="!curlInput.trim()" @click="importCurl">{{ t('admin.vision.curl.parse') }}</button>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="!curlInput.trim()" @click="curlInput = ''">{{ t('common.clear') }}</button>
              <span v-if="parsedFromCurl" class="text-xs text-emerald-700 dark:text-emerald-300">{{ t('admin.vision.curl.parsed') }}</span>
            </div>
            <p v-if="parseError" role="alert" class="mt-2 text-sm text-red-600 dark:text-red-400">{{ parseError }}</p>
            <p v-if="parseWarnings.length" class="mt-2 text-xs text-amber-700 dark:text-amber-300">{{ t('admin.vision.curl.warnings', { count: parseWarnings.length }) }}</p>
          </details>
        </div>
      </section>

      <div class="grid gap-6 xl:grid-cols-[minmax(0,1.35fr)_minmax(360px,0.65fr)]">
        <section class="card min-w-0">
          <div class="card-header">
            <span class="text-xs font-black uppercase text-primary-700 dark:text-primary-300">04 / REGISTER</span>
            <h2 class="mt-1 text-xl font-bold text-gray-950 dark:text-white">{{ t('admin.vision.register.title') }}</h2>
            <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.vision.register.description') }}</p>
          </div>
          <div class="card-body space-y-5">
            <div>
              <label for="edge-group" class="input-label">{{ t('admin.vision.register.group') }}</label>
              <select id="edge-group" v-model.number="selectedGroupID" class="input" data-testid="edge-group-select" @change="loadGroupModels">
                <option :value="0">{{ t('admin.vision.register.selectGroup') }}</option>
                <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }} · {{ group.platform }} · #{{ group.id }}</option>
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
              <button type="button" class="btn btn-secondary btn-sm" :disabled="!request.model" @click="selectedModel = request.model">{{ t('admin.vision.register.useImportedModel') }}</button>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="!selectedModel || loadingModels" @click="loadGroupModels">
                <Icon name="refresh" size="xs" :class="loadingModels ? 'animate-spin' : ''" />
                {{ t('admin.vision.register.loadCandidates') }}
              </button>
            </div>
            <div class="rounded-lg border border-gray-200 bg-gray-50 p-3 dark:border-dark-700 dark:bg-dark-900">
              <div class="flex items-center justify-between gap-3">
                <span class="text-xs font-bold uppercase text-gray-700 dark:text-gray-300">{{ t('admin.vision.register.availableModels') }}</span>
                <span class="text-xs text-gray-500 dark:text-gray-400">{{ candidateModels.length }}</span>
              </div>
              <div v-if="loadingModels" class="mt-3 text-sm text-gray-500">{{ t('common.loading') }}</div>
              <p v-else-if="!selectedGroupID" class="mt-3 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.vision.register.chooseGroupFirst') }}</p>
              <p v-else-if="candidateModels.length === 0" class="mt-3 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.vision.register.noCandidates') }}</p>
              <div v-else class="mt-3 max-h-56 space-y-2 overflow-y-auto pr-1">
                <label v-for="model in candidateModels" :key="model" class="flex items-center gap-2 rounded border border-gray-200 bg-white px-2.5 py-2 text-xs dark:border-dark-600 dark:bg-dark-800" :title="t('admin.vision.register.selectCandidateHint')" @click="selectCandidateModel(model)">
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

        <section class="card min-w-0">
          <div class="card-header flex flex-wrap items-center justify-between gap-3">
            <div>
              <span class="text-xs font-black uppercase text-primary-700 dark:text-primary-300">05 / OUTPUT</span>
              <h2 class="mt-1 text-xl font-bold text-gray-950 dark:text-white">{{ t('admin.vision.output.title') }}</h2>
              <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.vision.output.description') }}</p>
            </div>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!generatedCurl" @click="copyGeneratedCurl">
              <Icon name="document" size="xs" />
              {{ t('common.copy') }}
            </button>
          </div>
          <div class="card-body space-y-5">
            <pre class="max-h-64 overflow-auto rounded-lg border border-gray-800 bg-gray-950 p-4 text-xs leading-5 text-gray-100"><code>{{ generatedCurl || t('admin.vision.output.empty') }}</code></pre>
            <div class="space-y-3 border-t border-gray-200 pt-4 text-sm text-gray-700 dark:border-dark-700 dark:text-gray-300">
              <h3 class="font-bold text-gray-950 dark:text-white">{{ t('admin.vision.billingTitle') }}</h3>
              <p>{{ t('admin.vision.billingDesc') }}</p>
              <ul class="list-disc space-y-1 pl-5">
                <li>{{ t('admin.vision.billingPoint1') }}</li>
                <li>{{ t('admin.vision.billingPoint2') }}</li>
              </ul>
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
import { keysAPI } from '@/api'
import { useClipboard } from '@/composables/useClipboard'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { AdminGroup, ApiKey } from '@/types'
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
  media_enabled: boolean
}

type Preset = 'vision' | 'jev' | 'laya' | 'manbo' | 'video-dub' | 'video-generation'
type EndpointCategory = 'vision' | 'media' | 'decision'

interface EndpointDefinition {
  key: string
  path: string
  method: EdgeRequestMethod
  description: string
  category: EndpointCategory
  preset: Preset
}

interface EndpointResponse {
  status: number
  statusText: string
  durationMs: number
  size: number
  headers: Record<string, string>
  body: string
  ok: boolean
}

const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const loading = ref(true)
const error = ref('')
const status = ref<VisionStatus | null>(null)
const baseUrl = new URL(buildGatewayUrl('/v1/systemone')).origin
const groups = ref<AdminGroup[]>([])
const apiKeys = ref<ApiKey[]>([])
const selectedAPIKeyID = ref(0)
const loadingKeys = ref(false)
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
const selectedEndpointKey = ref('detect')
const detailOpen = ref(false)
const sending = ref(false)
const response = ref<EndpointResponse | null>(null)
const sendError = ref('')

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

const endpoints = computed<EndpointDefinition[]>(() => [
  { key: 'detect', path: '/vision/detect', method: 'POST', description: t('admin.vision.endpointDetect'), category: 'vision', preset: 'vision' },
  { key: 'segment', path: '/vision/segment', method: 'POST', description: t('admin.vision.endpointSegment'), category: 'vision', preset: 'vision' },
  { key: 'pose', path: '/vision/pose', method: 'POST', description: t('admin.vision.endpointPose'), category: 'vision', preset: 'vision' },
  { key: 'classify', path: '/vision/classify', method: 'POST', description: t('admin.vision.endpointClassify'), category: 'vision', preset: 'vision' },
  { key: 'ocr', path: '/vision/ocr', method: 'POST', description: t('admin.vision.endpointOcr'), category: 'vision', preset: 'vision' },
  { key: 'tts', path: '/media/tts', method: 'POST', description: t('admin.vision.endpointManboTts'), category: 'media', preset: 'manbo' },
  { key: 'dub', path: '/media/videos/dub', method: 'POST', description: t('admin.vision.endpointVideoDub'), category: 'media', preset: 'video-dub' },
  { key: 'generation', path: '/media/videos/generations', method: 'POST', description: t('admin.vision.endpointVideoGeneration'), category: 'media', preset: 'video-generation' },
  { key: 'jev', path: '/v1/systemone', method: 'POST', description: t('admin.vision.jevDescription'), category: 'decision', preset: 'jev' },
  { key: 'laya', path: '/v1/systemone', method: 'POST', description: t('admin.vision.layaDescription'), category: 'decision', preset: 'laya' },
])

const endpointGroups = computed(() => {
  const categories: Array<{ key: EndpointCategory; title: string; description: string }> = [
    { key: 'vision', title: t('admin.vision.categoryVision'), description: t('admin.vision.categoryVisionDescription') },
    { key: 'media', title: t('admin.vision.categoryMedia'), description: t('admin.vision.categoryMediaDescription') },
    { key: 'decision', title: t('admin.vision.categoryDecision'), description: t('admin.vision.categoryDecisionDescription') },
  ]
  return categories.map(category => ({
    ...category,
    endpoints: endpoints.value.filter(endpoint => endpoint.category === category.key),
  }))
})

const selectedEndpoint = computed(() => endpoints.value.find(endpoint => endpoint.key === selectedEndpointKey.value) ?? endpoints.value[0])
const selectedAPIKey = computed(() => apiKeys.value.find(apiKey => apiKey.id === selectedAPIKeyID.value) ?? null)

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

function endpointEnabled(endpoint: EndpointDefinition): boolean {
  if (!status.value) return false
  if (endpoint.category === 'vision') return status.value.enabled
  if (endpoint.category === 'media') return status.value.media_enabled
  return status.value.laya_enabled
}

function endpointStatusLabel(endpoint: EndpointDefinition): string {
  return endpointEnabled(endpoint) ? t('admin.vision.statusEnabled') : t('admin.vision.statusDisabled')
}

function openEndpoint(endpoint: EndpointDefinition) {
  selectedEndpointKey.value = endpoint.key
  usePreset(endpoint.preset)
  gatewayPath.value = endpoint.path
  request.method = endpoint.method
  request.url = buildGatewayUrl(endpoint.path)
  detailOpen.value = true
  response.value = null
  sendError.value = ''
}

function closeDetail() {
  detailOpen.value = false
}

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
  if (preset === 'manbo') {
    gatewayPath.value = '/media/tts'
    return {
      method: 'POST' as EdgeRequestMethod,
      url: buildGatewayUrl('/media/tts'),
      headers: [{ name: 'Content-Type', value: 'application/json' }],
      query: [] as EdgeKeyValue[],
      body: JSON.stringify({ text: '你好，我是曼波。', language: 'zh', response_format: 'wav' }, null, 2),
      bodyMode: 'json' as EdgeBodyMode,
      model: '',
    }
  }
  if (preset === 'video-dub') {
    gatewayPath.value = '/media/videos/dub'
    return {
      method: 'POST' as EdgeRequestMethod,
      url: buildGatewayUrl('/media/videos/dub'),
      headers: [] as EdgeKeyValue[],
      query: [] as EdgeKeyValue[],
      body: 'video=@input.mp4&options={}',
      bodyMode: 'form' as EdgeBodyMode,
      model: '',
    }
  }
  if (preset === 'video-generation') {
    gatewayPath.value = '/media/videos/generations'
    return {
      method: 'POST' as EdgeRequestMethod,
      url: buildGatewayUrl('/media/videos/generations'),
      headers: [{ name: 'Content-Type', value: 'application/json' }],
      query: [] as EdgeKeyValue[],
      body: JSON.stringify({ model: 'edge-video', prompt: '一台机器人在天津海边散步，电影感镜头' }, null, 2),
      bodyMode: 'json' as EdgeBodyMode,
      model: 'edge-video',
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

function openPreset(preset: Preset) {
  const endpoint = endpoints.value.find(item => item.preset === preset)
  if (endpoint) {
    openEndpoint(endpoint)
    return
  }
  usePreset(preset)
  selectedEndpointKey.value = 'detect'
  detailOpen.value = true
  response.value = null
  sendError.value = ''
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
    if (path.startsWith('/media/')) return path
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

async function loadAPIKeys() {
  loadingKeys.value = true
  try {
    const data = await keysAPI.list(1, 100, { status: 'active', sort_by: 'created_at', sort_order: 'desc' })
    apiKeys.value = data.items
    if (!selectedAPIKeyID.value && data.items.length > 0) {
      selectedAPIKeyID.value = data.items[0].id
    }
  } catch (cause) {
    sendError.value = extractApiErrorMessage(cause, t('admin.vision.requestFailed'))
  } finally {
    loadingKeys.value = false
  }
}

function buildRequestURL(): string {
  const url = new URL(request.url, baseUrl)
  request.query.forEach(({ name, value }) => {
    if (name.trim()) url.searchParams.set(name.trim(), value)
  })
  return url.toString()
}

function buildRequestHeaders(): Headers {
  const headers = new Headers()
  request.headers.forEach(({ name, value }) => {
    const normalized = name.trim()
    if (normalized && !/^authorization$/i.test(normalized) && !/^x-api-key$/i.test(normalized)) {
      headers.set(normalized, value)
    }
  })
  headers.set('Authorization', `Bearer ${selectedAPIKey.value?.key ?? ''}`)
  return headers
}

function buildRequestBody(): BodyInit | undefined {
  if (request.method === 'GET' || request.bodyMode === 'none' || !request.body.trim()) return undefined
  if (request.bodyMode === 'json') return request.body
  const form = new FormData()
  request.body.split('&').forEach((part) => {
    const separator = part.indexOf('=')
    const name = separator >= 0 ? part.slice(0, separator) : part
    const value = separator >= 0 ? part.slice(separator + 1) : ''
    if (name.trim()) form.append(name.trim(), value)
  })
  return form
}

async function sendRequest() {
  if (!selectedAPIKey.value || sending.value) return
  sending.value = true
  response.value = null
  sendError.value = ''
  const startedAt = performance.now()
  try {
    const headers = buildRequestHeaders()
    const responseBody = buildRequestBody()
    if (responseBody instanceof FormData) {
      headers.delete('Content-Type')
    } else if (request.bodyMode === 'json' && !headers.has('Content-Type')) {
      headers.set('Content-Type', 'application/json')
    }
    const result = await fetch(buildRequestURL(), {
      method: request.method,
      headers,
      body: responseBody,
    })
    const text = await result.text()
    const responseHeaders: Record<string, string> = {}
    result.headers.forEach((value, name) => { responseHeaders[name] = value })
    response.value = {
      status: result.status,
      statusText: result.statusText,
      durationMs: Math.round(performance.now() - startedAt),
      size: new Blob([text]).size,
      headers: responseHeaders,
      body: text,
      ok: result.ok,
    }
  } catch (cause) {
    sendError.value = cause instanceof Error ? cause.message : t('admin.vision.requestFailed')
  } finally {
    sending.value = false
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
  await Promise.all([loadStatus(), loadGroups(), loadAPIKeys()])
})
</script>
