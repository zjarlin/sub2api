<template>
  <section class="edge-docs" :class="{ 'has-endpoints': endpoints.length > 1 }" data-testid="edge-service-docs">
    <nav v-if="endpoints.length > 1" class="edge-endpoint-nav" :aria-label="t('admin.vision.catalog.endpoints')">
      <h3>{{ t('admin.vision.catalog.endpoints') }}</h3>
      <RouterLink v-for="item in endpoints" :key="item.key" :to="{ query: edgeQuery(route.query, service, 'docs', item.key, adapter) }" :class="{ selected: item.key === endpoint.key }" :aria-current="item.key === endpoint.key ? 'page' : undefined" :data-testid="`edge-doc-endpoint-${item.key}`">
        <span class="edge-method" :class="item.method.toLowerCase()">{{ item.method }}</span><span>{{ item.description }}</span>
      </RouterLink>
    </nav>
    <article class="edge-doc-content">
      <header class="edge-operation">
        <div><span class="edge-method" :class="endpoint.method.toLowerCase()">{{ endpoint.method }}</span><code>{{ endpoint.path }}</code></div>
        <p>{{ endpoint.description }}</p>
      </header>
      <dl class="edge-api-meta">
        <div><dt>{{ t('admin.vision.catalog.gateway') }}</dt><dd><code>{{ baseUrl }}</code></dd></div>
        <div><dt>{{ t('admin.vision.catalog.authentication') }}</dt><dd><code>Authorization: Bearer $SUB2API_KEY</code></dd></div>
        <div v-if="endpoint.method !== 'GET'"><dt>Content-Type</dt><dd><code>{{ service.key === 'dub' ? 'multipart/form-data' : 'application/json' }}</code></dd></div>
      </dl>
      <p class="edge-doc-note">{{ t('admin.vision.catalog.authDescription') }}</p>
      <DubbingDocs v-if="service.key === 'dub'" :base-url="baseUrl" />
      <section v-else class="edge-doc-section">
        <h3>{{ t('admin.vision.catalog.requestParameters') }}<span>{{ fields.length }}</span></h3>
        <p v-if="!fields.length" class="edge-doc-note">{{ t('admin.vision.detail.noParameters') }}</p>
        <div v-else class="edge-parameters">
          <div v-for="field in fields" :key="field.name" class="edge-parameter">
            <div class="edge-parameter-name"><code>{{ field.name }}</code><span>{{ field.type }}</span><small v-if="field.required">{{ t('admin.vision.detail.required') }}</small></div>
            <p>{{ t('admin.vision.catalog.fields.' + field.description) }}</p>
          </div>
        </div>
      </section>
      <section class="edge-doc-section">
        <h3>{{ t('admin.vision.catalog.response') }}<span class="edge-http-ok">200</span></h3>
        <template v-if="service.key === 'tts'">
          <div class="edge-audio-response"><Icon name="play" size="lg" /><div><strong>audio/wav · audio/mpeg</strong><p>{{ t('admin.vision.detail.audioResponse') }}</p></div></div>
          <p class="edge-doc-note">{{ t('admin.vision.detail.audioLimits') }}</p>
        </template>
        <pre v-else class="edge-json-response"><code>{{ responseExample }}</code></pre>
        <p v-if="service.key === 'translate'" class="edge-doc-note">{{ t('admin.vision.catalog.translationLimits') }}</p>
      </section>
      <section class="edge-doc-section">
        <h3>{{ t('admin.vision.detail.errors') }}</h3>
        <div class="edge-errors"><span v-for="status in ['400', '401', '503']" :key="status"><code>{{ status }}</code>{{ t(`admin.vision.detail.error${status}`) }}</span></div>
      </section>
    </article>
    <aside class="edge-doc-example">
      <div class="edge-example-heading"><span>{{ t('admin.vision.detail.requestExample') }}</span><span>cURL</span><button type="button" class="edge-icon-button" :aria-label="t('common.copy')" :title="t('common.copy')" @click="copyToClipboard(code)"><Icon name="document" size="sm" /></button></div>
      <pre><code>{{ code }}</code></pre>
      <div class="edge-example-actions">
        <RouterLink :to="{ query: edgeQuery(route.query, service, 'debug', endpoint.key, adapter) }" class="edge-button primary"><Icon name="play" size="sm" />{{ t('admin.vision.catalog.debug') }}</RouterLink>
        <RouterLink :to="{ query: edgeQuery(route.query, service, 'code', endpoint.key, adapter) }" class="edge-button">{{ t('admin.vision.detail.moreLanguages') }}<Icon name="chevronRight" size="xs" /></RouterLink>
      </div>
      <div class="edge-example-context"><Icon name="cog" size="sm" /><RouterLink :to="{ query: edgeQuery(route.query, service, 'context', endpoint.key, adapter) }">{{ t('admin.vision.catalog.context') }}<Icon name="chevronRight" size="xs" /></RouterLink></div>
    </aside>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import DubbingDocs from './DubbingDocs.vue'
import { edgeQuery, type EdgeAdapter, type EdgeService } from './catalog'

interface Endpoint { key: string; path: string; method: string; description: string }
const props = defineProps<{ service: EdgeService; endpoints: Endpoint[]; endpoint: Endpoint; code: string; baseUrl: string; adapter: EdgeAdapter }>()
const route = useRoute()
const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const responseExample = computed(() => {
  const examples: Record<string, unknown> = {
    translate: props.endpoint.method === 'GET' ? { code: 0, data: { providers: ['baidu', 'hymt'] } } : { code: 0, data: { translations: [{ text: '你好世界' }], provider: props.adapter } },
    vision: { ResponseMetadata: { Action: props.endpoint.key, Version: '2022-08-31' }, Result: {} },
    dub: { task_id: 'dub_...', status: 'succeeded', output: { path: '/media/tasks/dub_.../content' } },
    generation: { id: 'task_...' },
  }
  return JSON.stringify(examples[props.service.key] || { model: props.service.key === 'jev' ? 'typesafe/jev' : 'laya', answers: { urgent: { answer: 'yes' } }, usage: { input_tokens: 12, output_tokens: 0 } }, null, 2)
})
const fields = computed(() => {
  if (props.endpoint.method === 'GET') return []
  const schemas: Record<string, Array<{ name: string; type: string; required?: boolean; description: string }>> = {
    translate: [{ name: 'q', type: 'string[]', required: true, description: 'q' }, { name: 'target', type: 'string', required: true, description: 'target' }, { name: 'source', type: 'string', description: 'source' }, { name: 'provider', type: 'string', description: 'provider' }, { name: 'format', type: 'string', description: 'format' }],
    vision: [{ name: 'Action', type: 'string', required: true, description: 'action' }, { name: 'Version', type: 'string', required: true, description: 'version' }, { name: 'ImageBase64', type: 'string', required: true, description: 'image' }, { name: 'Params', type: 'object', description: 'params' }],
    tts: [{ name: 'text', type: 'string', required: true, description: 'text' }, { name: 'language', type: 'string', description: 'language' }, { name: 'response_format', type: 'string', description: 'audioFormat' }, { name: 'speed', type: 'number', description: 'speed' }, { name: 'return_base64', type: 'boolean', description: 'base64' }],
    generation: [{ name: 'model', type: 'string', required: true, description: 'arkModel' }, { name: 'content', type: 'object[]', required: true, description: 'content' }, { name: 'duration', type: 'number', description: 'duration' }, { name: 'resolution', type: 'string', description: 'resolution' }],
  }
  return schemas[props.service.key] ?? [{ name: 'model', type: 'string', required: true, description: 'decisionModel' }, { name: 'state', type: 'string', required: true, description: 'state' }, { name: 'questions', type: 'object', required: true, description: 'questions' }]
})
</script>
