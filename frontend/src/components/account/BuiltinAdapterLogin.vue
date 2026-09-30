<template>
  <div class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600" data-testid="builtin-adapter-login">
    <p class="text-sm text-gray-600 dark:text-gray-300">{{ t(hintKey) }}</p>
    <p class="input-hint">{{ t(platform === 'arena' ? 'admin.accounts.arena.loginSessionHint' : platform === 'deepseek_web' ? 'admin.accounts.builtinLogin.deepseekWebPoolHint' : platform === 'vibex' ? 'admin.accounts.builtinLogin.vibexPoolHint' : platform === 'zcode' ? 'admin.accounts.builtinLogin.zcodePoolHint' : platform === 'cursor' ? 'admin.accounts.builtinLogin.cursorPoolHint' : 'admin.accounts.builtinLogin.poolHint') }}</p>
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
    <div v-if="platform === 'deepseek_web'" class="grid grid-cols-1 gap-3 sm:grid-cols-2">
      <div>
        <label for="deepseek-login-email" class="input-label">{{ t('admin.accounts.builtinLogin.deepseekWebEmail') }}</label>
        <input id="deepseek-login-email" v-model="deepseekEmail" type="email" autocomplete="username" class="input" :disabled="busy || session?.status === 'pending'" />
      </div>
      <div>
        <label for="deepseek-login-password" class="input-label">{{ t('admin.accounts.builtinLogin.deepseekWebPassword') }}</label>
        <input id="deepseek-login-password" v-model="deepseekPassword" type="password" autocomplete="current-password" class="input" :disabled="busy || session?.status === 'pending'" @keydown.enter.prevent="start" />
      </div>
      <div class="sm:col-span-2">
        <label for="deepseek-auto-relogin" class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
          <input id="deepseek-auto-relogin" v-model="deepseekAutoRelogin" type="checkbox" class="rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500 dark:bg-dark-700" :disabled="busy || session?.status === 'pending'" aria-describedby="deepseek-auto-relogin-hint" />
          {{ t('admin.accounts.builtinLogin.deepseekWebAutoRelogin') }}
        </label>
        <p id="deepseek-auto-relogin-hint" class="input-hint">{{ t('admin.accounts.builtinLogin.deepseekWebAutoReloginHint') }}</p>
      </div>
    </div>
    <button type="button" class="btn btn-secondary" :disabled="startDisabled" @click="start">
      {{ t(startKey) }}
    </button>
    <template v-if="session?.status === 'pending'">
      <a v-if="session.auth_url && platform !== 'deepseek_web'" :href="session.auth_url" target="_blank" rel="noopener noreferrer" class="block text-sm text-primary-600 underline dark:text-primary-400">
        {{ t(platform === 'cursor' ? 'admin.accounts.builtinLogin.cursorOpen' : 'admin.accounts.builtinLogin.open') }}
      </a>
      <div v-if="platform === 'deepseek_web'" class="space-y-2">
        <img v-if="loginViewURL" :src="loginViewURL" data-testid="deepseek-login-view" :alt="t('admin.accounts.builtinLogin.deepseekWebQRCodeAlt')" class="mx-auto max-h-[520px] w-full rounded-lg border border-gray-200 object-contain dark:border-dark-600" />
        <p class="input-hint" role="status">{{ t(loginViewURL ? 'admin.accounts.builtinLogin.deepseekWebWaiting' : 'admin.accounts.builtinLogin.deepseekWebLoading') }}</p>
      </div>
      <template v-else-if="session.mode === 'callback'">
        <label :for="`builtin-callback-${platform}`" class="input-label">{{ t(platform === 'vibex' ? 'admin.accounts.builtinLogin.vibexToken' : 'admin.accounts.builtinLogin.callback') }}</label>
        <input :id="`builtin-callback-${platform}`" v-model="callback" type="password" autocomplete="off" class="input" :placeholder="t(platform === 'vibex' ? 'admin.accounts.builtinLogin.vibexTokenPlaceholder' : 'admin.accounts.builtinLogin.callbackPlaceholder')" />
        <button type="button" class="btn btn-primary" :disabled="busy || !callback.trim()" @click="complete">
          {{ t('admin.accounts.builtinLogin.complete') }}
        </button>
      </template>
      <p v-else class="input-hint" role="status">{{ t(platform === 'arena' ? 'admin.accounts.arena.loggingIn' : platform === 'cursor' ? 'admin.accounts.builtinLogin.cursorWaiting' : 'admin.accounts.builtinLogin.waiting') }}</p>
      <button type="button" class="btn btn-secondary ml-2" @click="cancel">{{ t('common.cancel') }}</button>
    </template>
    <p v-if="session?.status === 'completed'" role="status" class="text-sm text-green-700 dark:text-green-400">
      {{ t('admin.accounts.builtinLogin.success', { name: session.account?.nickname || session.account?.uid }) }}
    </p>
    <p v-if="platform === 'deepseek_web' && session?.status === 'completed' && session.account?.auto_relogin !== undefined" role="status" class="input-hint" data-testid="deepseek-auto-relogin-status">
      {{ t(session.account.auto_relogin ? 'admin.accounts.builtinLogin.deepseekWebAutoReloginEnabled' : 'admin.accounts.builtinLogin.deepseekWebAutoReloginDisabled') }}
    </p>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { cancelBuiltinLogin, completeBuiltinLogin, getBuiltinLoginView, startBuiltinLogin, type BuiltinLoginPlatform, type BuiltinLoginSession, type DeepseekLoginOptions, type PasswordLoginOptions, type ZcodeLoginOptions } from '@/api/admin/builtinAdapters'

const props = defineProps<{ platform: BuiltinLoginPlatform }>()
const emit = defineEmits<{
  authorized: [session: BuiltinLoginSession]
}>()
const { t } = useI18n()
const session = ref<BuiltinLoginSession | null>(null)
const callback = ref('')
const arenaEmail = ref('')
const arenaPassword = ref('')
const deepseekEmail = ref('')
const deepseekPassword = ref('')
const deepseekAutoRelogin = ref(true)
const loginViewURL = ref('')
const error = ref('')
const arenaErrorKeys = new Map<string, string>([
  ['BUILTIN_ADAPTER_DISABLED', 'admin.accounts.arena.unavailable'],
  ['ARENA_ACCESS_BLOCKED', 'admin.accounts.arena.accessBlocked'],
  ['ARENA_VERIFICATION_REQUIRED', 'admin.accounts.arena.verificationRequired'],
  ['ARENA_INVALID_CREDENTIALS', 'admin.accounts.arena.invalidCredentials'],
  ['ARENA_SESSION_UNUSABLE', 'admin.accounts.arena.sessionUnusable'],
  ['ARENA_SESSION_NOT_READY', 'admin.accounts.arena.sessionNotReady'],
  ['ARENA_BROWSER_UNAVAILABLE', 'admin.accounts.arena.browserUnavailable'],
  ['ARENA_NETWORK_ERROR', 'admin.accounts.arena.networkError'],
  ['ARENA_LOGIN_TIMEOUT', 'admin.accounts.arena.loginTimeout'],
  ['ARENA_SESSION_PREPARE_FAILED', 'admin.accounts.arena.sessionPrepareFailed']
])
const busy = ref(false)
const zcodePlan = ref<ZcodeLoginOptions['plan']>('coding-plan')
const zcodeProvider = ref<ZcodeLoginOptions['provider']>('zai')
const hintKey = computed(() => props.platform === 'arena'
  ? 'admin.accounts.arena.loginHint'
  : props.platform === 'deepseek_web'
  ? 'admin.accounts.builtinLogin.deepseekWebHint'
  : props.platform === 'cursor'
  ? 'admin.accounts.builtinLogin.cursorHint'
  : `admin.accounts.builtinLogin.${props.platform}Hint`)
const startKey = computed(() => {
  if (props.platform === 'arena') return 'admin.accounts.arena.login'
  if (props.platform === 'cursor') return session.value ? 'admin.accounts.builtinLogin.cursorRestart' : 'admin.accounts.builtinLogin.cursorStart'
  if (props.platform === 'deepseek_web') {
    return session.value ? 'admin.accounts.builtinLogin.deepseekWebRestart' : 'admin.accounts.builtinLogin.deepseekWebStart'
  }
  return session.value ? 'admin.accounts.builtinLogin.restart' : 'admin.accounts.builtinLogin.start'
})
const startDisabled = computed(() => {
  if (busy.value) return true
  if (props.platform === 'arena') {
    return !arenaEmail.value.trim() || !arenaPassword.value || session.value?.status === 'pending'
  }
  if (props.platform === 'deepseek_web') {
    return !deepseekEmail.value.trim() || !deepseekPassword.value || session.value?.status === 'pending'
  }
  return false
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
  if (loginViewURL.value) URL.revokeObjectURL(loginViewURL.value)
  loginViewURL.value = ''
  authorizedEmitted = false
}

function showError(err: unknown) {
  const detail = err as { code?: string; message?: string }
  if (props.platform === 'arena') {
    error.value = t(arenaErrorKeys.get(detail.code || '') || 'admin.accounts.arena.loginFailed')
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
  deepseekPassword.value = ''
  if (!current || current.status !== 'pending') return
  try {
    await cancelBuiltinLogin(props.platform, current.session_id)
  } catch (err) {
    if (!disposed) showError(err)
  }
}

async function refreshLoginView(current: BuiltinLoginSession, version: number) {
  if (props.platform !== 'deepseek_web' || current.status !== 'pending') return
  try {
    const blob = await getBuiltinLoginView(props.platform, current.session_id, controller?.signal)
    if (version !== generation) return
    const nextURL = URL.createObjectURL(blob)
    if (loginViewURL.value) URL.revokeObjectURL(loginViewURL.value)
    loginViewURL.value = nextURL
  } catch {
    // 登录页启动和截图刷新可能短暂重叠，下一次轮询会继续加载。
  }
}

async function start() {
  if (starting || disposed || startDisabled.value) return
  let options: ZcodeLoginOptions | PasswordLoginOptions | DeepseekLoginOptions | undefined
  if (props.platform === 'zcode') {
    options = { plan: zcodePlan.value, provider: zcodeProvider.value }
  } else if (props.platform === 'arena') {
    options = { email: arenaEmail.value.trim(), password: arenaPassword.value }
  } else if (props.platform === 'deepseek_web') {
    options = { email: deepseekEmail.value.trim(), password: deepseekPassword.value, auto_relogin: deepseekAutoRelogin.value }
  }
  starting = true
  await cancel()
  if (disposed) { starting = false; return }
  error.value = ''
  busy.value = true
  const version = generation
  controller = new AbortController()
  try {
    const result = await startBuiltinLogin(props.platform, controller.signal, options)
    if (version !== generation) {
      if (props.platform === 'arena' || props.platform === 'deepseek_web') await cancelBuiltinLogin(props.platform, result.session_id)
      return
    }
    session.value = result
    await refreshLoginView(result, version)
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
    const result = await completeBuiltinLogin(props.platform, current, callback.value.trim(), controller.signal)
    if (version !== generation) return
    session.value = result
    if (result.status === 'completed') {
      callback.value = ''
      if (loginViewURL.value) URL.revokeObjectURL(loginViewURL.value)
      loginViewURL.value = ''
      if (!authorizedEmitted) {
        authorizedEmitted = true
        emit('authorized', result)
      }
    }
    else if (result.mode === 'poll') {
      await refreshLoginView(result, version)
      timer = setTimeout(complete, 2500)
    }
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
  if (props.platform === 'arena' || props.platform === 'deepseek_web' || props.platform === 'cursor') {
    void cancel()
  } else {
    stop()
  }
})
</script>
