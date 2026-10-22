<template>
  <div>
    <p v-if="isArena" role="status" class="mb-3 text-sm text-gray-500 dark:text-gray-400">
      {{ t('admin.accounts.arena.modelsHint') }}
    </p>
    <!-- Multi-select Dropdown -->
    <div class="relative mb-3">
      <div
        @click="toggleDropdown"
        class="cursor-pointer rounded-lg border border-gray-300 bg-white px-3 py-2 dark:border-dark-500 dark:bg-dark-700"
      >
        <div class="grid grid-cols-2 gap-1.5">
          <span
            v-for="model in modelValue"
            :key="model"
            class="inline-flex items-center justify-between gap-1 rounded bg-gray-100 px-2 py-1 text-xs text-gray-700 dark:bg-dark-600 dark:text-gray-300"
          >
            <span class="flex min-w-0 flex-1 items-center gap-1 truncate" :title="model">
              <ModelIcon :model="model" size="14px" />
              <span class="truncate">{{ model }}</span>
            </span>
            <button
              type="button"
              @click.stop="removeModel(model)"
              class="shrink-0 rounded-full hover:bg-gray-200 dark:hover:bg-dark-500"
            >
              <Icon name="x" size="xs" class="h-3.5 w-3.5" :stroke-width="2" />
            </button>
            <button
              type="button"
              data-testid="copy-selected-model-id"
              class="shrink-0 rounded p-0.5 text-gray-400 hover:bg-gray-200 hover:text-primary-600 dark:text-gray-500 dark:hover:bg-dark-500 dark:hover:text-primary-400"
              :title="`${t('common.copy')} ${model}`"
              :aria-label="`${t('common.copy')} ${model}`"
              @click.stop.prevent="copyModelId(model)"
            >
              <Icon name="copy" size="xs" />
            </button>
          </span>
        </div>
        <div class="mt-2 flex items-center justify-between border-t border-gray-200 pt-2 dark:border-dark-600">
          <span class="text-xs text-gray-400">{{ t('admin.accounts.modelCount', { count: modelValue.length }) }}</span>
          <svg class="h-5 w-5 text-gray-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
          </svg>
        </div>
      </div>
      <!-- Dropdown List -->
      <div
        v-if="showDropdown"
        class="absolute left-0 right-0 top-full z-50 mt-1 rounded-lg border border-gray-200 bg-white shadow-lg dark:border-dark-600 dark:bg-dark-700"
      >
        <div class="sticky top-0 border-b border-gray-200 bg-white p-2 dark:border-dark-600 dark:bg-dark-700">
          <input
            v-model="searchQuery"
            type="text"
            class="input w-full text-sm"
            :placeholder="t('admin.accounts.searchModels')"
            @click.stop
          />
        </div>
        <div class="max-h-52 overflow-auto">
          <div
            v-for="model in filteredModels"
            :key="model.value"
            data-testid="model-option"
            class="group flex items-center hover:bg-gray-100 dark:hover:bg-dark-600"
          >
            <button
              type="button"
              data-testid="select-model"
              class="flex min-w-0 flex-1 items-center gap-2 px-3 py-2 text-left text-sm"
              @click="toggleModel(model.value)"
            >
              <span
                :class="[
                  'flex h-4 w-4 shrink-0 items-center justify-center rounded border',
                  modelValue.includes(model.value)
                    ? 'border-primary-500 bg-primary-500 text-white'
                    : 'border-gray-300 dark:border-dark-500'
                ]"
              >
                <svg v-if="modelValue.includes(model.value)" class="h-3 w-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="3" d="M5 13l4 4L19 7" />
                </svg>
              </span>
              <ModelIcon :model="model.value" size="18px" />
              <span class="truncate text-gray-900 dark:text-white" :title="model.value">{{ model.value }}</span>
              <span
                v-if="model.label && model.label !== model.value"
                data-testid="model-display-name"
                class="shrink-0 truncate text-xs text-gray-400 dark:text-gray-500"
                :title="model.label"
              >{{ model.label }}</span>
              <span
                v-if="model.enable === false"
                data-testid="model-unavailable"
                class="shrink-0 rounded bg-amber-50 px-1.5 py-0.5 text-[11px] text-amber-600 dark:bg-amber-900/30 dark:text-amber-400"
              >{{ t('admin.accounts.qoderModelNotEnabled') }}</span>
              <span
                v-else-if="model.free"
                class="shrink-0 rounded bg-emerald-50 px-1.5 py-0.5 text-[11px] text-emerald-600 dark:bg-emerald-900/30 dark:text-emerald-400"
              >{{ t('admin.accounts.qoderModelFree') }}</span>
            </button>
            <button
              type="button"
              data-testid="copy-model-id"
              class="mr-2 rounded p-1.5 text-gray-400 opacity-70 transition-colors hover:bg-gray-200 hover:text-primary-600 focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 group-hover:opacity-100 dark:text-gray-500 dark:hover:bg-dark-500 dark:hover:text-primary-400"
              :title="`${t('common.copy')} ${model.value}`"
              :aria-label="`${t('common.copy')} ${model.value}`"
              @click.stop.prevent="copyModelId(model.value)"
            >
              <Icon name="copy" size="sm" />
            </button>
          </div>
          <div v-if="filteredModels.length === 0" class="px-3 py-4 text-center text-sm text-gray-500">
            {{ t('admin.accounts.noMatchingModels') }}
          </div>
        </div>
      </div>
    </div>

    <!-- Quick Actions -->
    <div class="mb-4 flex flex-wrap gap-2">
      <button
        type="button"
        v-if="!isArena"
        @click="fillRelated"
        class="rounded-lg border border-blue-200 px-3 py-1.5 text-sm text-blue-600 hover:bg-blue-50 dark:border-blue-800 dark:text-blue-400 dark:hover:bg-blue-900/30"
      >
        {{ t('admin.accounts.fillRelatedModels') }}
      </button>
      <button
        v-if="canSyncUpstream"
        type="button"
        @click="syncUpstreamModels"
        :disabled="isSyncingUpstream"
        class="rounded-lg border border-emerald-200 px-3 py-1.5 text-sm text-emerald-600 hover:bg-emerald-50 disabled:cursor-not-allowed disabled:opacity-60 dark:border-emerald-800 dark:text-emerald-400 dark:hover:bg-emerald-900/30"
      >
        {{ isSyncingUpstream ? t('admin.accounts.syncUpstreamModelsLoading') : t('admin.accounts.syncUpstreamModels') }}
      </button>
      <button
        v-if="enableAliasMapping"
        type="button"
        data-testid="sync-model-aliases"
        @click="syncGlobalAliases"
        :disabled="isSyncingAliases"
        class="rounded-lg border border-violet-200 px-3 py-1.5 text-sm text-violet-600 hover:bg-violet-50 disabled:cursor-not-allowed disabled:opacity-60 dark:border-violet-800 dark:text-violet-400 dark:hover:bg-violet-900/30"
      >
        {{ isSyncingAliases ? t('admin.accounts.syncModelAliasesLoading') : t('admin.accounts.syncModelAliases') }}
      </button>
      <button
        type="button"
        @click="clearAll"
        class="rounded-lg border border-red-200 px-3 py-1.5 text-sm text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-900/30"
      >
        {{ t('admin.accounts.clearAllModels') }}
      </button>
    </div>

    <!-- Custom Model Input -->
    <div class="mb-3">
      <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.accounts.customModelName') }}</label>
      <div class="flex gap-2">
        <input
          v-model="customModel"
          type="text"
          class="input flex-1"
          :placeholder="t('admin.accounts.enterCustomModelName')"
          @keydown.enter.prevent="handleEnter"
          @compositionstart="isComposing = true"
          @compositionend="isComposing = false"
        />
        <button
          type="button"
          @click="addCustom"
          class="rounded-lg bg-primary-50 px-4 py-2 text-sm font-medium text-primary-600 hover:bg-primary-100 dark:bg-primary-900/30 dark:text-primary-400 dark:hover:bg-primary-900/50"
        >
          {{ t('admin.accounts.addModel') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { accountsAPI } from '@/api/admin/accounts'
import type { UpstreamModelSyncApi, UpstreamModelSyncPreviewParams } from '@/components/account/accountApi'
import { getModelAliasPolicy, type ModelAliasGroup } from '@/api/admin/settings'
import { useClipboard } from '@/composables/useClipboard'
import ModelIcon from '@/components/common/ModelIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { allModels, getModelsByPlatform } from '@/composables/useModelWhitelist'
import { supportsUpstreamModelSync } from '@/constants/platformCatalog'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()

const props = defineProps<{
  modelValue: string[]
  enableAliasMapping?: boolean
  modelMappings?: { from: string; to: string }[]
  platform?: string
  platforms?: string[]
  accountId?: number
  /**
   * 账号作用域 API（管理员 `adminAPI.accounts` 或用户 `userAccountsAPI`）。
   * 父级必须以 kebab `:account-api` 传入；缺省回退到管理员 API，
   * 保持管理端既有行为。用户侧传入后再也不会打到 /admin/*（否则 403）。
   */
  accountApi?: UpstreamModelSyncApi
  syncCredentials?: {
    platform: string
    type: string
    base_url?: string
    api_key: string
  }
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string[]]
  'upstream-synced': []
  'global-aliases-mapped': [mappings: Array<{ from: string; to: string }>]
}>()

const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const showDropdown = ref(false)
const searchQuery = ref('')
const customModel = ref('')
const isComposing = ref(false)
const isSyncingUpstream = ref(false)
const isSyncingAliases = ref(false)
const syncedModels = ref<string[]>([])
const isArena = computed(() => normalizedPlatforms.value.includes('arena'))
const normalizedPlatforms = computed(() => {
  const rawPlatforms =
    props.platforms && props.platforms.length > 0
      ? props.platforms
      : props.platform
        ? [props.platform]
        : []

  return Array.from(
    new Set(
      rawPlatforms
        .map(platform => platform?.trim())
        .filter((platform): platform is string => Boolean(platform))
    )
  )
})

let catalogVersion = 0
watch(() => [props.accountId, props.platform, props.platforms, props.syncCredentials], () => {
  catalogVersion += 1
  syncedModels.value = []
  void loadCatalogOptions()
}, { deep: true, flush: 'sync', immediate: true })

// 作用域 API：父级传入的账号 API（用户侧 userAccountsAPI）优先；未传入时才
// 惰性回退到管理员 API，保持管理端既有行为。回退必须惰性求值——否则只用到
// 平台白名单、并不需要同步能力的消费方（如批量编辑）也会在渲染期就去碰
// 管理员 API 模块。
const resolveSyncUpstreamApi = (): UpstreamModelSyncApi => props.accountApi ?? accountsAPI

const canSyncUpstream = computed(() => {
  if (props.accountId) {
    if (props.accountApi && typeof props.accountApi.syncUpstreamModels !== 'function') return false
    if (normalizedPlatforms.value.length === 0) return true
    return normalizedPlatforms.value.some(supportsUpstreamModelSync)
  }
  if (props.syncCredentials) {
    if (props.accountApi && typeof props.accountApi.syncUpstreamModelsPreview !== 'function') return false
    return supportsUpstreamModelSync(props.syncCredentials.platform)
  }
  return false
})

const availableOptions = computed(() => {
  if (normalizedPlatforms.value.length === 0) {
    return allModels
  }

  const allowedModels = new Set<string>()
  for (const platform of normalizedPlatforms.value) {
    for (const model of getModelsByPlatform(platform)) {
      allowedModels.add(model)
    }
  }

  const options = allModels.filter(model => allowedModels.has(model.value))
  const existing = new Set(options.map(model => model.value))
  for (const model of [...syncedModels.value, ...props.modelValue]) {
    if (!existing.has(model)) {
      options.push({ value: model, label: model })
      existing.add(model)
    }
  }
  return options
})

// 「已同步目录」的候选：测试弹窗用的是同一个接口（/admin/accounts/:id/models），
// 因此这里以它为准，避免白名单硬编码与账号真实目录不一致。
interface CatalogOption {
  value: string
  label: string
  /** 上游目录可用性；undefined 表示未知（旧快照/平台预设）。 */
  enable?: boolean
  free?: boolean
}

const catalogOptions = ref<CatalogOption[]>([])

const loadCatalogOptions = async () => {
  if (!props.accountId) {
    catalogOptions.value = []
    return
  }
  const api = props.accountApi ?? accountsAPI
  if (typeof api.getAvailableModels !== 'function') {
    catalogOptions.value = []
    return
  }
  try {
    const models = await api.getAvailableModels(props.accountId)
    catalogOptions.value = models
      .map(model => ({
        value: model.id,
        label: model.display_name || model.id,
        enable: model.enable,
        free: model.free,
      }))
      .filter(option => option.value)
  } catch {
    // 目录不可得时保持空：调用方回退平台预设。
    catalogOptions.value = []
  }
}

const allCandidateOptions = computed<CatalogOption[]>(() => {
  if (catalogOptions.value.length === 0) return availableOptions.value
  const seen = new Set<string>()
  const options: CatalogOption[] = []
  for (const option of catalogOptions.value) {
    if (seen.has(option.value)) continue
    seen.add(option.value)
    options.push(option)
  }
  for (const model of props.modelValue) {
    if (seen.has(model)) continue
    seen.add(model)
    options.push({ value: model, label: model })
  }
  return options
})

const filteredModels = computed(() => {
  const query = searchQuery.value.toLowerCase().trim()
  if (!query) return allCandidateOptions.value
  return allCandidateOptions.value.filter(
    m => m.value.toLowerCase().includes(query) || m.label.toLowerCase().includes(query)
  )
})

const toggleDropdown = () => {
  showDropdown.value = !showDropdown.value
  if (!showDropdown.value) searchQuery.value = ''
}

const removeModel = (model: string) => {
  emit('update:modelValue', props.modelValue.filter(m => m !== model))
}

const toggleModel = (model: string) => {
  if (props.modelValue.includes(model)) {
    removeModel(model)
  } else {
    emit('update:modelValue', [...props.modelValue, model])
  }
}

const copyModelId = async (model: string) => {
  await copyToClipboard(model)
}

const modelAliasMappings = (groups: ModelAliasGroup[]) => {
  const mappings = new Map<string, string>()
  const targetIDs = props.modelValue.length > 0 || syncedModels.value.length > 0
    ? [...syncedModels.value, ...props.modelValue]
    : availableOptions.value.map(model => model.value)
  for (const group of groups) {
    const equivalentIDs = [group.canonical, ...group.aliases]
      .map(model => model.trim())
      .filter(Boolean)

    // 优先使用上游目录中的真实 ID；同义词来源无需出现在候选列表中。
    const target = targetIDs.find(model => equivalentIDs.includes(model))
    if (!target) continue
    for (const model of equivalentIDs) {
      if (model !== target) mappings.set(model, target)
    }
  }
  return [...mappings].map(([from, to]) => ({ from, to }))
}

const syncGlobalAliases = async () => {
  if (isSyncingAliases.value) return
  isSyncingAliases.value = true
  const requestVersion = catalogVersion
  try {
    const policy = await getModelAliasPolicy()
    if (requestVersion !== catalogVersion) return
    const mappings = modelAliasMappings(policy.groups)
    if (mappings.length === 0) {
      appStore.showInfo(t('admin.accounts.syncModelAliasesEmpty'))
      return
    }
    emit('global-aliases-mapped', mappings)
  } catch (error) {
    const message = error instanceof Error ? error.message : t('admin.accounts.syncModelAliasesFailed')
    appStore.showError(t('admin.accounts.syncModelAliasesError', { message }))
  } finally {
    isSyncingAliases.value = false
  }
}

const addCustom = () => {
  const model = customModel.value.trim()
  if (!model) return
  if (props.modelValue.includes(model)) {
    appStore.showInfo(t('admin.accounts.modelExists'))
    return
  }
  const conflict = props.modelMappings?.find(mapping => mapping.from.trim() === model && mapping.to.trim() && mapping.to.trim() !== model)
  if (conflict) {
    appStore.showInfo(t('admin.accounts.modelMappingConflict', { from: model, to: conflict.to.trim() }))
    return
  }
  emit('update:modelValue', [...props.modelValue, model])
  customModel.value = ''
}

const handleEnter = () => {
  if (!isComposing.value) addCustom()
}

const fillRelated = () => {
  const newModels = [...props.modelValue]
  if (catalogOptions.value.length > 0) {
    for (const option of catalogOptions.value) {
      if (!newModels.includes(option.value)) newModels.push(option.value)
    }
    emit('update:modelValue', newModels)
    return
  }
  for (const platform of normalizedPlatforms.value) {
    for (const model of getModelsByPlatform(platform)) {
      if (!newModels.includes(model)) {
        newModels.push(model)
      }
    }
  }
  emit('update:modelValue', newModels)
}

const syncUpstreamModels = async () => {
  if (isSyncingUpstream.value || !canSyncUpstream.value) return

  isSyncingUpstream.value = true
  const requestVersion = catalogVersion
  try {
    let result
    const api = resolveSyncUpstreamApi()
    if (props.accountId) {
      if (typeof api.syncUpstreamModels !== 'function') {
        throw new Error(t('admin.accounts.syncUpstreamModelsFailed'))
      }
      result = await api.syncUpstreamModels(props.accountId)
    } else if (props.syncCredentials) {
      if (typeof api.syncUpstreamModelsPreview !== 'function') {
        throw new Error(t('admin.accounts.syncUpstreamModelsFailed'))
      }
      result = await api.syncUpstreamModelsPreview(
        props.syncCredentials as UpstreamModelSyncPreviewParams,
      )
    } else {
      return
    }

    if (requestVersion !== catalogVersion) return
    const upstreamModels = result.models.map(model => model.trim()).filter(Boolean)
    syncedModels.value = upstreamModels
    if (upstreamModels.length === 0) {
      appStore.showInfo(t(isArena.value ? 'admin.accounts.arena.modelsEmpty' : 'admin.accounts.syncUpstreamModelsEmpty'))
      return
    }

    if (!props.accountId) {
      emit('upstream-synced')
    }

    // 同步完成后刷新「真实目录」，让白名单候选与测试弹窗保持一致。
    void loadCatalogOptions()

    const newModels = [...props.modelValue]
    let addedCount = 0
    for (const model of upstreamModels) {
      if (!newModels.includes(model)) {
        newModels.push(model)
        addedCount += 1
      }
    }

    emit('update:modelValue', newModels)
    const warnings = result.warnings ?? []
    const hasPartialMetadata = warnings.some(
      warning => warning.code === 'upstream_model_metadata_partial'
    )
    const hasIncompleteMetadata = warnings.some(
      warning => warning.code === 'upstream_model_metadata_incomplete'
    )
    // 平台没有可直接拉取的目录时，同步结果来自内置官方 ID，必须明确告知而不是
    // 让用户以为这就是账号套餐的真实授权列表。
    const usedBuiltinCatalog = warnings.some(
      warning => warning.code === 'qoder_catalog_builtin'
    )
    const syncedCatalog = warnings.find(
      warning => warning.code === 'qoder_catalog_synced'
    )
    const noEnabledModels = warnings.some(
      warning => warning.code === 'qoder_catalog_no_enabled_models'
    )
    if (hasIncompleteMetadata) {
      appStore.showWarning(t('admin.accounts.syncUpstreamModelsMetadataIncomplete'))
      return
    }
    if (usedBuiltinCatalog) {
      appStore.showInfo(t('admin.accounts.syncUpstreamModelsBuiltinCatalog'))
      return
    }
    if (syncedCatalog) {
      const enabledMatch = /(\d+)\s+enabled/.exec(syncedCatalog.message || '')
      appStore.showInfo(
        t('admin.accounts.syncUpstreamModelsSyncedCatalog', {
          total: upstreamModels.length,
          enabled: enabledMatch ? Number(enabledMatch[1]) : upstreamModels.length,
        })
      )
      if (noEnabledModels) {
        appStore.showWarning(t('admin.accounts.syncUpstreamModelsNoEnabledModels'))
      }
      return
    }
    if (addedCount > 0) {
      appStore.showSuccess(t('admin.accounts.syncUpstreamModelsSuccess', { count: addedCount, total: upstreamModels.length }))
    } else {
      appStore.showInfo(t('admin.accounts.syncUpstreamModelsNoChanges', { count: upstreamModels.length }))
    }
    if (hasPartialMetadata) {
      appStore.showWarning(t('admin.accounts.syncUpstreamModelsMetadataPartial'))
    }
  } catch (error) {
    const message = extractApiErrorMessage(error, t('admin.accounts.syncUpstreamModelsFailed'))
    appStore.showError(t('admin.accounts.syncUpstreamModelsError', { message }))
  } finally {
    isSyncingUpstream.value = false
  }
}

const clearAll = () => {
  emit('update:modelValue', [])
}

</script>
