<template>
  <section data-testid="edge-service-catalog">
    <div class="mb-5 flex flex-wrap items-center justify-between gap-3">
      <div class="flex flex-wrap gap-1" role="tablist" :aria-label="t('admin.vision.catalog.categories')">
        <button v-for="category in categories" :key="category" type="button" role="tab" :aria-selected="filter === category" class="rounded-md px-3 py-2 text-sm font-medium transition-colors" :class="filter === category ? 'bg-gray-950 text-white dark:bg-white dark:text-gray-950' : 'text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-dark-800'" @click="filter = category">{{ t('admin.vision.catalog.' + category) }}</button>
      </div>
      <label class="relative w-full sm:w-64">
        <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-3 text-gray-400" />
        <input v-model="search" type="search" class="input w-full pl-9 text-sm" :aria-label="t('admin.vision.catalog.search')" :placeholder="t('admin.vision.catalog.search')" />
      </label>
    </div>
    <div class="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
      <RouterLink v-for="service in visibleServices" :key="service.key" :to="{ query: edgeQuery(route.query, service) }" class="service-card group flex min-h-[216px] min-w-0 flex-col rounded-lg border border-gray-200 bg-white p-5 transition-colors hover:border-primary-400 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 dark:border-dark-700 dark:bg-dark-900 dark:hover:border-primary-500" :data-testid="`edge-service-${service.key}`">
        <div class="flex items-start justify-between gap-3">
          <span class="service-icon flex h-11 w-11 shrink-0 items-center justify-center rounded-md" :class="'tone-' + service.tone"><Icon :name="service.icon" size="lg" /></span>
          <span class="flex items-center gap-1.5 text-xs" :class="enabled(service) ? 'text-emerald-700 dark:text-emerald-400' : 'text-gray-500 dark:text-gray-400'"><span class="h-1.5 w-1.5 rounded-full" :class="enabled(service) ? 'bg-emerald-500' : 'bg-gray-400'" />{{ loading ? t('common.loading') : enabled(service) ? t('admin.vision.statusEnabled') : t('admin.vision.statusDisabled') }}</span>
        </div>
        <h2 class="mt-4 text-base font-semibold text-gray-950 dark:text-white">{{ t('admin.vision.catalog.' + service.title + 'Title') }}</h2>
        <p class="mb-4 mt-1.5 text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.vision.catalog.' + service.title + 'Description') }}</p>
        <div class="mt-auto flex items-end justify-between gap-3 border-t border-gray-100 pt-3 dark:border-dark-800">
          <span class="min-w-0 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ service.tags.join(' / ') }}</span>
          <Icon name="chevronRight" size="sm" class="shrink-0 text-gray-400 group-hover:text-primary-600" />
        </div>
      </RouterLink>
    </div>
    <p v-if="!visibleServices.length" class="py-16 text-center text-sm text-gray-500">{{ t('admin.vision.catalog.noResults') }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { EDGE_SERVICES, edgeQuery, type EdgeService } from './catalog'

defineProps<{ enabled: (service: EdgeService) => boolean; loading: boolean }>()
const route = useRoute()
const { t } = useI18n()
const categories = ['all', 'language', 'vision', 'media', 'decision']
const filter = ref('all')
const search = ref('')
const visibleServices = computed(() => EDGE_SERVICES.filter(service =>
  (filter.value === 'all' || filter.value === service.category)
  && `${t('admin.vision.catalog.' + service.title + 'Title')} ${service.tags.join(' ')}`.toLowerCase().includes(search.value.trim().toLowerCase()),
))
</script>

<style scoped>
.tone-teal { background: #e6f6f1; color: #087b63; }
.tone-blue { background: #edf3ff; color: #2563eb; }
.tone-rose { background: #fff0f3; color: #c83761; }
.tone-violet { background: #f2efff; color: #7552b9; }
.tone-amber { background: #fff5da; color: #ad7800; }
:global(.dark) .service-icon { background: #222c29; }
:global(.dark) .tone-blue { background: #222d40; color: #93b4ff; }
:global(.dark) .tone-rose { background: #38262c; color: #fb9db5; }
:global(.dark) .tone-violet { background: #30283d; color: #c2adf7; }
:global(.dark) .tone-amber { background: #342e21; color: #eac76c; }
:global(.dark) .tone-teal { color: #6fd8b6; }
</style>
