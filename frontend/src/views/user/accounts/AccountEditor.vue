<template>
  <BaseDialog :show="true" :title="t(account ? 'myAccounts.edit' : 'myAccounts.create')" width="wide" @close="close">
    <form id="owned-account-form" class="space-y-4" @submit.prevent="save">
      <label class="block space-y-1"><span>{{ t('myAccounts.name') }}</span><input v-model="form.name" class="input w-full" required /></label>
      <div class="grid gap-4 sm:grid-cols-2">
        <label class="block space-y-1"><span>{{ t('myAccounts.platform') }}</span>
          <Select v-model="form.platform" :options="platforms" :disabled="!!account" />
        </label>
        <label class="block space-y-1"><span>{{ t('myAccounts.type') }}</span>
          <Select v-model="form.type" :options="types" :disabled="!!account" />
        </label>
      </div>
      <template v-if="usesApiKey">
        <label class="block space-y-1"><span>{{ t('myAccounts.baseUrl') }}</span><input v-model="form.baseUrl" class="input w-full" type="url" required /></label>
        <label class="block space-y-1"><span>{{ t('myAccounts.apiKey') }}</span><input v-model="form.apiKey" class="input w-full" type="password" autocomplete="new-password" :required="!account" /></label>
      </template>
      <p v-if="account" class="text-xs text-gray-500 dark:text-gray-400">{{ t('myAccounts.redactedNotice') }}</p>
      <GroupSelector v-model="form.groupIds" :groups="groups" :platform="form.platform" :mixed-scheduling="form.platform === 'antigravity'" />
      <label class="flex items-center gap-2 text-sm">
        <input v-model="form.shared" type="checkbox" class="accent-primary-600" />
        <span>{{ t('myAccounts.sharePublicly') }}</span>
      </label>
      <div class="grid gap-4 sm:grid-cols-2">
        <label class="block space-y-1"><span>{{ t('myAccounts.concurrency') }}</span><input v-model.number="form.concurrency" class="input w-full" type="number" min="1" required /></label>
        <label class="block space-y-1"><span>{{ t('myAccounts.priority') }}</span><input v-model.number="form.priority" class="input w-full" type="number" min="0" required /></label>
      </div>
      <label v-if="account" class="block space-y-1"><span>{{ t('myAccounts.status') }}</span><Select v-model="form.status" :options="statuses" /></label>
      <label class="block space-y-1"><span>{{ t('myAccounts.notes') }}</span><textarea v-model="form.notes" class="input w-full" rows="2" /></label>
      <label class="block space-y-1"><span>{{ t('admin.accounts.modelWhitelist') }}</span>
        <ModelWhitelistSelector v-model="form.allowedModels" :platform="form.platform" />
      </label>
      <label class="block space-y-1"><span>{{ t('myAccounts.modelMappingJson') }}</span><textarea v-model="form.mappingJson" class="input w-full font-mono" rows="3" /></label>
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
import { splitModelMappingObject } from '@/composables/useModelWhitelist'
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
const credentials = props.account?.credentials || {}
const restriction = splitModelMappingObject(credentials.model_mapping as Record<string, unknown>)
const form = reactive({
  name: props.account?.name || '', notes: props.account?.notes || '',
  platform: props.account?.platform || 'openai' as AccountPlatform,
  type: props.account?.type || 'apikey' as AccountType,
  baseUrl: String(credentials.base_url || ''), apiKey: '',
  groupIds: props.account?.group_ids || [],
  shared: props.account?.shared ?? false,
  concurrency: props.account?.concurrency ?? 3, priority: props.account?.priority ?? 50,
  status: props.account?.status || 'active',
  allowedModels: restriction.allowedModels,
  mappingJson: JSON.stringify(Object.fromEntries(restriction.modelMappings.map(entry => [entry.from, entry.to])), null, 2),
  credentialsJson: '', extraJson: JSON.stringify(props.account?.extra || {}, null, 2)
})
const usesApiKey = computed(() => ['apikey', 'upstream'].includes(form.type))
const platforms = ['openai', 'anthropic', 'gemini', 'antigravity', 'kiro', 'grok'].map(value => ({ value, label: value }))
const types = ['apikey', 'oauth', 'setup-token', 'upstream', 'bedrock', 'service_account'].map(value => ({ value, label: value }))
const statuses = computed(() => ['active', 'inactive', 'error'].map(value => ({ value, label: t(`admin.accounts.status.${value}`) })))

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
    const mappings = objectFromJSON(form.mappingJson, t('myAccounts.invalidCredentialsJson'))
    if (Object.values(mappings).some(value => typeof value !== 'string' || !value.trim())) {
      throw new Error(t('myAccounts.invalidCredentialsJson'))
    }
    const nextCredentials: Record<string, unknown> = { ...credentials, ...advanced }
    if (usesApiKey.value) {
      nextCredentials.base_url = form.baseUrl.trim()
      if (form.apiKey.trim()) {
        nextCredentials.api_key = form.apiKey.trim()
      }
    } else if (!props.account && Object.keys(advanced).length === 0) {
      throw new Error(t('myAccounts.credentialsRequired'))
    }
    nextCredentials.model_mapping = { ...Object.fromEntries(form.allowedModels.map(id => [id, id])), ...mappings }
    const payload = {
      name: form.name.trim(), notes: form.notes, type: form.type,
      credentials: nextCredentials, extra: objectFromJSON(form.extraJson, t('myAccounts.invalidExtraJson')),
      concurrency: form.concurrency, priority: form.priority, group_ids: form.groupIds,
      shared: form.shared
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
