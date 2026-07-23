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
    linear-gradient(rgba(146, 164, 174, 0.06) 1px, transparent 1px),
    linear-gradient(90deg, rgba(146, 164, 174, 0.045) 1px, transparent 1px),
    radial-gradient(110% 80% at 84% 10%, rgba(255, 138, 0, 0.08), transparent 58%);
  background-size: 44px 44px, 44px 44px, auto;
}

:global(.dark) .liquid-field {
  background:
    linear-gradient(rgba(146, 164, 174, 0.06) 1px, transparent 1px),
    linear-gradient(90deg, rgba(146, 164, 174, 0.045) 1px, transparent 1px),
    radial-gradient(110% 80% at 84% 10%, rgba(255, 138, 0, 0.1), transparent 58%);
  background-size: 44px 44px, 44px 44px, auto;
}
</style>
