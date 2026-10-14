<template>
  <BaseDialog :show="show" :title="t('admin.accounts.mail.title')" @close="emit('close')">
    <form class="space-y-4" @submit.prevent="save">
      <label class="block">{{ t('admin.accounts.mail.provider') }}
        <select v-model="provider" class="input" data-testid="mail-provider">
          <option value="google">Google / Gmail</option><option value="outlook">Outlook</option>
          <option value="qq">QQ</option><option value="163">163</option>
        </select>
      </label>
      <label class="block">{{ t('admin.accounts.mail.email') }}
        <input v-model="email" class="input" type="email" required autocomplete="username" data-testid="mail-email" />
      </label>
      <label class="block">{{ t('admin.accounts.mail.secret') }}
        <input v-model="secret" class="input" type="password" :required="!savedID" autocomplete="new-password" data-testid="mail-secret" />
      </label>
      <p class="input-hint">{{ t('admin.accounts.mail.hint') }}</p>
      <div><p class="input-label">{{ t('admin.accounts.mail.proxy') }}</p>
        <ProxySelector v-model="proxyID" :proxies="proxies" />
      </div>
      <label class="block">{{ t('admin.accounts.mail.groups') }}
        <select v-model="groupIDs" class="input" multiple>
          <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }}</option>
        </select>
      </label>
      <button class="btn btn-primary" :disabled="busy" data-testid="mail-save">{{ t('admin.accounts.mail.save') }}</button>
    </form>
    <div v-if="savedID" class="mt-4 space-y-3">
      <p class="input-hint">{{ t('admin.accounts.mail.savedHint') }}</p>
      <div class="flex flex-wrap gap-2">
        <button v-for="action in (['folders', 'messages', 'send'] as const)" :key="action" class="btn btn-secondary" :disabled="busy" @click="run(action)">{{ t(`admin.accounts.mail.${action}`) }}</button>
      </div>
    </div>
    <p v-if="error" role="alert" class="mt-3 text-red-600">{{ error }}</p>
    <pre v-if="result" class="mt-3 max-h-72 overflow-auto whitespace-pre-wrap text-xs" aria-live="polite">{{ result }}</pre>
  </BaseDialog>
</template>
<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ProxySelector from '@/components/common/ProxySelector.vue'
import { apiClient } from '@/api/client'
import * as accounts from '@/api/admin/accounts'
import type { Account, Group, Proxy } from '@/types'

const props = defineProps<{ show: boolean; account?: Account | null; proxies: Proxy[]; groups: Group[] }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const provider = ref('google')
const email = ref('')
const secret = ref('')
const proxyID = ref<number | null>(null)
const groupIDs = ref<number[]>([])
const savedID = ref<number | null>(null)
const savedEmail = ref('')
const busy = ref(false)
const error = ref('')
const result = ref('')
watch(() => props.show, show => {
  secret.value = ''
  if (!show) return
  provider.value = String(props.account?.credentials?.mail_provider || 'google')
  email.value = String(props.account?.credentials?.email || '')
  savedEmail.value = email.value
  proxyID.value = props.account?.proxy_id ?? null
  groupIDs.value = [...(props.account?.group_ids || [])]
  savedID.value = props.account?.id ?? null
  error.value = ''; result.value = ''
})
function showError(err: unknown) {
  const e = err as { message?: string; response?: { data?: { message?: string; error?: { message?: string } | string } } }
  const data = e.response?.data
  error.value = data?.message || (typeof data?.error === 'string' ? data.error : data?.error?.message) || e.message || t('common.error')
}
async function save() {
  busy.value = true; error.value = ''; result.value = ''
  try {
    const credentials: Record<string, unknown> = { email: email.value.trim(), mail_provider: provider.value }
    if (secret.value) credentials.api_key = secret.value
    const payload = { name: email.value.trim(), credentials, proxy_id: proxyID.value, group_ids: groupIDs.value }
    const account = savedID.value
      ? await accounts.update(savedID.value, { ...payload, proxy_id: proxyID.value ?? 0 })
      : await accounts.create({ ...payload, platform: 'mail', type: 'apikey', concurrency: 1, priority: 1, confirm_mixed_channel_risk: true })
    savedID.value = account.id; savedEmail.value = email.value.trim(); secret.value = ''
    result.value = t('admin.accounts.mail.saved'); emit('saved')
  } catch (err) { showError(err) } finally { busy.value = false }
}
async function run(action: 'folders' | 'messages' | 'send') {
  busy.value = true; error.value = ''; result.value = ''
  try {
    const body = action === 'send'
      ? { to: [savedEmail.value], subject: t('admin.accounts.mail.subject'), text: t('admin.accounts.mail.body') }
      : { limit: 5 }
    const { data } = await apiClient.post(`/admin/accounts/${savedID.value}/mail/${action}`, body, { timeout: 90000 })
    result.value = JSON.stringify(data, null, 2)
  } catch (err) { showError(err) } finally { busy.value = false }
}
</script>
