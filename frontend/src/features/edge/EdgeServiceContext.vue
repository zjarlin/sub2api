<template>
  <section class="edge-context" data-testid="edge-service-context">
    <header class="edge-section-heading">
      <div>
        <h3>{{ context?.adapter || title }}</h3>
        <p>{{ t('admin.vision.runtime.' + (context?.source === 'account' ? 'accountSource' : 'deploymentSource')) }}</p>
      </div>
      <button class="edge-icon-button" type="button" :disabled="loading" :title="t('common.refresh')" :aria-label="t('common.refresh')" @click="emit('refresh')"><Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" /></button>
    </header>
    <p v-if="error || context?.error" class="edge-notice error" role="alert">{{ error || t('admin.vision.runtime.unavailable') }}</p>
    <p v-if="loading && !context" class="edge-notice">{{ t('common.loading') }}</p>
    <template v-if="context">
      <div class="edge-context-summary">
        <span class="edge-state" :class="{ ready: context.enabled }"><span />{{ t('admin.vision.runtime.' + (context.enabled ? 'ready' : context.configured ? 'inactive' : 'missing')) }}</span>
        <span>{{ context.source === 'account' ? t('admin.vision.runtime.accountsCount', { count: context.accounts.length }) : t('admin.vision.runtime.deploymentManaged') }}</span>
      </div>
      <dl v-if="context.fields.length" class="edge-config-grid">
        <div v-for="field in context.fields" :key="field.key" class="edge-config-row">
          <dt>{{ t('admin.vision.runtime.fields.' + field.key) }}</dt>
          <dd :class="{ 'edge-value-muted': field.value === '' }" :data-testid="`edge-context-${field.key}`">{{ displayValue(field) }}</dd>
        </div>
      </dl>
      <div v-if="context.source === 'account' && !context.accounts.length" class="edge-empty-context">
        <Icon name="key" size="lg" />
        <h4>{{ t('admin.vision.runtime.noAccount') }}</h4>
        <p>{{ t('admin.vision.runtime.' + (service.key === 'generation' ? 'arkRequired' : 'accountRequired')) }}</p>
        <RouterLink :to="accountLink" class="edge-button primary"><Icon name="plus" size="sm" />{{ t('admin.vision.runtime.addAccount') }}</RouterLink>
      </div>
      <form v-for="account in context.accounts" :key="account.id" class="edge-account" @submit.prevent="save(account)">
        <header class="edge-section-heading">
          <div><h4>{{ account.name }}</h4><p>#{{ account.id }} <span class="mx-2">·</span>{{ t('admin.vision.runtime.groupsCount', { count: account.group_ids?.length || 0 }) }}</p></div>
          <span class="edge-state" :class="{ ready: account.schedulable && (account.api_key_set || service.key === 'laya') }"><span />{{ t('admin.vision.runtime.' + (account.schedulable ? 'activeAccount' : 'inactive')) }}</span>
        </header>
        <div class="edge-account-fields">
          <label><span>Base URL</span><input v-model="drafts[account.id]!.base_url" class="input" :readonly="service.key !== 'generation'" spellcheck="false" :data-testid="`edge-account-url-${account.id}`" /></label>
          <label><span class="flex items-center justify-between">API Key <small>{{ t('admin.vision.runtime.' + (account.api_key_set ? 'keyConfigured' : service.key === 'laya' ? 'keyOptional' : 'missing')) }}</small></span><input v-model="drafts[account.id]!.api_key" type="password" class="input" :disabled="!account.api_key_editable || saving === account.id" autocomplete="new-password" :placeholder="t('admin.vision.context.keepSecret')" :data-testid="`edge-account-key-${account.id}`" /></label>
        </div>
        <div class="edge-model-list"><span>{{ t('admin.vision.runtime.models') }}</span><code v-for="model in account.models" :key="model">{{ model }}</code><span v-if="!account.models.length">{{ t('admin.vision.runtime.accountDefaults') }}</span></div>
        <footer class="edge-account-footer">
          <p v-if="accountError[account.id]" role="alert" class="text-red-600">{{ accountError[account.id] }}</p>
          <p v-else-if="savedID === account.id" role="status" class="text-emerald-700">{{ t('admin.vision.runtime.saved') }}</p>
          <p v-else>{{ t('admin.vision.context.keepSecret') }}</p>
          <button type="submit" class="edge-button primary" :disabled="saving !== null || !changed(account)" :data-testid="`edge-account-save-${account.id}`"><Icon name="check" size="sm" />{{ t(saving === account.id ? 'common.saving' : 'common.save') }}</button>
        </footer>
      </form>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { apiClient } from '@/api/client'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { EdgeService } from './catalog'
import type { EdgeAccountContext, EdgeServiceContext } from './contexts'

const props = defineProps<{ service: EdgeService; context?: EdgeServiceContext; loading: boolean; error: string }>()
const emit = defineEmits<{ refresh: []; saved: [] }>()
const { t } = useI18n()
const title = computed(() => t('admin.vision.catalog.' + props.service.title + 'Title'))
const accountLink = computed(() => ({ path: '/admin/accounts', query: { platform: props.service.key === 'generation' ? 'openai' : 'systemone' } }))
const drafts = reactive<Record<number, { base_url: string; api_key: string }>>({})
const accountError = reactive<Record<number, string>>({})
const saving = ref<number | null>(null)
const savedID = ref<number | null>(null)

watch(() => props.context, context => {
  for (const account of context?.accounts || []) {
    drafts[account.id] = { base_url: account.base_url, api_key: '' }
  }
}, { immediate: true })

function displayValue(field: { key: string; value: string | boolean | number }) {
  if (typeof field.value === 'boolean') return t('admin.vision.runtime.' + (field.value ? 'yes' : 'no'))
  if (field.key === 'authentication') return t('admin.vision.runtime.gatewayAuth')
  if (field.key === 'max_upload_bytes') return `${Number(field.value) / (1024 * 1024)} MB`
  if (field.key === 'timeout_seconds' && field.value) return `${field.value} s`
  return field.value === '' ? t('admin.vision.runtime.upstreamDefault') : field.value
}

function changed(account: EdgeAccountContext) {
  const draft = drafts[account.id]
  return !!draft && (draft.api_key.trim() !== '' || (props.service.key === 'generation' && draft.base_url !== account.base_url))
}

async function save(account: EdgeAccountContext) {
  if (saving.value !== null || !changed(account)) return
  saving.value = account.id
  accountError[account.id] = ''
  savedID.value = null
  try {
    await apiClient.put(`/admin/vision/contexts/${props.service.key}/accounts/${account.id}`, { ...drafts[account.id] })
    drafts[account.id]!.api_key = ''
    savedID.value = account.id
    emit('saved')
  } catch (cause) {
    accountError[account.id] = extractApiErrorMessage(cause, t('admin.vision.runtime.saveFailed'))
  } finally {
    saving.value = null
  }
}
</script>
