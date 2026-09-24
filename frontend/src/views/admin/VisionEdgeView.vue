<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-6">
      <header>
        <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('admin.vision.title') }}</h1>
      </header>
      <section class="space-y-3">
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.vision.localService') }}</h2>
        <div>
          <div v-if="loading" class="flex items-center gap-2 text-sm text-gray-500 dark:text-gray-400">
            <div class="h-4 w-4 animate-spin rounded-full border-b-2 border-primary-600"></div>
            {{ t('common.loading') }}
          </div>
          <div v-else-if="error" role="alert" class="text-sm text-red-700 dark:text-red-400">
            {{ error }}
          </div>
          <template v-else>
            <div class="flex items-center gap-3">
              <span
                class="inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium"
                :class="status?.enabled
                  ? 'bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400'
                  : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-400'"
              >
                {{ status?.enabled ? t('admin.vision.statusEnabled') : t('admin.vision.statusDisabled') }}
              </span>
            </div>
            <p v-if="!status?.enabled" class="mt-3 text-sm text-amber-600 dark:text-amber-400">
              {{ t('admin.vision.disabledHint') }}
            </p>
          </template>
        </div>
      </section>

      <section class="space-y-3">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.vision.endpoints') }}</h2>
        <div>
          <div class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead>
                <tr class="border-b border-gray-100 dark:border-dark-700">
                  <th class="pb-3 text-left font-medium text-gray-500 dark:text-gray-400">{{ t('admin.vision.colEndpoint') }}</th>
                  <th class="pb-3 text-left font-medium text-gray-500 dark:text-gray-400">{{ t('admin.vision.colMethod') }}</th>
                  <th class="pb-3 text-left font-medium text-gray-500 dark:text-gray-400">{{ t('admin.vision.colDescription') }}</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-gray-50 dark:divide-dark-700">
                <tr v-for="ep in endpoints" :key="ep.path">
                  <td class="py-3 pr-4 font-mono text-xs text-primary-600 dark:text-primary-400">{{ ep.path }}</td>
                  <td class="py-3 pr-4">
                    <span class="inline-flex items-center rounded px-1.5 py-0.5 text-xs font-mono font-medium bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-400">{{ ep.method }}</span>
                  </td>
                  <td class="py-3 text-gray-600 dark:text-gray-300">{{ ep.description }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </section>

      <section class="space-y-3 border-t border-gray-200 pt-5 dark:border-dark-600">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.vision.billingTitle') }}</h2>
        <div class="space-y-3 text-sm text-gray-600 dark:text-gray-300">
          <p>{{ t('admin.vision.billingDesc') }}</p>
          <ul class="list-disc pl-5 space-y-1">
            <li>{{ t('admin.vision.billingPoint1') }}</li>
            <li>{{ t('admin.vision.billingPoint2') }}</li>
          </ul>
        </div>
      </section>

      <section class="space-y-3 border-t border-gray-200 pt-5 dark:border-dark-600">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.vision.usage') }}</h2>
        <div>
          <pre class="rounded-md bg-gray-50 p-4 text-xs text-gray-700 dark:bg-dark-800 dark:text-gray-300 overflow-x-auto"><code>curl -X POST {{ baseUrl }}/vision/detect \
  -H "Authorization: Bearer $API_KEY" \
  -F "image=@photo.jpg" \
  -F "confidence_threshold=0.5"</code></pre>
        </div>
      </section>

      <section class="space-y-3 border-t border-gray-200 pt-5 dark:border-dark-600">
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.vision.jevTitle') }}</h2>
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.vision.jevDescription') }}</p>
        <p class="text-sm text-gray-600 dark:text-gray-300">
          {{ t('admin.vision.jevConfig') }}
          <router-link to="/admin/risk-control" class="text-primary-600 underline dark:text-primary-400">{{ t('nav.contentModeration') }}</router-link>
        </p>
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.vision.jevBilling') }}</p>
        <pre class="overflow-x-auto rounded-md bg-gray-50 p-4 text-xs text-gray-700 dark:bg-dark-800 dark:text-gray-300"><code>curl -X POST {{ baseUrl }}/v1/systemone \
  -H "Authorization: Bearer $CODEX_GROUP_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"typesafe/jev","state":"Payments failed for three days.","questions":{"urgent":{"type":"noul","instructions":"Does this need urgent attention?"}}}'</code></pre>
      </section>

      <section class="space-y-3 border-t border-gray-200 pt-5 dark:border-dark-600">
        <div class="flex items-center gap-3">
          <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('admin.vision.layaTitle') }}</h2>
          <span class="text-xs text-gray-600 dark:text-gray-300">{{ status?.laya_enabled ? t('admin.vision.statusEnabled') : t('admin.vision.statusDisabled') }}</span>
        </div>
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.vision.layaDescription') }}</p>
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.vision.layaBilling') }}</p>
        <pre class="overflow-x-auto rounded-md bg-gray-50 p-4 text-xs text-gray-700 dark:bg-dark-800 dark:text-gray-300"><code>curl -X POST {{ baseUrl }}/v1/systemone \
  -H "Authorization: Bearer $CODEX_GROUP_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"laya","state":"Payments failed for three days.","questions":{"urgent":{"type":"noul","instructions":"Does this need urgent attention?"}}}'</code></pre>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { apiClient, buildGatewayUrl } from '@/api/client'

interface VisionStatus {
  enabled: boolean
  laya_enabled: boolean
}

const { t } = useI18n()
const loading = ref(true)
const error = ref('')
const status = ref<VisionStatus | null>(null)
const baseUrl = new URL(buildGatewayUrl('/v1/systemone')).origin

const endpoints = computed(() => [
  { path: '/vision/detect', method: 'POST', description: t('admin.vision.endpointDetect') },
  { path: '/vision/segment', method: 'POST', description: t('admin.vision.endpointSegment') },
  { path: '/vision/pose', method: 'POST', description: t('admin.vision.endpointPose') },
  { path: '/vision/classify', method: 'POST', description: t('admin.vision.endpointClassify') },
  { path: '/vision/ocr', method: 'POST', description: t('admin.vision.endpointOcr') },
])

async function loadStatus() {
  loading.value = true
  error.value = ''
  try {
    const { data } = await apiClient.get<VisionStatus>('/admin/vision/status')
    status.value = data
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : t('admin.vision.statusError')
  } finally {
    loading.value = false
  }
}

onMounted(loadStatus)
</script>
