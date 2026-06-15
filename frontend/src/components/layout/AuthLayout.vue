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
            class="mb-4 inline-flex h-16 w-16 items-center justify-center overflow-hidden rounded-md border-2 border-black bg-[#ffdc58] shadow-[6px_6px_0_#000] dark:border-white dark:shadow-[6px_6px_0_#fff]"
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
      <div class="card-glass rounded-md p-8">
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
  background: linear-gradient(100deg, var(--neo-pink), var(--neo-main) 52%, var(--neo-cyan));
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}

.auth-refraction {
  background:
    linear-gradient(to right, rgba(0, 0, 0, 0.16) 1px, transparent 1px),
    linear-gradient(to bottom, rgba(0, 0, 0, 0.16) 1px, transparent 1px),
    radial-gradient(90% 70% at 50% 50%, rgba(255, 220, 88, 0.24), transparent 64%);
  background-size: 70px 70px, 70px 70px, auto;
}

.glass-sheet {
  position: absolute;
  border: 3px solid var(--liquid-border);
  background: var(--neo-main);
  box-shadow: 12px 12px 0 var(--liquid-border);
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
  background: var(--neo-cyan);
}

.glass-sheet-c {
  left: 50%;
  top: 45%;
  width: 22rem;
  height: 22rem;
  border-radius: 0;
  transform: translate(-50%, -50%) rotate(28deg);
  opacity: 0.5;
  background: var(--neo-pink);
}

:global(.dark) .auth-refraction {
  background:
    linear-gradient(to right, rgba(255, 255, 255, 0.14) 1px, transparent 1px),
    linear-gradient(to bottom, rgba(255, 255, 255, 0.14) 1px, transparent 1px),
    radial-gradient(90% 70% at 50% 50%, rgba(255, 220, 88, 0.16), transparent 64%);
  background-size: 70px 70px, 70px 70px, auto;
}

:global(.dark) .glass-sheet {
  background: var(--neo-main);
}
</style>
