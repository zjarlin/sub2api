<template>
  <div class="liquid-page relative flex min-h-screen items-center justify-center overflow-hidden p-4">
    <div class="auth-refraction pointer-events-none absolute inset-0"></div>
    <div class="pointer-events-none absolute inset-0 overflow-hidden">
      <div class="glass-sheet glass-sheet-a"></div>
      <div class="glass-sheet glass-sheet-b"></div>
      <div class="glass-sheet glass-sheet-c"></div>
    </div>

    <!-- Content Container -->
    <div class="relative z-10 w-full max-w-md">
      <!-- Logo/Brand -->
      <div class="mb-8 text-center">
        <!-- Custom Logo or Default Logo -->
        <template v-if="settingsLoaded">
          <div
            class="mb-4 inline-flex h-16 w-16 items-center justify-center overflow-hidden rounded-2xl shadow-lg shadow-primary-500/30"
          >
            <img :src="siteLogo || '/logo.png'" alt="Logo" class="h-full w-full object-contain" />
          </div>
          <h1 class="text-gradient mb-2 text-3xl font-bold">
            {{ siteName }}
          </h1>
          <p class="text-sm text-gray-500 dark:text-dark-400">
            {{ siteSubtitle }}
          </p>
        </template>
      </div>

      <!-- Card Container -->
      <div class="card-glass rounded-2xl p-8">
        <slot />
      </div>

      <!-- Footer Links -->
      <div class="mt-6 text-center text-sm">
        <slot name="footer" />
      </div>

      <!-- Copyright -->
      <div class="mt-8 text-center text-xs text-gray-400 dark:text-dark-500">
        &copy; {{ currentYear }} {{ siteName }}. All rights reserved.
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'

const appStore = useAppStore()

const siteName = computed(() => appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || 'Subscription to API Conversion Platform')
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)

const currentYear = computed(() => new Date().getFullYear())

onMounted(() => {
  appStore.fetchPublicSettings()
})
</script>

<style scoped>
.text-gradient {
  background: linear-gradient(100deg, var(--product-blue), var(--product-magenta) 52%, var(--product-orange));
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}

.auth-refraction {
  background:
    linear-gradient(120deg, transparent 0 16%, rgba(0, 95, 255, 0.13) 18%, transparent 30%),
    linear-gradient(250deg, transparent 0 50%, rgba(255, 51, 102, 0.11) 54%, transparent 66%),
    radial-gradient(90% 70% at 50% 50%, rgba(255, 138, 0, 0.12), transparent 64%);
}

.glass-sheet {
  position: absolute;
  border: 1px solid rgba(255, 255, 255, 0.18);
  background: linear-gradient(135deg, rgba(0, 95, 255, 0.92), rgba(255, 51, 102, 0.86) 56%, rgba(255, 138, 0, 0.84));
  box-shadow: 0 26px 70px rgba(17, 19, 24, 0.18);
  clip-path: polygon(8% 0, 100% 0, 92% 100%, 0 100%);
  transform: rotate(-12deg);
}

.glass-sheet-a {
  top: -10rem;
  right: -7rem;
  width: 26rem;
  height: 42rem;
  border-radius: 0;
}

.glass-sheet-b {
  bottom: -14rem;
  left: -8rem;
  width: 30rem;
  height: 36rem;
  border-radius: 0;
  transform: rotate(17deg);
}

.glass-sheet-c {
  left: 50%;
  top: 45%;
  width: 22rem;
  height: 22rem;
  border-radius: 0;
  transform: translate(-50%, -50%) rotate(28deg);
  opacity: 0.5;
  background: linear-gradient(135deg, rgba(0, 200, 255, 0.88), rgba(183, 255, 0, 0.62));
}

:global(.dark) .auth-refraction {
  background:
    linear-gradient(120deg, transparent 0 16%, rgba(0, 95, 255, 0.16) 18%, transparent 30%),
    linear-gradient(250deg, transparent 0 50%, rgba(255, 51, 102, 0.13) 54%, transparent 66%),
    radial-gradient(90% 70% at 50% 50%, rgba(255, 138, 0, 0.12), transparent 64%);
}

:global(.dark) .glass-sheet {
  background: linear-gradient(135deg, rgba(0, 95, 255, 0.92), rgba(255, 51, 102, 0.86) 56%, rgba(255, 138, 0, 0.84));
}
</style>
