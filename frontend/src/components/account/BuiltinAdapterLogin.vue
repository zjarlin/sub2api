<template>
  <div class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600" data-testid="builtin-adapter-login">
    <p class="text-sm text-gray-600 dark:text-gray-300">{{ t(hintKey) }}</p>
    <p class="input-hint">{{ t(platform === 'arena' ? 'admin.accounts.arena.loginSessionHint' : platform === 'deepseek_web' ? 'admin.accounts.builtinLogin.deepseekWebPoolHint' : platform === 'vibex' ? 'admin.accounts.builtinLogin.vibexPoolHint' : platform === 'zcode' ? 'admin.accounts.builtinLogin.zcodePoolHint' : 'admin.accounts.builtinLogin.poolHint') }}</p>
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
    <div v-if="platform === 'arena'" class="grid grid-cols-1 gap-3 sm:grid-cols-2">
      <div>
        <label for="arena-login-email" class="input-label">{{ t('admin.accounts.arena.email') }}</label>
        <input id="arena-login-email" v-model="arenaEmail" type="email" autocomplete="username" class="input" :disabled="busy || session?.status === 'pending'" />
      </div>
      <div>
        <label for="arena-login-password" class="input-label">{{ t('admin.accounts.arena.password') }}</label>
        <input id="arena-login-password" v-model="arenaPassword" type="password" autocomplete="current-password" class="input" :disabled="busy || session?.status === 'pending'" @keydown.enter.prevent="start" />
      </div>
    </div>
    <button type="button" class="btn btn-secondary" :disabled="busy || (platform === 'arena' && (!arenaEmail.trim() || !arenaPassword || session?.status === 'pending'))" @click="start">
      {{ t(startKey) }}
    </button>
    <template v-if="session?.status === 'pending'">
      <a v-if="session.auth_url" :href="session.auth_url" target="_blank" rel="noopener noreferrer" class="block text-sm text-primary-600 underline dark:text-primary-400">
        {{ t(platform === 'deepseek_web' ? 'admin.accounts.builtinLogin.deepseekWebOpen' : 'admin.accounts.builtinLogin.open') }}
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
          {{ t(platform === 'deepseek_web' ? 'admin.accounts.builtinLogin.deepseekWebComplete' : 'admin.accounts.builtinLogin.complete') }}
        </button>
      </template>
      <p v-else class="input-hint" role="status">{{ t(platform === 'arena' ? 'admin.accounts.arena.loggingIn' : 'admin.accounts.builtinLogin.waiting') }}</p>
      <button type="button" class="btn btn-secondary ml-2" @click="cancel">{{ t('common.cancel') }}</button>
    </template>
    <p v-if="session?.status === 'completed'" role="status" class="text-sm text-green-700 dark:text-green-400">
      {{ t('admin.accounts.builtinLogin.success', { name: session.account?.nickname || session.account?.uid }) }}
    </p>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { cancelBuiltinLogin, completeBuiltinLogin, startBuiltinLogin, type BuiltinLoginPlatform, type BuiltinLoginSession, type ZcodeLoginOptions } from '@/api/admin/builtinAdapters'

const props = defineProps<{ platform: BuiltinLoginPlatform }>()
const emit = defineEmits<{
  authorized: [session: BuiltinLoginSession]
}>()
const { t } = useI18n()
const session = ref<BuiltinLoginSession | null>(null)
const callback = ref('')
const arenaEmail = ref('')
const arenaPassword = ref('')
const browserToken = ref('')
const browserDeviceID = ref('')
const error = ref('')
const busy = ref(false)
const zcodePlan = ref<ZcodeLoginOptions['plan']>('coding-plan')
const zcodeProvider = ref<ZcodeLoginOptions['provider']>('zai')
const hintKey = computed(() => props.platform === 'arena'
  ? 'admin.accounts.arena.loginHint'
  : props.platform === 'deepseek_web'
  ? 'admin.accounts.builtinLogin.deepseekWebHint'
  : `admin.accounts.builtinLogin.${props.platform}Hint`)
const startKey = computed(() => {
  if (props.platform === 'arena') return 'admin.accounts.arena.login'
  if (props.platform === 'deepseek_web') {
    return session.value ? 'admin.accounts.builtinLogin.deepseekWebRestart' : 'admin.accounts.builtinLogin.deepseekWebStart'
  }
  return session.value ? 'admin.accounts.builtinLogin.restart' : 'admin.accounts.builtinLogin.start'
})
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
  const detail = err as { code?: string; message?: string }
  if (props.platform === 'arena') {
    error.value = t(detail.code === 'BUILTIN_ADAPTER_DISABLED' ? 'admin.accounts.arena.unavailable' : 'admin.accounts.arena.loginFailed')
    return
  }
  if (props.platform === 'deepseek_web' && detail.code === 'BUILTIN_ADAPTER_DISABLED') {
    error.value = t('admin.accounts.builtinLogin.deepseekWebUnavailable')
    return
  }
  error.value = detail.message || t('admin.accounts.builtinLogin.failed')
}

async function cancel() {
  const current = session.value
  stop()
  session.value = null
  arenaPassword.value = ''
  if (!current || current.status !== 'pending') return
  try {
    await cancelBuiltinLogin(props.platform, current.session_id)
  } catch (err) {
    if (!disposed) showError(err)
  }
}

async function start() {
  if (starting || disposed) return
  if (props.platform === 'arena' && (!arenaEmail.value.trim() || !arenaPassword.value || session.value?.status === 'pending')) return
  const arenaCredentials = props.platform === 'arena' ? { email: arenaEmail.value.trim(), password: arenaPassword.value } : undefined
  starting = true
  await cancel()
  if (disposed) { starting = false; return }
  error.value = ''
  busy.value = true
  const version = generation
  controller = new AbortController()
  try {
    const result = props.platform === 'arena'
      ? await startBuiltinLogin(props.platform, controller.signal, arenaCredentials)
      : props.platform === 'zcode'
      ? await startBuiltinLogin(props.platform, controller.signal, { plan: zcodePlan.value, provider: zcodeProvider.value })
      : await startBuiltinLogin(props.platform, controller.signal)
    if (version !== generation) {
      if (props.platform === 'arena') await cancelBuiltinLogin(props.platform, result.session_id)
      return
    }
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
        emit('authorized', result)
      }
    }
    else if (result.mode === 'poll') timer = setTimeout(complete, 2500)
  } catch (err) {
    if (version === generation) {
      showError(err)
      if (props.platform === 'arena') {
        await cancel()
      }
    }
  } finally {
    if (version === generation) busy.value = false
  }
}

// 关闭表单立即中止轮询；服务端未完成会话会在十分钟后过期。
onBeforeUnmount(() => {
  disposed = true
  if (props.platform === 'arena') {
    void cancel()
  } else {
    stop()
  }
})
</script>
