<template>
  <BaseDialog
    :show="show"
    :title="t('keys.useKeyModal.title')"
    width="wide"
    @close="emit('close')"
  >
    <div class="space-y-4">
      <!-- No Group Assigned Warning -->
      <div v-if="!platform" class="flex items-start gap-3 p-4 rounded-lg bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-yellow-800">
        <svg class="w-5 h-5 text-yellow-500 flex-shrink-0 mt-0.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
        </svg>
        <div>
          <p class="text-sm font-medium text-yellow-800 dark:text-yellow-200">
            {{ t('keys.useKeyModal.noGroupTitle') }}
          </p>
          <p class="text-sm text-yellow-700 dark:text-yellow-300 mt-1">
            {{ t('keys.useKeyModal.noGroupDescription') }}
          </p>
        </div>
      </div>

      <!-- Platform-specific content -->
      <template v-else>
        <!-- Description -->
        <p class="text-sm text-gray-600 dark:text-gray-400">
          {{ platformDescription }}
        </p>

        <!-- Client Tabs -->
        <div v-if="clientTabs.length" class="border-b border-gray-200 dark:border-dark-700">
          <nav class="-mb-px flex space-x-6" aria-label="Client">
            <button
              v-for="tab in clientTabs"
              :key="tab.id"
              @click="activeClientTab = tab.id"
              :class="[
                'whitespace-nowrap py-2.5 px-1 border-b-2 font-medium text-sm transition-colors',
                activeClientTab === tab.id
                  ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-300'
              ]"
            >
              <span class="flex items-center gap-2">
                <component :is="tab.icon" class="w-4 h-4" />
                {{ tab.label }}
              </span>
            </button>
          </nav>
        </div>

        <!-- OS/Shell Tabs -->
        <div v-if="showShellTabs" class="border-b border-gray-200 dark:border-dark-700">
          <nav class="-mb-px flex space-x-4" aria-label="Tabs">
            <button
              v-for="tab in currentTabs"
              :key="tab.id"
              @click="activeTab = tab.id"
              :class="[
                'whitespace-nowrap py-2.5 px-1 border-b-2 font-medium text-sm transition-colors',
                activeTab === tab.id
                  ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-300'
              ]"
            >
              <span class="flex items-center gap-2">
                <component :is="tab.icon" class="w-4 h-4" />
                {{ tab.label }}
              </span>
            </button>
          </nav>
        </div>

        <div
          v-if="showCodexModelEditor"
          class="rounded-lg border border-gray-200 dark:border-dark-700 bg-gray-50 dark:bg-dark-800/40 p-3 space-y-3"
        >
          <div class="flex items-center justify-between gap-3">
            <div class="min-w-0">
              <p class="text-sm font-medium text-gray-900 dark:text-gray-100">Codex models</p>
              <p class="text-xs text-gray-500 dark:text-gray-400">
                {{ normalizedCodexModelCatalogModels.length }} models -> {{ CODEX_MODEL_CATALOG_FILENAME }}
              </p>
            </div>
            <button
              type="button"
              class="inline-flex h-8 w-8 items-center justify-center rounded-md border border-gray-300 dark:border-dark-600 text-gray-600 dark:text-gray-300 hover:bg-white dark:hover:bg-dark-700 transition-colors"
              aria-label="Add Codex model"
              title="Add Codex model"
              @click="addCodexModel"
            >
              <Icon name="plus" size="sm" :stroke-width="2" />
            </button>
          </div>

          <div v-if="codexModelRows.length" class="space-y-2 max-h-56 overflow-y-auto pr-1">
            <div
              v-for="(row, index) in codexModelRows"
              :key="row.id"
              class="grid grid-cols-12 gap-2 items-center"
            >
              <input
                v-model="row.slug"
                type="text"
                class="col-span-5 h-9 rounded-md border border-gray-300 dark:border-dark-600 bg-white dark:bg-dark-900 px-2 text-sm text-gray-900 dark:text-gray-100 font-mono focus:outline-none focus:ring-2 focus:ring-primary-500"
                placeholder="model id"
              />
              <input
                v-model="row.displayName"
                type="text"
                class="col-span-4 h-9 rounded-md border border-gray-300 dark:border-dark-600 bg-white dark:bg-dark-900 px-2 text-sm text-gray-900 dark:text-gray-100 focus:outline-none focus:ring-2 focus:ring-primary-500"
                placeholder="display name"
              />
              <input
                v-model.number="row.contextWindow"
                type="number"
                min="1"
                class="col-span-2 h-9 rounded-md border border-gray-300 dark:border-dark-600 bg-white dark:bg-dark-900 px-2 text-sm text-gray-900 dark:text-gray-100 focus:outline-none focus:ring-2 focus:ring-primary-500"
                placeholder="context"
              />
              <button
                type="button"
                class="col-span-1 inline-flex h-9 w-9 items-center justify-center rounded-md text-red-500 hover:bg-red-50 dark:hover:bg-red-900/20 transition-colors"
                aria-label="Delete Codex model"
                title="Delete Codex model"
                @click="removeCodexModel(index)"
              >
                <Icon name="trash" size="sm" :stroke-width="2" />
              </button>
            </div>
          </div>

          <div v-else class="text-sm text-gray-500 dark:text-gray-400">
            No models
          </div>
        </div>

        <!-- Code Blocks (Stacked for multi-file platforms) -->
        <div class="space-y-4">
          <div
            v-for="(file, index) in currentFiles"
            :key="index"
            class="relative"
          >
            <!-- File Hint (if exists) -->
            <p v-if="file.hint" class="text-xs text-amber-600 dark:text-amber-400 mb-1.5 flex items-center gap-1">
              <Icon name="exclamationCircle" size="sm" class="flex-shrink-0" />
              {{ file.hint }}
            </p>
            <div class="bg-gray-900 dark:bg-dark-900 rounded-xl overflow-hidden">
              <!-- Code Header -->
              <div
                class="flex items-center justify-between gap-3 px-4 py-2 bg-gray-800 dark:bg-dark-800"
                :class="isFileExpanded(index) ? 'border-b border-gray-700 dark:border-dark-700' : ''"
              >
                <button
                  type="button"
                  class="min-w-0 flex flex-1 items-center gap-2 text-left text-xs text-gray-400 hover:text-gray-200 font-mono transition-colors"
                  :aria-expanded="isFileExpanded(index)"
                  :aria-controls="`use-key-code-${index}`"
                  :aria-label="isFileExpanded(index) ? 'Collapse command' : 'Expand command'"
                  @click="toggleFileExpanded(index)"
                >
                  <Icon
                    :name="isFileExpanded(index) ? 'chevronDown' : 'chevronRight'"
                    size="xs"
                    :stroke-width="2"
                    class="flex-shrink-0"
                  />
                  <span class="truncate">{{ file.path }}</span>
                </button>
                <button
                  type="button"
                  class="flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium rounded-lg transition-colors"
                  :class="copiedIndex === index
                    ? 'bg-green-500/20 text-green-400'
                    : 'bg-gray-700 hover:bg-gray-600 text-gray-300 hover:text-white'"
                  @click.stop="copyContent(file.content, index)"
                >
                  <svg v-if="copiedIndex === index" class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                  </svg>
                  <svg v-else class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M15.666 3.888A2.25 2.25 0 0013.5 2.25h-3c-1.03 0-1.9.693-2.166 1.638m7.332 0c.055.194.084.4.084.612v0a.75.75 0 01-.75.75H9a.75.75 0 01-.75-.75v0c0-.212.03-.418.084-.612m7.332 0c.646.049 1.288.11 1.927.184 1.1.128 1.907 1.077 1.907 2.185V19.5a2.25 2.25 0 01-2.25 2.25H6.75A2.25 2.25 0 014.5 19.5V6.257c0-1.108.806-2.057 1.907-2.185a48.208 48.208 0 011.927-.184" />
                  </svg>
                  {{ copiedIndex === index ? t('keys.useKeyModal.copied') : t('keys.useKeyModal.copy') }}
                </button>
              </div>
              <!-- Code Content -->
              <pre
                v-show="isFileExpanded(index)"
                :id="`use-key-code-${index}`"
                class="p-4 text-sm font-mono text-gray-100 overflow-x-auto"
              ><code v-if="file.highlighted" v-html="file.highlighted"></code><code v-else v-text="file.content"></code></pre>
            </div>
          </div>
        </div>

        <!-- Usage Note -->
        <div v-if="showPlatformNote" class="flex items-start gap-3 p-3 rounded-lg bg-blue-50 dark:bg-blue-900/20 border border-blue-100 dark:border-blue-800">
          <Icon name="infoCircle" size="md" class="text-blue-500 flex-shrink-0 mt-0.5" />
          <p class="text-sm text-blue-700 dark:text-blue-300">
            {{ platformNote }}
          </p>
        </div>
      </template>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button
          @click="emit('close')"
          class="btn btn-secondary"
        >
          {{ t('common.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, computed, h, watch, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import type { GroupPlatform } from '@/types'
import { keysAPI, type CodexModelCatalogModel } from '@/api/keys'

interface Props {
  show: boolean
  apiKeyId?: number | null
  apiKey: string
  baseUrl: string
  platform: GroupPlatform | null
  allowMessagesDispatch?: boolean
}

interface Emits {
  (e: 'close'): void
}

interface TabConfig {
  id: string
  label: string
  icon: Component
}

interface FileConfig {
  path: string
  content: string
  hint?: string  // Optional hint message for this file
  highlighted?: string
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const { copyToClipboard: clipboardCopy } = useClipboard()

const copiedIndex = ref<number | null>(null)
const activeTab = ref<string>('unix')
const activeClientTab = ref<string>('claude')
const expandedFileIndexes = ref<Set<number>>(new Set())
const codexModelCatalogKeyId = ref<number | null>(null)
interface CodexModelRow {
  id: number
  slug: string
  displayName: string
  contextWindow: number
}

type PersistedCodexModelRow = Omit<CodexModelRow, 'id'>

interface PersistedCodexModelCatalog {
  version: 1
  models: PersistedCodexModelRow[]
  blacklistedSlugs: string[]
}

const CODEX_MODEL_CATALOG_FILENAME = 'model-catalog.json'
const CODEX_MODEL_CATALOG_STORAGE_PREFIX = 'sub2api:codex-model-catalog'
const CODEX_CONTEXT_WINDOW_DEFAULT = 272000
const CODEX_REASONING_LEVELS = [
  { effort: 'low', description: 'Fast responses with lighter reasoning' },
  { effort: 'medium', description: 'Balances speed and reasoning depth for everyday tasks' },
  { effort: 'high', description: 'Greater reasoning depth for complex problems' },
  { effort: 'xhigh', description: 'Extra high reasoning depth for complex problems' }
]
let codexModelRowId = 0
const codexModelRows = ref<CodexModelRow[]>([])
const codexModelCatalogStorageState = ref<PersistedCodexModelCatalog>(emptyCodexModelCatalogStorageState())
let hydratingCodexModelRows = false

// Reset tabs when platform changes
const defaultClientTab = computed(() => {
  switch (props.platform) {
    case 'openai':
      return 'codex'
    case 'gemini':
      return 'codex'
    case 'antigravity':
      return 'claude'
    default:
      return 'claude'
  }
})

watch(() => props.platform, () => {
  activeTab.value = 'unix'
  activeClientTab.value = defaultClientTab.value
}, { immediate: true })

// Reset shell tab when client changes
watch(activeClientTab, () => {
  activeTab.value = 'unix'
})

watch(
  () => [props.show, props.apiKeyId, props.apiKey, props.baseUrl, props.platform, activeClientTab.value, activeTab.value] as const,
  () => {
    expandedFileIndexes.value = new Set()
  }
)

watch(
  () => [props.show, props.apiKeyId, props.platform] as const,
  async ([show, apiKeyId, platform]) => {
    if (!show || !apiKeyId || (platform !== 'openai' && platform !== 'gemini')) {
      codexModelCatalogKeyId.value = null
      resetCodexModelRows()
      return
    }
    hydrateCodexModelRows([])
    codexModelCatalogKeyId.value = apiKeyId
    try {
      const requestedPlatform = platform
      const catalog = await keysAPI.getCodexModelCatalog(apiKeyId)
      if (codexModelCatalogKeyId.value === apiKeyId && props.platform === requestedPlatform) {
        hydrateCodexModelRows(catalog.models)
      }
    } catch (error) {
      if (codexModelCatalogKeyId.value === apiKeyId && props.platform === platform) {
        hydrateCodexModelRows([])
      }
    }
  },
  { immediate: true }
)

watch(codexModelRows, () => {
  if (!hydratingCodexModelRows) {
    persistCodexModelRows()
  }
}, {
  deep: true,
  flush: 'sync'
})

// Icon components
const AppleIcon = {
  render() {
    return h('svg', {
      fill: 'currentColor',
      viewBox: '0 0 24 24',
      class: 'w-4 h-4'
    }, [
      h('path', { d: 'M18.71 19.5c-.83 1.24-1.71 2.45-3.05 2.47-1.34.03-1.77-.79-3.29-.79-1.53 0-2 .77-3.27.82-1.31.05-2.3-1.32-3.14-2.53C4.25 17 2.94 12.45 4.7 9.39c.87-1.52 2.43-2.48 4.12-2.51 1.28-.02 2.5.87 3.29.87.78 0 2.26-1.07 3.81-.91.65.03 2.47.26 3.64 1.98-.09.06-2.17 1.28-2.15 3.81.03 3.02 2.65 4.03 2.68 4.04-.03.07-.42 1.44-1.38 2.83M13 3.5c.73-.83 1.94-1.46 2.94-1.5.13 1.17-.34 2.35-1.04 3.19-.69.85-1.83 1.51-2.95 1.42-.15-1.15.41-2.35 1.05-3.11z' })
    ])
  }
}

const WindowsIcon = {
  render() {
    return h('svg', {
      fill: 'currentColor',
      viewBox: '0 0 24 24',
      class: 'w-4 h-4'
    }, [
      h('path', { d: 'M3 12V6.75l6-1.32v6.48L3 12zm17-9v8.75l-10 .15V5.21L20 3zM3 13l6 .09v6.81l-6-1.15V13zm7 .25l10 .15V21l-10-1.91v-5.84z' })
    ])
  }
}

// Terminal icon for Claude Code
const TerminalIcon = {
  render() {
    return h('svg', {
      fill: 'none',
      stroke: 'currentColor',
      viewBox: '0 0 24 24',
      'stroke-width': '1.5',
      class: 'w-4 h-4'
    }, [
      h('path', {
        'stroke-linecap': 'round',
        'stroke-linejoin': 'round',
        d: 'm6.75 7.5 3 2.25-3 2.25m4.5 0h3m-9 8.25h13.5A2.25 2.25 0 0 0 21 17.25V6.75A2.25 2.25 0 0 0 18.75 4.5H5.25A2.25 2.25 0 0 0 3 6.75v10.5A2.25 2.25 0 0 0 5.25 20.25Z'
      })
    ])
  }
}

// Sparkle icon for Gemini
const SparkleIcon = {
  render() {
    return h('svg', {
      fill: 'none',
      stroke: 'currentColor',
      viewBox: '0 0 24 24',
      'stroke-width': '1.5',
      class: 'w-4 h-4'
    }, [
      h('path', {
        'stroke-linecap': 'round',
        'stroke-linejoin': 'round',
        d: 'M9.813 15.904 9 18.75l-.813-2.846a4.5 4.5 0 0 0-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 0 0 3.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 0 0 3.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 0 0-3.09 3.09ZM18.259 8.715 18 9.75l-.259-1.035a3.375 3.375 0 0 0-2.455-2.456L14.25 6l1.036-.259a3.375 3.375 0 0 0 2.455-2.456L18 2.25l.259 1.035a3.375 3.375 0 0 0 2.456 2.456L21.75 6l-1.035.259a3.375 3.375 0 0 0-2.456 2.456ZM16.894 20.567 16.5 21.75l-.394-1.183a2.25 2.25 0 0 0-1.423-1.423L13.5 18.75l1.183-.394a2.25 2.25 0 0 0 1.423-1.423l.394-1.183.394 1.183a2.25 2.25 0 0 0 1.423 1.423l1.183.394-1.183.394a2.25 2.25 0 0 0-1.423 1.423Z'
      })
    ])
  }
}

const clientTabs = computed((): TabConfig[] => {
  if (!props.platform) return []
  switch (props.platform) {
    case 'openai': {
      const tabs: TabConfig[] = [
        { id: 'codex', label: t('keys.useKeyModal.cliTabs.codexCli'), icon: TerminalIcon },
        { id: 'codex-ws', label: t('keys.useKeyModal.cliTabs.codexCliWs'), icon: TerminalIcon },
      ]
      if (props.allowMessagesDispatch) {
        tabs.push({ id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: TerminalIcon })
      }
      tabs.push({ id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: TerminalIcon })
      return tabs
    }
    case 'gemini':
      return [
        { id: 'codex', label: t('keys.useKeyModal.cliTabs.codexCli'), icon: TerminalIcon },
        { id: 'gemini', label: t('keys.useKeyModal.cliTabs.geminiCli'), icon: SparkleIcon },
        { id: 'opencode-responses', label: t('keys.useKeyModal.cliTabs.opencodeResponses'), icon: TerminalIcon },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: TerminalIcon }
      ]
    case 'antigravity':
      return [
        { id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: TerminalIcon },
        { id: 'gemini', label: t('keys.useKeyModal.cliTabs.geminiCli'), icon: SparkleIcon },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: TerminalIcon }
      ]
    default:
      return [
        { id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: TerminalIcon },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: TerminalIcon }
      ]
  }
})

// Shell tabs (3 types for environment variable based configs)
const shellTabs: TabConfig[] = [
  { id: 'unix', label: 'macOS / Linux', icon: AppleIcon },
  { id: 'cmd', label: 'Windows CMD', icon: WindowsIcon },
  { id: 'powershell', label: 'PowerShell', icon: WindowsIcon }
]

// OpenAI tabs (2 OS types)
const openaiTabs: TabConfig[] = [
  { id: 'unix', label: 'macOS / Linux', icon: AppleIcon },
  { id: 'windows', label: 'Windows', icon: WindowsIcon }
]

const isOpenCodeTab = computed(() => activeClientTab.value.startsWith('opencode'))

const showShellTabs = computed(() => !isOpenCodeTab.value)

const currentTabs = computed(() => {
  if (!showShellTabs.value) return []
  if (activeClientTab.value === 'codex' || activeClientTab.value === 'codex-ws') {
    return openaiTabs
  }
  return shellTabs
})

const platformDescription = computed(() => {
  if (isOpenCodeTab.value) {
    return t('keys.useKeyModal.opencode.title')
  }
  switch (props.platform) {
    case 'openai':
      if (activeClientTab.value === 'claude') {
        return t('keys.useKeyModal.description')
      }
      return t('keys.useKeyModal.openai.description')
    case 'gemini':
      if (activeClientTab.value === 'codex') {
        return t('keys.useKeyModal.openai.description')
      }
      return t('keys.useKeyModal.gemini.description')
    case 'antigravity':
      return t('keys.useKeyModal.antigravity.description')
    default:
      return t('keys.useKeyModal.description')
  }
})

const platformNote = computed(() => {
  switch (props.platform) {
    case 'openai':
      if (activeClientTab.value === 'claude') {
        return t('keys.useKeyModal.note')
      }
      return activeTab.value === 'windows'
        ? t('keys.useKeyModal.openai.noteWindows')
        : t('keys.useKeyModal.openai.note')
    case 'gemini':
      if (activeClientTab.value === 'codex') {
        return activeTab.value === 'windows'
          ? t('keys.useKeyModal.openai.noteWindows')
          : t('keys.useKeyModal.openai.note')
      }
      return t('keys.useKeyModal.gemini.note')
    case 'antigravity':
      return activeClientTab.value === 'claude'
        ? t('keys.useKeyModal.antigravity.claudeNote')
        : t('keys.useKeyModal.antigravity.geminiNote')
    default:
      return t('keys.useKeyModal.note')
  }
})

const showPlatformNote = computed(() => !isOpenCodeTab.value)

const escapeHtml = (value: string) => value
  .replace(/&/g, '&amp;')
  .replace(/</g, '&lt;')
  .replace(/>/g, '&gt;')
  .replace(/"/g, '&quot;')
  .replace(/'/g, '&#39;')

const wrapToken = (className: string, value: string) =>
  `<span class="${className}">${escapeHtml(value)}</span>`

const keyword = (value: string) => wrapToken('text-emerald-300', value)
const variable = (value: string) => wrapToken('text-sky-200', value)
const operator = (value: string) => wrapToken('text-slate-400', value)
const string = (value: string) => wrapToken('text-amber-200', value)
const comment = (value: string) => wrapToken('text-slate-500', value)

// Syntax highlighting helpers
// Generate file configs based on platform and active tab
const currentFiles = computed((): FileConfig[] => {
  const baseUrl = props.baseUrl || window.location.origin
  const apiKey = props.apiKey
  const baseRoot = baseUrl.replace(/\/v1\/?$/, '').replace(/\/+$/, '')
  const ensureV1 = (value: string) => {
    const trimmed = value.replace(/\/+$/, '')
    return trimmed.endsWith('/v1') ? trimmed : `${trimmed}/v1`
  }
  const apiBase = ensureV1(baseRoot)
  const antigravityBase = ensureV1(`${baseRoot}/antigravity`)
  const antigravityGeminiBase = (() => {
    const trimmed = `${baseRoot}/antigravity`.replace(/\/+$/, '')
    return trimmed.endsWith('/v1beta') ? trimmed : `${trimmed}/v1beta`
  })()
  const geminiBase = (() => {
    const trimmed = baseRoot.replace(/\/+$/, '')
    return trimmed.endsWith('/v1beta') ? trimmed : `${trimmed}/v1beta`
  })()

  if (activeClientTab.value === 'opencode') {
    switch (props.platform) {
      case 'anthropic':
        return [generateOpenCodeConfig('anthropic', apiBase, apiKey)]
      case 'openai':
        return [generateOpenCodeConfig('openai', apiBase, apiKey)]
      case 'gemini':
        return [generateOpenCodeConfig('gemini', geminiBase, apiKey)]
      case 'antigravity':
        return [
          generateOpenCodeConfig('antigravity-claude', antigravityBase, apiKey, 'opencode.json (Claude)'),
          generateOpenCodeConfig('antigravity-gemini', antigravityGeminiBase, apiKey, 'opencode.json (Gemini)')
        ]
      default:
        return [generateOpenCodeConfig('openai', apiBase, apiKey)]
    }
  }

  if (activeClientTab.value === 'opencode-responses') {
    if (props.platform === 'gemini') {
      return [generateOpenCodeConfig('openai', apiBase, apiKey, 'opencode.json (Responses)', {
        name: 'Gemini (Responses)',
        npm: '@ai-sdk/openai',
        models: geminiModelsForOpenCode(),
        agent: true,
      })]
    }
    return [generateOpenCodeConfig('openai', apiBase, apiKey)]
  }

  switch (props.platform) {
    case 'openai':
      if (activeClientTab.value === 'claude') {
        return generateAnthropicFiles(baseUrl, apiKey)
      }
      if (activeClientTab.value === 'codex-ws') {
        return generateOpenAIWsFiles(baseUrl, apiKey)
      }
      return generateOpenAIFiles(baseUrl, apiKey)
    case 'gemini':
      if (activeClientTab.value === 'codex') {
        return generateOpenAIFiles(baseUrl, apiKey, {
          providerKey: 'Gemini',
          providerName: 'Gemini',
          model: 'gemini-2.5-pro',
          reviewModel: 'gemini-2.5-pro',
        })
      }
      return [generateGeminiCliContent(baseUrl, apiKey)]
    case 'antigravity':
      if (activeClientTab.value === 'gemini') {
        return [generateGeminiCliContent(`${baseUrl}/antigravity`, apiKey)]
      }
      return generateAnthropicFiles(`${baseUrl}/antigravity`, apiKey)
    default:
      return generateAnthropicFiles(baseUrl, apiKey)
  }
})

function isFileExpanded(index: number): boolean {
  return expandedFileIndexes.value.has(index)
}

function toggleFileExpanded(index: number): void {
  const nextExpandedIndexes = new Set(expandedFileIndexes.value)
  if (nextExpandedIndexes.has(index)) {
    nextExpandedIndexes.delete(index)
  } else {
    nextExpandedIndexes.add(index)
  }
  expandedFileIndexes.value = nextExpandedIndexes
}

function generateAnthropicFiles(baseUrl: string, apiKey: string): FileConfig[] {
  let path: string
  let content: string

  switch (activeTab.value) {
    case 'unix':
      path = 'Terminal'
      content = `export ANTHROPIC_BASE_URL="${baseUrl}"
export ANTHROPIC_AUTH_TOKEN="${apiKey}"
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`
      break
    case 'cmd':
      path = 'Command Prompt'
      content = `set ANTHROPIC_BASE_URL=${baseUrl}
set ANTHROPIC_AUTH_TOKEN=${apiKey}
set CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`
      break
    case 'powershell':
      path = 'PowerShell'
      content = `$env:ANTHROPIC_BASE_URL="${baseUrl}"
$env:ANTHROPIC_AUTH_TOKEN="${apiKey}"
$env:CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`
      break
    default:
      path = 'Terminal'
      content = ''
  }

  const vscodeSettingsPath = activeTab.value === 'unix'
    ? '~/.claude/settings.json'
    : '%userprofile%\\.claude\\settings.json'

  const vscodeContent = `{
  "env": {
    "ANTHROPIC_BASE_URL": "${baseUrl}",
    "ANTHROPIC_AUTH_TOKEN": "${apiKey}",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
    "CLAUDE_CODE_ATTRIBUTION_HEADER": "0"
  }
}`

  return [
    { path, content },
    { path: vscodeSettingsPath, content: vscodeContent, hint: 'VSCode Claude Code' }
  ]
}

function generateGeminiCliContent(baseUrl: string, apiKey: string): FileConfig {
  const model = 'gemini-2.0-flash'
  const modelComment = t('keys.useKeyModal.gemini.modelComment')
  let path: string
  let content: string
  let highlighted: string

  switch (activeTab.value) {
    case 'unix':
      path = 'Terminal'
      content = `export GOOGLE_GEMINI_BASE_URL="${baseUrl}"
export GEMINI_API_KEY="${apiKey}"
export GEMINI_MODEL="${model}"  # ${modelComment}`
      highlighted = `${keyword('export')} ${variable('GOOGLE_GEMINI_BASE_URL')}${operator('=')}${string(`"${baseUrl}"`)}
${keyword('export')} ${variable('GEMINI_API_KEY')}${operator('=')}${string(`"${apiKey}"`)}
${keyword('export')} ${variable('GEMINI_MODEL')}${operator('=')}${string(`"${model}"`)}  ${comment(`# ${modelComment}`)}`
      break
    case 'cmd':
      path = 'Command Prompt'
      content = `set GOOGLE_GEMINI_BASE_URL=${baseUrl}
set GEMINI_API_KEY=${apiKey}
set GEMINI_MODEL=${model}`
      highlighted = `${keyword('set')} ${variable('GOOGLE_GEMINI_BASE_URL')}${operator('=')}${string(baseUrl)}
${keyword('set')} ${variable('GEMINI_API_KEY')}${operator('=')}${string(apiKey)}
${keyword('set')} ${variable('GEMINI_MODEL')}${operator('=')}${string(model)}
${comment(`REM ${modelComment}`)}`
      break
    case 'powershell':
      path = 'PowerShell'
      content = `$env:GOOGLE_GEMINI_BASE_URL="${baseUrl}"
$env:GEMINI_API_KEY="${apiKey}"
$env:GEMINI_MODEL="${model}"  # ${modelComment}`
      highlighted = `${keyword('$env:')}${variable('GOOGLE_GEMINI_BASE_URL')}${operator('=')}${string(`"${baseUrl}"`)}
${keyword('$env:')}${variable('GEMINI_API_KEY')}${operator('=')}${string(`"${apiKey}"`)}
${keyword('$env:')}${variable('GEMINI_MODEL')}${operator('=')}${string(`"${model}"`)}  ${comment(`# ${modelComment}`)}`
      break
    default:
      path = 'Terminal'
      content = ''
      highlighted = ''
  }

  return { path, content, highlighted }
}

interface CodexResponsesConfigOptions {
  providerKey?: string
  providerName?: string
  model?: string
  reviewModel?: string
}

const showCodexModelEditor = computed(() =>
  activeClientTab.value === 'codex' || activeClientTab.value === 'codex-ws'
)

const normalizedCodexModelCatalogModels = computed(() => {
  const seen = new Set<string>()
  return codexModelRows.value
    .map((row) => {
      const slug = row.slug.trim()
      if (!slug || seen.has(slug)) {
        return null
      }
      seen.add(slug)
      const displayName = row.displayName.trim() || codexCatalogDisplayName(slug)
      const contextWindow = Number.isFinite(row.contextWindow) && row.contextWindow > 0
        ? Math.trunc(row.contextWindow)
        : CODEX_CONTEXT_WINDOW_DEFAULT
      return {
        slug,
        display_name: displayName,
        description: `Custom ${displayName} model routed through the configured Codex provider.`,
        default_reasoning_level: 'medium',
        supported_reasoning_levels: CODEX_REASONING_LEVELS,
        shell_type: 'shell_command',
        visibility: 'list',
        supported_in_api: true,
        priority: 1000 + seen.size - 1,
        additional_speed_tiers: ['fast'],
        availability_nux: null,
        upgrade: null,
        default_reasoning_summary: 'none',
        support_verbosity: true,
        default_verbosity: 'low',
        apply_patch_tool_type: 'freeform',
        web_search_tool_type: 'text_and_image',
        truncation_policy: {
          mode: 'tokens',
          limit: 10000
        },
        supports_parallel_tool_calls: true,
        supports_image_detail_original: true,
        context_window: contextWindow,
        max_context_window: contextWindow,
        effective_context_window_percent: 95,
        experimental_supported_tools: [],
        input_modalities: ['text', 'image'],
        supports_search_tool: true,
        supports_reasoning_summaries: true
      }
    })
    .filter((model): model is NonNullable<typeof model> => model !== null)
})

function codexCatalogDisplayName(slug: string): string {
  return slug
    .split(/[-_:/]+/)
    .filter(Boolean)
    .map((part) => {
      const lower = part.toLowerCase()
      if (lower === 'gpt' || lower === 'api' || lower === 'ai') {
        return lower.toUpperCase()
      }
      return `${lower.charAt(0).toUpperCase()}${lower.slice(1)}`
    })
    .join(' ') || slug
}

function addCodexModel(): void {
  codexModelRows.value.push({
    id: ++codexModelRowId,
    slug: '',
    displayName: '',
    contextWindow: CODEX_CONTEXT_WINDOW_DEFAULT
  })
}

function removeCodexModel(index: number): void {
  const slug = codexModelRows.value[index]?.slug.trim()
  if (slug) {
    codexModelCatalogStorageState.value = {
      ...codexModelCatalogStorageState.value,
      blacklistedSlugs: uniqueStrings([
        ...codexModelCatalogStorageState.value.blacklistedSlugs,
        slug
      ])
    }
  }
  codexModelRows.value.splice(index, 1)
  persistCodexModelRows()
}

function currentCodexModelCatalogContent(): string | null {
  const models = normalizedCodexModelCatalogModels.value
  if (!models?.length) {
    return null
  }
  return JSON.stringify({ models }, null, 2)
}

function resetCodexModelRows(): void {
  codexModelCatalogStorageState.value = emptyCodexModelCatalogStorageState()
  hydratingCodexModelRows = true
  try {
    codexModelRows.value = []
  } finally {
    hydratingCodexModelRows = false
  }
}

function hydrateCodexModelRows(serverModels: CodexModelCatalogModel[]): void {
  const persisted = readCodexModelCatalogStorage()
  const blacklist = new Set(persisted.blacklistedSlugs)
  const mergedRows = mergeCodexModelRows(
    serverModels.map(codexModelToPersistedRow),
    persisted.models
  ).filter((row) => {
    const slug = row.slug.trim()
    return slug && !blacklist.has(slug)
  })

  codexModelCatalogStorageState.value = {
    version: 1,
    models: mergedRows,
    blacklistedSlugs: persisted.blacklistedSlugs
  }

  hydratingCodexModelRows = true
  try {
    codexModelRows.value = mergedRows.map((row) => ({
      id: ++codexModelRowId,
      ...row
    }))
  } finally {
    hydratingCodexModelRows = false
  }
  writeCodexModelCatalogStorage(codexModelCatalogStorageState.value)
}

function persistCodexModelRows(): void {
  const rows = mergeCodexModelRows(codexModelRows.value.map(rowToPersistedCodexModelRow))
    .filter((row) => row.slug.trim())
  const rowSlugs = new Set(rows.map((row) => row.slug))
  codexModelCatalogStorageState.value = {
    version: 1,
    models: rows,
    blacklistedSlugs: codexModelCatalogStorageState.value.blacklistedSlugs.filter((slug) => !rowSlugs.has(slug))
  }
  writeCodexModelCatalogStorage(codexModelCatalogStorageState.value)
}

function codexModelToPersistedRow(model: CodexModelCatalogModel): PersistedCodexModelRow {
  return {
    slug: model.slug,
    displayName: model.display_name || model.slug,
    contextWindow: model.context_window || CODEX_CONTEXT_WINDOW_DEFAULT
  }
}

function rowToPersistedCodexModelRow(row: CodexModelRow): PersistedCodexModelRow {
  return {
    slug: row.slug,
    displayName: row.displayName,
    contextWindow: row.contextWindow
  }
}

function mergeCodexModelRows(...rowGroups: PersistedCodexModelRow[][]): PersistedCodexModelRow[] {
  const merged = new Map<string, PersistedCodexModelRow>()
  for (const rows of rowGroups) {
    for (const row of rows) {
      const slug = row.slug.trim()
      if (!slug) continue
      merged.set(slug, normalizePersistedCodexModelRow(row))
    }
  }
  return Array.from(merged.values())
}

function normalizePersistedCodexModelRow(row: PersistedCodexModelRow): PersistedCodexModelRow {
  const slug = row.slug.trim()
  const displayName = row.displayName.trim() || codexCatalogDisplayName(slug)
  const contextWindow = Number.isFinite(row.contextWindow) && row.contextWindow > 0
    ? Math.trunc(row.contextWindow)
    : CODEX_CONTEXT_WINDOW_DEFAULT
  return {
    slug,
    displayName,
    contextWindow
  }
}

function readCodexModelCatalogStorage(): PersistedCodexModelCatalog {
  const storageKey = codexModelCatalogStorageKey()
  if (!storageKey || typeof window === 'undefined') {
    return emptyCodexModelCatalogStorageState()
  }
  try {
    const raw = window.localStorage.getItem(storageKey)
    if (!raw) {
      return emptyCodexModelCatalogStorageState()
    }
    return normalizeCodexModelCatalogStorage(JSON.parse(raw))
  } catch {
    return emptyCodexModelCatalogStorageState()
  }
}

function writeCodexModelCatalogStorage(state: PersistedCodexModelCatalog): void {
  const storageKey = codexModelCatalogStorageKey()
  if (!storageKey || typeof window === 'undefined') {
    return
  }
  try {
    window.localStorage.setItem(storageKey, JSON.stringify(normalizeCodexModelCatalogStorage(state)))
  } catch {
    // localStorage may be disabled; the generated script still uses the current in-memory rows.
  }
}

function codexModelCatalogStorageKey(): string | null {
  if (!props.apiKeyId || (props.platform !== 'openai' && props.platform !== 'gemini')) {
    return null
  }
  return `${CODEX_MODEL_CATALOG_STORAGE_PREFIX}:${props.apiKeyId}:${props.platform}`
}

function normalizeCodexModelCatalogStorage(value: unknown): PersistedCodexModelCatalog {
  const candidate = value as Partial<PersistedCodexModelCatalog> | null
  const models = Array.isArray(candidate?.models)
    ? candidate.models
        .filter(isPersistedCodexModelRow)
        .map(normalizePersistedCodexModelRow)
    : []
  const blacklistedSlugs = Array.isArray(candidate?.blacklistedSlugs)
    ? uniqueStrings(candidate.blacklistedSlugs.filter((slug): slug is string => typeof slug === 'string').map((slug) => slug.trim()).filter(Boolean))
    : []
  return {
    version: 1,
    models: mergeCodexModelRows(models),
    blacklistedSlugs
  }
}

function isPersistedCodexModelRow(value: unknown): value is PersistedCodexModelRow {
  const candidate = value as Partial<PersistedCodexModelRow> | null
  return typeof candidate?.slug === 'string'
    && typeof candidate.displayName === 'string'
    && typeof candidate.contextWindow === 'number'
}

function emptyCodexModelCatalogStorageState(): PersistedCodexModelCatalog {
  return {
    version: 1,
    models: [],
    blacklistedSlugs: []
  }
}

function uniqueStrings(values: string[]): string[] {
  return Array.from(new Set(values.map((value) => value.trim()).filter(Boolean)))
}

function generateOpenAIFiles(baseUrl: string, apiKey: string, options: CodexResponsesConfigOptions = {}): FileConfig[] {
  const isWindows = activeTab.value === 'windows'
  const configDir = isWindows ? '%userprofile%\\.codex' : '~/.codex'
  const providerKey = options.providerKey ?? 'OpenAI'
  const providerName = options.providerName ?? providerKey
  const model = options.model ?? 'gpt-5.5'
  const reviewModel = options.reviewModel ?? model
  const modelCatalogContent = currentCodexModelCatalogContent()
  const modelCatalogConfig = modelCatalogContent ? `model_catalog_json = "${CODEX_MODEL_CATALOG_FILENAME}"\n` : ''

  // config.toml content
  const configContent = `model_provider = "${providerKey}"
model = "${model}"
review_model = "${reviewModel}"
${modelCatalogConfig}model_reasoning_effort = "xhigh"
disable_response_storage = true
network_access = "enabled"
windows_wsl_setup_acknowledged = true

[model_providers.${providerKey}]
name = "${providerName}"
base_url = "${baseUrl}"
wire_api = "responses"
requires_openai_auth = true

[features]
goals = true`

  // auth.json content
  const authContent = `{
  "OPENAI_API_KEY": "${apiKey}"
}`

  const files = [
    {
      path: `${configDir}/config.toml`,
      content: configContent,
      hint: t('keys.useKeyModal.openai.configTomlHint')
    },
    {
      path: `${configDir}/auth.json`,
      content: authContent
    }
  ]
  if (modelCatalogContent) {
    files.push({
      path: `${configDir}/${CODEX_MODEL_CATALOG_FILENAME}`,
      content: modelCatalogContent
    })
  }
  return [...files, generateCodexSetupScript(configContent, authContent, isWindows, modelCatalogContent)]
}

function generateOpenAIWsFiles(baseUrl: string, apiKey: string): FileConfig[] {
  const isWindows = activeTab.value === 'windows'
  const configDir = isWindows ? '%userprofile%\\.codex' : '~/.codex'
  const modelCatalogContent = currentCodexModelCatalogContent()
  const modelCatalogConfig = modelCatalogContent ? `model_catalog_json = "${CODEX_MODEL_CATALOG_FILENAME}"\n` : ''

  // config.toml content with WebSocket v2
  const configContent = `model_provider = "OpenAI"
model = "gpt-5.5"
review_model = "gpt-5.5"
${modelCatalogConfig}model_reasoning_effort = "xhigh"
disable_response_storage = true
network_access = "enabled"
windows_wsl_setup_acknowledged = true

[model_providers.OpenAI]
name = "OpenAI"
base_url = "${baseUrl}"
wire_api = "responses"
supports_websockets = true
requires_openai_auth = true

[features]
responses_websockets_v2 = true
goals = true`

  // auth.json content
  const authContent = `{
  "OPENAI_API_KEY": "${apiKey}"
}`

  const files = [
    {
      path: `${configDir}/config.toml`,
      content: configContent,
      hint: t('keys.useKeyModal.openai.configTomlHint')
    },
    {
      path: `${configDir}/auth.json`,
      content: authContent
    }
  ]
  if (modelCatalogContent) {
    files.push({
      path: `${configDir}/${CODEX_MODEL_CATALOG_FILENAME}`,
      content: modelCatalogContent
    })
  }
  return [...files, generateCodexSetupScript(configContent, authContent, isWindows, modelCatalogContent)]
}

function generateCodexSetupScript(
  configContent: string,
  authContent: string,
  isWindows: boolean,
  modelCatalogContent: string | null = null
): FileConfig {
  if (isWindows) {
    const modelCatalogBlock = modelCatalogContent ? `
$modelCatalogJson = @'
${modelCatalogContent}
'@
` : ''
    const modelCatalogWrite = modelCatalogContent
      ? `[System.IO.File]::WriteAllText((Join-Path $configDir "${CODEX_MODEL_CATALOG_FILENAME}"), $modelCatalogJson, $utf8NoBom)\n`
      : ''
    const content = `$ErrorActionPreference = "Stop"
$configDir = Join-Path $env:USERPROFILE ".codex"
New-Item -ItemType Directory -Force -Path $configDir | Out-Null

$configToml = @'
${configContent}
'@

$authJson = @'
${authContent}
'@
${modelCatalogBlock}

$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
[System.IO.File]::WriteAllText((Join-Path $configDir "config.toml"), $configToml, $utf8NoBom)
[System.IO.File]::WriteAllText((Join-Path $configDir "auth.json"), $authJson, $utf8NoBom)
${modelCatalogWrite}Write-Host "Codex CLI configuration written to $configDir"`

    return {
      path: 'setup-codex.ps1',
      content,
      hint: t('keys.useKeyModal.openai.setupScriptHintWindows')
    }
  }

  const modelCatalogWrite = modelCatalogContent ? `
cat > "$config_dir/${CODEX_MODEL_CATALOG_FILENAME}" <<'EOF'
${modelCatalogContent}
EOF
` : ''
  const content = `#!/usr/bin/env bash
set -euo pipefail

config_dir="\${HOME}/.codex"
mkdir -p "$config_dir"

cat > "$config_dir/config.toml" <<'EOF'
${configContent}
EOF

cat > "$config_dir/auth.json" <<'EOF'
${authContent}
EOF
${modelCatalogWrite}

chmod 600 "$config_dir/auth.json"
echo "Codex CLI configuration written to $config_dir"`

  return {
    path: 'setup-codex.sh',
    content,
    hint: t('keys.useKeyModal.openai.setupScriptHintUnix')
  }
}

interface OpenCodeProviderOptions {
  name?: string
  npm?: string
  models?: Record<string, any>
  agent?: boolean
}

function generateOpenCodeConfig(
  platform: string,
  baseUrl: string,
  apiKey: string,
  pathLabel?: string,
  options: OpenCodeProviderOptions = {},
): FileConfig {
  const provider: Record<string, any> = {
    [platform]: {
      options: {
        baseURL: baseUrl,
        apiKey
      }
    }
  }
  const openaiModels = {
    'gpt-5.2': {
      name: 'GPT-5.2',
      limit: {
        context: 400000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.5': {
      name: 'GPT-5.5',
      limit: {
        context: 1050000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.4': {
      name: 'GPT-5.4',
      limit: {
        context: 1050000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.4-mini': {
      name: 'GPT-5.4 Mini',
      limit: {
        context: 400000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.3-codex-spark': {
      name: 'GPT-5.3 Codex Spark',
      limit: {
        context: 128000,
        output: 32000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'gpt-5.3-codex': {
      name: 'GPT-5.3 Codex',
      limit: {
        context: 400000,
        output: 128000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {}
      }
    },
    'codex-mini-latest': {
      name: 'Codex Mini',
      limit: {
        context: 200000,
        output: 100000
      },
      options: {
        store: false
      },
      variants: {
        low: {},
        medium: {},
        high: {}
      }
    }
  }
  const geminiModels = {
    'gemini-2.0-flash': {
      name: 'Gemini 2.0 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-2.5-flash': {
      name: 'Gemini 2.5 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-2.5-pro': {
      name: 'Gemini 2.5 Pro',
      limit: {
        context: 2097152,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.5-flash': {
      name: 'Gemini 3.5 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-3-flash-preview': {
      name: 'Gemini 3 Flash Preview',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      }
    },
    'gemini-3-pro-preview': {
      name: 'Gemini 3 Pro Preview',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-pro-preview': {
      name: 'Gemini 3.1 Pro Preview',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    }
  }

  const antigravityGeminiModels = {
    'gemini-2.5-flash': {
      name: 'Gemini 2.5 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'disable'
        }
      }
    },
    'gemini-2.5-flash-lite': {
      name: 'Gemini 2.5 Flash Lite',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-2.5-flash-thinking': {
      name: 'Gemini 2.5 Flash (Thinking)',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3-flash': {
      name: 'Gemini 3 Flash',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-pro-low': {
      name: 'Gemini 3.1 Pro Low',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-pro-high': {
      name: 'Gemini 3.1 Pro High',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-2.5-flash-image': {
      name: 'Gemini 2.5 Flash Image',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image'],
        output: ['image']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'gemini-3.1-flash-image': {
      name: 'Gemini 3.1 Flash Image',
      limit: {
        context: 1048576,
        output: 65536
      },
      modalities: {
        input: ['text', 'image'],
        output: ['image']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    }
  }
  const claudeModels = {
    'claude-opus-4-6-thinking': {
      name: 'Claude 4.6 Opus (Thinking)',
      limit: {
        context: 200000,
        output: 128000
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    },
    'claude-sonnet-4-6': {
      name: 'Claude 4.6 Sonnet',
      limit: {
        context: 200000,
        output: 64000
      },
      modalities: {
        input: ['text', 'image', 'pdf'],
        output: ['text']
      },
      options: {
        thinking: {
          budgetTokens: 24576,
          type: 'enabled'
        }
      }
    }
  }

  const resolvedProvider = provider[platform]
  if (options.name) {
    resolvedProvider.name = options.name
  }
  if (options.npm) {
    resolvedProvider.npm = options.npm
  }
  if (options.models) {
    resolvedProvider.models = options.models
  } else if (platform === 'gemini') {
    resolvedProvider.npm = '@ai-sdk/google'
    resolvedProvider.models = geminiModels
  } else if (platform === 'anthropic') {
    resolvedProvider.npm = '@ai-sdk/anthropic'
  } else if (platform === 'antigravity-claude') {
    resolvedProvider.npm = '@ai-sdk/anthropic'
    resolvedProvider.name = 'Antigravity (Claude)'
    resolvedProvider.models = claudeModels
  } else if (platform === 'antigravity-gemini') {
    resolvedProvider.npm = '@ai-sdk/google'
    resolvedProvider.name = 'Antigravity (Gemini)'
    resolvedProvider.models = antigravityGeminiModels
  } else if (platform === 'openai') {
    resolvedProvider.models = openaiModels
  }

  const agent =
    platform === 'openai' || options.agent
      ? {
          build: {
            options: {
              store: false
            }
          },
          plan: {
            options: {
              store: false
            }
          }
        }
      : undefined

  const content = JSON.stringify(
    {
      provider,
      ...(agent ? { agent } : {}),
      $schema: 'https://opencode.ai/config.json'
    },
    null,
    2
  )

  return {
    path: pathLabel ?? 'opencode.json',
    content,
    hint: t('keys.useKeyModal.opencode.hint')
  }
}

function geminiModelsForOpenCode(): Record<string, any> {
  return {
    'gemini-2.5-pro': {
      name: 'Gemini 2.5 Pro',
      limit: {
        context: 2097152,
        output: 65536,
      },
      options: {
        store: false,
      },
      variants: {
        low: {},
        medium: {},
        high: {},
        xhigh: {},
      },
    },
    'gemini-2.5-flash': {
      name: 'Gemini 2.5 Flash',
      limit: {
        context: 1048576,
        output: 65536,
      },
      options: {
        store: false,
      },
      variants: {
        low: {},
        medium: {},
        high: {},
      },
    },
  }
}

const copyContent = async (content: string, index: number) => {
  const success = await clipboardCopy(content, t('keys.copied'))
  if (success) {
    copiedIndex.value = index
    setTimeout(() => {
      copiedIndex.value = null
    }, 2000)
  }
}
</script>
