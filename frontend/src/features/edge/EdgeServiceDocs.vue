<template>
  <section class="grid min-w-0 gap-8 py-2 lg:grid-cols-[minmax(0,1fr)_240px]" data-testid="edge-service-docs">
    <div class="min-w-0 space-y-8">
      <div>
        <h3 class="text-sm font-semibold text-gray-950 dark:text-white">{{ t('admin.vision.catalog.endpoints') }}</h3>
        <div class="mt-3 divide-y divide-gray-100 border-y border-gray-200 dark:divide-dark-800 dark:border-dark-700">
          <RouterLink v-for="endpoint in endpoints" :key="endpoint.key" :to="{ query: edgeQuery(route.query, service, 'debug', endpoint.key) }" class="flex min-w-0 flex-wrap items-center gap-3 py-3 hover:text-primary-600" :data-testid="`edge-doc-endpoint-${endpoint.key}`">
            <span class="w-12 shrink-0 font-mono text-xs font-semibold text-primary-600 dark:text-primary-300">{{ endpoint.method }}</span>
            <code class="min-w-0 flex-1 break-all text-xs">{{ endpoint.path }}</code>
            <Icon name="chevronRight" size="sm" class="shrink-0 text-gray-400" />
            <span class="w-full pl-15 text-xs text-gray-500 dark:text-gray-400">{{ endpoint.description }}</span>
          </RouterLink>
        </div>
      </div>
      <DubbingDocs v-if="service.key === 'dub'" :base-url="baseUrl" />
      <template v-else>
        <div>
          <h3 class="text-sm font-semibold text-gray-950 dark:text-white">{{ t('admin.vision.catalog.requestParameters') }}</h3>
          <div class="mt-3 overflow-x-auto">
            <table class="w-full text-left text-sm">
              <thead class="border-y border-gray-200 bg-gray-50 text-xs text-gray-500 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-400"><tr><th class="px-3 py-2.5">{{ t('admin.vision.catalog.parameter') }}</th><th class="px-3 py-2.5">{{ t('admin.vision.catalog.type') }}</th><th class="px-3 py-2.5">{{ t('admin.vision.catalog.description') }}</th></tr></thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-800"><tr v-for="field in fields" :key="field.name"><td class="px-3 py-3 align-top"><code class="text-xs">{{ field.name }}</code><span v-if="field.required" class="ml-1 text-red-500">*</span></td><td class="px-3 py-3 align-top font-mono text-xs text-gray-500">{{ field.type }}</td><td class="px-3 py-3 text-xs leading-5 text-gray-600 dark:text-gray-400">{{ t('admin.vision.catalog.fields.' + field.description) }}</td></tr></tbody>
            </table>
          </div>
        </div>
        <div v-if="service.key === 'translate'" class="space-y-3">
          <h3 class="text-sm font-semibold">{{ t('admin.vision.catalog.response') }}</h3>
          <pre class="overflow-auto rounded-md border border-gray-200 bg-gray-50 p-4 text-xs leading-6 dark:border-dark-700 dark:bg-dark-900"><code>{{ translationResponse }}</code></pre>
          <p class="text-xs leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.vision.catalog.translationLimits') }}</p>
        </div>
      </template>
    </div>
    <aside class="space-y-6 border-t border-gray-200 pt-6 lg:border-l lg:border-t-0 lg:pl-6 lg:pt-0 dark:border-dark-700">
      <div><h3 class="text-xs font-semibold text-gray-500 dark:text-gray-400">{{ t('admin.vision.catalog.authentication') }}</h3><code class="mt-3 block break-all text-xs leading-6">Authorization: Bearer $SUB2API_KEY</code><p class="mt-2 text-xs leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.vision.catalog.authDescription') }}</p></div>
      <div><h3 class="text-xs font-semibold text-gray-500 dark:text-gray-400">{{ t('admin.vision.catalog.gateway') }}</h3><code class="mt-3 block break-all text-xs leading-6">{{ baseUrl }}</code></div>
      <div v-if="service.key === 'translate'" class="space-y-3"><h3 class="text-xs font-semibold text-gray-500 dark:text-gray-400">{{ t('admin.vision.catalog.adapters') }}</h3><RouterLink v-for="adapter in EDGE_ADAPTERS" :key="adapter" :to="{ query: edgeQuery(route.query, service, 'context', undefined, adapter) }" class="flex items-center justify-between text-sm text-gray-700 hover:text-primary-600 dark:text-gray-300"><span>{{ adapter }}</span><Icon name="chevronRight" size="xs" /></RouterLink></div>
    </aside>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { buildGatewayUrl } from '@/api/client'
import DubbingDocs from './DubbingDocs.vue'
import { EDGE_ADAPTERS, edgeQuery, type EdgeService } from './catalog'

const props = defineProps<{ service: EdgeService; endpoints: Array<{ key: string; path: string; method: string; description: string }> }>()
const route = useRoute()
const { t } = useI18n()
const baseUrl = new URL(buildGatewayUrl('/api/v1/translate')).origin
const translationResponse = JSON.stringify({ code: 0, data: { translations: [{ text: 'Hello world' }], provider: 'baidu' } }, null, 2)
const fields = computed(() => {
  const schemas: Record<string, Array<{ name: string; type: string; required?: boolean; description: string }>> = {
    translate: [{ name: 'q', type: 'string[]', required: true, description: 'q' }, { name: 'target', type: 'string', required: true, description: 'target' }, { name: 'source', type: 'string', description: 'source' }, { name: 'provider', type: 'string', description: 'provider' }, { name: 'format', type: 'string', description: 'format' }],
    vision: [{ name: 'Action', type: 'string', required: true, description: 'action' }, { name: 'Version', type: 'string', required: true, description: 'version' }, { name: 'ImageBase64', type: 'string', required: true, description: 'image' }, { name: 'Params', type: 'object', description: 'params' }],
    tts: [{ name: 'text', type: 'string', required: true, description: 'text' }, { name: 'language', type: 'string', description: 'language' }, { name: 'response_format', type: 'string', description: 'audioFormat' }],
    generation: [{ name: 'model', type: 'string', required: true, description: 'arkModel' }, { name: 'content', type: 'object[]', required: true, description: 'content' }, { name: 'duration', type: 'number', description: 'duration' }, { name: 'resolution', type: 'string', description: 'resolution' }],
  }
  return schemas[props.service.key] ?? [{ name: 'model', type: 'string', required: true, description: 'decisionModel' }, { name: 'state', type: 'string', required: true, description: 'state' }, { name: 'questions', type: 'object', required: true, description: 'questions' }]
})
</script>
