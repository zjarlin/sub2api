<template>
  <div v-if="homeContent" class="min-h-screen">
    <iframe
      v-if="isHomeContentUrl"
      :src="homeContent.trim()"
      class="h-screen w-full border-0"
      allowfullscreen
    ></iframe>
    <div v-else v-html="homeContent"></div>
  </div>

  <div v-else class="terminal-home min-h-screen overflow-hidden bg-[#030507] text-[#f5f7fb]">
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
import { sanitizeUrl } from '@/utils/url'

const { t } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'AI API Gateway Platform')
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')

const isHomeContentUrl = computed(() => {
  const content = homeContent.value.trim()
  return content.startsWith('http://') || content.startsWith('https://')
})

const isDark = ref(document.documentElement.classList.contains('dark'))
const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => (isAdmin.value ? '/admin/dashboard' : '/dashboard'))
const siteNameInitial = computed(() => siteName.value.trim().charAt(0).toUpperCase() || 'S')
const currentYear = computed(() => new Date().getFullYear())
const githubUrl = 'https://github.com/Wei-Shaw/sub2api'

const telemetry = [
  { label: 'ROUTE LATENCY', value: '< 90MS' },
  { label: 'UPSTREAM HEALTH', value: 'SYNCED' },
  { label: 'BILLING MODE', value: 'REALTIME' },
  { label: 'SESSION POLICY', value: 'STICKY' }
]

const featureStrips = computed(() => [
  {
    code: '01 / ROUTER',
    title: t('home.features.unifiedGateway'),
    description: t('home.features.unifiedGatewayDesc')
  },
  {
    code: '02 / POOL',
    title: t('home.features.multiAccount'),
    description: t('home.features.multiAccountDesc')
  },
  {
    code: '03 / METER',
    title: t('home.features.balanceQuota'),
    description: t('home.features.balanceQuotaDesc')
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
  const shouldUseDark = savedTheme === 'light' ? false : true
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
  --home-line: rgba(140, 162, 174, 0.26);
  --home-line-strong: rgba(183, 255, 0, 0.66);
  --home-panel: rgba(5, 9, 12, 0.78);
  --home-orange: #ff8a00;
  --home-lime: #b7ff00;
  --home-cyan: #00c8ff;
  --home-red: #ff344f;
  font-family:
    'IBM Plex Mono',
    'JetBrains Mono',
    'SFMono-Regular',
    ui-monospace,
    monospace;
  letter-spacing: 0;
}

.terminal-home__hero {
  position: relative;
  min-height: 94vh;
  display: grid;
  grid-template-rows: auto 1fr auto;
  isolation: isolate;
  border-bottom: 1px solid var(--home-line);
}

.terminal-home__hero::after {
  content: '';
  position: absolute;
  inset: 0;
  z-index: 1;
  pointer-events: none;
  background:
    linear-gradient(90deg, rgba(3, 5, 7, 0.92) 0%, rgba(3, 5, 7, 0.38) 44%, rgba(3, 5, 7, 0.86) 100%),
    linear-gradient(180deg, rgba(3, 5, 7, 0.18) 0%, rgba(3, 5, 7, 0.34) 58%, #030507 100%);
}

.terminal-home__noise,
.terminal-home__scanline {
  position: absolute;
  inset: 0;
  z-index: 2;
  pointer-events: none;
}

.terminal-home__noise {
  opacity: 0.3;
  background-image:
    linear-gradient(rgba(255, 255, 255, 0.045) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.035) 1px, transparent 1px);
  background-size: 36px 36px;
  mask-image: linear-gradient(to bottom, black, transparent 92%);
}

.terminal-home__scanline {
  opacity: 0.11;
  background: repeating-linear-gradient(180deg, rgba(255, 255, 255, 0.4) 0 1px, transparent 1px 5px);
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
  border-bottom: 1px solid var(--home-line);
  background: rgba(3, 5, 7, 0.62);
  backdrop-filter: blur(10px);
}

.terminal-home__brand {
  display: inline-flex;
  align-items: center;
  min-width: 0;
  gap: 0.75rem;
  color: #f5f7fb;
  text-transform: uppercase;
}

.terminal-home__logo {
  display: inline-flex;
  width: 2.25rem;
  height: 2.25rem;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  border: 1px solid var(--home-line-strong);
  background: rgba(183, 255, 0, 0.12);
  color: var(--home-lime);
  font-weight: 900;
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
  font-size: 0.9rem;
  font-weight: 900;
}

.terminal-home__actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 0.5rem;
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
  border: 1px solid var(--home-line);
  background: rgba(5, 9, 12, 0.76);
  color: #f5f7fb;
  text-transform: uppercase;
  transition:
    border-color 0.16s ease,
    background 0.16s ease,
    transform 0.16s ease,
    box-shadow 0.16s ease;
}

.terminal-home__icon-button {
  width: 2.5rem;
}

.terminal-home__nav-cta {
  padding: 0 0.9rem;
  color: #030507;
  border-color: var(--home-lime);
  background: var(--home-lime);
  font-size: 0.76rem;
  font-weight: 900;
}

.terminal-home__icon-button:hover,
.terminal-home__secondary-cta:hover {
  border-color: var(--home-cyan);
  background: rgba(0, 200, 255, 0.12);
}

.terminal-home__nav-cta:hover,
.terminal-home__primary-cta:hover {
  transform: translateY(-1px);
  box-shadow: 0 0 30px rgba(183, 255, 0, 0.24);
}

.terminal-home__hero-content {
  align-self: center;
  max-width: 62rem;
  padding: clamp(4rem, 10vw, 9rem) clamp(1rem, 5vw, 5rem) clamp(7rem, 11vw, 9rem);
}

.terminal-home__eyebrow {
  display: inline-flex;
  align-items: center;
  gap: 0.65rem;
  margin-bottom: 1.25rem;
  padding: 0.5rem 0.72rem;
  border: 1px solid rgba(183, 255, 0, 0.48);
  background: rgba(183, 255, 0, 0.08);
  color: var(--home-lime);
  font-size: 0.76rem;
  font-weight: 900;
}

.terminal-home__live-dot {
  width: 0.55rem;
  height: 0.55rem;
  background: var(--home-lime);
  box-shadow: 0 0 20px rgba(183, 255, 0, 0.8);
}

.terminal-home__hero-content h1 {
  max-width: 12ch;
  margin: 0;
  color: #fff;
  font-family:
    'Arial Black',
    'Impact',
    'SF Pro Display',
    sans-serif;
  font-size: clamp(4.8rem, 15vw, 13.5rem);
  font-weight: 950;
  line-height: 0.8;
  text-transform: uppercase;
  text-wrap: balance;
  overflow-wrap: anywhere;
  text-shadow:
    0 0 2px rgba(255, 255, 255, 0.8),
    0 0 38px rgba(0, 200, 255, 0.2),
    0 0 76px rgba(255, 138, 0, 0.18);
}

.terminal-home__hero-content p {
  max-width: 46rem;
  margin: 1.35rem 0 0;
  color: rgba(222, 232, 238, 0.82);
  font-size: clamp(1rem, 2vw, 1.35rem);
  line-height: 1.65;
}

.terminal-home__cta-row {
  display: flex;
  flex-wrap: wrap;
  gap: 0.75rem;
  margin-top: 2rem;
}

.terminal-home__primary-cta,
.terminal-home__secondary-cta {
  padding: 0.9rem 1.2rem;
  font-size: 0.86rem;
  font-weight: 950;
}

.terminal-home__primary-cta {
  color: #030507;
  border-color: var(--home-orange);
  background: linear-gradient(90deg, var(--home-orange), var(--home-lime));
}

.terminal-home__secondary-cta {
  color: #f5f7fb;
}

.terminal-home__telemetry {
  position: absolute;
  right: clamp(1rem, 3vw, 2.5rem);
  bottom: 5.5rem;
  width: min(23rem, calc(100vw - 2rem));
  border: 1px solid var(--home-line);
  background: rgba(3, 5, 7, 0.72);
  backdrop-filter: blur(12px);
}

.terminal-home__telemetry-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 0.72rem 0.9rem;
  border-bottom: 1px solid var(--home-line);
  font-size: 0.74rem;
}

.terminal-home__telemetry-row:last-child {
  border-bottom: 0;
}

.terminal-home__telemetry-row span {
  color: rgba(190, 204, 212, 0.72);
}

.terminal-home__telemetry-row strong {
  color: var(--home-lime);
}

.terminal-home__hero-bottom {
  padding: 0.85rem clamp(1rem, 3vw, 2.5rem);
  border-top: 1px solid var(--home-line);
  color: rgba(190, 204, 212, 0.7);
  font-size: 0.74rem;
  background: rgba(3, 5, 7, 0.76);
}

.terminal-home__strips {
  position: relative;
  z-index: 1;
  padding: clamp(1rem, 3vw, 2.5rem);
  background:
    linear-gradient(rgba(255, 255, 255, 0.035) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.026) 1px, transparent 1px),
    #030507;
  background-size: 44px 44px;
}

.terminal-home__strip-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  border-top: 1px solid var(--home-line);
  border-left: 1px solid var(--home-line);
}

.terminal-home__strip {
  min-height: 16rem;
  padding: 1.2rem;
  border-right: 1px solid var(--home-line);
  border-bottom: 1px solid var(--home-line);
  background: rgba(5, 9, 12, 0.78);
}

.terminal-home__strip-code {
  margin-bottom: 3rem;
  color: var(--home-orange);
  font-size: 0.72rem;
  font-weight: 950;
}

.terminal-home__strip h2 {
  margin: 0;
  color: #fff;
  font-size: clamp(1.35rem, 2vw, 2.1rem);
  font-weight: 950;
  text-transform: uppercase;
}

.terminal-home__strip p {
  margin: 0.8rem 0 0;
  color: rgba(214, 224, 230, 0.72);
  line-height: 1.65;
}

.terminal-home__provider-rail {
  display: grid;
  grid-template-columns: minmax(0, 0.7fr) minmax(0, 1.3fr);
  border-right: 1px solid var(--home-line);
  border-bottom: 1px solid var(--home-line);
  border-left: 1px solid var(--home-line);
}

.terminal-home__provider-heading,
.terminal-home__provider-list {
  padding: 1rem;
}

.terminal-home__provider-heading {
  border-right: 1px solid var(--home-line);
}

.terminal-home__provider-heading span {
  display: block;
  color: var(--home-cyan);
  font-size: 0.74rem;
  font-weight: 900;
  text-transform: uppercase;
}

.terminal-home__provider-heading strong {
  display: block;
  margin-top: 0.3rem;
  color: rgba(245, 247, 251, 0.82);
  font-size: 0.9rem;
}

.terminal-home__provider-list {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}

.terminal-home__provider-list span {
  border: 1px solid var(--home-line);
  padding: 0.55rem 0.75rem;
  color: rgba(245, 247, 251, 0.86);
  background: rgba(255, 255, 255, 0.035);
  font-size: 0.78rem;
  font-weight: 900;
  text-transform: uppercase;
}

.terminal-home__footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 1rem clamp(1rem, 3vw, 2.5rem);
  border-top: 1px solid var(--home-line);
  background: #030507;
  color: rgba(190, 204, 212, 0.7);
  font-size: 0.78rem;
}

.terminal-home__footer div {
  display: flex;
  gap: 1rem;
}

.terminal-home__footer a {
  color: rgba(245, 247, 251, 0.82);
}

.terminal-home__footer a:hover {
  color: var(--home-lime);
}

@media (max-width: 1024px) {
  .terminal-home__telemetry {
    position: relative;
    right: auto;
    bottom: auto;
    margin: 0 1rem 1rem;
    width: auto;
  }

  .terminal-home__strip-grid,
  .terminal-home__provider-rail {
    grid-template-columns: 1fr;
  }

  .terminal-home__provider-heading {
    border-right: 0;
    border-bottom: 1px solid var(--home-line);
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
    padding-top: 3.25rem;
    width: 100%;
    max-width: 100vw;
    box-sizing: border-box;
  }

  .terminal-home__hero-content h1 {
    max-width: 100%;
    font-size: clamp(3rem, 17vw, 5rem);
    line-height: 0.88;
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
