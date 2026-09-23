<template>
  <section class="card" data-testid="model-alias-settings">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.modelAliases.title') }}</h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.modelAliases.description') }}</p>
    </div>
    <div class="space-y-4 p-6">
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <p v-if="loading" role="status" class="text-sm text-gray-500 dark:text-gray-400">{{ t('common.loading') }}</p>
      <fieldset v-if="loaded" :disabled="saving || loading" class="space-y-3">
        <div v-for="(group, index) in groups" :key="index" class="space-y-2 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
          <div class="flex gap-2">
            <input v-model.trim="group.canonical" class="input min-w-0 flex-1 font-mono" :aria-label="t('admin.settings.modelAliases.canonical', { index: index + 1 })" />
            <button type="button" class="btn btn-secondary" :aria-label="t('common.delete')" @click="groups.splice(index, 1)">{{ t('common.delete') }}</button>
          </div>
          <textarea v-model="group.aliasesText" class="input w-full font-mono text-sm" rows="2" :aria-label="t('admin.settings.modelAliases.aliases', { index: index + 1 })" spellcheck="false" />
        </div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.modelAliases.hint') }}</p>
        <p v-if="validationError" role="alert" class="text-sm text-amber-700 dark:text-amber-300">{{ validationError }}</p>
        <div class="flex gap-2">
          <button type="button" class="btn btn-secondary" @click="groups.push({ canonical: '', aliasesText: '' })">{{ t('admin.settings.modelAliases.add') }}</button>
          <button type="button" class="btn btn-primary ml-auto" :disabled="!!validationError" @click="save">{{ saving ? t('common.saving') : t('common.save') }}</button>
        </div>
        <p v-if="saved" role="status" class="text-sm text-green-700 dark:text-green-400">{{ t('admin.settings.modelAliases.saved') }}</p>
      </fieldset>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getModelAliasPolicy, updateModelAliasPolicy, type ModelAliasPolicy } from '@/api/admin/settings'
import { extractApiErrorMessage } from '@/utils/apiError'
const { t } = useI18n()
const groups = ref<Array<{ canonical: string; aliasesText: string }>>([])
const loading = ref(false); const saving = ref(false); const loaded = ref(false); const saved = ref(false); const error = ref('')
const policy = computed<ModelAliasPolicy>(() => ({ groups: groups.value.map(g => ({ canonical: g.canonical.trim(), aliases: g.aliasesText.split(/[\s,，]+/).filter(Boolean) })) }))
const validationError = computed(() => {
  const ids = policy.value.groups.flatMap(g => [g.canonical, ...g.aliases])
  if (policy.value.groups.some(g => !g.canonical || !g.aliases.length)) return t('admin.settings.modelAliases.required')
  if (new Set(ids).size !== ids.length) return t('admin.settings.modelAliases.duplicate')
  return ''
})
function assign(value: ModelAliasPolicy) { groups.value = value.groups.map(g => ({ canonical: g.canonical, aliasesText: g.aliases.join('\n') })) }
async function load() { loading.value = true; error.value = ''; try { assign(await getModelAliasPolicy()); loaded.value = true } catch (e) { error.value = extractApiErrorMessage(e, t('common.error')) } finally { loading.value = false } }
async function save() { if (saving.value || validationError.value) return; saving.value = true; error.value = ''; saved.value = false; try { assign(await updateModelAliasPolicy(policy.value)); saved.value = true } catch (e) { error.value = extractApiErrorMessage(e, t('common.error')) } finally { saving.value = false } }
onMounted(load)
</script>
