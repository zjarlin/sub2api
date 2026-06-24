<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <AccountTableActions :loading="loading" @refresh="loadAccounts" @create="openCreate" />
      </template>

      <template #filters>
        <div class="flex flex-wrap items-center gap-3">
          <SearchInput
            v-model="filters.search"
            :placeholder="t('myAccounts.searchPlaceholder')"
            class="w-full sm:w-72"
            @search="handleFiltersChanged"
          />
          <Select
            v-model="filters.platform"
            class="w-44"
            :options="platformFilterOptions"
            @change="handleFiltersChanged"
          />
          <Select
            v-model="filters.type"
            class="w-44"
            :options="typeFilterOptions"
            @change="handleFiltersChanged"
          />
          <Select
            v-model="filters.status"
            class="w-44"
            :options="statusFilterOptions"
            @change="handleFiltersChanged"
          />
        </div>
      </template>

      <template #table>
        <DataTable
          :columns="columns"
          :data="accounts"
          :loading="loading"
          row-key="id"
          :server-side-sort="true"
          default-sort-key="created_at"
          default-sort-order="desc"
          :estimate-row-height="72"
          :overscan="5"
          @sort="handleSort"
        >
          <template #empty>
            <div class="flex flex-col items-center px-6 py-8 text-center">
              <div class="mb-4 rounded-lg bg-gray-100 p-3 text-gray-500 dark:bg-dark-700 dark:text-gray-300">
                <Icon name="globe" size="lg" />
              </div>
              <p class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('myAccounts.empty') }}</p>
              <p class="mt-1 max-w-md text-sm text-gray-500 dark:text-gray-400">{{ t('myAccounts.emptyHint') }}</p>
              <button type="button" class="btn btn-primary mt-5" @click="openCreate">
                <Icon name="plus" size="sm" class="mr-2" />
                {{ t('admin.accounts.createAccount') }}
              </button>
            </div>
          </template>

          <template #cell-name="{ row }">
            <div class="flex min-w-[220px] flex-col">
              <div class="flex min-w-0 items-center gap-2">
                <span class="truncate font-medium text-gray-900 dark:text-white">{{ row.name }}</span>
                <span
                  v-if="getOpenAIVendorLabel(row)"
                  class="shrink-0 rounded bg-emerald-100 px-1.5 py-0.5 text-[10px] font-semibold text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300"
                >
                  {{ getOpenAIVendorLabel(row) }}
                </span>
              </div>
              <span
                v-if="row.notes"
                class="mt-1 block max-w-xs truncate text-xs text-gray-500 dark:text-gray-400"
                :title="row.notes"
              >
                {{ row.notes }}
              </span>
            </div>
          </template>

          <template #cell-platform_type="{ row }">
            <div class="flex flex-col gap-1">
              <PlatformTypeBadge
                :platform="row.platform"
                :type="row.type"
                :plan-type="row.credentials?.plan_type"
                :overages-enabled="row.extra?.allow_overages === true"
                :privacy-mode="row.extra?.privacy_mode"
                :subscription-expires-at="row.credentials?.subscription_expires_at"
              />
              <span
                v-if="getCredentialBaseURL(row)"
                class="max-w-[220px] truncate pl-0.5 text-[11px] text-gray-400 dark:text-dark-400"
                :title="getCredentialBaseURL(row)"
              >
                {{ getCredentialBaseURL(row) }}
              </span>
            </div>
          </template>

          <template #cell-status="{ row }">
            <AccountStatusIndicator :account="row" />
          </template>

          <template #cell-groups="{ row }">
            <AccountGroupsCell :groups="row.groups" />
          </template>

          <template #cell-capacity="{ row }">
            <AccountCapacityCell :account="row" />
          </template>

          <template #cell-priority="{ value }">
            <span class="text-sm text-gray-700 dark:text-gray-300">{{ value }}</span>
          </template>

          <template #cell-last_used_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">{{ formatRelativeTime(value) }}</span>
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">{{ formatDateTime(value) }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <button
                type="button"
                class="account-row-action hover:text-primary-600 dark:hover:text-primary-400"
                :title="t('myAccounts.test')"
                :disabled="testingId === row.id"
                @click="testAccount(row)"
              >
                <Icon name="beaker" size="sm" :class="testingId === row.id ? 'animate-pulse' : ''" />
                <span class="text-xs">{{ testingId === row.id ? t('myAccounts.testing') : t('myAccounts.test') }}</span>
              </button>
              <button
                type="button"
                class="account-row-action hover:text-primary-600 dark:hover:text-primary-400"
                :title="t('common.edit')"
                @click="openEdit(row)"
              >
                <Icon name="edit" size="sm" />
                <span class="text-xs">{{ t('common.edit') }}</span>
              </button>
              <button
                type="button"
                class="account-row-action hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
                :title="t('common.delete')"
                @click="deleteAccount(row)"
              >
                <Icon name="trash" size="sm" />
                <span class="text-xs">{{ t('common.delete') }}</span>
              </button>
            </div>
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>

    <BaseDialog
      :show="showAccountDialog"
      :title="editingAccount ? t('admin.accounts.editAccount') : t('admin.accounts.createAccount')"
      width="wide"
      @close="closeDialog"
    >
      <form id="my-account-form" class="space-y-5" @submit.prevent="submitForm">
        <div>
          <label class="input-label">{{ t('admin.accounts.accountName') }}</label>
          <input
            v-model.trim="form.name"
            type="text"
            required
            class="input"
            :placeholder="t('admin.accounts.enterAccountName')"
          />
        </div>

        <div>
          <label class="input-label">{{ t('admin.accounts.notes') }}</label>
          <textarea
            v-model="form.notes"
            rows="3"
            class="input"
            :placeholder="t('admin.accounts.notesPlaceholder')"
          ></textarea>
          <p class="input-hint">{{ t('myAccounts.privateAccountHint') }}</p>
        </div>

        <GroupSelector
          v-model="form.group_ids"
          :groups="selectableGroups"
          :platform="form.platform"
          :mixed-scheduling="false"
        />

        <div>
          <label class="input-label">{{ t('admin.accounts.platform') }}</label>
          <div class="mt-2 grid grid-cols-2 gap-2 rounded-lg bg-gray-100 p-1 dark:bg-dark-700 md:grid-cols-5">
            <button
              v-for="option in platformOptions"
              :key="option.value"
              type="button"
              :disabled="!!editingAccount"
              :class="[
                'flex min-h-[2.75rem] items-center justify-center gap-2 rounded-md px-3 py-2 text-sm font-medium transition-all disabled:cursor-not-allowed disabled:opacity-70',
                form.platform === option.value
                  ? option.activeClass
                  : 'text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-200'
              ]"
              @click="setPlatform(option.value)"
            >
              <Icon :name="option.icon" size="sm" />
              <span>{{ option.label }}</span>
            </button>
          </div>
          <p v-if="editingAccount" class="input-hint">{{ t('myAccounts.platformLockedHint') }}</p>
        </div>

        <div>
          <label class="input-label">{{ t('admin.accounts.accountType') }}</label>
          <div class="mt-2 grid grid-cols-1 gap-3 sm:grid-cols-3">
            <button
              v-for="option in accountTypeOptions"
              :key="option.value"
              type="button"
              :class="[
                'flex items-center gap-3 rounded-lg border-2 p-3 text-left transition-all',
                form.type === option.value
                  ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/20'
                  : 'border-gray-200 hover:border-primary-300 dark:border-dark-600 dark:hover:border-primary-700'
              ]"
              @click="form.type = option.value"
            >
              <div
                :class="[
                  'flex h-8 w-8 shrink-0 items-center justify-center rounded-lg',
                  form.type === option.value
                    ? 'bg-primary-500 text-white'
                    : 'bg-gray-100 text-gray-500 dark:bg-dark-600 dark:text-gray-400'
                ]"
              >
                <Icon :name="option.icon" size="sm" />
              </div>
              <div>
                <span class="block text-sm font-medium text-gray-900 dark:text-white">{{ option.label }}</span>
                <span class="text-xs text-gray-500 dark:text-gray-400">{{ option.description }}</span>
              </div>
            </button>
          </div>
        </div>

        <div v-if="form.platform === 'openai' && form.type === 'apikey'" class="grid gap-4 md:grid-cols-2">
          <div class="md:col-span-2">
            <label class="input-label">{{ t('admin.accounts.openai.vendorPreset') }}</label>
            <Select
              v-model="form.vendor"
              :options="openAIVendorOptions"
              searchable
              @change="handleOpenAIVendorChanged"
            />
            <p class="input-hint">{{ t('admin.accounts.openai.vendorPresetHint') }}</p>
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.openai.authHeader') }}</label>
            <input v-model.trim="form.authHeader" type="text" class="input font-mono" :placeholder="selectedVendorPreset.authHeader" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.openai.authScheme') }}</label>
            <input v-model.trim="form.authScheme" type="text" class="input font-mono" :placeholder="selectedVendorPreset.authScheme" />
          </div>
        </div>

        <div v-if="usesApiKeyFields" class="space-y-4">
          <div>
            <label class="input-label">{{ t('admin.accounts.baseUrl') }}</label>
            <input
              v-model.trim="form.baseUrl"
              type="text"
              class="input"
              :placeholder="currentBaseUrlPlaceholder"
            />
            <p class="input-hint">{{ currentBaseUrlHint }}</p>
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.apiKey') }}</label>
            <input
              v-model.trim="form.apiKey"
              type="password"
              class="input font-mono"
              autocomplete="new-password"
              data-1p-ignore
              data-lpignore="true"
              data-bwignore="true"
              :placeholder="currentApiKeyPlaceholder"
            />
            <p class="input-hint">
              {{ currentApiKeyHint }}
              <span v-if="editingAccount" class="ml-1">{{ t('admin.accounts.leaveEmptyToKeep') }}</span>
            </p>
          </div>
        </div>

        <div v-if="showModelRestriction" class="border-t border-gray-200 pt-4 dark:border-dark-600">
          <label class="input-label">{{ t('admin.accounts.modelRestriction') }}</label>
          <div class="mb-4 mt-2 flex gap-2">
            <button
              type="button"
              :class="[
                'flex-1 rounded-lg px-4 py-2 text-sm font-medium transition-all',
                form.modelRestrictionMode === 'whitelist'
                  ? 'bg-primary-100 text-primary-700 dark:bg-primary-900/30 dark:text-primary-400'
                  : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-400 dark:hover:bg-dark-500'
              ]"
              @click="form.modelRestrictionMode = 'whitelist'"
            >
              {{ t('admin.accounts.modelWhitelist') }}
            </button>
            <button
              type="button"
              :class="[
                'flex-1 rounded-lg px-4 py-2 text-sm font-medium transition-all',
                form.modelRestrictionMode === 'mapping'
                  ? 'bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400'
                  : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-400 dark:hover:bg-dark-500'
              ]"
              @click="form.modelRestrictionMode = 'mapping'"
            >
              {{ t('admin.accounts.modelMapping') }}
            </button>
          </div>

          <div v-if="form.modelRestrictionMode === 'whitelist'">
            <ModelWhitelistSelector v-model="form.allowedModels" :platforms="currentModelWhitelistPlatforms" />
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.selectedModels', { count: form.allowedModels.length }) }}
              <span v-if="form.allowedModels.length === 0 && form.modelMappings.length === 0">
                {{ t('admin.accounts.supportsAllModels') }}
              </span>
            </p>
          </div>

          <div v-else>
            <div class="mb-3 rounded-lg bg-purple-50 p-3 dark:bg-purple-900/20">
              <p class="text-xs text-purple-700 dark:text-purple-400">{{ t('admin.accounts.mapRequestModels') }}</p>
            </div>
            <ModelMappingBulkImporter
              :title="t('admin.accounts.bulkImportMappings')"
              :hint="t('admin.accounts.bulkImportMappingsHint')"
              :placeholder="t('admin.accounts.bulkImportMappingsPlaceholder')"
              :copy-text="serializeModelMappings(form.modelMappings)"
              @import="importModelMappings"
            />

            <div v-if="form.modelMappings.length > 0" class="mb-3 space-y-2">
              <div
                v-for="(mapping, index) in form.modelMappings"
                :key="getModelMappingKey(mapping)"
                class="flex items-center gap-2"
              >
                <input v-model="mapping.from" type="text" class="input flex-1" :placeholder="t('admin.accounts.requestModel')" />
                <span class="text-gray-400">→</span>
                <input v-model="mapping.to" type="text" class="input flex-1" :placeholder="t('admin.accounts.actualModel')" />
                <button
                  type="button"
                  class="rounded-lg p-2 text-red-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20"
                  @click="removeModelMapping(index)"
                >
                  <Icon name="trash" size="sm" />
                </button>
              </div>
            </div>

            <button
              type="button"
              class="mb-3 w-full rounded-lg border-2 border-dashed border-gray-300 px-4 py-2 text-gray-600 transition-colors hover:border-gray-400 hover:text-gray-700 dark:border-dark-500 dark:text-gray-400 dark:hover:border-dark-400 dark:hover:text-gray-300"
              @click="addModelMapping"
            >
              <Icon name="plus" size="sm" class="mr-1 inline" />
              {{ t('admin.accounts.addMapping') }}
            </button>

            <div class="flex flex-wrap gap-2">
              <button
                v-for="preset in presetMappings"
                :key="`${preset.from}-${preset.to}`"
                type="button"
                :class="['rounded-lg px-3 py-1 text-xs transition-colors', preset.color]"
                @click="addPresetMapping(preset.from, preset.to)"
              >
                + {{ preset.label }}
              </button>
            </div>
          </div>
        </div>

        <div class="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div>
            <label class="input-label">{{ t('admin.accounts.concurrency') }}</label>
            <input v-model.number="form.concurrency" class="input" type="number" min="1" max="10000" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.priority') }}</label>
            <input v-model.number="form.priority" class="input" type="number" min="0" max="10000" />
          </div>
          <div v-if="editingAccount">
            <label class="input-label">{{ t('myAccounts.status') }}</label>
            <Select v-model="form.status" :options="statusEditOptions" />
          </div>
        </div>

        <div class="rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-900/40">
          <button
            type="button"
            class="flex w-full items-center justify-between text-left text-sm font-semibold text-gray-800 dark:text-gray-100"
            @click="showAdvancedJson = !showAdvancedJson"
          >
            <span>{{ t('myAccounts.advancedJson') }}</span>
            <Icon name="chevronDown" size="sm" :class="['transition-transform', showAdvancedJson ? 'rotate-180' : '']" />
          </button>
          <div v-if="showAdvancedJson" class="mt-4 grid gap-4">
            <div>
              <label class="input-label">{{ t('myAccounts.credentialsJson') }}</label>
              <textarea v-model="form.credentialsJson" class="input min-h-[120px] font-mono text-xs" spellcheck="false"></textarea>
              <p class="input-hint">{{ t('myAccounts.credentialsJsonHint') }}</p>
            </div>
            <div>
              <label class="input-label">{{ t('myAccounts.extraJson') }}</label>
              <textarea v-model="form.extraJson" class="input min-h-[120px] font-mono text-xs" spellcheck="false"></textarea>
              <p class="input-hint">{{ t('myAccounts.extraJsonHint') }}</p>
            </div>
          </div>
        </div>
      </form>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="closeDialog">{{ t('common.cancel') }}</button>
          <button type="submit" form="my-account-form" class="btn btn-primary" :disabled="saving">
            {{ saving ? t('myAccounts.saving') : t('myAccounts.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Select from '@/components/common/Select.vue'
import type { Column } from '@/components/common/types'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import Icon from '@/components/icons/Icon.vue'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import AccountCapacityCell from '@/components/account/AccountCapacityCell.vue'
import AccountGroupsCell from '@/components/account/AccountGroupsCell.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import ModelWhitelistSelector from '@/components/account/ModelWhitelistSelector.vue'
import ModelMappingBulkImporter from '@/components/account/ModelMappingBulkImporter.vue'
import userAccountsAPI from '@/api/user/accounts'
import { userGroupsAPI } from '@/api/groups'
import type { Account, AccountPlatform, AccountType, AdminGroup, CreateAccountRequest, Group, UpdateAccountRequest } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime, formatRelativeTime } from '@/utils/format'
import {
  buildModelMappingObject,
  getPresetMappingsByPlatform,
  splitModelMappingObject,
  type ModelMappingEntry,
  type ModelRestrictionMode
} from '@/composables/useModelWhitelist'
import {
  getOpenAIVendorModelPlatforms,
  getOpenAIVendorPreset,
  getOpenAIVendorPresetPlatform,
  inferOpenAIVendorPreset,
  listOpenAIVendorPresets,
  type OpenAIVendorPresetId
} from '@/composables/useOpenAIVendorPreset'
import { mergeModelMappings, serializeModelMappings } from '@/utils/accountFormBulk'

const { t } = useI18n()
const appStore = useAppStore()

type AccountStatus = 'active' | 'inactive' | 'error'

const accounts = ref<Account[]>([])
const loading = ref(false)
const saving = ref(false)
const showAccountDialog = ref(false)
const showAdvancedJson = ref(false)
const testingId = ref<number | null>(null)
const editingAccount = ref<Account | null>(null)
const groups = ref<Group[]>([])

const filters = reactive({
  search: '',
  platform: '',
  type: '',
  status: '',
  sort_by: 'created_at',
  sort_order: 'desc' as 'asc' | 'desc',
})

const pagination = reactive({
  page: 1,
  page_size: 20,
  total: 0,
  pages: 0,
})

const form = reactive({
  name: '',
  notes: '',
  platform: 'openai' as AccountPlatform,
  type: 'apikey' as AccountType,
  vendor: 'openai' as OpenAIVendorPresetId,
  baseUrl: '',
  apiKey: '',
  authHeader: 'authorization',
  authScheme: 'bearer',
  credentialsJson: '',
  extraJson: '{}',
  concurrency: 3,
  priority: 50,
  group_ids: [] as number[],
  status: 'active' as AccountStatus,
  modelRestrictionMode: 'whitelist' as ModelRestrictionMode,
  allowedModels: [] as string[],
  modelMappings: [] as ModelMappingEntry[],
})

const columns = computed<Column[]>(() => [
  { key: 'name', label: t('myAccounts.name'), sortable: true, class: 'min-w-[240px]' },
  { key: 'platform_type', label: t('myAccounts.platform'), class: 'min-w-[180px]' },
  { key: 'status', label: t('myAccounts.status'), class: 'min-w-[160px]' },
  { key: 'groups', label: t('keys.group'), class: 'min-w-[180px]' },
  { key: 'capacity', label: t('admin.accounts.columns.capacity'), class: 'min-w-[140px]' },
  { key: 'priority', label: t('myAccounts.priority'), sortable: true },
  { key: 'last_used_at', label: t('admin.accounts.columns.lastUsed'), sortable: true },
  { key: 'created_at', label: t('admin.accounts.columns.createdAt'), sortable: true },
  { key: 'actions', label: t('common.actions'), class: 'sticky-right bg-white dark:bg-dark-900' },
])

const selectableGroups = computed(() => groups.value as unknown as AdminGroup[])

const platformOptions: Array<{
  value: AccountPlatform
  label: string
  icon: 'sparkles' | 'key' | 'cloud'
  activeClass: string
}> = [
  {
    value: 'anthropic',
    label: 'Anthropic',
    icon: 'sparkles',
    activeClass: 'bg-white text-orange-600 shadow-sm dark:bg-dark-600 dark:text-orange-400',
  },
  {
    value: 'openai',
    label: t('myAccounts.openaiCompatible'),
    icon: 'key',
    activeClass: 'bg-white text-green-600 shadow-sm dark:bg-dark-600 dark:text-green-400',
  },
  {
    value: 'gemini',
    label: 'Gemini',
    icon: 'sparkles',
    activeClass: 'bg-white text-blue-600 shadow-sm dark:bg-dark-600 dark:text-blue-400',
  },
  {
    value: 'antigravity',
    label: 'Antigravity',
    icon: 'cloud',
    activeClass: 'bg-white text-purple-600 shadow-sm dark:bg-dark-600 dark:text-purple-400',
  },
  {
    value: 'kiro',
    label: 'Kiro',
    icon: 'sparkles',
    activeClass: 'bg-white text-amber-700 shadow-sm dark:bg-dark-600 dark:text-amber-300',
  },
]

const platformFilterOptions = computed(() => [
  { value: '', label: t('admin.accounts.allPlatforms') },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai', label: t('myAccounts.openaiCompatible') },
  { value: 'gemini', label: 'Gemini' },
  { value: 'antigravity', label: 'Antigravity' },
  { value: 'kiro', label: 'Kiro' },
])

const typeFilterOptions = computed(() => [
  { value: '', label: t('admin.accounts.allTypes') },
  { value: 'apikey', label: t('admin.accounts.apiKey') },
  { value: 'oauth', label: t('admin.accounts.oauthType') },
  { value: 'setup-token', label: t('admin.accounts.setupToken') },
  { value: 'upstream', label: 'Upstream' },
  { value: 'bedrock', label: 'AWS Bedrock' },
  { value: 'service_account', label: 'Service Account' },
])

const statusFilterOptions = computed(() => [
  { value: '', label: t('admin.accounts.allStatus') },
  { value: 'active', label: t('admin.accounts.status.active') },
  { value: 'inactive', label: t('admin.accounts.status.inactive') },
  { value: 'error', label: t('admin.accounts.status.error') },
])

const statusEditOptions = computed(() => statusFilterOptions.value.filter(option => option.value !== ''))

const accountTypeOptions = computed<Array<{
  value: AccountType
  label: string
  description: string
  icon: 'key' | 'cloud' | 'sparkles'
}>>(() => {
  const apiKey = {
    value: 'apikey' as AccountType,
    label: 'API Key',
    description: t('myAccounts.typeApiKeyHint'),
    icon: 'key' as const,
  }
  if (form.platform === 'anthropic') {
    return [
      apiKey,
      {
        value: 'oauth',
        label: 'OAuth',
        description: t('myAccounts.typeAdvancedJsonHint'),
        icon: 'sparkles',
      },
      {
        value: 'bedrock',
        label: 'AWS Bedrock',
        description: t('myAccounts.typeAdvancedJsonHint'),
        icon: 'cloud',
      },
    ]
  }
  if (form.platform === 'gemini') {
    return [
      apiKey,
      {
        value: 'oauth',
        label: 'OAuth',
        description: t('myAccounts.typeAdvancedJsonHint'),
        icon: 'sparkles',
      },
      {
        value: 'service_account',
        label: 'Service Account',
        description: t('myAccounts.typeAdvancedJsonHint'),
        icon: 'cloud',
      },
    ]
  }
  if (form.platform === 'antigravity') {
    return [
      apiKey,
      {
        value: 'oauth',
        label: 'OAuth',
        description: t('myAccounts.typeAdvancedJsonHint'),
        icon: 'sparkles',
      },
    ]
  }
  if (form.platform === 'kiro') {
    return [
      apiKey,
      {
        value: 'oauth',
        label: 'OAuth',
        description: t('myAccounts.typeAdvancedJsonHint'),
        icon: 'sparkles',
      },
    ]
  }
  return [
    apiKey,
    {
      value: 'oauth',
      label: 'OAuth',
      description: t('myAccounts.typeAdvancedJsonHint'),
      icon: 'sparkles',
    },
  ]
})

const selectedVendorPreset = computed(() => getOpenAIVendorPreset(form.vendor))
const openAIVendorOptions = computed(() =>
  listOpenAIVendorPresets().map(preset => ({
    value: preset.id,
    label: t(preset.labelKey),
  }))
)

const currentModelWhitelistPlatforms = computed(() => {
  if (form.platform === 'openai' && form.type === 'apikey') {
    return getOpenAIVendorModelPlatforms(form.vendor)
  }
  return [form.platform]
})

const currentPresetMappingPlatform = computed(() => {
  if (form.platform === 'openai' && form.type === 'apikey') {
    return getOpenAIVendorPresetPlatform(form.vendor)
  }
  return form.platform
})

const presetMappings = computed(() => getPresetMappingsByPlatform(currentPresetMappingPlatform.value))

const usesApiKeyFields = computed(() => form.type === 'apikey' || form.type === 'upstream')
const showModelRestriction = computed(() => form.type === 'apikey' || form.type === 'bedrock' || form.type === 'service_account')

const currentBaseUrlPlaceholder = computed(() => {
  if (form.platform === 'openai' && form.type === 'apikey') return selectedVendorPreset.value.baseUrl
  if (form.platform === 'gemini') return 'https://generativelanguage.googleapis.com'
  if (form.platform === 'antigravity') return 'https://cloudcode-pa.googleapis.com'
  if (form.platform === 'kiro') return 'https://your-kiro-upstream.example.com'
  return 'https://api.anthropic.com'
})

const currentApiKeyPlaceholder = computed(() => {
  if (form.platform === 'openai' && form.type === 'apikey') return selectedVendorPreset.value.apiKeyPlaceholder
  if (form.platform === 'gemini') return 'AIza...'
  return 'sk-...'
})

const currentBaseUrlHint = computed(() => {
  if (form.platform === 'openai' && form.type === 'apikey') return t(selectedVendorPreset.value.baseUrlHintKey)
  if (form.platform === 'openai') return t('admin.accounts.openai.baseUrlHint')
  if (form.platform === 'gemini') return t('admin.accounts.gemini.baseUrlHint')
  if (form.platform === 'kiro') return t('admin.accounts.kiro.baseUrlHint')
  return t('admin.accounts.baseUrlHint')
})

const currentApiKeyHint = computed(() => {
  if (form.platform === 'openai' && form.type === 'apikey') return t(selectedVendorPreset.value.apiKeyHintKey)
  if (form.platform === 'openai') return t('admin.accounts.openai.apiKeyHint')
  if (form.platform === 'gemini') return t('admin.accounts.gemini.apiKeyHint')
  if (form.platform === 'kiro') return t('admin.accounts.kiro.apiKeyHint')
  return t('admin.accounts.apiKeyHint')
})

watch(
  () => accountTypeOptions.value.map(option => option.value).join(','),
  () => {
    if (!accountTypeOptions.value.some(option => option.value === form.type)) {
      form.type = accountTypeOptions.value[0]?.value || 'apikey'
    }
  }
)

async function loadAccounts() {
  loading.value = true
  try {
    const result = await userAccountsAPI.list(pagination.page, pagination.page_size, {
      search: filters.search.trim() || undefined,
      platform: filters.platform || undefined,
      type: filters.type || undefined,
      status: filters.status || undefined,
      sort_by: filters.sort_by,
      sort_order: filters.sort_order,
    })
    accounts.value = result.items
    pagination.total = result.total
    pagination.page = result.page
    pagination.page_size = result.page_size
    pagination.pages = result.pages
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('myAccounts.failedToLoad')))
  } finally {
    loading.value = false
  }
}

function handleFiltersChanged() {
  pagination.page = 1
  void loadAccounts()
}

function handleSort(key: string, order: 'asc' | 'desc') {
  filters.sort_by = key === 'platform_type' ? 'platform' : key
  filters.sort_order = order
  void loadAccounts()
}

function handlePageChange(page: number) {
  pagination.page = page
  void loadAccounts()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page = 1
  pagination.page_size = pageSize
  void loadAccounts()
}

function resetForm() {
  Object.assign(form, {
    name: '',
    notes: '',
    platform: 'openai',
    type: 'apikey',
    vendor: 'openai',
    baseUrl: getOpenAIVendorPreset('openai').baseUrl,
    apiKey: '',
    authHeader: 'authorization',
    authScheme: 'bearer',
    credentialsJson: '',
    extraJson: '{}',
    concurrency: 3,
    priority: 50,
    group_ids: [],
    status: 'active',
    modelRestrictionMode: 'whitelist',
    allowedModels: [],
    modelMappings: [],
  })
  showAdvancedJson.value = false
}

function openCreate() {
  editingAccount.value = null
  resetForm()
  showAccountDialog.value = true
}

function openEdit(account: Account) {
  editingAccount.value = account
  const credentials = (account.credentials || {}) as Record<string, unknown>
  const baseUrl = readCredentialString(credentials, 'base_url') || readExtraBaseURL(account) || defaultBaseURLFor(account.platform)
  const vendor = account.platform === 'openai'
    ? inferOpenAIVendorPreset({
        vendor: readCredentialString(credentials, 'vendor'),
        baseUrl,
        authHeader: readCredentialString(credentials, 'auth_header'),
        authScheme: readCredentialString(credentials, 'auth_scheme'),
      })
    : 'openai'
  const modelRestriction = splitModelMappingObject(credentials.model_mapping as Record<string, unknown> | undefined)

  Object.assign(form, {
    name: account.name,
    notes: account.notes || '',
    platform: account.platform,
    type: account.type,
    vendor,
    baseUrl,
    apiKey: '',
    authHeader: readCredentialString(credentials, 'auth_header') || getOpenAIVendorPreset(vendor).authHeader,
    authScheme: readCredentialString(credentials, 'auth_scheme') || getOpenAIVendorPreset(vendor).authScheme,
    credentialsJson: '',
    extraJson: JSON.stringify(account.extra || {}, null, 2),
    concurrency: account.concurrency || 3,
    priority: account.priority ?? 50,
    group_ids: account.group_ids || account.groups?.map((group) => group.id) || [],
    status: account.status,
    modelRestrictionMode: modelRestriction.modelMappings.length > 0 ? 'mapping' : 'whitelist',
    allowedModels: modelRestriction.allowedModels,
    modelMappings: modelRestriction.modelMappings,
  })
  showAdvancedJson.value = false
  showAccountDialog.value = true
}

function closeDialog() {
  showAccountDialog.value = false
}

function setPlatform(platform: AccountPlatform) {
  form.platform = platform
  form.type = 'apikey'
  if (platform === 'openai') {
    form.vendor = 'openai'
    handleOpenAIVendorChanged()
  } else {
    form.baseUrl = defaultBaseURLFor(platform)
  }
  form.allowedModels = []
  form.modelMappings = []
  form.modelRestrictionMode = 'whitelist'
}

function handleOpenAIVendorChanged() {
  const preset = selectedVendorPreset.value
  form.baseUrl = preset.baseUrl
  form.authHeader = preset.authHeader
  form.authScheme = preset.authScheme
  form.allowedModels = []
  form.modelMappings = []
}

function buildPayload(): CreateAccountRequest | UpdateAccountRequest | null {
  const extra = parseJSONMap(form.extraJson || '{}', t('myAccounts.invalidExtraJson'))
  if (!extra) return null

  let credentials = buildGeneratedCredentials()
  if (!credentials) return null

  if (form.credentialsJson.trim()) {
    const parsedCredentials = parseJSONMap(form.credentialsJson, t('myAccounts.invalidCredentialsJson'))
    if (!parsedCredentials) return null
    credentials = { ...credentials, ...parsedCredentials }
  }

  const base = {
    name: form.name.trim(),
    notes: form.notes.trim() || null,
    type: form.type,
    credentials,
    extra,
    concurrency: Number(form.concurrency) || 3,
    priority: Number(form.priority) || 50,
    group_ids: form.group_ids,
    auto_pause_on_expired: true,
  }

  if (editingAccount.value) {
    return {
      ...base,
      status: form.status,
    }
  }

  return {
    ...base,
    platform: form.platform,
  }
}

function buildGeneratedCredentials(): Record<string, unknown> | null {
  const credentials: Record<string, unknown> = {}

  if (usesApiKeyFields.value) {
    const baseUrl = form.baseUrl.trim() || currentBaseUrlPlaceholder.value
    if (!baseUrl) {
      appStore.showError(t('admin.accounts.upstream.pleaseEnterBaseUrl'))
      return null
    }
    credentials.base_url = baseUrl

    if (form.apiKey.trim()) {
      credentials.api_key = form.apiKey.trim()
    } else if (!editingAccount.value || !editingAccount.value.credentials_status?.has_api_key) {
      appStore.showError(t('admin.accounts.pleaseEnterApiKey'))
      return null
    }

    if (form.platform === 'openai' && form.type === 'apikey') {
      credentials.vendor = form.vendor
      credentials.auth_header = (form.authHeader.trim() || selectedVendorPreset.value.authHeader).toLowerCase()
      credentials.auth_scheme = (form.authScheme.trim() || selectedVendorPreset.value.authScheme).toLowerCase()
    }
  }

  if (showModelRestriction.value) {
    const modelMapping = buildModelMappingObject(
      form.modelRestrictionMode,
      form.allowedModels,
      form.modelMappings
    )
    if (modelMapping) {
      credentials.model_mapping = modelMapping
    }
  }

  if (!usesApiKeyFields.value && Object.keys(credentials).length === 0 && !form.credentialsJson.trim()) {
    appStore.showError(t('myAccounts.credentialsRequired'))
    return null
  }

  return credentials
}

async function submitForm() {
  const payload = buildPayload()
  if (!payload) return

  saving.value = true
  try {
    if (editingAccount.value) {
      await userAccountsAPI.update(editingAccount.value.id, payload as UpdateAccountRequest)
      appStore.showSuccess(t('myAccounts.updated'))
    } else {
      await userAccountsAPI.create(payload as CreateAccountRequest)
      appStore.showSuccess(t('myAccounts.created'))
    }
    closeDialog()
    await loadAccounts()
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('myAccounts.saveFailed')))
  } finally {
    saving.value = false
  }
}

function parseJSONMap(raw: string, errorMessage: string): Record<string, unknown> | null {
  try {
    const parsed = JSON.parse(raw || '{}')
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      appStore.showError(errorMessage)
      return null
    }
    return parsed as Record<string, unknown>
  } catch {
    appStore.showError(errorMessage)
    return null
  }
}

function readCredentialString(credentials: Record<string, unknown>, key: string): string {
  const value = credentials[key]
  return typeof value === 'string' ? value : ''
}

function readExtraBaseURL(account: Account): string {
  const extraBaseURL = account.extra?.base_url
  return typeof extraBaseURL === 'string' ? extraBaseURL : ''
}

function getCredentialBaseURL(account: Account): string {
  const credentials = (account.credentials || {}) as Record<string, unknown>
  return readCredentialString(credentials, 'base_url') || readExtraBaseURL(account)
}

function defaultBaseURLFor(platform: AccountPlatform): string {
  if (platform === 'openai') return selectedVendorPreset.value.baseUrl
  if (platform === 'gemini') return 'https://generativelanguage.googleapis.com'
  if (platform === 'antigravity') return 'https://cloudcode-pa.googleapis.com'
  if (platform === 'kiro') return ''
  return 'https://api.anthropic.com'
}

function getOpenAIVendorLabel(account: Account): string {
  if (account.platform !== 'openai') return ''
  const credentials = (account.credentials || {}) as Record<string, unknown>
  const vendor = inferOpenAIVendorPreset({
    vendor: readCredentialString(credentials, 'vendor'),
    baseUrl: getCredentialBaseURL(account),
    authHeader: readCredentialString(credentials, 'auth_header'),
    authScheme: readCredentialString(credentials, 'auth_scheme'),
  })
  if (vendor === 'openai') return ''
  return t(getOpenAIVendorPreset(vendor).labelKey)
}

function getModelMappingKey(mapping: ModelMappingEntry) {
  return `${mapping.from}->${mapping.to}`
}

function addModelMapping() {
  form.modelMappings.push({ from: '', to: '' })
}

function removeModelMapping(index: number) {
  form.modelMappings.splice(index, 1)
}

function addPresetMapping(from: string, to: string) {
  form.modelMappings = mergeModelMappings(form.modelMappings, `${from} => ${to}`)
  form.modelRestrictionMode = 'mapping'
}

function importModelMappings(text: string) {
  form.modelMappings = mergeModelMappings(form.modelMappings, text)
  form.modelRestrictionMode = 'mapping'
}

async function testAccount(account: Account) {
  testingId.value = account.id
  try {
    await userAccountsAPI.testAccount(account.id)
    appStore.showSuccess(t('myAccounts.testStarted'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('myAccounts.testFailed')))
  } finally {
    testingId.value = null
  }
}

async function deleteAccount(account: Account) {
  if (!window.confirm(t('myAccounts.confirmDelete'))) return
  try {
    await userAccountsAPI.deleteAccount(account.id)
    appStore.showSuccess(t('myAccounts.deleted'))
    if (accounts.value.length === 1 && pagination.page > 1) {
      pagination.page -= 1
    }
    await loadAccounts()
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('myAccounts.deleteFailed')))
  }
}

async function loadGroups() {
  try {
    groups.value = await userGroupsAPI.getAvailable()
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.groups.failedToLoad')))
  }
}

onMounted(() => {
  void Promise.all([loadAccounts(), loadGroups()])
})
</script>

<style scoped>
.account-row-action {
  @apply flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 dark:hover:bg-dark-700;
}
</style>
