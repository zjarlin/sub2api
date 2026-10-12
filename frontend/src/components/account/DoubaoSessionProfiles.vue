<template>
  <div class="space-y-3" data-testid="doubao-session-profiles">
    <div class="space-y-1">
      <label for="doubao-session-profile" class="input-label">{{ t('admin.accounts.doubao.sessionLabel') }}</label>
      <div class="flex flex-wrap gap-2">
        <select
          id="doubao-session-profile"
          v-model="selected"
          class="input min-w-0 flex-1"
          :disabled="busy || editing"
          data-testid="doubao-session-profile"
        >
          <option v-if="!profiles.includes(selected)" :value="selected" disabled>
            {{ t('admin.accounts.doubao.missingSession', { name: selected }) }}
          </option>
          <option v-for="profile in profiles" :key="profile" :value="profile">
            {{ profile === 'default' ? t('admin.accounts.doubao.defaultSession') : profile }}
          </option>
        </select>
        <button type="button" class="btn btn-secondary" :disabled="busy || editing" @click="loadProfiles">
          {{ t('common.refresh') }}
        </button>
      </div>
      <p class="input-hint">{{ t('admin.accounts.doubao.sessionHint') }}</p>
    </div>

    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <p v-if="loading" role="status" class="input-hint">{{ t('common.loading') }}</p>

    <div v-if="!editing" class="flex flex-wrap gap-2">
      <button type="button" class="btn btn-secondary" :disabled="busy" data-testid="doubao-add-session" @click="openUpload()">
        {{ t('admin.accounts.doubao.addSession') }}
      </button>
      <template v-if="selected !== 'default' && profiles.includes(selected)">
        <button type="button" class="btn btn-secondary" :disabled="busy" data-testid="doubao-update-session" @click="openUpload(selected)">
          {{ t('admin.accounts.doubao.updateSession') }}
        </button>
        <button type="button" class="btn btn-danger" :disabled="busy" data-testid="doubao-delete-session" @click="confirmation = 'delete'">
          {{ t('common.delete') }}
        </button>
      </template>
    </div>

    <div v-else class="space-y-3 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
      <p class="input-hint">{{ t('admin.accounts.doubao.uploadHint') }}</p>
      <div>
        <label for="doubao-profile-name" class="input-label">{{ t('admin.accounts.doubao.profileName') }}</label>
        <input id="doubao-profile-name" v-model="profileName" type="text" maxlength="64" autocomplete="off" class="input" :disabled="busy" data-testid="doubao-profile-name" />
        <p class="input-hint">{{ t('admin.accounts.doubao.profileNameHint') }}</p>
      </div>
      <div>
        <label for="doubao-session-file" class="input-label">{{ t('admin.accounts.doubao.sessionFile') }}</label>
        <input id="doubao-session-file" type="file" accept=".json,application/json" class="input" :disabled="busy" data-testid="doubao-session-file" @change="selectFile" />
      </div>
      <div class="flex gap-2">
        <button type="button" class="btn btn-primary" :disabled="busy || !sessionFile || !profileName.trim()" data-testid="doubao-upload-session" @click="requestUpload">
          {{ saving ? t('admin.accounts.doubao.uploading') : t('admin.accounts.doubao.uploadSession') }}
        </button>
        <button type="button" class="btn btn-secondary" :disabled="busy" @click="closeUpload">{{ t('common.cancel') }}</button>
      </div>
    </div>
  </div>

  <ConfirmDialog
    :show="confirmation !== null"
    :title="t(confirmation === 'delete' ? 'admin.accounts.doubao.deleteSessionTitle' : 'admin.accounts.doubao.replaceSessionTitle')"
    :message="t(confirmation === 'delete' ? 'admin.accounts.doubao.deleteSessionMessage' : 'admin.accounts.doubao.replaceSessionMessage', { name: confirmation === 'delete' ? selected : profileName.trim() })"
    danger
    @confirm="confirmAction"
    @cancel="confirmation = null"
  />
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { listDoubaoSessionProfiles, uploadDoubaoSessionProfile, deleteDoubaoSessionProfile } from '@/api/admin/builtinAdapters'
import { useStepUp, isStepUpBlocked, isStepUpCancelled, stepUpBlockReason } from '@/composables/useStepUp'
import { extractApiErrorMessage } from '@/utils/apiError'
import { useAppStore } from '@/stores/app'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'

const props = defineProps<{ modelValue: string }>()
const emit = defineEmits<{
  (event: 'update:modelValue', value: string): void
  (event: 'busy', value: boolean): void
}>()
const { t } = useI18n()
const appStore = useAppStore()
const stepUp = useStepUp()
const controller = new AbortController()
const profiles = ref<string[]>(['default'])
const loading = ref(false)
const saving = ref(false)
const editing = ref(false)
const error = ref('')
const profileName = ref('')
const sessionFile = ref<File | null>(null)
const confirmation = ref<'delete' | 'replace' | null>(null)
const selected = computed({ get: () => props.modelValue || 'default', set: value => emit('update:modelValue', value) })
const busy = computed(() => loading.value || saving.value)
watch(() => busy.value || editing.value || !profiles.value.includes(selected.value), value => emit('busy', value), { immediate: true })

function showError(cause: unknown, fallback: string) {
  if (controller.signal.aborted || isStepUpCancelled(cause)) {
    return
  }
  if (isStepUpBlocked(cause)) {
    error.value = t(stepUpBlockReason(cause) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? 'stepUp.adminApiKeyForbidden' : 'stepUp.notEnabled')
    return
  }
  error.value = extractApiErrorMessage(cause, t(fallback))
}

async function loadProfiles() {
  loading.value = true
  error.value = ''
  try {
    const data = await listDoubaoSessionProfiles(controller.signal)
    profiles.value = data.profiles
  } catch (cause) {
    showError(cause, 'admin.accounts.doubao.loadFailed')
  } finally {
    loading.value = false
  }
}

function openUpload(name = '') {
  profileName.value = name
  sessionFile.value = null
  error.value = ''
  editing.value = true
}

function closeUpload() {
  editing.value = false
  sessionFile.value = null
  profileName.value = ''
  error.value = ''
}

function selectFile(event: Event) {
  const input = event.target as HTMLInputElement
  sessionFile.value = input.files?.[0] ?? null
  error.value = ''
  if (sessionFile.value && (sessionFile.value.size === 0 || sessionFile.value.size > 65536)) {
    error.value = t('admin.accounts.doubao.fileSizeError')
    sessionFile.value = null
    input.value = ''
  }
}

function requestUpload() {
  const name = profileName.value.trim()
  if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(name) || name === 'default') {
    error.value = t('admin.accounts.doubao.profileNameHint')
    return
  }
  if (!sessionFile.value || busy.value) {
    return
  }
  if (profiles.value.includes(name)) {
    confirmation.value = 'replace'
    return
  }
  void upload()
}

async function upload() {
  const file = sessionFile.value
  const name = profileName.value.trim()
  if (!file || busy.value) {
    return
  }
  saving.value = true
  error.value = ''
  try {
    await stepUp.run(() => uploadDoubaoSessionProfile(name, file, controller.signal))
    if (controller.signal.aborted) {
      return
    }
    profiles.value = Array.from(new Set([...profiles.value, name]))
    selected.value = name
    closeUpload()
    appStore.showSuccess(t('admin.accounts.doubao.uploadSuccess'))
    await loadProfiles()
  } catch (cause) {
    showError(cause, 'admin.accounts.doubao.uploadFailed')
  } finally {
    saving.value = false
  }
}

async function remove() {
  const name = selected.value
  if (name === 'default' || busy.value) {
    return
  }
  saving.value = true
  error.value = ''
  try {
    await stepUp.run(() => deleteDoubaoSessionProfile(name, controller.signal))
    if (controller.signal.aborted) {
      return
    }
    profiles.value = profiles.value.filter(profile => profile !== name)
    selected.value = 'default'
    appStore.showSuccess(t('admin.accounts.doubao.deleteSuccess'))
  } catch (cause) {
    showError(cause, 'admin.accounts.doubao.deleteFailed')
  } finally {
    saving.value = false
  }
}

function confirmAction() {
  const action = confirmation.value
  confirmation.value = null
  if (action === 'delete') {
    void remove()
  } else if (action === 'replace') {
    void upload()
  }
}

onMounted(loadProfiles)
onBeforeUnmount(() => {
  controller.abort()
  stepUp.onCancel()
  emit('busy', false)
})
</script>
