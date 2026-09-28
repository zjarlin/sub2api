<template>
  <div class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600" data-testid="builtin-adapter-login">
    <p class="text-sm text-gray-600 dark:text-gray-300">{{ t(`admin.accounts.builtinLogin.${platform}Hint`) }}</p>
    <p class="input-hint">{{ t(platform === 'deepseek_web' ? 'admin.accounts.builtinLogin.deepseekWebPoolHint' : platform === 'vibex' ? 'admin.accounts.builtinLogin.vibexPoolHint' : platform === 'zcode' ? 'admin.accounts.builtinLogin.zcodePoolHint' : 'admin.accounts.builtinLogin.poolHint') }}</p>
    <div v-if="platform === 'zcode'" class="grid grid-cols-1 gap-3 sm:grid-cols-2">
      <div>
        <label for="zcode-login-plan" class="input-label">{{ t('admin.accounts.builtinLogin.zcodePlan') }}</label>
        <select id="zcode-login-plan" v-model="zcodePlan" class="input" :disabled="busy || session?.status === 'pending'">
          <option value="coding-plan">Coding Plan</option>
          <option value="start-plan">Start Plan</option>
        </select>
      </div>
      <div>
        <label for="zcode-login-provider" class="input-label">{{ t('admin.accounts.builtinLogin.zcodeProvider') }}</label>
        <select id="zcode-login-provider" v-model="zcodeProvider" class="input" :disabled="busy || session?.status === 'pending'">
          <option value="bigmodel">{{ t('admin.accounts.builtinLogin.zcodeBigmodel') }}</option>
          <option value="zai">Z.ai</option>
        </select>
      </div>
    </div>
    <button type="button" class="btn btn-secondary" :disabled="busy" @click="start">
      {{ t(session ? 'admin.accounts.builtinLogin.restart' : 'admin.accounts.builtinLogin.start') }}
    </button>
    <template v-if="session?.status === 'pending'">
      <a :href="session.auth_url" target="_blank" rel="noopener noreferrer" class="block text-sm text-primary-600 underline dark:text-primary-400">
        {{ t('admin.accounts.builtinLogin.open') }}
      </a>
      <template v-if="session.mode === 'callback'">
        <div v-if="platform === 'deepseek_web'" class="space-y-3">
          <div>
            <label for="deepseek-web-token" class="input-label">{{ t('admin.accounts.builtinLogin.deepseekWebToken') }}</label>
            <input id="deepseek-web-token" v-model="browserToken" type="password" autocomplete="off" class="input" />
          </div>
          <div>
            <label for="deepseek-web-device" class="input-label">{{ t('admin.accounts.builtinLogin.deepseekWebDevice') }}</label>
            <input id="deepseek-web-device" v-model="browserDeviceID" type="text" autocomplete="off" class="input" />
          </div>
        </div>
        <template v-else>
          <label :for="`builtin-callback-${platform}`" class="input-label">{{ t(platform === 'vibex' ? 'admin.accounts.builtinLogin.vibexToken' : 'admin.accounts.builtinLogin.callback') }}</label>
          <input :id="`builtin-callback-${platform}`" v-model="callback" type="password" autocomplete="off" class="input" :placeholder="t(platform === 'vibex' ? 'admin.accounts.builtinLogin.vibexTokenPlaceholder' : 'admin.accounts.builtinLogin.callbackPlaceholder')" />
        </template>
        <button type="button" class="btn btn-primary" :disabled="busy || (platform === 'deepseek_web' ? !browserToken.trim() || !browserDeviceID.trim() : !callback.trim())" @click="complete">
          {{ t('admin.accounts.builtinLogin.complete') }}
        </button>
      </template>
      <p v-else class="input-hint" role="status">{{ t('admin.accounts.builtinLogin.waiting') }}</p>
      <button type="button" class="btn btn-secondary ml-2" @click="cancel">{{ t('common.cancel') }}</button>
    </template>
    <p v-if="session?.status === 'completed'" role="status" class="text-sm text-green-700 dark:text-green-400">
      {{ t('admin.accounts.builtinLogin.success', { name: session.account?.nickname || session.account?.uid }) }}
    </p>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { cancelBuiltinLogin, completeBuiltinLogin, startBuiltinLogin, type BuiltinLoginPlatform, type BuiltinLoginSession, type ZcodeLoginOptions } from '@/api/admin/builtinAdapters'

const props = defineProps<{ platform: BuiltinLoginPlatform }>()
const emit = defineEmits<{
  authorized: []
}>()
const { t } = useI18n()
const session = ref<BuiltinLoginSession | null>(null)
const callback = ref('')
const browserToken = ref('')
const browserDeviceID = ref('')
const error = ref('')
const busy = ref(false)
const zcodePlan = ref<ZcodeLoginOptions['plan']>('coding-plan')
const zcodeProvider = ref<ZcodeLoginOptions['provider']>('zai')
let timer: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | undefined
let generation = 0
let disposed = false
let starting = false
let authorizedEmitted = false

function stop() {
  generation++
  clearTimeout(timer)
  controller?.abort()
  busy.value = false
  callback.value = ''
  browserToken.value = ''
  browserDeviceID.value = ''
  authorizedEmitted = false
}

function showError(err: unknown) {
  const detail = err as { message?: string }
  error.value = detail.message || t('admin.accounts.builtinLogin.failed')
}

async function cancel() {
  const current = session.value
  stop()
  session.value = null
  if (!current || current.status !== 'pending') return
  try {
    await cancelBuiltinLogin(props.platform, current.session_id)
  } catch (err) {
    if (!disposed) showError(err)
  }
}

async function start() {
  if (starting || disposed) return
  starting = true
  await cancel()
  if (disposed) { starting = false; return }
  error.value = ''
  busy.value = true
  const version = generation
  controller = new AbortController()
  try {
    const result = props.platform === 'zcode'
      ? await startBuiltinLogin(props.platform, controller.signal, { plan: zcodePlan.value, provider: zcodeProvider.value })
      : await startBuiltinLogin(props.platform, controller.signal)
    if (version !== generation) return
    session.value = result
    if (result.mode === 'poll') timer = setTimeout(complete, 2500)
  } catch (err) {
    if (version === generation) showError(err)
  } finally {
    starting = false
    if (version === generation) busy.value = false
  }
}

async function complete() {
  const current = session.value
  if (!current || current.status !== 'pending') return
  if (Date.now() >= current.expires_at) {
    stop()
    session.value = null
    error.value = t('admin.accounts.builtinLogin.expired')
    return
  }
  busy.value = true
  error.value = ''
  const version = generation
  controller = new AbortController()
  try {
    const credential = props.platform === 'deepseek_web'
      ? JSON.stringify({ token: browserToken.value.trim(), device_id: browserDeviceID.value.trim() })
      : callback.value.trim()
    const result = await completeBuiltinLogin(props.platform, current, credential, controller.signal)
    if (version !== generation) return
    session.value = result
    if (result.status === 'completed') {
      callback.value = ''
      browserToken.value = ''
      browserDeviceID.value = ''
      if (!authorizedEmitted) {
        authorizedEmitted = true
        emit('authorized')
      }
    }
    else if (result.mode === 'poll') timer = setTimeout(complete, 2500)
  } catch (err) {
    if (version === generation) showError(err)
  } finally {
    if (version === generation) busy.value = false
  }
}

// 关闭表单立即中止轮询；服务端未完成会话会在十分钟后过期。
onBeforeUnmount(() => { disposed = true; stop() })
</script>
