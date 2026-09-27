<template>
  <div class="space-y-4" data-testid="dubbing-form">
    <div class="grid grid-cols-2 gap-1 rounded-lg bg-gray-100 p-1 dark:bg-dark-800" role="group" :aria-label="t('admin.vision.dubbing.mode')">
      <button v-for="mode in ['auto', 'timeline'] as const" :key="mode" type="button"
        class="min-w-0 rounded-md px-2 py-2 text-sm font-medium" :data-testid="`dub-mode-${mode}`"
        :class="options.mode === mode ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-600 dark:text-gray-300'"
        :aria-pressed="options.mode === mode" @click="setMode(mode)">{{ t(`admin.vision.dubbing.${mode}`) }}</button>
    </div>
    <label class="block text-sm font-medium">
      {{ t('admin.vision.dubbing.video') }}
      <input type="file" accept="video/*,.mkv,.avi,.flv,.ts" class="input mt-1 w-full text-xs" data-testid="dub-file" @change="selectVideo" />
    </label>
    <video v-if="previewUrl" :src="previewUrl" controls preload="metadata" class="max-h-48 w-full bg-black" @loadedmetadata="loadDuration" />
    <p v-if="file" class="break-all text-xs text-gray-500">{{ file.name }} · {{ (file.size / 1048576).toFixed(1) }} MiB<span v-if="duration"> · {{ duration.toFixed(2) }} s</span></p>
    <div class="flex flex-wrap items-end gap-4">
      <label class="block text-sm font-medium">
        {{ t('admin.vision.dubbing.language') }}
        <select v-model="options.language" class="input mt-1" data-testid="dub-language">
          <option v-for="lang in ['zh', 'en', 'ja', 'ko', 'yue']" :key="lang" :value="lang">{{ t(`admin.vision.dubbing.languages.${lang}`) }}</option>
        </select>
      </label>
      <label class="flex items-center gap-2 pb-2 text-sm">
        <input v-model="options.keep_original_audio" type="checkbox" data-testid="dub-keep-original" />
        {{ t('admin.vision.dubbing.keepOriginal') }}
      </label>
    </div>
    <template v-if="options.mode === 'timeline'">
      <div v-for="(segment, index) in options.segments" :key="index" class="space-y-2 border-t border-gray-200 pt-3 dark:border-dark-700" data-testid="dub-segment">
        <div class="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_32px] items-end gap-2">
          <label class="text-xs">{{ t('admin.vision.dubbing.start') }}
            <input v-model.number="segment.start" type="number" min="0" step="0.01" class="input mt-1 w-full" :aria-label="`${index + 1} ${t('admin.vision.dubbing.start')}`" data-testid="dub-start" />
          </label>
          <label class="text-xs">{{ t('admin.vision.dubbing.end') }}
            <input v-model.number="segment.end" type="number" min="0" step="0.01" class="input mt-1 w-full" :aria-label="`${index + 1} ${t('admin.vision.dubbing.end')}`" data-testid="dub-end" />
          </label>
          <button type="button" class="btn btn-icon btn-secondary h-8 w-8" :title="t('admin.vision.dubbing.removeSegment')" :aria-label="t('admin.vision.dubbing.removeSegment')" @click="options.segments?.splice(index, 1)"><Icon name="trash" size="sm" /></button>
        </div>
        <label class="block text-xs">{{ t('admin.vision.dubbing.text') }}
          <textarea v-model="segment.text" rows="2" maxlength="2000" class="input mt-1 w-full" data-testid="dub-text" />
        </label>
      </div>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="(options.segments?.length ?? 0) >= 500" @click="addSegment"><Icon name="plus" size="sm" />{{ t('admin.vision.dubbing.addSegment') }}</button>
    </template>
    <div class="flex flex-wrap gap-2 border-t border-gray-200 pt-3 dark:border-dark-700">
      <label class="btn btn-secondary btn-sm cursor-pointer"><Icon name="upload" size="sm" />{{ t('admin.vision.dubbing.importOptions') }}
        <input class="sr-only" type="file" accept="application/json,.json" data-testid="dub-import" @change="importOptions" />
      </label>
      <button type="button" class="btn btn-secondary btn-sm" @click="exportOptions"><Icon name="download" size="sm" />{{ t('admin.vision.dubbing.exportOptions') }}</button>
    </div>
    <p v-if="formError" role="alert" class="text-xs text-red-600 dark:text-red-400">{{ formError }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { dubbingOptionsError, videoFileError, type DubbingOptions } from './dubbing'

const options = defineModel<DubbingOptions>({ required: true })
const file = defineModel<File | null>('file', { required: true })
const duration = defineModel<number>('duration', { required: true })
const { t } = useI18n()
const importError = ref('')
const previewUrl = ref('')
const formError = computed(() => importError.value || (dubbingOptionsError(options.value, duration.value) ? t(`admin.vision.dubbing.${dubbingOptionsError(options.value, duration.value)}`) : ''))

watch(file, (value) => {
  if (previewUrl.value) URL.revokeObjectURL(previewUrl.value)
  duration.value = 0
  previewUrl.value = value ? URL.createObjectURL(value) : ''
}, { immediate: true })
onBeforeUnmount(() => { if (previewUrl.value) URL.revokeObjectURL(previewUrl.value) })

function selectVideo(event: Event) {
  const input = event.target as HTMLInputElement
  const selected = input.files?.[0] ?? null
  if (!selected) return
  const error = videoFileError(selected)
  importError.value = error ? t(`admin.vision.dubbing.${error}`) : ''
  file.value = error ? null : selected
  if (error) input.value = ''
}
function loadDuration(event: Event) {
  const seconds = (event.target as HTMLVideoElement).duration
  duration.value = Number.isFinite(seconds) ? seconds : 0
}
function setMode(mode: 'auto' | 'timeline') {
  const { segments, ...shared } = options.value
  options.value = mode === 'auto' ? { ...shared, mode } : { ...shared, mode, segments: segments ?? [{ start: 0, end: 3, text: '' }] }
  importError.value = ''
}
function addSegment() {
  const start = options.value.segments?.at(-1)?.end ?? 0
  options.value.segments?.push({ start, end: start + 3, text: '' })
}
async function importOptions(event: Event) {
  const input = event.target as HTMLInputElement
  const selected = input.files?.[0]
  if (!selected) return
  try {
    if (selected.size > 1024 * 1024) throw new Error('invalidOptions')
    const parsed: unknown = JSON.parse(await selected.text())
    const error = dubbingOptionsError(parsed, duration.value)
    if (error) throw new Error(error)
    options.value = parsed as DubbingOptions
    importError.value = ''
  } catch (cause) {
    importError.value = t(`admin.vision.dubbing.${cause instanceof Error && cause.message === 'invalidSegments' ? 'invalidSegments' : 'invalidOptions'}`)
  } finally { input.value = '' }
}
function exportOptions() {
  const url = URL.createObjectURL(new Blob([JSON.stringify(options.value, null, 2)], { type: 'application/json' }))
  const link = document.createElement('a')
  link.href = url
  link.download = 'options.json'
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}
</script>
