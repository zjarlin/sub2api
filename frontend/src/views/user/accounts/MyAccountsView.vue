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

          <template #cell-shared="{ row }">
            <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <input
                type="checkbox"
                class="accent-primary-600"
                :checked="row.shared === true"
                :disabled="sharingId === row.id"
                :aria-label="t('myAccounts.sharePublicly')"
                @change="toggleSharing(row)"
              />
              {{ row.shared ? t('myAccounts.shared') : t('myAccounts.private') }}
            </label>
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
                :disabled="testingId !== null"
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

    <AccountEditor v-if="showAccountDialog" :account="editingAccount" :groups="groups" @close="showAccountDialog = false" @saved="handleSaved" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Select from '@/components/common/Select.vue'
import type { Column } from '@/components/common/types'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import Icon from '@/components/icons/Icon.vue'
import AccountTableActions from '@/components/admin/account/AccountTableActions.vue'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import AccountCapacityCell from '@/components/account/AccountCapacityCell.vue'
import AccountGroupsCell from '@/components/account/AccountGroupsCell.vue'
import AccountEditor from './AccountEditor.vue'
import userAccountsAPI from '@/api/user/accounts'
import { userGroupsAPI } from '@/api/groups'
import type { Account, Group } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime, formatRelativeTime } from '@/utils/format'
const { t } = useI18n()
const appStore = useAppStore()
const accounts = ref<Account[]>([])
const loading = ref(false)
const showAccountDialog = ref(false)
const testingId = ref<number | null>(null)
const sharingId = ref<number | null>(null)
const editingAccount = ref<Account | null>(null)
const groups = ref<Group[]>([])
const filters = reactive({ search: '', platform: '', type: '', status: '', sort_by: 'created_at', sort_order: 'desc' as 'asc' | 'desc' })
const pagination = reactive({ page: 1, page_size: 20, total: 0, pages: 0 })
const columns = computed<Column[]>(() => [
  { key: 'name', label: t('myAccounts.name'), sortable: true, class: 'min-w-[240px]' },
  { key: 'platform_type', label: t('myAccounts.platform'), class: 'min-w-[180px]' },
  { key: 'status', label: t('myAccounts.status'), class: 'min-w-[160px]' },
  { key: 'groups', label: t('keys.group'), class: 'min-w-[180px]' },
  { key: 'shared', label: t('myAccounts.sharing'), class: 'min-w-[120px]' },
  { key: 'capacity', label: t('admin.accounts.columns.capacity'), class: 'min-w-[140px]' },
  { key: 'priority', label: t('myAccounts.priority'), sortable: true },
  { key: 'last_used_at', label: t('admin.accounts.columns.lastUsed'), sortable: true },
  { key: 'created_at', label: t('admin.accounts.columns.createdAt'), sortable: true },
  { key: 'actions', label: t('common.actions'), class: 'sticky-right bg-white dark:bg-dark-900' },
])

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

function openCreate() {
  editingAccount.value = null
  showAccountDialog.value = true
}
function openEdit(account: Account) {
  editingAccount.value = account
  showAccountDialog.value = true
}
function handleSaved() {
  showAccountDialog.value = false
  void loadAccounts()
}
function getCredentialBaseURL(account: Account) { return String(account.credentials?.base_url || '') }
async function testAccount(account: Account) {
  testingId.value = account.id
  try {
    await userAccountsAPI.testAccount(account.id)
    appStore.showSuccess(t('admin.accounts.testCompleted'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('myAccounts.testFailed')))
  } finally {
    testingId.value = null
  }
}

async function toggleSharing(account: Account) {
  const previous = account.shared === true
  account.shared = !previous
  sharingId.value = account.id
  try {
    const updated = await userAccountsAPI.update(account.id, { shared: account.shared })
    account.shared = updated.shared
  } catch (err: unknown) {
    account.shared = previous
    appStore.showError(extractApiErrorMessage(err, t('myAccounts.saveFailed')))
  } finally {
    sharingId.value = null
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
