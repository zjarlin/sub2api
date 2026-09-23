<template>
  <div v-if="hasHomeContent" class="min-h-screen">
    <iframe
      v-if="isHomeContentUrl"
      :src="homeContent.trim()"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <div v-else v-html="homeContent"></div>
  </div>

  <div
    v-else-if="compactHomeEnabled"
    data-testid="compact-home"
    class="flex min-h-screen flex-col bg-gray-50 text-gray-900 dark:bg-dark-950 dark:text-white"
  >
    <header class="border-b border-gray-200 px-4 py-4 sm:px-6 dark:border-dark-800">
      <nav class="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-3 sm:gap-4">
        <div class="flex min-w-0 flex-1 items-center gap-3">
          <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-9 w-9 shrink-0 rounded-lg object-contain" />
          <span class="min-w-0 truncate text-base font-semibold">{{ siteName }}</span>
        </div>
        <div class="flex max-w-full shrink-0 flex-wrap items-center justify-end gap-2">
          <LocaleSwitcher />
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>
          <router-link
            v-if="showModelPlazaEntry"
            to="/model-plaza"
            class="flex h-10 shrink-0 items-center gap-1.5 rounded-lg px-2.5 text-sm font-medium text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white"
            :title="t('nav.modelPlaza')"
          >
            <Icon name="grid" size="md" />
            <span class="hidden sm:inline">{{ t('nav.modelPlaza') }}</span>
          </router-link>
          <button
            type="button"
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex min-h-10 shrink-0 items-center justify-center rounded-lg bg-gray-900 px-4 py-2 text-sm font-medium text-white hover:bg-gray-800 dark:bg-white dark:text-gray-900 dark:hover:bg-gray-200"
          >
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>
    <main class="flex min-w-0 flex-1 items-center justify-center px-4 py-16 sm:px-6">
      <div class="min-w-0 max-w-2xl text-center">
        <img :src="siteLogo || '/logo.svg'" alt="Logo" class="mx-auto mb-6 h-20 w-20 rounded-2xl object-contain" />
        <h1 class="[overflow-wrap:anywhere] text-3xl font-bold md:text-4xl">{{ siteName }}</h1>
        <p class="mt-4 whitespace-pre-wrap [overflow-wrap:anywhere] text-base text-gray-600 dark:text-dark-300">{{ siteSubtitle }}</p>
        <router-link
          :to="isAuthenticated ? dashboardPath : '/login'"
          class="mt-8 inline-flex min-h-10 items-center justify-center rounded-lg bg-primary-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-primary-700"
        >
          {{ isAuthenticated ? t('home.goToDashboard') : t('home.login') }}
        </router-link>
      </div>
    </main>
    <footer class="min-w-0 border-t border-gray-200 px-4 py-5 text-center text-sm text-gray-500 [overflow-wrap:anywhere] sm:px-6 dark:border-dark-800 dark:text-dark-400">
      &copy; {{ currentYear }} {{ siteName }}
    </footer>
  </div>

  <div v-else class="terminal-container terminal-home min-h-screen overflow-hidden bg-[#030507] text-[#f5f7fb]">
    <section class="terminal-home__hero">
      <HomeGatewayScene />

      <div class="terminal-home__noise" aria-hidden="true"></div>
      <div class="terminal-home__scanline" aria-hidden="true"></div>

      <header class="terminal-home__nav">
        <router-link to="/home" class="terminal-home__brand" aria-label="Home">
          <span class="terminal-home__logo">
            <img v-if="siteLogo" :src="siteLogo" alt="" />
            <span v-else>{{ siteNameInitial }}</span>
          </span>
          <span class="terminal-home__brand-text">{{ siteName }}</span>
        </router-link>

        <div class="terminal-home__actions">
          <LocaleSwitcher />
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="terminal-home__icon-button"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>
          <router-link
            v-if="showModelPlazaEntry"
            to="/model-plaza"
            class="terminal-home__icon-button"
            :title="t('nav.modelPlaza')"
          >
            <Icon name="grid" size="md" />
          </router-link>
          <button
            type="button"
            class="terminal-home__icon-button"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link :to="isAuthenticated ? dashboardPath : '/login'" class="terminal-home__nav-cta">
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </header>

      <main class="terminal-home__hero-content">
        <div class="terminal-home__eyebrow">
          <span class="terminal-home__live-dot"></span>
          API GATEWAY REACTOR / ONLINE
        </div>
        <h1>{{ siteName }}</h1>
        <p>{{ siteSubtitle }}</p>
        <div class="terminal-home__cta-row">
          <router-link :to="isAuthenticated ? dashboardPath : '/login'" class="terminal-home__primary-cta">
            {{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}
            <Icon name="arrowRight" size="md" />
          </router-link>
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="terminal-home__secondary-cta"
          >
            {{ t('home.docs') }}
          </a>
        </div>
      </main>

      <aside class="terminal-home__telemetry" aria-label="Gateway telemetry">
        <div v-for="item in telemetry" :key="item.label" class="terminal-home__telemetry-row">
          <span>{{ item.label }}</span>
          <strong>{{ item.value }}</strong>
        </div>
      </aside>

      <div class="terminal-home__hero-bottom">
        <span>SCROLL / SYSTEM CAPABILITIES</span>
      </div>
    </section>

    <section class="terminal-home__strips" aria-label="Platform capabilities">
      <div class="terminal-home__strip-grid">
        <article v-for="item in featureStrips" :key="item.title" class="terminal-home__strip">
          <div class="terminal-home__strip-code">{{ item.code }}</div>
          <h2>{{ item.title }}</h2>
          <p>{{ item.description }}</p>
        </article>
      </div>

      <div class="terminal-home__provider-rail">
        <div class="terminal-home__provider-heading">
          <span>{{ t('home.providers.title') }}</span>
          <strong>{{ t('home.providers.description') }}</strong>
        </div>
        <div class="terminal-home__provider-list">
          <span v-for="provider in providers" :key="provider">{{ provider }}</span>
        </div>
      </div>
    </section>

    <footer class="terminal-home__footer">
      <span>&copy; {{ currentYear }} {{ siteName }}</span>
      <div>
        <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer">{{ t('home.docs') }}</a>
        <a :href="githubUrl" target="_blank" rel="noopener noreferrer">GitHub</a>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore, useAuthStore } from '@/stores'
import HomeGatewayScene from '@/components/home/HomeGatewayScene.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import { resolveDocsUrl } from '@/utils/docs'
import { sanitizeUrl } from '@/utils/url'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'

const { t } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || '++0 的 API')
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || '一个接口，接上主流 AI 模型和上游账号池')
const docUrl = computed(() => sanitizeUrl(resolveDocsUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl), { allowRelative: true }))
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const hasHomeContent = computed(() => homeContent.value.trim().length > 0)
const compactHomeEnabled = computed(() => appStore.cachedPublicSettings?.compact_home_enabled === true)
const modelPlazaEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPlaza))
const homeFeatureText = (key: keyof NonNullable<typeof appStore.cachedPublicSettings>, fallback: string) => {
  const value = appStore.cachedPublicSettings?.[key]
  return typeof value === 'string' && value.trim() ? value.trim() : fallback
}

const isHomeContentUrl = computed(() => {
  const content = homeContent.value.trim()
  return content.startsWith('http://') || content.startsWith('https://')
})

const isDark = ref(document.documentElement.classList.contains('dark'))
const isAuthenticated = computed(() => authStore.isAuthenticated)
const modelPlazaRequiresAuth = computed(() => appStore.cachedPublicSettings?.model_plaza_require_auth === true)
const showModelPlazaEntry = computed(
  () => modelPlazaEnabled.value && (isAuthenticated.value || !modelPlazaRequiresAuth.value)
)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => (isAdmin.value ? '/admin/dashboard' : '/dashboard'))
const siteNameInitial = computed(() => siteName.value.trim().charAt(0).toUpperCase() || 'S')
const currentYear = computed(() => new Date().getFullYear())
const githubUrl = 'https://github.com/zjarlin/sub2api'

const telemetry = [
  { label: 'ROUTE LATENCY', value: '< 90MS' },
  { label: 'UPSTREAM HEALTH', value: 'SYNCED' },
  { label: 'BILLING MODE', value: 'REALTIME' },
  { label: 'SESSION POLICY', value: 'STICKY' }
]

const featureStrips = computed(() => [
  {
    code: '01 / ROUTER',
    title: homeFeatureText('home_feature_1_title', t('home.features.unifiedGateway')),
    description: homeFeatureText('home_feature_1_description', t('home.features.unifiedGatewayDesc'))
  },
  {
    code: '02 / POOL',
    title: homeFeatureText('home_feature_2_title', t('home.features.multiAccount')),
    description: homeFeatureText('home_feature_2_description', t('home.features.multiAccountDesc'))
  },
  {
    code: '03 / METER',
    title: homeFeatureText('home_feature_3_title', t('home.features.balanceQuota')),
    description: homeFeatureText('home_feature_3_description', t('home.features.balanceQuotaDesc'))
  }
])

const providers = computed(() => [
  t('home.providers.claude'),
  'GPT',
  t('home.providers.gemini'),
  t('home.providers.antigravity'),
  t('home.providers.more')
])

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

function syncThemeState() {
  const savedTheme = localStorage.getItem('theme')
  const shouldUseDark = savedTheme === 'dark'
  isDark.value = shouldUseDark
  document.documentElement.classList.toggle('dark', shouldUseDark)
}

onMounted(() => {
  syncThemeState()
  authStore.checkAuth()
  if (!appStore.publicSettingsLoaded) {
    appStore.fetchPublicSettings()
  }
})
</script>

<style scoped>
.terminal-home {
  --home-border: #000000;
  --home-bg: #fff3bf;
  --home-surface: #fffdf2;
  --home-main: #ffdc58;
  --home-pink: #ff5fa2;
  --home-cyan: #35d9ff;
  --home-green: #8fff6a;
  --home-blue: #7084ff;
  --home-ink: #050505;
  --home-shadow: 7px 7px 0 #000000;
  --home-shadow-lg: 12px 12px 0 #000000;
  --home-radius: 6px;
  color: var(--home-ink);
  background: var(--home-bg);
  font-family:
    'Arial Black',
    'PingFang SC',
    'Microsoft YaHei',
    system-ui,
    sans-serif;
  letter-spacing: 0;
}

.terminal-home__hero {
  position: relative;
  min-height: 88vh;
  display: grid;
  grid-template-rows: auto 1fr auto;
  isolation: isolate;
  overflow: hidden;
  border-bottom: 4px solid var(--home-border);
  background: var(--home-bg);
}

.terminal-home__noise,
.terminal-home__scanline {
  position: absolute;
  inset: 0;
  z-index: 2;
  pointer-events: none;
}

.terminal-home__noise {
  display: none;
}

.terminal-home__scanline {
  display: none;
}

.terminal-home__nav,
.terminal-home__hero-content,
.terminal-home__telemetry,
.terminal-home__hero-bottom {
  position: relative;
  z-index: 3;
}

.terminal-home__nav {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 1rem clamp(1rem, 3vw, 2.5rem);
  border-bottom: 4px solid var(--home-border);
  background: var(--home-surface);
}

.terminal-home__brand {
  display: inline-flex;
  align-items: center;
  min-width: 0;
  gap: 0.75rem;
  color: var(--home-ink);
  text-transform: uppercase;
}

.terminal-home__logo {
  display: inline-flex;
  width: 2.35rem;
  height: 2.35rem;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  border: 2px solid var(--home-border);
  border-radius: var(--home-radius);
  background: var(--home-main);
  color: var(--home-ink);
  box-shadow: 4px 4px 0 var(--home-border);
  font-weight: 950;
}

.terminal-home__logo img {
  width: 100%;
  height: 100%;
  object-fit: contain;
}

.terminal-home__brand-text {
  max-width: min(48vw, 24rem);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 1rem;
  font-weight: 950;
}

.terminal-home__actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 0.65rem;
  min-width: 0;
}

.terminal-home__icon-button,
.terminal-home__nav-cta,
.terminal-home__primary-cta,
.terminal-home__secondary-cta {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  min-height: 2.5rem;
  border: 2px solid var(--home-border);
  border-radius: var(--home-radius);
  background: var(--home-surface);
  color: var(--home-ink);
  box-shadow: 4px 4px 0 var(--home-border);
  text-transform: uppercase;
  transition:
    background 0.16s ease,
    transform 0.16s ease,
    box-shadow 0.16s ease;
}

.terminal-home__icon-button {
  width: 2.5rem;
}

.terminal-home__nav-cta {
  padding: 0 0.95rem;
  background: var(--home-main);
  font-size: 0.78rem;
  font-weight: 950;
}

.terminal-home__icon-button:hover,
.terminal-home__nav-cta:hover,
.terminal-home__primary-cta:hover,
.terminal-home__secondary-cta:hover {
  transform: translate(4px, 4px);
  box-shadow: none;
}

.terminal-home__icon-button:hover,
.terminal-home__secondary-cta:hover {
  background: var(--home-cyan);
}

.terminal-home__hero-content {
  align-self: center;
  max-width: 68rem;
  padding: clamp(2.5rem, 6vw, 5rem) clamp(1rem, 5vw, 5rem) clamp(5rem, 8vw, 6.5rem);
}

.terminal-home__eyebrow {
  display: inline-flex;
  align-items: center;
  gap: 0.65rem;
  margin-bottom: 1.35rem;
  padding: 0.58rem 0.8rem;
  border: 2px solid var(--home-border);
  border-radius: var(--home-radius);
  background: var(--home-green);
  color: var(--home-ink);
  box-shadow: 5px 5px 0 var(--home-border);
  font-size: 0.78rem;
  font-weight: 950;
  text-transform: uppercase;
}

.terminal-home__live-dot {
  width: 0.62rem;
  height: 0.62rem;
  border: 2px solid var(--home-border);
  border-radius: 999px;
  background: var(--home-pink);
}

.terminal-home__hero-content h1 {
  max-width: 11ch;
  margin: 0;
  color: var(--home-ink);
  font-size: 8.5rem;
  font-weight: 950;
  line-height: 0.82;
  text-transform: uppercase;
  text-wrap: balance;
  overflow-wrap: anywhere;
  text-shadow:
    5px 5px 0 var(--home-main),
    10px 10px 0 var(--home-pink),
    15px 15px 0 var(--home-border);
}

.terminal-home__hero-content p {
  width: min(46rem, 100%);
  margin: 2rem 0 0;
  border: 2px solid var(--home-border);
  border-radius: var(--home-radius);
  padding: 0.95rem 1.1rem;
  background: var(--home-surface);
  box-shadow: var(--home-shadow);
  color: var(--home-ink);
  font-family:
    'IBM Plex Mono',
    'SFMono-Regular',
    ui-monospace,
    monospace;
  font-size: 1.1rem;
  font-weight: 800;
  line-height: 1.55;
}

.terminal-home__cta-row {
  display: flex;
  flex-wrap: wrap;
  gap: 0.95rem;
  margin-top: 2rem;
}

.terminal-home__primary-cta,
.terminal-home__secondary-cta {
  padding: 0.95rem 1.2rem;
  font-size: 0.9rem;
  font-weight: 950;
}

.terminal-home__primary-cta {
  background: var(--home-main);
}

.terminal-home__secondary-cta {
  background: var(--home-pink);
}

.terminal-home__telemetry {
  position: absolute;
  right: clamp(1rem, 3vw, 2.5rem);
  bottom: 4.7rem;
  width: min(23rem, calc(100vw - 2rem));
  border: 2px solid var(--home-border);
  border-radius: var(--home-radius);
  background: var(--home-surface);
  box-shadow: var(--home-shadow-lg);
  transform: rotate(1.5deg);
}

.terminal-home__telemetry-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.76rem 0.92rem;
  border-bottom: 2px solid var(--home-border);
  font-family:
    'IBM Plex Mono',
    'SFMono-Regular',
    ui-monospace,
    monospace;
  font-size: 0.75rem;
  font-weight: 900;
}

.terminal-home__telemetry-row:nth-child(1) {
  background: var(--home-cyan);
}

.terminal-home__telemetry-row:nth-child(2) {
  background: var(--home-main);
}

.terminal-home__telemetry-row:nth-child(3) {
  background: var(--home-green);
}

.terminal-home__telemetry-row:nth-child(4) {
  background: var(--home-pink);
}

.terminal-home__telemetry-row:last-child {
  border-bottom: 0;
}

.terminal-home__telemetry-row span,
.terminal-home__telemetry-row strong {
  color: var(--home-ink);
}

.terminal-home__hero-bottom {
  padding: 0.9rem clamp(1rem, 3vw, 2.5rem);
  border-top: 4px solid var(--home-border);
  background: var(--home-main);
  color: var(--home-ink);
  font-family:
    'IBM Plex Mono',
    'SFMono-Regular',
    ui-monospace,
    monospace;
  font-size: 0.75rem;
  font-weight: 950;
}

.terminal-home__strips {
  position: relative;
  z-index: 1;
  padding: clamp(1.25rem, 3vw, 2.75rem);
  background: #ffffff;
}

.terminal-home__strip-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 1rem;
}

.terminal-home__strip {
  min-height: 16rem;
  padding: 1.2rem;
  border: 2px solid var(--home-border);
  border-radius: var(--home-radius);
  background: var(--home-surface);
  box-shadow: var(--home-shadow);
  transition:
    transform 0.16s ease,
    box-shadow 0.16s ease;
}

.terminal-home__strip:nth-child(1) {
  background: var(--home-main);
}

.terminal-home__strip:nth-child(2) {
  background: var(--home-cyan);
}

.terminal-home__strip:nth-child(3) {
  background: var(--home-pink);
}

.terminal-home__strip:hover {
  transform: translate(6px, 6px);
  box-shadow: none;
}

.terminal-home__strip-code {
  display: inline-flex;
  margin-bottom: 3rem;
  border: 2px solid var(--home-border);
  border-radius: var(--home-radius);
  padding: 0.35rem 0.55rem;
  background: var(--home-surface);
  color: var(--home-ink);
  font-family:
    'IBM Plex Mono',
    'SFMono-Regular',
    ui-monospace,
    monospace;
  font-size: 0.72rem;
  font-weight: 950;
}

.terminal-home__strip h2 {
  margin: 0;
  color: var(--home-ink);
  font-size: 1.8rem;
  font-weight: 950;
  line-height: 1.05;
  text-transform: uppercase;
}

.terminal-home__strip p {
  margin: 0.8rem 0 0;
  color: var(--home-ink);
  font-family:
    'IBM Plex Mono',
    'SFMono-Regular',
    ui-monospace,
    monospace;
  font-size: 0.92rem;
  font-weight: 800;
  line-height: 1.6;
}

.terminal-home__provider-rail {
  display: grid;
  grid-template-columns: minmax(0, 0.7fr) minmax(0, 1.3fr);
  gap: 1rem;
  margin-top: 1.25rem;
}

.terminal-home__provider-heading,
.terminal-home__provider-list {
  border: 2px solid var(--home-border);
  border-radius: var(--home-radius);
  background: var(--home-surface);
  box-shadow: var(--home-shadow);
  padding: 1rem;
}

.terminal-home__provider-heading {
  background: var(--home-green);
}

.terminal-home__provider-heading span {
  display: block;
  color: var(--home-ink);
  font-family:
    'IBM Plex Mono',
    'SFMono-Regular',
    ui-monospace,
    monospace;
  font-size: 0.76rem;
  font-weight: 950;
  text-transform: uppercase;
}

.terminal-home__provider-heading strong {
  display: block;
  margin-top: 0.45rem;
  color: var(--home-ink);
  font-size: 1rem;
  font-weight: 950;
}

.terminal-home__provider-list {
  display: flex;
  flex-wrap: wrap;
  gap: 0.65rem;
}

.terminal-home__provider-list span {
  border: 2px solid var(--home-border);
  border-radius: var(--home-radius);
  padding: 0.55rem 0.75rem;
  color: var(--home-ink);
  background: var(--home-main);
  box-shadow: 3px 3px 0 var(--home-border);
  font-size: 0.8rem;
  font-weight: 950;
  text-transform: uppercase;
}

.terminal-home__provider-list span:nth-child(2n) {
  background: var(--home-cyan);
}

.terminal-home__provider-list span:nth-child(3n) {
  background: var(--home-pink);
}

.terminal-home__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 1rem clamp(1rem, 3vw, 2.5rem);
  border-top: 4px solid var(--home-border);
  background: var(--home-surface);
  color: var(--home-ink);
  font-family:
    'IBM Plex Mono',
    'SFMono-Regular',
    ui-monospace,
    monospace;
  font-size: 0.82rem;
  font-weight: 900;
}

.terminal-home__footer div {
  display: flex;
  gap: 1rem;
}

.terminal-home__footer a {
  color: var(--home-ink);
  text-decoration: underline;
  text-decoration-thickness: 2px;
  text-underline-offset: 4px;
}

.terminal-home__footer a:hover {
  background: var(--home-main);
}

@media (max-width: 1024px) {
  .terminal-home__hero-content h1 {
    font-size: 5.5rem;
  }

  .terminal-home__telemetry {
    position: relative;
    right: auto;
    bottom: auto;
    margin: 0 1rem 1.25rem;
    width: auto;
    transform: none;
  }

  .terminal-home__strip-grid,
  .terminal-home__provider-rail {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 720px) {
  .terminal-home__nav {
    align-items: flex-start;
    flex-direction: column;
  }

  .terminal-home__actions {
    width: 100%;
    justify-content: flex-start;
    flex-wrap: wrap;
  }

  .terminal-home__hero {
    min-height: 100vh;
  }

  .terminal-home__hero-content {
    padding-top: 3.2rem;
    width: 100%;
    max-width: 100vw;
    box-sizing: border-box;
  }

  .terminal-home__hero-content h1 {
    max-width: 100%;
    font-size: 3.2rem;
    line-height: 0.9;
    text-shadow:
      3px 3px 0 var(--home-main),
      6px 6px 0 var(--home-pink),
      9px 9px 0 var(--home-border);
  }

  .terminal-home__primary-cta,
  .terminal-home__secondary-cta {
    width: min(100%, 22rem);
  }

  .terminal-home__telemetry {
    width: calc(100% - 2rem);
    max-width: calc(100% - 2rem);
    box-sizing: border-box;
  }

  .terminal-home__footer {
    align-items: flex-start;
    flex-direction: column;
  }
}

@media (prefers-reduced-motion: reduce) {
  .terminal-home *,
  .terminal-home *::before,
  .terminal-home *::after {
    transition-duration: 1ms !important;
    animation-duration: 1ms !important;
  }
}
</style>
