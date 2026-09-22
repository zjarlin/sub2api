<template>
  <BaseDialog :show="true" :title="t(account ? 'myAccounts.edit' : 'myAccounts.create')" width="wide" @close="close">
    <form id="owned-account-form" class="space-y-5" @submit.prevent="save">
      <div class="grid gap-4 sm:grid-cols-2">
        <div>
          <label class="input-label">{{ t('myAccounts.name') }}</label>
          <input v-model="form.name" type="text" required class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('myAccounts.platform') }}</label>
          <Select v-model="form.platform" :options="platforms" :disabled="!!account" />
          <p v-if="account" class="input-hint">{{ t('myAccounts.platformLockedHint') }}</p>
        </div>
      </div>

      <div>
        <label class="input-label">{{ t('myAccounts.notes') }}</label>
        <textarea v-model="form.notes" rows="2" class="input" />
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.accountType') }}</label>
        <div class="mt-2 grid grid-cols-2 gap-2 sm:grid-cols-4">
          <button
            v-for="option in types"
            :key="option.value"
            type="button"
            :disabled="!!account"
            :class="[
              'rounded-lg border-2 px-3 py-2 text-left text-sm transition-all disabled:cursor-not-allowed disabled:opacity-60',
              form.type === option.value
                ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/20 dark:text-primary-300'
                : 'border-gray-200 text-gray-700 hover:border-primary-300 dark:border-dark-600 dark:text-gray-300 dark:hover:border-primary-700'
            ]"
            @click="form.type = option.value"
          >
            <span class="block font-medium">{{ option.label }}</span>
            <span class="block text-xs text-gray-500 dark:text-gray-400">{{ option.hint }}</span>
          </button>
        </div>
      </div>

      <template v-if="usesApiKey">
        <div>
          <label class="input-label">{{ t('myAccounts.baseUrl') }}</label>
          <input v-model="form.baseUrl" type="url" required class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('myAccounts.apiKey') }}</label>
          <input v-model="form.apiKey" type="password" autocomplete="new-password" :required="!account" class="input" />
          <p v-if="account" class="input-hint">{{ t('myAccounts.redactedNotice') }}</p>
        </div>
      </template>
      <p v-else-if="account" class="text-xs text-gray-500 dark:text-gray-400">{{ t('myAccounts.redactedNotice') }}</p>

      <div class="border-t border-gray-200 pt-4 dark:border-dark-600">
        <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <div>
            <label class="input-label">{{ t('admin.accounts.concurrency') }}</label>
            <input v-model.number="form.concurrency" type="number" min="1" required class="input" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.loadFactor') }}</label>
            <input v-model.number="form.loadFactor" type="number" min="1" class="input" :placeholder="String(form.concurrency || 1)" />
            <p class="input-hint">{{ t('admin.accounts.loadFactorHint') }}</p>
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.priority') }}</label>
            <input v-model.number="form.priority" type="number" min="0" required class="input" />
            <p class="input-hint">{{ t('admin.accounts.priorityHint') }}</p>
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.billingRateMultiplier') }}</label>
            <input v-model.number="form.rateMultiplier" type="number" min="0" step="0.001" class="input" />
            <p class="input-hint">{{ t('admin.accounts.billingRateMultiplierHint') }}</p>
          </div>
        </div>

        <div class="mt-4">
          <label class="input-label">{{ t('admin.accounts.expiresAt') }}</label>
          <input v-model="expiresAtInput" type="datetime-local" class="input" />
          <div class="mt-2 flex gap-2">
            <button type="button" class="btn btn-secondary btn-sm" @click="form.expiresAt = getAccountExpiryTimestamp(1)">
              {{ t('payment.oneMonth') }}
            </button>
            <button type="button" class="btn btn-secondary btn-sm" @click="form.expiresAt = getAccountExpiryTimestamp(12)">
              {{ t('payment.oneYear') }}
            </button>
            <button v-if="form.expiresAt" type="button" class="btn btn-secondary btn-sm" @click="form.expiresAt = null">
              {{ t('common.clear') }}
            </button>
          </div>
          <p class="input-hint">
            {{ t('admin.accounts.expiresAtHint') }}
            {{ t('admin.accounts.expiresAtTimezoneHint', { timezone: browserTimeZone }) }}
          </p>
          <label class="mt-2 flex items-center gap-2 text-sm">
            <input v-model="form.autoPauseOnExpired" type="checkbox" class="accent-primary-600" />
            <span>{{ t('admin.accounts.autoPauseOnExpired') }}</span>
          </label>
        </div>
      </div>

      <div class="border-t border-gray-200 pt-4 dark:border-dark-600">
        <label v-if="account" class="input-label">{{ t('myAccounts.status') }}</label>
        <Select v-if="account" v-model="form.status" :options="statuses" />
        <label class="mt-3 flex items-center gap-2 text-sm">
          <input v-model="form.shared" type="checkbox" class="accent-primary-600" data-testid="owned-account-shared" />
          <span>{{ t('myAccounts.sharePublicly') }}</span>
        </label>

        <div
          v-if="requiresMixedSchedulingOptIn(form.platform)"
          class="mt-3 space-y-2"
          data-testid="mixed-scheduling-toggle"
        >
          <label class="flex cursor-pointer items-center gap-2 text-sm">
            <input v-model="form.mixedScheduling" type="checkbox" class="accent-primary-600" />
            <span>{{ t('admin.accounts.mixedScheduling') }}</span>
          </label>
          <label v-if="form.platform === 'antigravity'" class="flex cursor-pointer items-center gap-2 text-sm">
            <input v-model="form.allowOverages" type="checkbox" class="accent-primary-600" />
            <span>{{ t('admin.accounts.allowOverages') }}</span>
          </label>
        </div>
        <p
          v-if="usesAutomaticMixedScheduling(form.platform)"
          class="mt-3 text-xs text-gray-500 dark:text-gray-400"
          data-testid="automatic-mixed-scheduling-hint"
        >
          {{ t('admin.accounts.automaticMixedSchedulingHint') }}
        </p>

        <GroupSelector
          v-model="form.groupIds"
          class="mt-4"
          :groups="groups"
          :platform="form.platform"
          :mixed-scheduling="form.mixedScheduling"
        />
      </div>

      <div class="border-t border-gray-200 pt-4 dark:border-dark-600">
        <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
          <label class="input-label mb-0">{{ t('admin.accounts.modelRestriction') }}</label>
          <div class="flex gap-2">
            <button
              type="button"
              :class="[
                'rounded-lg px-3 py-1.5 text-sm font-medium transition-all',
                modelRestrictionMode === 'whitelist'
                  ? 'bg-primary-100 text-primary-700 dark:bg-primary-900/30 dark:text-primary-400'
                  : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-400 dark:hover:bg-dark-500'
              ]"
              @click="modelRestrictionMode = 'whitelist'"
            >
              {{ t('admin.accounts.modelWhitelist') }}
            </button>
            <button
              type="button"
              :class="[
                'rounded-lg px-3 py-1.5 text-sm font-medium transition-all',
                modelRestrictionMode === 'mapping'
                  ? 'bg-purple-100 text-purple-700 dark:bg-purple-900/30 dark:text-purple-400'
                  : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-600 dark:text-gray-400 dark:hover:bg-dark-500'
              ]"
              @click="modelRestrictionMode = 'mapping'"
            >
              {{ t('admin.accounts.modelMapping') }}
            </button>
          </div>
        </div>

        <ModelWhitelistSelector
          v-if="modelRestrictionMode === 'whitelist'"
          v-model="form.allowedModels"
          :platform="form.platform"
        />
        <div v-else>
          <div v-for="(mapping, index) in form.modelMappings" :key="index" class="mb-2 flex items-center gap-2">
            <input v-model="mapping.from" type="text" class="input flex-1" :placeholder="t('admin.accounts.requestModel')" />
            <span class="text-gray-400">-&gt;</span>
            <input v-model="mapping.to" type="text" class="input flex-1" :placeholder="t('admin.accounts.actualModel')" />
            <button type="button" class="btn btn-secondary btn-sm" @click="form.modelMappings.splice(index, 1)">
              {{ t('common.delete') }}
            </button>
          </div>
          <button type="button" class="btn btn-secondary btn-sm" @click="form.modelMappings.push({ from: '', to: '' })">
            {{ t('admin.accounts.addMapping') }}
          </button>
        </div>
      </div>

      <details :open="!usesApiKey">
        <summary class="cursor-pointer text-sm">{{ t('myAccounts.advancedJson') }}</summary>
        <label class="mt-3 block space-y-1"><span>{{ t('myAccounts.credentialsJson') }}</span><textarea v-model="form.credentialsJson" class="input w-full font-mono" rows="5" /></label>
        <label class="mt-3 block space-y-1"><span>{{ t('myAccounts.extraJson') }}</span><textarea v-model="form.extraJson" class="input w-full font-mono" rows="4" /></label>
      </details>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    </form>
    <template #footer>
      <button type="button" class="btn btn-secondary" :disabled="saving" @click="close">{{ t('common.cancel') }}</button>
      <button type="submit" form="owned-account-form" class="btn btn-primary" :disabled="saving">{{ t(saving ? 'myAccounts.saving' : 'myAccounts.save') }}</button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import Select from '@/components/common/Select.vue'
import ModelWhitelistSelector from '@/components/account/ModelWhitelistSelector.vue'
import {
  CONCRETE_PLATFORM_OPTIONS,
  requiresMixedSchedulingOptIn,
  usesAutomaticMixedScheduling
} from '@/constants/platforms'
import { buildModelMappingObject, splitModelMappingObject, type ModelRestrictionMode } from '@/composables/useModelWhitelist'
import { getAccountExpiryTimestamp } from '@/components/account/accountExpiry'
import { formatDateTimeLocalInput, getBrowserTimeZone, parseDateTimeLocalInput } from '@/utils/format'
import userAccountsAPI from '@/api/user/accounts'
import type { Account, AccountPlatform, AccountType, Group } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ account: Account | null; groups: Group[] }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const app = useAppStore()
const saving = ref(false)
const error = ref('')
const browserTimeZone = getBrowserTimeZone()
const credentials = props.account?.credentials || {}
const restriction = splitModelMappingObject(credentials.model_mapping as Record<string, unknown>)
const extra = (props.account?.extra || {}) as Record<string, unknown>
const form = reactive({
  name: props.account?.name || '',
  notes: props.account?.notes || '',
  platform: props.account?.platform || 'openai' as AccountPlatform,
  type: props.account?.type || 'apikey' as AccountType,
  baseUrl: String(credentials.base_url || ''),
  apiKey: '',
  groupIds: props.account?.group_ids || [],
  shared: props.account?.shared ?? false,
  concurrency: props.account?.concurrency ?? 3,
  loadFactor: props.account?.load_factor ?? null as number | null,
  priority: props.account?.priority ?? 50,
  rateMultiplier: props.account?.rate_multiplier ?? 1,
  status: props.account?.status || 'active',
  expiresAt: props.account?.expires_at ?? null as number | null,
  autoPauseOnExpired: props.account?.auto_pause_on_expired ?? true,
  mixedScheduling: extra.mixed_scheduling === true,
  allowOverages: extra.allow_overages === true,
  allowedModels: restriction.allowedModels,
  modelMappings: restriction.modelMappings,
  credentialsJson: '',
  extraJson: JSON.stringify(props.account?.extra || {}, null, 2),
})
const modelRestrictionMode = ref<ModelRestrictionMode>(
  restriction.modelMappings.length > 0 && restriction.allowedModels.length === 0 ? 'mapping' : 'whitelist',
)
const usesApiKey = computed(() => ['apikey', 'upstream'].includes(form.type))
const platforms = CONCRETE_PLATFORM_OPTIONS.map(option => ({ value: option.value, label: option.label }))
const types = [
  { value: 'apikey' as AccountType, label: t('admin.accounts.apiKey'), hint: t('myAccounts.typeApiKeyHint') },
  { value: 'oauth' as AccountType, label: t('admin.accounts.oauthType'), hint: t('myAccounts.typeAdvancedJsonHint') },
  { value: 'setup-token' as AccountType, label: t('admin.accounts.setupToken'), hint: t('myAccounts.typeAdvancedJsonHint') },
  { value: 'upstream' as AccountType, label: 'Upstream', hint: t('myAccounts.typeApiKeyHint') },
  { value: 'bedrock' as AccountType, label: t('admin.accounts.bedrockLabel'), hint: t('myAccounts.typeAdvancedJsonHint') },
  { value: 'service_account' as AccountType, label: 'Service Account', hint: t('myAccounts.typeAdvancedJsonHint') },
]
const statuses = computed(() => ['active', 'inactive', 'error'].map(value => ({ value, label: t(`admin.accounts.status.${value}`) })))
const expiresAtInput = computed({
  get: () => formatDateTimeLocalInput(form.expiresAt),
  set: (value: string) => { form.expiresAt = parseDateTimeLocalInput(value) },
})

function objectFromJSON(raw: string, message: string): Record<string, unknown> {
  try {
    const value = JSON.parse(raw || '{}')
    if (value && typeof value === 'object' && !Array.isArray(value)) {
      return value
    }
  } catch {
    // 将语法和类型错误统一显示在当前表单。
  }
  throw new Error(message)
}

function close() {
  if (!saving.value) {
    emit('close')
  }
}

async function save() {
  if (saving.value) {
    return
  }
  error.value = ''
  saving.value = true
  try {
    const advanced = objectFromJSON(form.credentialsJson, t('myAccounts.invalidCredentialsJson'))
    const nextCredentials: Record<string, unknown> = { ...credentials, ...advanced }
    if (usesApiKey.value) {
      nextCredentials.base_url = form.baseUrl.trim()
      if (form.apiKey.trim()) {
        nextCredentials.api_key = form.apiKey.trim()
      }
    } else if (!props.account && Object.keys(advanced).length === 0) {
      throw new Error(t('myAccounts.credentialsRequired'))
    }
    const modelMapping = buildModelMappingObject('combined', form.allowedModels, form.modelMappings)
    if (modelMapping) {
      nextCredentials.model_mapping = modelMapping
    } else {
      delete nextCredentials.model_mapping
    }

    const nextExtra = objectFromJSON(form.extraJson, t('myAccounts.invalidExtraJson'))
    if (form.platform === 'antigravity') {
      if (form.mixedScheduling) nextExtra.mixed_scheduling = true
      else delete nextExtra.mixed_scheduling
      if (form.allowOverages) nextExtra.allow_overages = true
      else delete nextExtra.allow_overages
    }

    const loadFactor = form.loadFactor == null || Number.isNaN(form.loadFactor) || form.loadFactor <= 0 ? 0 : form.loadFactor
    const payload = {
      name: form.name.trim(), notes: form.notes, type: form.type,
      credentials: nextCredentials, extra: nextExtra,
      concurrency: form.concurrency, load_factor: loadFactor, priority: form.priority,
      rate_multiplier: form.rateMultiplier, group_ids: form.groupIds,
      expires_at: form.expiresAt ?? 0, auto_pause_on_expired: form.autoPauseOnExpired,
      shared: form.shared,
    }
    if (props.account) {
      await userAccountsAPI.update(props.account.id, { ...payload, status: form.status })
    } else {
      await userAccountsAPI.create({ ...payload, platform: form.platform })
    }
    app.showSuccess(t(props.account ? 'myAccounts.updated' : 'myAccounts.created'))
    emit('saved')
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, t('myAccounts.saveFailed'))
  } finally {
    saving.value = false
  }
}
</script>
