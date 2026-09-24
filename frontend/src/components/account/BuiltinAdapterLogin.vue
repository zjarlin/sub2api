<template>
  <div class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-600" data-testid="builtin-adapter-login">
    <p class="text-sm text-gray-600 dark:text-gray-300">{{ t(`admin.accounts.builtinLogin.${platform}Hint`) }}</p>
    <p class="input-hint">{{ t('admin.accounts.builtinLogin.poolHint') }}</p>
    <button type="button" class="btn btn-secondary" :disabled="busy" @click="start">
      {{ t(session ? 'admin.accounts.builtinLogin.restart' : 'admin.accounts.builtinLogin.start') }}
    </button>
    <template v-if="session?.status === 'pending'">
      <a :href="session.auth_url" target="_blank" rel="noopener noreferrer" class="block text-sm text-primary-600 underline dark:text-primary-400">
        {{ t('admin.accounts.builtinLogin.open') }}
      </a>
      <template v-if="session.mode === 'callback'">
        <label :for="`builtin-callback-${platform}`" class="input-label">{{ t('admin.accounts.builtinLogin.callback') }}</label>
        <input :id="`builtin-callback-${platform}`" v-model="callback" type="password" autocomplete="off" class="input" :placeholder="t('admin.accounts.builtinLogin.callbackPlaceholder')" />
        <button type="button" class="btn btn-primary" :disabled="busy || !callback.trim()" @click="complete">
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
import { cancelBuiltinLogin, completeBuiltinLogin, startBuiltinLogin, type BuiltinLoginPlatform, type BuiltinLoginSession } from '@/api/admin/builtinAdapters'

const props = defineProps<{ platform: BuiltinLoginPlatform }>()
const emit = defineEmits<{
  authorized: []
}>()
const { t } = useI18n()
const session = ref<BuiltinLoginSession | null>(null)
const callback = ref('')
const error = ref('')
const busy = ref(false)
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
    const result = await startBuiltinLogin(props.platform, controller.signal)
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
    const result = await completeBuiltinLogin(props.platform, current, callback.value.trim(), controller.signal)
    if (version !== generation) return
    session.value = result
    if (result.status === 'completed') {
      callback.value = ''
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
