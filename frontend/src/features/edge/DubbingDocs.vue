<template>
  <article class="space-y-4 text-sm leading-6" data-testid="dubbing-docs">
    <section v-for="mode in ['auto', 'timeline']" :key="mode">
      <h3 class="font-semibold">{{ t(`admin.vision.dubbing.${mode}`) }}</h3>
      <p class="text-gray-600 dark:text-gray-300">{{ t(`admin.vision.dubbing.docs.${mode}`) }}</p>
    </section>
    <p>{{ t('admin.vision.dubbing.docs.multipart') }}</p>
    <dl class="divide-y divide-gray-200 dark:divide-dark-700">
      <div v-for="field in ['video', 'mode', 'language', 'keep_original_audio', 'segments']" :key="field" class="py-2">
        <dt class="break-all font-mono text-xs font-semibold">{{ field }}</dt>
        <dd class="text-gray-600 dark:text-gray-300">{{ t(`admin.vision.dubbing.docs.fields.${field}`) }}</dd>
      </div>
    </dl>
    <p>{{ t('admin.vision.dubbing.docs.alignment') }}</p>
    <p>{{ t('admin.vision.dubbing.docs.result') }}</p>
    <p>{{ t('admin.vision.dubbing.docs.auth') }}</p>
    <pre class="overflow-x-auto whitespace-pre-wrap break-all bg-gray-950 p-3 text-xs text-gray-100">{{ downloadExample }}</pre>
    <p class="text-gray-600 dark:text-gray-300">{{ t('admin.vision.dubbing.docs.limits') }}</p>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
const props = defineProps<{ baseUrl: string }>()
const { t } = useI18n()
const downloadExample = computed(() => `curl --fail-with-body '${props.baseUrl}/media/tasks/TASK_ID/content' \\\n  -H "Authorization: Bearer $SUB2API_KEY" \\\n  -o dubbed.mp4`)
</script>
