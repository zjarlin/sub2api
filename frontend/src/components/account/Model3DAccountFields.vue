<template>
  <div class="space-y-3" data-testid="model3d-account-fields">
    <div>
      <label class="input-label" for="model3d-provider">{{ t('admin.accounts.model3d.provider') }}</label>
      <select id="model3d-provider" v-model="provider" class="input" :disabled="editing" data-testid="model3d-provider">
        <option value="meshy">Meshy</option><option value="tripo">Tripo</option><option value="hi3d">Hi3D / Hitem3D</option>
      </select>
    </div>
    <div v-if="provider === 'hi3d'">
      <label class="input-label" for="model3d-client-id">{{ t('admin.accounts.model3d.clientId') }}</label>
      <input id="model3d-client-id" v-model="clientId" type="text" required class="input" data-testid="model3d-client-id" />
    </div>
    <div>
      <label class="input-label" for="model3d-secret">{{ t(provider === 'hi3d' ? 'admin.accounts.model3d.clientSecret' : 'admin.accounts.apiKey') }}</label>
      <input id="model3d-secret" v-model="secret" type="password" :required="!editing" autocomplete="new-password" class="input font-mono" data-testid="model3d-secret" />
      <p class="input-hint">{{ t(editing ? 'admin.accounts.model3d.keepSecret' : 'admin.accounts.model3d.authorization') }}</p>
    </div>
    <div>
      <label class="input-label" for="model3d-default-model">{{ t('admin.accounts.model3d.defaultModel') }}</label>
      <input id="model3d-default-model" v-model="model" type="text" class="input" :placeholder="defaults[provider]" data-testid="model3d-default-model" />
    </div>
    <p class="input-hint">{{ t('admin.accounts.model3d.billingHint') }}</p>
  </div>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
defineProps<{ editing?: boolean }>()
const provider = defineModel<'meshy' | 'tripo' | 'hi3d'>('provider', { required: true })
const clientId = defineModel<string>('clientId', { required: true })
const secret = defineModel<string>('secret', { required: true })
const model = defineModel<string>('model', { required: true })
const defaults = { meshy: 'meshy-5', tripo: 'v2.5-20250123', hi3d: 'hi3dv3.0' }
const { t } = useI18n()
</script>
