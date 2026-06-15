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
    linear-gradient(to right, rgba(0, 0, 0, 0.16) 1px, transparent 1px),
    linear-gradient(to bottom, rgba(0, 0, 0, 0.16) 1px, transparent 1px),
    radial-gradient(110% 80% at 84% 10%, rgba(255, 95, 162, 0.18), transparent 58%);
  background-size: 70px 70px, 70px 70px, auto;
}

:global(.dark) .liquid-field {
  background:
    linear-gradient(to right, rgba(255, 255, 255, 0.14) 1px, transparent 1px),
    linear-gradient(to bottom, rgba(255, 255, 255, 0.14) 1px, transparent 1px),
    radial-gradient(110% 80% at 84% 10%, rgba(255, 220, 88, 0.16), transparent 58%);
  background-size: 70px 70px, 70px 70px, auto;
}
</style>
