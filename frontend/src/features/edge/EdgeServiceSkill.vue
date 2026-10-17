<template>
  <section class="edge-skill-view min-w-0" data-testid="edge-skill-panel">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <h2 class="text-sm font-semibold text-gray-950 dark:text-white">{{ t('admin.vision.catalog.skill') }}</h2>
        <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.vision.skill.description') }}</p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <button type="button" class="btn btn-secondary btn-sm" data-testid="edge-skill-copy" @click="copySkill">
          <Icon name="copy" size="xs" />
          {{ t('admin.vision.skill.copy') }}
        </button>
        <button type="button" class="btn btn-secondary btn-sm" data-testid="edge-skill-download" @click="downloadSkill">
          <Icon name="download" size="xs" />
          {{ t('admin.vision.skill.download') }}
        </button>
      </div>
    </div>

    <dl class="edge-skill-meta">
      <div>
        <dt>{{ t('admin.vision.skill.name') }}</dt>
        <dd><code data-testid="edge-skill-name">{{ skillName }}</code></dd>
      </div>
      <div>
        <dt>{{ t('admin.vision.skill.installPath') }}</dt>
        <dd><code data-testid="edge-skill-path">{{ installPath }}</code></dd>
      </div>
    </dl>

    <p class="edge-doc-note">{{ t('admin.vision.skill.installHint') }}</p>

    <pre class="edge-skill-preview" data-testid="edge-skill-preview"><code>{{ skill }}</code></pre>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import type { EdgeService } from './catalog'
import { buildEdgeSkill, edgeSkillName, edgeSkillPath, type EdgeSkillEndpoint } from './skill'

const props = defineProps<{
  service: EdgeService
  serviceTitle: string
  serviceDescription: string
  endpoints: EdgeSkillEndpoint[]
  endpoint: EdgeSkillEndpoint
  gatewayOrigin: string
  example: string
}>()

const { t } = useI18n()
const { copyToClipboard } = useClipboard()

const skill = computed(() => buildEdgeSkill({
  service: props.service,
  serviceTitle: props.serviceTitle,
  serviceDescription: props.serviceDescription,
  endpoints: props.endpoints,
  endpoint: props.endpoint,
  gatewayOrigin: props.gatewayOrigin,
  example: props.example,
}))
const skillName = computed(() => edgeSkillName(props.service))
const installPath = computed(() => edgeSkillPath(props.service))

function downloadSkill() {
  const url = URL.createObjectURL(new Blob([skill.value], { type: 'text/markdown;charset=utf-8' }))
  const link = document.createElement('a')
  link.href = url
  link.download = 'SKILL.md'
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

async function copySkill() {
  await copyToClipboard(skill.value, t('common.copiedToClipboard'))
}
</script>
