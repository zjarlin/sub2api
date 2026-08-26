<template>
  <div class="auth-shell relative flex min-h-screen items-center justify-center overflow-hidden p-4">
    <div class="auth-grid absolute inset-0"></div>

    <!-- Decorative Elements -->
    <div class="pointer-events-none absolute inset-0 overflow-hidden">
      <div class="auth-sheet auth-sheet-primary"></div>
      <div class="auth-sheet auth-sheet-cyan"></div>
      <div class="auth-sheet auth-sheet-pink"></div>
    </div>

    <!-- Content Container -->
    <div class="relative z-10 w-full max-w-md">
      <!-- Logo/Brand -->
      <div class="mb-8 text-center">
        <!-- Custom Logo or Default Logo -->
        <template v-if="settingsLoaded">
          <div
            class="mb-4 inline-flex h-16 w-16 items-center justify-center overflow-hidden rounded-md border-2 border-black bg-[#ffdc58] shadow-[6px_6px_0_#000] dark:border-white dark:shadow-[6px_6px_0_#fff]"
          >
            <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
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
      <div class="card-glass rounded-md border-2 border-black p-8 shadow-[8px_8px_0_#000] dark:border-white dark:shadow-[8px_8px_0_#fff]">
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

const siteName = computed(() => appStore.siteName || '++0 的 API')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => appStore.cachedPublicSettings?.site_subtitle || '一个接口，接上主流 AI 模型和上游账号池')
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)

const currentYear = computed(() => new Date().getFullYear())

onMounted(() => {
  appStore.fetchPublicSettings()
})
</script>

<style scoped>
.text-gradient {
  color: var(--liquid-text);
}

.auth-shell {
  background: var(--neo-page);
}

.auth-grid {
  background: var(--neo-page);
}

.auth-sheet {
  position: absolute;
  border: 3px solid var(--liquid-border);
  box-shadow: 12px 12px 0 var(--liquid-border);
  clip-path: polygon(8% 0, 100% 0, 92% 100%, 0 100%);
}

.auth-sheet-primary {
  right: -7rem;
  top: -10rem;
  width: 26rem;
  height: 42rem;
  background: var(--neo-main);
  transform: rotate(-12deg);
}

.auth-sheet-cyan {
  bottom: -14rem;
  left: -8rem;
  width: 30rem;
  height: 36rem;
  background: var(--neo-cyan);
  transform: rotate(17deg);
}

.auth-sheet-pink {
  left: 50%;
  top: 45%;
  width: 22rem;
  height: 22rem;
  background: var(--neo-pink);
  opacity: 0.5;
  transform: translate(-50%, -50%) rotate(28deg);
}

:global(.dark) .auth-shell {
  background: #080808;
}

:global(.dark) .auth-grid {
  background: #080808;
}

:global(.dark) .auth-sheet {
  border-color: #fff;
  box-shadow: 12px 12px 0 #fff;
}
</style>
