<template>
  <div class="liquid-page min-h-screen">
    <!-- Background Decoration -->
    <div class="liquid-field pointer-events-none fixed inset-0"></div>

    <!-- Sidebar -->
    <AppSidebar />

    <!-- Main Content Area -->
    <div
      class="relative min-h-screen transition-all duration-300"
      :class="[sidebarCollapsed ? 'lg:ml-[72px]' : 'lg:ml-64']"
    >
      <!-- Header -->
      <AppHeader />

      <!-- Main Content -->
      <main class="relative p-4 md:p-6 lg:p-8">
        <slot />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import '@/styles/onboarding.css'
import { computed, onMounted } from 'vue'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useOnboardingTour } from '@/composables/useOnboardingTour'
import { useOnboardingStore } from '@/stores/onboarding'
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'

const appStore = useAppStore()
const authStore = useAuthStore()
const sidebarCollapsed = computed(() => appStore.sidebarCollapsed)
const isAdmin = computed(() => authStore.user?.role === 'admin')

const { replayTour } = useOnboardingTour({
  storageKey: isAdmin.value ? 'admin_guide' : 'user_guide',
  autoStart: true
})

const onboardingStore = useOnboardingStore()

onMounted(() => {
  onboardingStore.setReplayCallback(replayTour)
})

defineExpose({ replayTour })
</script>

<style scoped>
.liquid-field {
  background:
    linear-gradient(120deg, transparent 0 20%, rgba(0, 95, 255, 0.1) 20% 30%, transparent 30%),
    linear-gradient(304deg, transparent 0 54%, rgba(255, 51, 102, 0.09) 54% 63%, transparent 63%),
    radial-gradient(110% 80% at 85% 10%, rgba(0, 200, 255, 0.09), transparent 58%),
    radial-gradient(90% 70% at 12% 88%, rgba(255, 138, 0, 0.1), transparent 62%);
}

:global(.dark) .liquid-field {
  background:
    linear-gradient(120deg, transparent 0 20%, rgba(0, 95, 255, 0.16) 20% 30%, transparent 30%),
    linear-gradient(304deg, transparent 0 54%, rgba(255, 51, 102, 0.13) 54% 63%, transparent 63%),
    radial-gradient(110% 80% at 85% 10%, rgba(0, 200, 255, 0.12), transparent 58%),
    radial-gradient(90% 70% at 12% 88%, rgba(255, 138, 0, 0.11), transparent 62%);
}
</style>
