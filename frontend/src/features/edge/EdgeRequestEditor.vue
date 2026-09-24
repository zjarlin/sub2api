<template>
  <div class="space-y-5" data-testid="edge-request-editor">
    <div class="grid gap-3 sm:grid-cols-[130px_minmax(0,1fr)]">
      <label class="space-y-1.5">
        <span class="input-label">{{ t('admin.vision.workbench.method') }}</span>
        <select v-model="method" class="input font-mono" data-testid="edge-request-method">
          <option v-for="item in methods" :key="item" :value="item">{{ item }}</option>
        </select>
      </label>
      <label class="space-y-1.5">
        <span class="input-label">{{ t('admin.vision.workbench.url') }}</span>
        <input
          v-model.trim="url"
          class="input font-mono text-xs"
          data-testid="edge-request-url"
          spellcheck="false"
          :placeholder="t('admin.vision.workbench.urlPlaceholder')"
        />
      </label>
    </div>

    <div class="grid min-w-0 gap-5 xl:grid-cols-2">
      <section class="min-w-0 space-y-2">
        <div class="flex items-center justify-between gap-3">
          <div>
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.vision.workbench.headers') }}</h3>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.vision.workbench.headersHint') }}</p>
          </div>
          <button type="button" class="btn btn-secondary btn-sm" @click="addHeader">
            <Icon name="plus" size="xs" />
            {{ t('common.add') }}
          </button>
        </div>
        <div class="space-y-2">
          <div v-for="(row, index) in headers" :key="`header-${index}`" class="grid grid-cols-[minmax(120px,0.8fr)_minmax(0,1.2fr)_auto] gap-2">
            <input v-model.trim="row.name" class="input font-mono text-xs" spellcheck="false" :placeholder="t('admin.vision.workbench.headerName')" />
            <input v-model="row.value" class="input font-mono text-xs" spellcheck="false" :placeholder="t('admin.vision.workbench.headerValue')" />
            <button type="button" class="btn btn-secondary btn-icon h-10 w-10" :title="t('common.delete')" @click="headers.splice(index, 1)">
              <Icon name="trash" size="sm" />
            </button>
          </div>
          <p v-if="headers.length === 0" class="rounded-md border border-dashed border-gray-300 px-3 py-2 text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400">
            {{ t('admin.vision.workbench.noHeaders') }}
          </p>
        </div>
      </section>

      <section class="min-w-0 space-y-2">
        <div class="flex items-center justify-between gap-3">
          <div>
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.vision.workbench.query') }}</h3>
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.vision.workbench.queryHint') }}</p>
          </div>
          <button type="button" class="btn btn-secondary btn-sm" @click="addQuery">
            <Icon name="plus" size="xs" />
            {{ t('common.add') }}
          </button>
        </div>
        <div class="space-y-2">
          <div v-for="(row, index) in query" :key="`query-${index}`" class="grid grid-cols-[minmax(120px,0.8fr)_minmax(0,1.2fr)_auto] gap-2">
            <input v-model.trim="row.name" class="input font-mono text-xs" spellcheck="false" :placeholder="t('admin.vision.workbench.queryName')" />
            <input v-model="row.value" class="input font-mono text-xs" spellcheck="false" :placeholder="t('admin.vision.workbench.queryValue')" />
            <button type="button" class="btn btn-secondary btn-icon h-10 w-10" :title="t('common.delete')" @click="query.splice(index, 1)">
              <Icon name="trash" size="sm" />
            </button>
          </div>
          <p v-if="query.length === 0" class="rounded-md border border-dashed border-gray-300 px-3 py-2 text-xs text-gray-500 dark:border-dark-600 dark:text-gray-400">
            {{ t('admin.vision.workbench.noQuery') }}
          </p>
        </div>
      </section>
    </div>

    <section class="space-y-2">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.vision.workbench.body') }}</h3>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.vision.workbench.bodyHint') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button
            v-for="mode in bodyModes"
            :key="mode"
            type="button"
            class="btn btn-sm"
            :class="bodyMode === mode ? 'btn-primary' : 'btn-secondary'"
            @click="bodyMode = mode"
          >
            {{ t(`admin.vision.workbench.bodyMode.${mode}`) }}
          </button>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="bodyMode !== 'json' || !body.trim()" @click="formatBody">
            <Icon name="terminal" size="xs" />
            {{ t('admin.vision.workbench.formatJson') }}
          </button>
        </div>
      </div>
      <textarea
        v-if="bodyMode !== 'none'"
        v-model="body"
        rows="10"
        class="input min-h-[220px] w-full whitespace-pre font-mono text-xs"
        data-testid="edge-request-body"
        spellcheck="false"
        :placeholder="bodyMode === 'form' ? t('admin.vision.workbench.formPlaceholder') : t('admin.vision.workbench.jsonPlaceholder')"
      />
      <p v-if="bodyError" role="alert" class="text-xs text-red-600 dark:text-red-400">{{ bodyError }}</p>
    </section>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import Icon from '@/components/icons/Icon.vue'
import type { EdgeBodyMode, EdgeKeyValue, EdgeRequestMethod } from './curl'

const { t } = useI18n()
const methods: EdgeRequestMethod[] = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']
const bodyModes: EdgeBodyMode[] = ['none', 'json', 'form']
const bodyError = ref('')

const method = defineModel<EdgeRequestMethod>('method', { required: true })
const url = defineModel<string>('url', { required: true })
const headers = defineModel<EdgeKeyValue[]>('headers', { required: true })
const query = defineModel<EdgeKeyValue[]>('query', { required: true })
const body = defineModel<string>('body', { required: true })
const bodyMode = defineModel<EdgeBodyMode>('bodyMode', { required: true })

watch(body, () => {
  if (bodyError.value) bodyError.value = ''
})

function addHeader() {
  headers.value = [...headers.value, { name: '', value: '' }]
}

function addQuery() {
  query.value = [...query.value, { name: '', value: '' }]
}

function formatBody() {
  try {
    body.value = JSON.stringify(JSON.parse(body.value), null, 2)
    bodyError.value = ''
  } catch {
    bodyError.value = t('admin.vision.workbench.invalidJson')
  }
}
</script>
