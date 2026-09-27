<template>
  <AppLayout>
    <div class="mx-auto min-w-0 max-w-7xl space-y-6 overflow-x-hidden pb-10">
      <header class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 pb-4 dark:border-dark-700">
        <h1 class="text-xl font-bold text-gray-950 dark:text-white">{{ t('admin.vision.title') }}</h1>
        <div class="flex flex-wrap items-center gap-2 text-xs">
          <span class="badge" :class="status?.enabled ? 'badge-success' : 'badge-gray'">{{ t('admin.vision.visualStatus') }}</span>
          <span class="badge" :class="status?.media_enabled ? 'badge-success' : 'badge-gray'">{{ t('admin.vision.mediaStatus') }}</span>
          <span class="badge" :class="status?.laya_enabled ? 'badge-success' : 'badge-gray'">{{ t('admin.vision.layaStatus') }}</span>
          <button type="button" class="btn btn-icon btn-secondary h-8 w-8" :title="t('common.refresh')" :aria-label="t('common.refresh')" :disabled="loading" @click="loadStatus">
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
          </button>
        </div>
        <p v-if="error" role="alert" class="w-full text-sm text-red-600">{{ error }}</p>
      </header>

      <section class="overflow-hidden border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
        <div class="flex items-center gap-3 border-b border-gray-200 px-4 py-3 dark:border-dark-700">
          <select class="input min-w-0 w-full text-xs sm:hidden" :value="selectedEndpointKey" :aria-label="t('admin.vision.endpoints')" @change="selectEndpointFromMenu">
            <option v-for="endpoint in endpoints" :key="endpoint.key" :value="endpoint.key">{{ endpoint.path }}</option>
          </select>
          <div class="hidden min-w-0 flex-1 items-center overflow-x-auto sm:flex">
            <button
              v-for="endpoint in endpoints"
              :key="endpoint.key"
              type="button"
              class="flex shrink-0 items-center gap-2 border-b-2 px-3 py-2 text-xs font-semibold transition-colors"
              :class="selectedEndpointKey === endpoint.key ? 'border-primary-500 text-primary-700 dark:text-primary-300' : 'border-transparent text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white'"
              :data-testid="`edge-endpoint-${endpoint.key}`"
              @click="openEndpoint(endpoint)"
            >
              <span class="h-2 w-2 rounded-full" :class="endpointEnabled(endpoint) ? 'bg-emerald-500' : 'bg-gray-400'" />
              {{ endpoint.method }}
              <code class="max-w-48 truncate">{{ endpoint.path }}</code>
            </button>
          </div>
          <span class="hidden shrink-0 items-center gap-1 text-xs text-gray-500 sm:flex dark:text-gray-400">
            <span class="h-2 w-2 rounded-full" :class="status?.enabled ? 'bg-emerald-500' : 'bg-gray-400'" />
            {{ status?.enabled ? t('admin.vision.statusEnabled') : t('admin.vision.statusDisabled') }}
          </span>
        </div>

        <div class="grid min-h-[680px] lg:grid-cols-[260px_minmax(0,1fr)]">
          <aside class="hidden lg:block border-b border-gray-200 bg-gray-50/70 p-3 lg:border-b-0 lg:border-r dark:border-dark-700 dark:bg-dark-950/30">
            <div class="flex items-center justify-between px-2 py-1">
              <span class="text-xs font-black uppercase text-gray-500 dark:text-gray-400">{{ t('admin.vision.endpoints') }}</span>
              <button type="button" class="btn btn-icon btn-secondary h-8 w-8" :title="t('common.refresh')" :disabled="loading" @click="loadStatus">
                <Icon name="refresh" size="xs" :class="loading ? 'animate-spin' : ''" />
              </button>
            </div>
            <div class="mt-2 space-y-4">
              <div v-for="group in endpointGroups" :key="group.key">
                <div class="px-2 text-xs font-bold uppercase text-gray-400">{{ group.title }}</div>
                <div class="mt-1 space-y-1">
                  <button
                    v-for="endpoint in group.endpoints"
                    :key="endpoint.key"
                    type="button"
                    class="flex w-full items-start gap-2 rounded-lg px-2 py-2 text-left transition-colors"
                    :class="selectedEndpointKey === endpoint.key ? 'bg-primary-50 text-primary-800 dark:bg-primary-950/40 dark:text-primary-200' : 'text-gray-700 hover:bg-white dark:text-gray-300 dark:hover:bg-dark-800'"
                    @click="openEndpoint(endpoint)"
                  >
                    <span class="mt-1 h-2 w-2 shrink-0 rounded-full" :class="endpointEnabled(endpoint) ? 'bg-emerald-500' : 'bg-gray-400'" />
                    <span class="min-w-0">
                      <code class="block break-all text-xs font-semibold">{{ endpoint.path }}</code>
                      <span class="mt-0.5 block text-xs leading-4 text-gray-500 dark:text-gray-400">{{ endpoint.description }}</span>
                    </span>
                  </button>
                </div>
              </div>
            </div>
          </aside>

          <main class="min-w-0" data-testid="edge-request-detail">
            <div class="border-b border-gray-200 p-4 dark:border-dark-700">
              <div class="flex flex-wrap items-center gap-2">
                <span class="badge badge-primary">{{ request.method }}</span>
                <input v-model.trim="request.url" class="input min-w-0 flex-1 font-mono text-xs" data-testid="edge-request-url" spellcheck="false" />
                <button type="button" class="btn btn-primary" :disabled="sending || !selectedAPIKey" data-testid="edge-send-request" @click="sendRequest">
                  <Icon name="play" size="sm" />
                  {{ sending ? t('admin.vision.sending') : t('admin.vision.sendRequest') }}
                </button>
              </div>
              <div class="mt-3 flex flex-wrap items-center gap-2">
                <select v-model.number="selectedAPIKeyID" class="input max-w-72 text-xs" data-testid="edge-api-key-select" :disabled="loadingKeys || apiKeys.length === 0">
                  <option :value="0">{{ loadingKeys ? t('common.loading') : t('admin.vision.selectApiKey') }}</option>
                  <option v-for="apiKey in apiKeys" :key="apiKey.id" :value="apiKey.id">{{ apiKey.name }} · {{ apiKey.key.slice(0, 8) }}...{{ apiKey.key.slice(-4) }}</option>
                </select>
                <label v-if="selectedEndpoint.category === 'vision'" class="btn btn-secondary cursor-pointer">
                  <input class="sr-only" type="file" accept="image/*" data-testid="edge-vision-image" @change="selectVisionImage" />
                  <Icon name="upload" size="sm" />
                  {{ visionImageName || t('admin.vision.chooseImage') }}
                </label>
                <button type="button" class="btn btn-secondary" @click="usePreset(selectedEndpoint.preset)">
                  <Icon name="refresh" size="sm" />
                  {{ t('admin.vision.exampleRequest') }}
                </button>
              </div>
              <p v-if="visionImageError" role="alert" class="mt-2 text-xs text-red-600 dark:text-red-400">{{ visionImageError }}</p>
              <p v-if="!loadingKeys && apiKeys.length === 0" class="mt-2 text-xs text-amber-700 dark:text-amber-300">{{ t('admin.vision.noApiKeys') }}</p>
            </div>

            <div class="grid min-h-[520px] xl:grid-cols-[minmax(0,1fr)_minmax(360px,0.8fr)]">
              <section class="min-w-0 border-b border-gray-200 p-4 xl:border-b-0 xl:border-r dark:border-dark-700">
                <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 dark:border-dark-700">
                  <div class="flex gap-1">
                    <button
                      v-for="tab in requestTabs"
                      :key="tab.value"
                      type="button"
                      class="border-b-2 px-3 py-2 text-xs font-semibold"
                      :class="requestTab === tab.value ? 'border-primary-500 text-primary-700 dark:text-primary-300' : 'border-transparent text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white'"
                      @click="requestTab = tab.value"
                    >
                      {{ t(tab.label) }}
                      <span v-if="tab.count" class="ml-1 rounded-full bg-gray-100 px-1.5 py-0.5 text-[10px] text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ tab.count }}</span>
                    </button>
                  </div>
                  <button v-if="selectedEndpointKey !== 'dub'" type="button" class="btn btn-secondary btn-sm" :disabled="requestTab !== 'body' || request.bodyMode !== 'json' || !request.body.trim()" @click="formatRequestBody">
                    <Icon name="terminal" size="xs" />
                    {{ t('admin.vision.workbench.formatJson') }}
                  </button>
                </div>

                <div v-if="requestTab === 'params'" class="mt-3 space-y-2">
                  <KeyValueEditor v-model="request.query" :name-placeholder="t('admin.vision.workbench.queryName')" :value-placeholder="t('admin.vision.workbench.queryValue')" @add="addQuery" />
                </div>

                <div v-else-if="requestTab === 'headers'" class="mt-3 space-y-2">
                  <KeyValueEditor v-model="request.headers" :name-placeholder="t('admin.vision.workbench.headerName')" :value-placeholder="t('admin.vision.workbench.headerValue')" @add="addHeader" />
                </div>

                <div v-else-if="requestTab === 'docs'" class="mt-3">
                  <DubbingDocs v-if="selectedEndpointKey === 'dub'" :base-url="baseUrl" />
                  <p v-else class="text-sm leading-6">{{ t('admin.vision.mediaResult.generationDocs') }}</p>
                </div>

                <div v-else-if="selectedEndpointKey === 'dub'" class="mt-3">
                  <DubbingForm v-model="dubbingOptions" v-model:file="dubbingFile" v-model:duration="dubbingDuration" />
                </div>

                <div v-else class="mt-3 space-y-3">
                  <div class="flex flex-wrap gap-1">
                    <button
                      v-for="mode in bodyModes"
                      :key="mode"
                      type="button"
                      class="btn btn-sm"
                      :class="request.bodyMode === mode ? 'btn-primary' : 'btn-secondary'"
                      @click="request.bodyMode = mode"
                    >
                      {{ t(`admin.vision.workbench.bodyMode.${mode}`) }}
                    </button>
                  </div>
                  <textarea
                    v-if="request.bodyMode !== 'none'"
                    v-model="request.body"
                    rows="18"
                    class="input min-h-[360px] w-full whitespace-pre font-mono text-xs leading-5"
                    data-testid="edge-request-body"
                    spellcheck="false"
                    :placeholder="request.bodyMode === 'form' ? t('admin.vision.workbench.formPlaceholder') : t('admin.vision.workbench.jsonPlaceholder')"
                  />
                </div>
              </section>

              <section class="min-w-0 bg-gray-50/50 p-4 dark:bg-dark-950/20">
                <div class="flex items-center justify-between gap-3">
                  <div>
                    <h2 class="text-sm font-bold text-gray-950 dark:text-white">{{ t('admin.vision.responseTitle') }}</h2>
                    <p v-if="response" class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ response.durationMs }} ms · {{ response.size }} B</p>
                  </div>
                  <span v-if="response" class="badge" :class="response.ok ? 'badge-success' : 'badge-danger'">{{ response.status }} {{ response.statusText }}</span>
                </div>
                <p v-if="sendError" role="alert" class="mt-3 rounded-lg border border-red-200 bg-red-50 p-3 text-xs text-red-700 dark:border-red-900 dark:bg-red-950/40 dark:text-red-200">{{ sendError }}</p>
                <div v-if="loadingMedia" class="mt-3 text-xs text-gray-500">{{ t('admin.vision.mediaResult.loading') }}</div>
                <div v-if="media" class="mt-3 space-y-3" data-testid="edge-media-result">
                  <video v-if="media.type.startsWith('video/')" :src="media.url" controls class="max-h-80 w-full bg-black" />
                  <audio v-else-if="media.type.startsWith('audio/')" :src="media.url" controls class="w-full" />
                  <a :href="media.url" :download="media.name" class="btn btn-secondary" data-testid="edge-media-download"><Icon name="download" size="sm" />{{ t('admin.vision.mediaResult.download') }}</a>
                  <span class="ml-2 text-xs text-gray-500">{{ media.size }} B</span>
                </div>
                <div v-if="selectedEndpointKey === 'dub'" class="mt-3 flex flex-wrap gap-2">
                  <input v-model.trim="taskID" :aria-label="t('admin.vision.mediaResult.task')" :placeholder="t('admin.vision.mediaResult.task')" class="input min-w-0 flex-1 text-xs" data-testid="edge-task-id" />
                  <button type="button" class="btn btn-secondary btn-sm" :disabled="sending || !selectedAPIKey || !/^[a-zA-Z0-9_-]+$/.test(taskID)" @click="queryTask"><Icon name="refresh" size="xs" />{{ t('admin.vision.mediaResult.query') }}</button>
                </div>
                <pre v-if="response?.body" class="mt-3 max-h-[430px] overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-950 p-3 text-xs leading-5 text-gray-100">{{ response.body }}</pre>
                <div v-else-if="!response && !sendError" class="mt-3 flex min-h-64 items-center justify-center rounded-lg border border-dashed border-gray-300 text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400">
                  {{ t('admin.vision.responseEmpty') }}
                </div>
              </section>
            </div>
          </main>
        </div>
      </section>

      <section class="card min-w-0">
        <div class="card-header flex flex-wrap items-center justify-between gap-3">
          <div>
            <span class="text-xs font-black uppercase text-primary-700 dark:text-primary-300">02 / CODE</span>
            <h2 class="mt-1 text-xl font-bold text-gray-950 dark:text-white">{{ t('admin.vision.output.title') }}</h2>
            <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.vision.output.description') }}</p>
          </div>
          <div class="flex items-center gap-2">
            <select v-model="codeLanguage" class="input w-40 text-xs">
              <option v-for="language in EDGE_CODE_LANGUAGES" :key="language.value" :value="language.value">{{ language.label }}</option>
            </select>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!generatedCode" @click="copyGeneratedCode">
              <Icon name="document" size="xs" />
              {{ t('common.copy') }}
            </button>
          </div>
        </div>
        <div class="card-body">
          <pre class="max-h-[480px] overflow-auto rounded-lg border border-gray-800 bg-gray-950 p-4 text-xs leading-5 text-gray-100"><code>{{ generatedCode || t('admin.vision.output.empty') }}</code></pre>
        </div>
      </section>

      <details class="card min-w-0">
        <summary class="flex cursor-pointer list-none items-center justify-between gap-3 px-6 py-4">
          <span>
            <span class="block text-xs font-black uppercase text-primary-700 dark:text-primary-300">03 / ADVANCED</span>
            <span class="mt-1 block text-lg font-bold text-gray-950 dark:text-white">{{ t('admin.vision.curl.title') }}</span>
          </span>
          <Icon name="chevronDown" size="sm" />
        </summary>
        <div class="card-body grid gap-4 border-t border-gray-200 pt-4 dark:border-dark-700 lg:grid-cols-2">
          <div>
            <label class="input-label" for="edge-curl-input">{{ t('admin.vision.curl.title') }}</label>
            <textarea id="edge-curl-input" v-model="curlInput" rows="8" class="input mt-2 w-full whitespace-pre font-mono text-xs" data-testid="edge-curl-input" :placeholder="t('admin.vision.curl.placeholder')" />
            <div class="mt-2 flex items-center gap-2">
              <button type="button" class="btn btn-secondary btn-sm" @click="importCurl">{{ t('admin.vision.curl.parse') }}</button>
              <span v-if="parsedFromCurl" class="text-xs text-emerald-700 dark:text-emerald-300">{{ t('admin.vision.curl.parsed') }}</span>
            </div>
            <p v-if="parseError" class="mt-2 text-xs text-red-600 dark:text-red-400">{{ parseError }}</p>
            <p v-if="parseWarnings.length" class="mt-2 text-xs text-amber-700 dark:text-amber-300">{{ t('admin.vision.curl.warnings', { count: parseWarnings.length }) }}</p>
          </div>
          <section>
            <h3 class="text-sm font-bold text-gray-950 dark:text-white">{{ t('admin.vision.register.title') }}</h3>
            <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.vision.register.description') }}</p>
            <div class="mt-3 grid gap-3 sm:grid-cols-2">
              <label class="space-y-1.5">
                <span class="input-label">{{ t('admin.vision.register.group') }}</span>
                <select v-model.number="selectedGroupID" class="input w-full text-xs" data-testid="edge-group-select" @change="loadGroupModels">
                  <option :value="0">{{ t('admin.vision.register.selectGroup') }}</option>
                  <option v-for="candidateGroup in groups" :key="candidateGroup.id" :value="candidateGroup.id">{{ candidateGroup.name }}</option>
                </select>
              </label>
              <label class="space-y-1.5">
                <span class="input-label">{{ t('admin.vision.register.model') }}</span>
                <input v-model.trim="selectedModel" class="input w-full text-xs" data-testid="edge-model-input" :placeholder="t('admin.vision.register.modelPlaceholder')" />
              </label>
            </div>
            <div class="mt-3 flex flex-wrap gap-2">
              <button type="button" class="btn btn-secondary btn-sm" :disabled="!request.model" @click="selectedModel = request.model">{{ t('admin.vision.register.useImportedModel') }}</button>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="!selectedModel || loadingModels" @click="loadGroupModels">
                <Icon name="refresh" size="xs" :class="loadingModels ? 'animate-spin' : ''" />
                {{ t('admin.vision.register.loadCandidates') }}
              </button>
            </div>
            <div class="mt-3 max-h-44 space-y-1 overflow-y-auto rounded-lg border border-gray-200 p-2 dark:border-dark-700">
              <p v-if="loadingModels" class="p-2 text-xs text-gray-500">{{ t('common.loading') }}</p>
              <p v-else-if="!selectedGroupID" class="p-2 text-xs text-gray-500">{{ t('admin.vision.register.chooseGroupFirst') }}</p>
              <p v-else-if="candidateModels.length === 0" class="p-2 text-xs text-gray-500">{{ t('admin.vision.register.noCandidates') }}</p>
              <template v-else>
                <label v-for="model in candidateModels" :key="model" class="flex items-center gap-2 rounded px-2 py-1.5 text-xs hover:bg-gray-50 dark:hover:bg-dark-800" @click="selectCandidateModel(model)">
                  <input v-model="selectedCandidateModels" type="checkbox" class="h-4 w-4 accent-primary-600" :value="model" />
                  <code class="min-w-0 flex-1 truncate">{{ model }}</code>
                </label>
              </template>
            </div>
            <label class="mt-3 flex items-start gap-2 text-xs text-gray-600 dark:text-gray-400">
              <input v-model="replaceAllowlist" type="checkbox" class="mt-0.5 h-4 w-4 accent-primary-600" />
              <span>{{ t('admin.vision.register.replaceHint') }}</span>
            </label>
            <p v-if="registerError" class="mt-2 text-xs text-red-600 dark:text-red-400">{{ registerError }}</p>
            <p v-if="registerSuccess" class="mt-2 text-xs text-emerald-700 dark:text-emerald-300">{{ registerSuccess }}</p>
            <button type="button" class="btn btn-primary btn-sm mt-3" :disabled="!canRegister || registering" @click="registerModels">
              <Icon name="plus" size="xs" />
              {{ registering ? t('common.saving') : t('admin.vision.register.register') }}
            </button>
          </section>
        </div>
      </details>
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
import DubbingForm from '@/features/edge/DubbingForm.vue'
import DubbingDocs from '@/features/edge/DubbingDocs.vue'
import { defaultDubbingOptions, dubbingOptionsError, videoFileError } from '@/features/edge/dubbing'
import { useEdgeResponse } from '@/features/edge/useEdgeResponse'
import KeyValueEditor from '@/features/edge/KeyValueEditor.vue'
import {
  applyGatewayPath,
  parseEdgeCurl,
  replaceBodyModel,
  sanitizeHeaders,
  type EdgeBodyMode,
  type EdgeKeyValue,
  type EdgeRequestMethod,
} from '@/features/edge/curl'
import {
  EDGE_CODE_LANGUAGES,
  generateEdgeCode,
  type EdgeCodeLanguage,
} from '@/features/edge/codegen'

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
const { sending, response, media, sendError, loadingMedia, taskID, execute, reset } = useEdgeResponse(baseUrl, t)
const dubbingOptions = ref(defaultDubbingOptions())
const dubbingFile = ref<File | null>(null)
const dubbingDuration = ref(0)
const visionImageName = ref('')
const visionImageBase64 = ref('')
const visionImageError = ref('')
const requestTab = ref<'params' | 'headers' | 'body' | 'docs'>('body')
const bodyModes: EdgeBodyMode[] = ['none', 'json', 'form']
const codeLanguage = ref<EdgeCodeLanguage>('curl')

const requestTabs = computed(() => [
  { value: 'params' as const, label: 'admin.vision.workbench.query', count: request.query.filter(item => item.name.trim()).length },
  { value: 'headers' as const, label: 'admin.vision.workbench.headers', count: request.headers.filter(item => item.name.trim()).length },
  { value: 'body' as const, label: 'admin.vision.workbench.body', count: request.body.trim() || selectedEndpointKey.value === 'dub' ? 1 : 0 },
  ...(['dub', 'generation'].includes(selectedEndpointKey.value) ? [{ value: 'docs' as const, label: 'admin.vision.mediaResult.docs', count: 0 }] : []),
])

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
  { key: 'detect', path: '/vision/volcengine/detect', method: 'POST', description: t('admin.vision.endpointDetect'), category: 'vision', preset: 'vision' },
  { key: 'segment', path: '/vision/volcengine/segment', method: 'POST', description: t('admin.vision.endpointSegment'), category: 'vision', preset: 'vision' },
  { key: 'pose', path: '/vision/volcengine/pose', method: 'POST', description: t('admin.vision.endpointPose'), category: 'vision', preset: 'vision' },
  { key: 'classify', path: '/vision/volcengine/classify', method: 'POST', description: t('admin.vision.endpointClassify'), category: 'vision', preset: 'vision' },
  { key: 'ocr', path: '/vision/volcengine/ocr', method: 'POST', description: t('admin.vision.endpointOcr'), category: 'vision', preset: 'vision' },
  { key: 'tts', path: '/media/tts', method: 'POST', description: t('admin.vision.endpointManboTts'), category: 'media', preset: 'manbo' },
  { key: 'dub', path: '/media/videos/dub', method: 'POST', description: t('admin.vision.endpointVideoDub'), category: 'media', preset: 'video-dub' },
  { key: 'generation', path: '/v1/contents/generations/tasks', method: 'POST', description: t('admin.vision.endpointVideoGeneration'), category: 'media', preset: 'video-generation' },
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

const generatedCode = computed(() => generateEdgeCode({
  language: codeLanguage.value,
  gatewayOrigin: baseUrl,
  method: request.method,
  url: gatewayPath.value ? applyGatewayPath(request.url, baseUrl, gatewayPath.value) : request.url,
  headers: sanitizeHeaders(request.headers, '$SUB2API_KEY'),
  query: request.query,
  body: request.body,
  bodyMode: selectedEndpointKey.value === 'dub' ? 'form' : request.bodyMode,
  form: selectedEndpointKey.value === 'dub' ? [
    { name: 'video', kind: 'file', value: dubbingFile.value?.name ?? 'input.mp4' },
    { name: 'options', kind: 'text', value: JSON.stringify(dubbingOptions.value) },
  ] : undefined,
  outputFile: selectedEndpointKey.value === 'tts' ? 'speech.' + ttsFormat() : undefined,
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


function openEndpoint(endpoint: EndpointDefinition) {
  selectedEndpointKey.value = endpoint.key
  usePreset(endpoint.preset)
  gatewayPath.value = endpoint.path
  request.method = endpoint.method
  request.url = buildGatewayUrl(endpoint.path)
  requestTab.value = 'body'
  taskID.value = ''
  reset()
}

function selectEndpointFromMenu(event: Event) {
  const endpoint = endpoints.value.find(item => item.key === (event.target as HTMLSelectElement).value)
  if (endpoint) openEndpoint(endpoint)
}


function visionActionForEndpoint(): string {
  const endpoint = selectedEndpoint.value
  if (!endpoint.path.startsWith('/vision/volcengine/')) return 'Detect'
  return endpoint.path.slice('/vision/volcengine/'.length)
}

function visionActionName(action: string): string {
  const names: Record<string, string> = {
    detect: 'Detect',
    segment: 'Segment',
    pose: 'Pose',
    classify: 'Classify',
    ocr: 'OCR',
  }
  return names[action] ?? 'Detect'
}

function visionParamsForAction(action: string): Record<string, number> {
  if (action === 'classify') return { TopK: 5 }
  if (action === 'ocr') return {}
  return { ConfidenceThreshold: 0.5 }
}

function presetRequest(preset: Preset) {
  if (preset === 'vision') {
    const action = visionActionForEndpoint()
    gatewayPath.value = `/vision/volcengine/${action}`
    return {
      method: 'POST' as EdgeRequestMethod,
      url: buildGatewayUrl(`/vision/volcengine/${action}`),
      headers: [{ name: 'Content-Type', value: 'application/json' }],
      query: [] as EdgeKeyValue[],
      body: JSON.stringify({
        Action: visionActionName(action),
        Version: '2022-08-31',
        ImageBase64: visionImageBase64.value || '$IMAGE_BASE64',
        Params: visionParamsForAction(action),
      }, null, 2),
      bodyMode: 'json' as EdgeBodyMode,
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
      body: '',
      bodyMode: 'form' as EdgeBodyMode,
      model: '',
    }
  }
  if (preset === 'video-generation') {
    gatewayPath.value = '/v1/contents/generations/tasks'
    return {
      method: 'POST' as EdgeRequestMethod,
      url: buildGatewayUrl('/v1/contents/generations/tasks'),
      headers: [{ name: 'Content-Type', value: 'application/json' }],
      query: [] as EdgeKeyValue[],
      body: JSON.stringify({ model: 'YOUR_ARK_MODEL_ID', content: [{ type: 'text', text: '海边日出，固定镜头' }], duration: 5, resolution: '720p' }, null, 2),
      bodyMode: 'json' as EdgeBodyMode,
      model: 'YOUR_ARK_MODEL_ID',
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
  if (preset === 'video-dub') dubbingOptions.value = defaultDubbingOptions()
  applyRequest(presetRequest(preset))
}

function importCurl() {
  parseError.value = ''
  registerError.value = ''
  registerSuccess.value = ''
  try {
    const parsed = parseEdgeCurl(curlInput.value)
    const endpoint = endpoints.value.find(item => item.path === new URL(parsed.url).pathname)
    selectedEndpointKey.value = endpoint?.key ?? ''
    requestTab.value = 'body'
    reset()
    if (endpoint?.key === 'dub') {
      const options = JSON.parse(parsed.form.find(field => field.name === 'options')?.value ?? '{}')
      const normalized = { ...defaultDubbingOptions(), ...options }
      const error = dubbingOptionsError(normalized)
      if (error) throw new Error(t('admin.vision.dubbing.' + error))
      dubbingOptions.value = normalized
      dubbingFile.value = null
    }
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

function addHeader() {
  request.headers.push({ name: '', value: '' })
}

function addQuery() {
  request.query.push({ name: '', value: '' })
}

function formatRequestBody() {
  try {
    request.body = JSON.stringify(JSON.parse(request.body), null, 2)
  } catch {
    return
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

async function selectVisionImage(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  visionImageError.value = ''
  if (!file) return
  if (!file.type.startsWith('image/')) {
    visionImageError.value = t('admin.vision.imageTypeError')
    visionImageName.value = ''
    visionImageBase64.value = ''
    input.value = ''
    return
  }
  try {
    const bytes = new Uint8Array(await file.arrayBuffer())
    let binary = ''
    const chunkSize = 0x8000
    for (let offset = 0; offset < bytes.length; offset += chunkSize) {
      binary += String.fromCharCode(...bytes.subarray(offset, offset + chunkSize))
    }
    visionImageBase64.value = btoa(binary)
    visionImageName.value = file.name
    if (request.body.includes('$IMAGE_BASE64')) {
      request.body = request.body.replace(/\$IMAGE_BASE64/g, visionImageBase64.value)
    }
    request.headers = request.headers.map(header => (
      header.name.toLowerCase() === 'content-type' && !header.value
        ? { ...header, value: 'application/json' }
        : header
    ))
  } catch {
    visionImageError.value = t('admin.vision.imageReadFailed')
    visionImageName.value = ''
    visionImageBase64.value = ''
  }
}

function ttsFormat(): string {
  try {
    const value = JSON.parse(request.body).response_format
    return ['wav', 'mp3', 'ogg'].includes(value) ? value : 'wav'
  } catch { return 'wav' }
}

function buildRequestBody() {
  if (selectedEndpointKey.value === 'dub') {
    const error = videoFileError(dubbingFile.value) || dubbingOptionsError(dubbingOptions.value, dubbingDuration.value)
    if (error) throw new Error(t('admin.vision.dubbing.' + error))
    const form = new FormData()
    form.append('video', dubbingFile.value!)
    form.append('options', JSON.stringify(dubbingOptions.value))
    return form
  }
  if (request.method === 'GET' || request.bodyMode === 'none' || !request.body.trim()) return undefined
  if (request.bodyMode === 'json') return request.body
  const form = new FormData()
  request.body.split('&').forEach((part) => {
    const separator = part.indexOf('=')
    const name = separator >= 0 ? part.slice(0, separator) : part
    const value = separator >= 0 ? part.slice(separator + 1) : ''
    if (value.startsWith('@')) throw new Error(t('admin.vision.mediaResult.unsupportedFormFile'))
    if (name.trim()) form.append(name.trim(), value)
  })
  return form
}

async function sendRequest() {
  if (!selectedAPIKey.value || sending.value) return
  try {
    const headers = buildRequestHeaders()
    const body = buildRequestBody()
    if (body instanceof FormData) headers.delete('Content-Type')
    else if (request.bodyMode === 'json' && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
    await execute(buildRequestURL(), { method: request.method, headers, body })
  } catch (cause) {
    reset()
    sendError.value = cause instanceof Error ? cause.message : t('admin.vision.requestFailed')
  }
}

async function queryTask() {
  if (!selectedAPIKey.value || sending.value || !/^[a-zA-Z0-9_-]+$/.test(taskID.value)) return
  await execute(buildGatewayUrl('/media/tasks/' + taskID.value), { headers: buildRequestHeaders() })
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

async function copyGeneratedCode() {
  await copyToClipboard(generatedCode.value, t('common.copiedToClipboard'))
}

onMounted(async () => {
  openEndpoint(endpoints.value[0])
  await Promise.all([loadStatus(), loadGroups(), loadAPIKeys()])
})
</script>
