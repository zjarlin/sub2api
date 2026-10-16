<template>
  <section class="edge-context" data-testid="edge-mail-context">
    <header class="edge-section-heading">
      <div>
        <h3>{{ t('admin.vision.mailContext.title') }}</h3>
        <p>{{ t('admin.vision.mailContext.description') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <button type="button" class="edge-icon-button" :title="t('common.refresh')" :aria-label="t('common.refresh')" :disabled="loading" data-testid="edge-mail-refresh" @click="load()"><Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" /></button>
        <button type="button" class="edge-button primary" data-testid="edge-mail-add" @click="openEditor(null)"><Icon name="plus" size="sm" />{{ t('admin.vision.mailContext.addAccount') }}</button>
      </div>
    </header>
    <div class="edge-context-summary">
      <span class="edge-state" :class="{ ready: accounts.length > 0 }"><span />{{ accounts.length > 0 ? t('admin.vision.runtime.ready') : t('admin.vision.runtime.missing') }}</span>
      <span>{{ t('admin.vision.mailContext.credentials') }}</span>
      <span>{{ t('admin.vision.mailContext.proxy') }}</span>
    </div>
    <p v-if="error" role="alert" class="edge-notice error">{{ error }}</p>
    <p v-else-if="notice" role="status" class="edge-notice">{{ notice }}</p>
    <p v-if="loading && !accounts.length" class="edge-notice">{{ t('common.loading') }}</p>
    <div v-else-if="!accounts.length" class="edge-empty-context">
      <Icon name="mail" size="lg" />
      <h4>{{ t('admin.vision.mailContext.emptyTitle') }}</h4>
      <p>{{ t('admin.vision.mailContext.hint') }}</p>
      <button type="button" class="edge-button primary" data-testid="edge-mail-empty-add" @click="openEditor(null)"><Icon name="plus" size="sm" />{{ t('admin.vision.mailContext.addAccount') }}</button>
    </div>
    <template v-else>
    <article v-for="account in accounts" :key="account.id" class="edge-account" :data-testid="`edge-mail-account-${account.id}`">
      <header class="edge-section-heading">
        <div>
          <h4>{{ mailEmail(account) }}</h4>
          <p>
            {{ mailProviderLabel(account) }}
            <span class="mx-2">·</span>{{ t('admin.vision.runtime.groupsCount', { count: account.group_ids?.length || 0 }) }}
            <span class="mx-2">·</span>{{ account.proxy_id ? t('admin.vision.mailContext.proxyOn') : t('admin.vision.mailContext.proxyOff') }}
          </p>
        </div>
        <span class="edge-state" :class="{ ready: account.status === 'active' }"><span />{{ t('admin.vision.runtime.' + (account.status === 'active' ? 'activeAccount' : 'inactive')) }}</span>
      </header>
      <footer class="edge-account-footer">
        <p>{{ t('admin.vision.mailContext.rowHint') }}</p>
        <div class="flex flex-wrap gap-2">
          <button type="button" class="edge-button" :disabled="busy" data-testid="edge-mail-edit" @click="openEditor(account)"><Icon name="edit" size="sm" />{{ t('admin.vision.mailContext.edit') }}</button>
          <button type="button" class="edge-button" :disabled="busy" data-testid="edge-mail-test" @click="test(account)"><Icon name="check" size="sm" />{{ t('admin.vision.mailContext.test') }}</button>
          <button type="button" class="edge-button" :disabled="busy" data-testid="edge-mail-delete" @click="remove(account)">{{ t('admin.vision.mailContext.remove') }}</button>
        </div>
      </footer>
      <pre v-if="results[account.id]" class="mt-3 max-h-72 overflow-auto whitespace-pre-wrap text-xs" aria-live="polite" :data-testid="`edge-mail-result-${account.id}`">{{ results[account.id] }}</pre>
    </article>
    </template>
    <MailAccountModal :show="showEditor" :account="editing" :proxies="proxies" :groups="groups" @close="showEditor = false" @saved="handleSaved" />
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import MailAccountModal from '@/components/account/MailAccountModal.vue'
import { apiClient } from '@/api/client'
import { adminAPI } from '@/api/admin'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Account, Group, Proxy } from '@/types'

const { t } = useI18n()
const accounts = ref<Account[]>([])
const proxies = ref<Proxy[]>([])
const groups = ref<Group[]>([])
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const notice = ref('')
const showEditor = ref(false)
const editing = ref<Account | null>(null)
const results = reactive<Record<number, string>>({})

const PROVIDERS: Array<[string, string]> = [
  ['google', 'Google / Gmail'],
  ['outlook', 'Outlook'],
  ['qq', 'QQ'],
  ['163', '163'],
]

function mailEmail(account: Account): string {
  return String(account.credentials?.email || account.name || `#${account.id}`)
}

function mailProviderLabel(account: Account): string {
  const key = String(account.credentials?.mail_provider || '')
  return PROVIDERS.find(([value]) => value === key)?.[1] || key || t('admin.vision.mailContext.providerUnknown')
}

function openEditor(account: Account | null) {
  editing.value = account
  showEditor.value = true
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const [list, proxyList, groupList] = await Promise.all([
      adminAPI.accounts.list(1, 100, { platform: 'mail' }),
      adminAPI.proxies.getAll(),
      adminAPI.groups.getAllIncludingInactive(),
    ])
    accounts.value = list.items as unknown as Account[]
    proxies.value = proxyList
    groups.value = groupList
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, t('admin.vision.runtime.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function handleSaved() {
  showEditor.value = false
  notice.value = t('admin.vision.mailContext.saved')
  await load()
}

async function test(account: Account) {
  busy.value = true
  error.value = ''
  notice.value = ''
  results[account.id] = ''
  try {
    const email = mailEmail(account)
    const { data } = await apiClient.post(`/admin/accounts/${account.id}/mail/folders`, {}, { timeout: 90000 })
    const sent = await apiClient.post(`/admin/accounts/${account.id}/mail/send`, { to: [email], subject: t('admin.vision.mailContext.testSubject'), text: t('admin.vision.mailContext.testBody') }, { timeout: 90000 })
    results[account.id] = JSON.stringify({ folders: data, send: sent.data }, null, 2)
    notice.value = t('admin.vision.mailContext.testPassed')
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, t('admin.vision.mailContext.testFailed'))
  } finally {
    busy.value = false
  }
}

async function remove(account: Account) {
  const confirmed = typeof window !== 'undefined' && typeof window.confirm === 'function'
    ? window.confirm(t('admin.vision.mailContext.confirmRemove', { email: mailEmail(account) }))
    : true
  if (!confirmed) return
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    await adminAPI.accounts.delete(account.id)
    notice.value = t('admin.vision.mailContext.removed')
    await load()
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, t('admin.vision.mailContext.removeFailed'))
  } finally {
    busy.value = false
  }
}

onMounted(load)
</script>
