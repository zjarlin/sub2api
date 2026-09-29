<template>
  <div class="min-h-screen bg-slate-50 text-slate-950 dark:bg-dark-950 dark:text-white">
    <header class="border-b border-slate-200 bg-white/90 px-6 py-4 backdrop-blur dark:border-dark-800 dark:bg-dark-900/90">
      <nav class="mx-auto flex max-w-6xl items-center justify-between gap-4">
        <router-link to="/home" class="flex min-w-0 items-center gap-3">
          <div class="flex h-10 w-10 shrink-0 items-center justify-center overflow-hidden rounded-lg border border-slate-200 bg-slate-100 dark:border-dark-700 dark:bg-dark-800">
            <img v-if="siteLogo" :src="siteLogo" alt="" class="h-full w-full object-contain" />
            <span v-else class="text-sm font-bold">{{ siteNameInitial }}</span>
          </div>
          <span class="truncate text-base font-semibold">{{ siteName }}</span>
        </router-link>

        <div class="flex items-center gap-2">
          <LocaleSwitcher />
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm font-semibold text-slate-800 transition-colors hover:bg-slate-100 dark:border-dark-700 dark:bg-dark-900 dark:text-white dark:hover:bg-dark-800"
          >
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="mx-auto max-w-4xl space-y-8 px-4 py-8 sm:px-6">
      <section id="quick-start" class="scroll-mt-6">
        <h1 class="text-2xl font-semibold">{{ t('docs.title') }}</h1>
        <a href="https://learn.chatgpt.com/docs/app" target="_blank" rel="noopener noreferrer" class="mt-3 inline-flex items-center gap-2 text-primary-600 hover:underline dark:text-primary-300">
          <Icon name="book" size="sm" />
          {{ t('docs.official') }}
        </a>
        <div class="mt-4 flex flex-wrap gap-1" role="radiogroup" :aria-label="t('docs.setup.platform')">
          <button v-for="platform in platforms" :key="platform.id" type="button" role="radio" :aria-checked="selectedPlatform === platform.id" :data-testid="'platform-' + platform.id" class="rounded-md border px-3 py-2 text-sm" :class="selectedPlatform === platform.id ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-950 dark:text-primary-200' : 'border-gray-300 dark:border-dark-700'" @click="selectPlatform(platform.id)">
            {{ platform.label }}
          </button>
        </div>
      </section>

      <section id="downloads" class="scroll-mt-6 border-t border-gray-200 pt-6 dark:border-dark-700">
        <h2 class="text-lg font-semibold">{{ t('docs.downloads.title') }}</h2>
        <div v-for="download in downloads" :key="download.id" class="mt-5 min-w-0" :data-testid="'download-' + download.id">
          <div class="flex items-center justify-between gap-3">
            <h3 class="text-sm font-semibold">{{ download.title }}</h3>
            <button type="button" :aria-label="t('common.copy')" :title="t('common.copy')" class="shrink-0 rounded p-2 hover:bg-gray-200 dark:hover:bg-dark-700" @click="copyCommand(download.code, download.id)">
              <Icon :name="copied === download.id ? 'check' : 'document'" size="sm" />
            </button>
          </div>
          <p class="mb-2 text-xs leading-5 text-gray-500 dark:text-dark-300">{{ download.description }}</p>
          <pre class="max-w-full overflow-x-auto rounded-lg bg-gray-950 p-4 text-sm text-gray-100"><code>{{ download.code }}</code></pre>
        </div>
      </section>

      <section id="codex-cli" class="scroll-mt-6 space-y-4 border-t border-gray-200 pt-6 dark:border-dark-700">
        <h2 class="text-lg font-semibold">{{ t('docs.setup.title') }}</h2>
        <p class="text-sm text-gray-600 dark:text-dark-300">{{ t('docs.setup.requirements') }}</p>
        <p v-if="currentUserKeyLoading" class="text-sm text-gray-500">{{ t('docs.codex.items.setupCommand.loading') }}</p>
        <p v-else-if="currentUserKey" class="text-sm text-gray-500">{{ t('docs.codex.items.setupCommand.usingKey', { name: currentUserKey.name }) }}</p>
        <template v-else>
          <p class="text-sm text-gray-500 dark:text-dark-300">{{ t(currentUserKeyError ? 'docs.codex.items.setupCommand.error' : isAuthenticated ? 'docs.codex.items.setupCommand.noKey' : 'docs.codex.items.setupCommand.loginRequired') }}</p>
          <label class="block space-y-1 text-sm">
            <span>{{ t('docs.codex.items.setupCommand.manualKeyLabel') }}</span>
            <input v-model.trim="manualApiKey" data-testid="setup-api-key" type="password" spellcheck="false" autocomplete="off" :placeholder="t('docs.codex.items.setupCommand.manualKeyPlaceholder')" class="input w-full font-mono" />
          </label>
          <router-link v-if="isAuthenticated" to="/keys" class="inline-block text-sm text-primary-600 dark:text-primary-300">{{ t('docs.codex.items.setupCommand.createKey') }}</router-link>
        </template>

        <CodexSetupOptions v-model="setupOptions" :windows="selectedPlatform === 'windows'" />
        <p v-if="selectedPlatform === 'linux' && setupOptions.client === 'desktop'" class="text-xs text-amber-700 dark:text-amber-300">{{ t('docs.setup.linuxDesktop') }}</p>
        <pre class="max-w-full overflow-x-auto rounded-lg bg-gray-950 p-4 text-sm text-gray-100" data-testid="setup-command"><code>{{ setupCommand }}</code></pre>
        <button type="button" class="inline-flex items-center gap-2 rounded-md border border-primary-300 px-3 py-2 text-sm text-primary-700 dark:border-primary-700 dark:text-primary-200" @click="copyCommand(setupCommand, 'setup')">
          <Icon :name="copied === 'setup' ? 'check' : 'document'" size="sm" />
          {{ copied === 'setup' ? t('common.copied') : t('common.copy') }}
        </button>
        <p v-if="copyError" role="status" class="text-sm text-red-600">{{ t('docs.setup.copyError') }}</p>
      </section>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore, useAuthStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import CodexSetupOptions from '@/components/keys/CodexSetupOptions.vue'
import { buildCodexSetupCommand, defaultCodexSetupOptions } from '@/utils/codexSetup'
import { keysAPI } from '@/api/keys'
import type { ApiKey } from '@/types'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '')
const siteNameInitial = computed(() => siteName.value.trim().charAt(0).toUpperCase() || 'S')
const isAuthenticated = computed(() => authStore.isAuthenticated)
const dashboardPath = computed(() => (authStore.isAdmin ? '/admin/dashboard' : '/dashboard'))
const currentUserKey = ref<ApiKey | null>(null)
const currentUserKeyLoading = ref(false)
const currentUserKeyError = ref(false)
const manualApiKey = ref('')
const setupOptions = ref(defaultCodexSetupOptions())
type Platform = 'macos' | 'windows' | 'linux'
const selectedPlatform = ref<Platform>(/Win/i.test(navigator.platform) ? 'windows' : /Linux/i.test(navigator.platform) ? 'linux' : 'macos')
if (selectedPlatform.value === 'linux') setupOptions.value.client = 'cli'
const platforms: Array<{ id: Platform; label: string }> = [
  { id: 'macos', label: 'macOS' },
  { id: 'windows', label: 'Windows (PowerShell)' },
  { id: 'linux', label: 'Linux' }
]
function selectPlatform(platform: Platform) {
  selectedPlatform.value = platform
  setupOptions.value = { ...defaultCodexSetupOptions(), client: platform === 'linux' ? 'cli' : 'desktop' }
}
const downloads = computed(() => [
  {
    id: 'macos',
    title: 'macOS',
    description: t('docs.downloads.macos'),
    code: 'curl -fL "https://persistent.oaistatic.com/codex-app-prod/Codex.dmg" -o Codex.dmg\nopen Codex.dmg'
  },
  {
    id: 'windows',
    title: 'Windows (CMD / PowerShell)',
    description: t('docs.downloads.windows'),
    code: 'powershell.exe -NoProfile -Command "try { Invoke-WebRequest -UseBasicParsing -Uri \'https://get.microsoft.com/installer/download/9PLM9XGG6VKS\' -OutFile \'ChatGPT-Setup.exe\' -ErrorAction Stop; exit (Start-Process -FilePath \'./ChatGPT-Setup.exe\' -Wait -PassThru -ErrorAction Stop).ExitCode } catch { exit 1 }"'
  },
  {
    id: 'linux',
    title: 'Linux',
    description: t('docs.downloads.linux'),
    code: 'curl -fL "https://chatgpt.com/codex/install.sh" -o codex-install.sh && sh codex-install.sh'
  }
].filter(download => download.id === selectedPlatform.value))
const setupCommand = computed(() => buildCodexSetupCommand(
  window.location.origin.replace(/\/+$/, ''),
  currentUserKey.value?.key || manualApiKey.value || 'sk-xxxx',
  setupOptions.value,
  selectedPlatform.value === 'windows'
))
const copied = ref('')
const copyError = ref(false)
let copyTimer: ReturnType<typeof setTimeout> | undefined
async function copyCommand(command: string, id: string) {
  copyError.value = false
  try {
    await navigator.clipboard.writeText(command)
    copied.value = id
    clearTimeout(copyTimer)
    copyTimer = setTimeout(() => { copied.value = '' }, 2000)
  } catch {
    copyError.value = true
  }
}
async function loadCurrentUserKey() {
  if (!isAuthenticated.value) return
  currentUserKeyLoading.value = true
  try {
    const response = await keysAPI.list(1, 1, { status: 'active', sort_by: 'created_at', sort_order: 'desc' })
    currentUserKey.value = response.items[0] || null
  } catch {
    currentUserKeyError.value = true
  } finally {
    currentUserKeyLoading.value = false
  }
}
onMounted(() => { void loadCurrentUserKey() })
onUnmounted(() => { clearTimeout(copyTimer) })
</script>
