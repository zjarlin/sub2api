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
  background: linear-gradient(100deg, #ff5fa2, #ffdc58 52%, #35d9ff);
  background-clip: text;
  color: transparent;
  -webkit-background-clip: text;
}

.auth-shell {
  background: #fff3bf;
}

.auth-grid {
  background-image:
    linear-gradient(to right, rgb(0 0 0 / 16%) 1px, transparent 1px),
    linear-gradient(to bottom, rgb(0 0 0 / 16%) 1px, transparent 1px);
  background-size: 70px 70px;
}

.auth-sheet {
  position: absolute;
  border: 3px solid #000;
  box-shadow: 12px 12px 0 #000;
  clip-path: polygon(8% 0, 100% 0, 92% 100%, 0 100%);
}

.auth-sheet-primary {
  right: -7rem;
  top: -10rem;
  width: 26rem;
  height: 42rem;
  background: #ffdc58;
  transform: rotate(-12deg);
}

.auth-sheet-cyan {
  bottom: -14rem;
  left: -8rem;
  width: 30rem;
  height: 36rem;
  background: #35d9ff;
  transform: rotate(17deg);
}

.auth-sheet-pink {
  left: 50%;
  top: 45%;
  width: 22rem;
  height: 22rem;
  background: #ff5fa2;
  opacity: 0.5;
  transform: translate(-50%, -50%) rotate(28deg);
}

:global(.dark) .auth-shell {
  background: #080808;
}

:global(.dark) .auth-grid {
  background-image:
    linear-gradient(to right, rgb(255 255 255 / 14%) 1px, transparent 1px),
    linear-gradient(to bottom, rgb(255 255 255 / 14%) 1px, transparent 1px);
}

:global(.dark) .auth-sheet {
  border-color: #fff;
  box-shadow: 12px 12px 0 #fff;
}
</style>
