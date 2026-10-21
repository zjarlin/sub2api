<template>
  <section class="card" data-testid="model-alias-settings">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="flex items-center gap-2 text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.modelAliases.title') }}<HelpTooltip :content="t('admin.settings.modelAliases.description') + ' ' + t('admin.settings.modelAliases.hint')" /></h2>
    </div>
    <div class="space-y-4 p-6">
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <p v-if="loading" role="status" class="text-sm text-gray-500 dark:text-gray-400">{{ t('common.loading') }}</p>
      <fieldset v-if="loaded" :disabled="saving || loading" class="space-y-3">
        <div v-for="{ group, index, depth } in treeRows" :key="index" :style="{ marginLeft: `${depth * 16}px` }" class="space-y-2 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <div class="flex gap-2">
            <input v-model.trim="group.canonical" class="input min-w-0 flex-1 font-mono" :aria-label="t('admin.settings.modelAliases.canonical', { index: index + 1 })" />
            <button type="button" class="btn btn-secondary" :aria-label="t('common.delete')" @click="groups.splice(index, 1)">{{ t('common.delete') }}</button>
          </div>
          <label class="flex items-center gap-2 text-sm"><span>{{ t('admin.settings.modelAliases.parent') }}</span><select v-model="group.parent" class="input min-w-0 flex-1"><option value="">{{ t('admin.settings.modelAliases.root') }}</option><option v-for="candidate in groups.filter(g => g !== group && g.canonical)" :key="candidate.canonical" :value="candidate.canonical">{{ candidate.canonical }}</option></select></label>
          <textarea v-model="group.aliasesText" class="input w-full font-mono text-sm" rows="2" :aria-label="t('admin.settings.modelAliases.aliases', { index: index + 1 })" spellcheck="false" />
          <div v-for="(route, routeIndex) in group.routes" :key="routeIndex" class="grid grid-cols-[minmax(0,1fr)_5rem_auto] gap-2">
            <input v-model.trim="route.model" class="input min-w-0 font-mono text-sm" :aria-label="t('admin.settings.modelAliases.routeModel')" :placeholder="t('admin.settings.modelAliases.routeModel')" />
            <input v-model.number="route.priority" type="number" class="input w-full" :aria-label="t('admin.settings.modelAliases.priority')" />
            <button type="button" class="btn btn-secondary" :aria-label="t('common.delete')" @click="group.routes.splice(routeIndex, 1)">×</button>
            <input v-model="route.baseURLsText" class="input col-span-3 w-full font-mono text-sm" :aria-label="t('admin.settings.modelAliases.baseURLs')" :placeholder="t('admin.settings.modelAliases.baseURLs')" />
          </div>
          <button type="button" class="btn btn-secondary text-sm" @click="group.routes.push({ model: '', priority: group.routes.length, baseURLsText: '' })">{{ t('admin.settings.modelAliases.addRoute') }}</button>
        </div>
        <p v-if="validationError" role="alert" class="text-sm text-amber-700 dark:text-amber-300">{{ validationError }}</p>
        <div class="flex gap-2">
          <button type="button" class="btn btn-secondary" @click="groups.push({ canonical: '', aliasesText: '', parent: '', routes: [] })">{{ t('admin.settings.modelAliases.add') }}</button>
          <button type="button" class="btn btn-primary ml-auto" :disabled="!!validationError" @click="save">{{ saving ? t('common.saving') : t('common.save') }}</button>
        </div>
        <p v-if="saved" role="status" class="text-sm text-green-700 dark:text-green-400">{{ t('admin.settings.modelAliases.saved') }}</p>
      </fieldset>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import { useI18n } from 'vue-i18n'
import { getModelAliasPolicy, updateModelAliasPolicy, type ModelAliasPolicy } from '@/api/admin/settings'
import { extractApiErrorMessage } from '@/utils/apiError'
const { t } = useI18n()
type EditableGroup = { canonical: string; aliasesText: string; parent: string; routes: Array<{ model: string; priority: number; baseURLsText: string }> }
const groups = ref<EditableGroup[]>([])
const treeRows = computed(() => {
 const rows: Array<{ group: EditableGroup; index: number; depth: number }> = []
 const visited = new Set<number>()
 function visit(index: number, depth: number) {
  if (visited.has(index)) return
  visited.add(index)
  const group = groups.value[index]
  rows.push({ group, index, depth })
  groups.value.forEach((g, i) => { if (g.parent && g.parent === group.canonical) visit(i, depth + 1) })
 }
 groups.value.forEach((g, i) => { if (!g.parent) visit(i, 0) })
 groups.value.forEach((_, i) => visit(i, 0))
 return rows
})
const loading = ref(false)
const saving = ref(false)
const loaded = ref(false)
const saved = ref(false)
const error = ref('')
const splitIDs = (value: string) => value.split(/[\s,，]+/).filter(Boolean)
const policy = computed<ModelAliasPolicy>(() => ({
  groups: groups.value.map(group => ({
    canonical: group.canonical.trim(),
    aliases: splitIDs(group.aliasesText),
    parent: group.parent || undefined,
    routes: group.routes.map(route => ({
      model: route.model.trim(),
      priority: route.priority,
      base_urls: splitIDs(route.baseURLsText)
    }))
  }))
}))
const validationError = computed(() => {
  const ids = policy.value.groups.flatMap(group => [group.canonical, ...group.aliases])
  if (policy.value.groups.some(group => !group.canonical)) return t('admin.settings.modelAliases.required')
  if (new Set(ids).size !== ids.length) return t('admin.settings.modelAliases.duplicate')
  for (const group of policy.value.groups) {
    const seen = new Set([group.canonical])
    let parent = group.parent
    while (parent) {
      if (seen.has(parent)) return t('admin.settings.modelAliases.invalidTree')
      seen.add(parent)
      const node = policy.value.groups.find(candidate => candidate.canonical === parent)
      if (!node) return t('admin.settings.modelAliases.invalidTree')
      parent = node.parent
    }
    const members = [group.canonical, ...group.aliases]
    if (group.routes?.some(route => !members.includes(route.model) || !Number.isInteger(route.priority))) {
      return t('admin.settings.modelAliases.invalidRoute')
    }
  }
  return ''
})
function assign(value: ModelAliasPolicy) {
  groups.value = value.groups.map(group => ({
    canonical: group.canonical,
    aliasesText: group.aliases.join('\n'),
    parent: group.parent || '',
    routes: (group.routes || []).map(route => ({
      model: route.model,
      priority: route.priority,
      baseURLsText: (route.base_urls || []).join(' ')
    }))
  }))
}
async function load() {
  loading.value = true
  error.value = ''
  try {
    assign(await getModelAliasPolicy())
    loaded.value = true
  } catch (failure) {
    error.value = extractApiErrorMessage(failure, t('common.error'))
  } finally {
    loading.value = false
  }
}
async function save() {
  if (saving.value || validationError.value) return
  saving.value = true
  error.value = ''
  saved.value = false
  try {
    assign(await updateModelAliasPolicy(policy.value))
    saved.value = true
  } catch (failure) {
    error.value = extractApiErrorMessage(failure, t('common.error'))
  } finally {
    saving.value = false
  }
}
onMounted(load)
</script>
