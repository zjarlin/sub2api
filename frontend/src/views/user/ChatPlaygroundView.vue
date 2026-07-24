<template>
  <AppLayout>
    <section class="chat-page">
      <header class="chat-page__header">
        <div>
          <p class="chat-page__eyebrow">PLAYGROUND / CHAT COMPLETIONS</p>
          <h1>{{ t('chatPlayground.title') }}</h1>
          <p>{{ t('chatPlayground.description') }}</p>
        </div>
        <button
          type="button"
          class="btn btn-secondary btn-icon"
          :title="t('chatPlayground.clearConversation')"
          :disabled="messages.length === 0"
          @click="clearConversation"
        >
          <Icon name="trash" />
        </button>
      </header>

      <div class="chat-workbench">
        <aside class="chat-config">
          <div class="chat-config__heading">
            <span>01</span>
            <strong>{{ t('chatPlayground.configuration') }}</strong>
          </div>

          <div class="chat-field">
            <label for="chat-api-key">{{ t('chatPlayground.apiKey') }}</label>
            <Select
              id="chat-api-key"
              v-model="selectedApiKeyId"
              :options="apiKeyOptions"
              :disabled="loadingKeys || isGenerating"
              :placeholder="loadingKeys ? t('common.loading') : t('chatPlayground.selectApiKey')"
              :empty-text="t('chatPlayground.noApiKeys')"
              searchable
            />
          </div>

          <div class="chat-field">
            <div class="chat-field__label-row">
              <label for="chat-model">{{ t('chatPlayground.model') }}</label>
              <button
                type="button"
                class="chat-icon-button"
                :title="t('chatPlayground.refreshModels')"
                :disabled="!selectedApiKey || loadingModels || isGenerating"
                @click="refreshModels"
              >
                <Icon name="refresh" size="sm" :class="loadingModels ? 'animate-spin' : ''" />
              </button>
            </div>
            <Select
              id="chat-model"
              v-model="selectedModel"
              :options="modelOptions"
              :disabled="!selectedApiKey || loadingModels || isGenerating"
              :placeholder="loadingModels ? t('chatPlayground.loadingModels') : t('chatPlayground.selectModel')"
              :empty-text="t('chatPlayground.noModels')"
              searchable
            />
          </div>

          <div class="chat-field chat-field--grow">
            <label for="chat-system-prompt">{{ t('chatPlayground.systemPrompt') }}</label>
            <textarea
              id="chat-system-prompt"
              v-model="systemPrompt"
              class="chat-textarea"
              :disabled="isGenerating"
              :placeholder="t('chatPlayground.systemPromptPlaceholder')"
              rows="7"
            ></textarea>
          </div>

          <div v-if="modelError" class="chat-alert" role="alert">
            <Icon name="exclamationTriangle" size="sm" />
            <span>{{ modelError }}</span>
          </div>

          <router-link v-if="!loadingKeys && apiKeys.length === 0" to="/keys" class="btn btn-primary">
            <Icon name="key" />
            {{ t('chatPlayground.createApiKey') }}
          </router-link>

          <div class="chat-config__status">
            <span :class="['chat-status-dot', selectedApiKey ? 'is-ready' : '']"></span>
            <span>{{ selectedApiKey ? selectedApiKey.name : t('chatPlayground.waitingForKey') }}</span>
            <strong>{{ models.length }}</strong>
          </div>
        </aside>

        <div class="chat-console">
          <div class="chat-console__toolbar">
            <div>
              <span>{{ t('chatPlayground.conversation') }}</span>
              <strong>{{ selectedModel || t('chatPlayground.noModelSelected') }}</strong>
            </div>
            <span :class="['chat-run-state', isGenerating ? 'is-running' : '']">
              {{ isGenerating ? t('chatPlayground.streaming') : t('chatPlayground.ready') }}
            </span>
          </div>

          <div ref="messageListRef" class="chat-message-list" aria-live="polite">
            <div v-if="messages.length === 0" class="chat-empty">
              <div class="chat-empty__icon">
                <Icon name="chat" size="xl" />
              </div>
              <h2>{{ t('chatPlayground.emptyTitle') }}</h2>
              <p>{{ t('chatPlayground.emptyDescription') }}</p>
            </div>

            <article
              v-for="message in messages"
              :key="message.id"
              :class="['chat-message', `chat-message--${message.role}`]"
            >
              <header>
                <span>{{ message.role === 'user' ? t('chatPlayground.you') : t('chatPlayground.assistant') }}</span>
                <button
                  v-if="message.role === 'assistant' && message.content"
                  type="button"
                  class="chat-icon-button"
                  :title="t('chatPlayground.copyResponse')"
                  @click="copyMessage(message.content)"
                >
                  <Icon name="copy" size="sm" />
                </button>
              </header>

              <div
                v-if="message.role === 'assistant'"
                class="chat-message__content chat-markdown"
                v-html="renderMarkdown(message.content || (isGenerating ? '▌' : ''))"
              ></div>
              <div v-else class="chat-message__content chat-message__plain">{{ message.content }}</div>

              <footer v-if="message.error || message.stopped || message.usage?.total_tokens">
                <span v-if="message.error" class="chat-message__error">{{ message.error }}</span>
                <span v-else-if="message.stopped">{{ t('chatPlayground.stopped') }}</span>
                <span v-if="message.usage?.total_tokens">
                  {{ t('chatPlayground.tokens', { count: message.usage.total_tokens }) }}
                </span>
              </footer>
            </article>
          </div>

          <form class="chat-composer" @submit.prevent="sendMessage">
            <textarea
              ref="composerRef"
              v-model="draft"
              class="chat-composer__input"
              :placeholder="t('chatPlayground.messagePlaceholder')"
              :disabled="!selectedApiKey || !selectedModel"
              rows="3"
              @keydown="handleComposerKeydown"
            ></textarea>
            <button
              v-if="isGenerating"
              type="button"
              class="btn btn-secondary chat-composer__action"
              @click="stopGeneration"
            >
              <Icon name="x" />
              {{ t('chatPlayground.stop') }}
            </button>
            <button
              v-else
              type="submit"
              class="btn btn-primary chat-composer__action"
              :disabled="!canSend"
            >
              <Icon name="arrowUp" />
              {{ t('chatPlayground.send') }}
            </button>
          </form>
        </div>
      </div>
    </section>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import DOMPurify from 'dompurify'
import { marked } from 'marked'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import { keysAPI } from '@/api/keys'
import {
  listChatPlaygroundModels,
  streamChatCompletion,
  type ChatPlaygroundMessage,
  type ChatPlaygroundModel,
  type ChatPlaygroundUsage,
} from '@/api/chatPlayground'
import type { ApiKey } from '@/types'
import { useAppStore } from '@/stores/app'
import { useClipboard } from '@/composables/useClipboard'

interface DisplayMessage {
  id: number
  role: 'user' | 'assistant'
  content: string
  error?: string
  stopped?: boolean
  usage?: ChatPlaygroundUsage | null
}

const KEY_SELECTION_STORAGE = 'sub2api-chat-api-key-id'
const MODEL_SELECTION_PREFIX = 'sub2api-chat-model:'

const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const apiKeys = ref<ApiKey[]>([])
const models = ref<ChatPlaygroundModel[]>([])
const selectedApiKeyId = ref<number | null>(null)
const selectedModel = ref<string | null>(null)
const systemPrompt = ref('')
const draft = ref('')
const messages = ref<DisplayMessage[]>([])
const loadingKeys = ref(false)
const loadingModels = ref(false)
const modelError = ref('')
const isGenerating = ref(false)
const messageListRef = ref<HTMLElement | null>(null)
const composerRef = ref<HTMLTextAreaElement | null>(null)

let messageSequence = 0
let modelRequestController: AbortController | null = null
let generationController: AbortController | null = null
let initializing = true

const selectedApiKey = computed(() => (
  apiKeys.value.find((apiKey) => apiKey.id === selectedApiKeyId.value) || null
))

const apiKeyOptions = computed<SelectOption[]>(() => apiKeys.value.map((apiKey) => ({
  value: apiKey.id,
  label: apiKey.group?.name ? `${apiKey.name} · ${apiKey.group.name}` : apiKey.name,
})))

const modelOptions = computed<SelectOption[]>(() => models.value.map((model) => ({
  value: model.id,
  label: model.id,
  description: model.owned_by,
})))

const canSend = computed(() => Boolean(
  selectedApiKey.value
  && selectedModel.value
  && draft.value.trim()
  && !isGenerating.value,
))

function readStoredKeyID(): number | null {
  const value = localStorage.getItem(KEY_SELECTION_STORAGE)
  if (!value) {
    return null
  }
  const parsed = Number(value)
  return Number.isInteger(parsed) && parsed > 0 ? parsed : null
}

function modelStorageKey(apiKeyID: number): string {
  return `${MODEL_SELECTION_PREFIX}${apiKeyID}`
}

function renderMarkdown(content: string): string {
  if (!content) {
    return ''
  }
  const html = marked.parse(content, { async: false }) as string
  return DOMPurify.sanitize(html)
}

async function scrollMessagesToBottom(): Promise<void> {
  await nextTick()
  const element = messageListRef.value
  if (element) {
    element.scrollTop = element.scrollHeight
  }
}

async function loadApiKeys(): Promise<void> {
  loadingKeys.value = true
  try {
    const response = await keysAPI.list(1, 100, {
      status: 'active',
      sort_by: 'created_at',
      sort_order: 'desc',
    })
    apiKeys.value = response.items.filter((apiKey) => apiKey.status === 'active')

    const storedKeyID = readStoredKeyID()
    const storedKeyExists = apiKeys.value.some((apiKey) => apiKey.id === storedKeyID)
    selectedApiKeyId.value = storedKeyExists ? storedKeyID : apiKeys.value[0]?.id ?? null
  } catch (error) {
    appStore.showError(error instanceof Error ? error.message : t('chatPlayground.loadKeysFailed'))
  } finally {
    loadingKeys.value = false
  }
}

async function refreshModels(): Promise<void> {
  modelRequestController?.abort()
  models.value = []
  selectedModel.value = null
  modelError.value = ''

  const apiKey = selectedApiKey.value
  if (!apiKey) {
    return
  }

  const requestController = new AbortController()
  modelRequestController = requestController
  loadingModels.value = true

  try {
    const result = await listChatPlaygroundModels(apiKey.key, requestController.signal)
    if (requestController.signal.aborted) {
      return
    }
    models.value = result

    const storedModel = localStorage.getItem(modelStorageKey(apiKey.id))
    const storedModelExists = result.some((model) => model.id === storedModel)
    selectedModel.value = storedModelExists ? storedModel : result[0]?.id ?? null
  } catch (error) {
    if (requestController.signal.aborted) {
      return
    }
    modelError.value = error instanceof Error ? error.message : t('chatPlayground.loadModelsFailed')
  } finally {
    if (modelRequestController === requestController) {
      modelRequestController = null
      loadingModels.value = false
    }
  }
}

function buildRequestMessages(): ChatPlaygroundMessage[] {
  const result: ChatPlaygroundMessage[] = []
  const normalizedSystemPrompt = systemPrompt.value.trim()
  if (normalizedSystemPrompt) {
    result.push({ role: 'system', content: normalizedSystemPrompt })
  }
  result.push(...messages.value
    .filter((message) => message.content.trim())
    .map((message) => ({
      role: message.role,
      content: message.content,
    })))
  return result
}

async function sendMessage(): Promise<void> {
  const apiKey = selectedApiKey.value
  const model = selectedModel.value
  const content = draft.value.trim()
  if (!apiKey || !model || !content || isGenerating.value) {
    return
  }

  const userMessage: DisplayMessage = {
    id: ++messageSequence,
    role: 'user',
    content,
  }
  messages.value.push(userMessage)
  draft.value = ''

  const requestMessages = buildRequestMessages()
  const assistantMessage: DisplayMessage = {
    id: ++messageSequence,
    role: 'assistant',
    content: '',
  }
  messages.value.push(assistantMessage)
  await scrollMessagesToBottom()

  const requestController = new AbortController()
  generationController = requestController
  isGenerating.value = true

  try {
    const result = await streamChatCompletion({
      apiKey: apiKey.key,
      model,
      messages: requestMessages,
      signal: requestController.signal,
      onDelta: (delta) => {
        assistantMessage.content += delta
        void scrollMessagesToBottom()
      },
    })
    assistantMessage.usage = result.usage
    if (!assistantMessage.content) {
      assistantMessage.error = t('chatPlayground.emptyResponse')
    }
  } catch (error) {
    if (requestController.signal.aborted) {
      assistantMessage.stopped = true
      return
    }
    const message = error instanceof Error ? error.message : t('chatPlayground.requestFailed')
    assistantMessage.error = message
    appStore.showError(message)
  } finally {
    if (generationController === requestController) {
      generationController = null
      isGenerating.value = false
    }
    await scrollMessagesToBottom()
    composerRef.value?.focus()
  }
}

function stopGeneration(): void {
  generationController?.abort()
}

function clearConversation(): void {
  stopGeneration()
  messages.value = []
  draft.value = ''
  composerRef.value?.focus()
}

function handleComposerKeydown(event: KeyboardEvent): void {
  if (event.key !== 'Enter' || event.shiftKey || event.isComposing) {
    return
  }
  event.preventDefault()
  void sendMessage()
}

function copyMessage(content: string): void {
  void copyToClipboard(content, t('chatPlayground.responseCopied'))
}

watch(selectedApiKeyId, (apiKeyID) => {
  stopGeneration()
  if (apiKeyID) {
    localStorage.setItem(KEY_SELECTION_STORAGE, String(apiKeyID))
  } else {
    localStorage.removeItem(KEY_SELECTION_STORAGE)
  }
  if (!initializing) {
    void refreshModels()
  }
})

watch(selectedModel, (model) => {
  const apiKeyID = selectedApiKeyId.value
  if (!apiKeyID || !model) {
    return
  }
  localStorage.setItem(modelStorageKey(apiKeyID), model)
})

onMounted(async () => {
  await loadApiKeys()
  initializing = false
  await refreshModels()
})

onBeforeUnmount(() => {
  modelRequestController?.abort()
  generationController?.abort()
})
</script>

<style scoped>
.chat-page {
  display: flex;
  min-height: calc(100dvh - 8rem);
  flex-direction: column;
  gap: 1rem;
  color: var(--neo-ink);
}

.chat-page__header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 1rem;
}

.chat-page__eyebrow {
  margin: 0 0 0.25rem;
  color: var(--neo-muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.72rem;
  font-weight: 900;
}

.chat-page__header h1 {
  margin: 0;
  font-size: 1.75rem;
  font-weight: 950;
  letter-spacing: 0;
}

.chat-page__header p:last-child {
  margin: 0.3rem 0 0;
  color: var(--neo-muted);
  font-size: 0.9rem;
  font-weight: 600;
}

.chat-workbench {
  display: grid;
  flex: 1;
  min-height: 0;
  grid-template-columns: minmax(15rem, 18rem) minmax(0, 1fr);
  gap: 1rem;
}

.chat-config,
.chat-console {
  min-width: 0;
  border: 2px solid var(--neo-border);
  border-radius: var(--neo-radius);
  background: var(--neo-panel);
  box-shadow: var(--neo-shadow);
}

.chat-config {
  display: flex;
  flex-direction: column;
  gap: 1rem;
  padding: 1rem;
}

.chat-config__heading {
  display: flex;
  align-items: center;
  gap: 0.65rem;
  border-bottom: 2px solid var(--neo-border);
  padding-bottom: 0.85rem;
  font-size: 0.78rem;
  text-transform: uppercase;
}

.chat-config__heading span {
  display: inline-flex;
  width: 2rem;
  height: 2rem;
  align-items: center;
  justify-content: center;
  border: 2px solid var(--neo-border);
  border-radius: var(--neo-radius);
  background: var(--neo-cyan);
  color: #050505;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-weight: 950;
}

.chat-field {
  display: flex;
  flex-direction: column;
  gap: 0.45rem;
}

.chat-field--grow {
  flex: 1;
}

.chat-field label,
.chat-field__label-row label {
  color: var(--neo-ink);
  font-size: 0.72rem;
  font-weight: 950;
  text-transform: uppercase;
}

.chat-field__label-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}

.chat-icon-button {
  display: inline-flex;
  width: 2rem;
  height: 2rem;
  flex: 0 0 2rem;
  align-items: center;
  justify-content: center;
  border: 2px solid var(--neo-border);
  border-radius: var(--neo-radius);
  background: var(--neo-panel-strong);
  color: var(--neo-ink);
  box-shadow: 2px 2px 0 var(--neo-border);
  transition: transform 0.15s ease, box-shadow 0.15s ease;
}

.chat-icon-button:hover:not(:disabled) {
  transform: translate(2px, 2px);
  box-shadow: none;
}

.chat-icon-button:disabled {
  cursor: not-allowed;
  opacity: 0.5;
}

.chat-textarea {
  width: 100%;
  flex: 1;
  resize: vertical;
  border: 2px solid var(--neo-border);
  border-radius: var(--neo-radius);
  padding: 0.75rem;
  background: var(--neo-panel-strong);
  color: var(--neo-ink);
  box-shadow: var(--neo-shadow-sm);
  font-size: 0.85rem;
  line-height: 1.55;
  outline: none;
}

.chat-textarea:focus {
  transform: translate(3px, 3px);
  box-shadow: none;
}

.chat-alert {
  display: flex;
  align-items: flex-start;
  gap: 0.5rem;
  border: 2px solid var(--neo-border);
  border-radius: var(--neo-radius);
  padding: 0.65rem;
  background: var(--neo-pink);
  color: #050505;
  font-size: 0.78rem;
  font-weight: 700;
}

.chat-config__status {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 0.5rem;
  border-top: 2px solid var(--neo-border);
  padding-top: 0.85rem;
  color: var(--neo-muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.7rem;
  font-weight: 800;
}

.chat-config__status span:nth-child(2) {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chat-config__status strong {
  display: inline-flex;
  min-width: 2rem;
  justify-content: center;
  border: 2px solid var(--neo-border);
  border-radius: var(--neo-radius);
  padding: 0.15rem 0.35rem;
  background: var(--neo-main);
  color: #050505;
}

.chat-status-dot {
  width: 0.65rem;
  height: 0.65rem;
  border: 2px solid var(--neo-border);
  border-radius: 50%;
  background: var(--neo-muted);
}

.chat-status-dot.is-ready {
  background: var(--neo-green);
}

.chat-console {
  display: grid;
  min-height: 34rem;
  overflow: hidden;
  grid-template-rows: auto minmax(0, 1fr) auto;
}

.chat-console__toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  border-bottom: 2px solid var(--neo-border);
  padding: 0.8rem 1rem;
  background: var(--neo-main);
  color: #050505;
}

.chat-console__toolbar div {
  display: flex;
  min-width: 0;
  align-items: baseline;
  gap: 0.65rem;
}

.chat-console__toolbar span,
.chat-console__toolbar strong {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.chat-console__toolbar span {
  font-size: 0.68rem;
  font-weight: 950;
  text-transform: uppercase;
}

.chat-console__toolbar strong {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.78rem;
}

.chat-run-state {
  flex: 0 0 auto;
  border: 2px solid #050505;
  border-radius: var(--neo-radius);
  padding: 0.25rem 0.5rem;
  background: #ffffff;
}

.chat-run-state.is-running {
  background: var(--neo-green);
}

.chat-message-list {
  min-height: 0;
  overflow-y: auto;
  padding: 1rem;
  scrollbar-gutter: stable;
}

.chat-empty {
  display: flex;
  min-height: 100%;
  align-items: center;
  justify-content: center;
  flex-direction: column;
  padding: 2rem;
  text-align: center;
}

.chat-empty__icon {
  display: flex;
  width: 4rem;
  height: 4rem;
  align-items: center;
  justify-content: center;
  border: 2px solid var(--neo-border);
  border-radius: var(--neo-radius);
  background: var(--neo-cyan);
  color: #050505;
  box-shadow: var(--neo-shadow);
}

.chat-empty h2 {
  margin: 1.25rem 0 0.35rem;
  color: var(--neo-ink);
  font-size: 1.15rem;
  font-weight: 950;
}

.chat-empty p {
  max-width: 28rem;
  margin: 0;
  color: var(--neo-muted);
  font-size: 0.85rem;
  font-weight: 600;
}

.chat-message {
  width: min(48rem, 88%);
  margin-bottom: 1rem;
  border: 2px solid var(--neo-border);
  border-radius: var(--neo-radius);
  background: var(--neo-panel-strong);
  color: var(--neo-ink);
  box-shadow: 4px 4px 0 var(--neo-border);
}

.chat-message--user {
  margin-left: auto;
  background: var(--neo-pink);
  color: #050505;
}

.chat-message--assistant {
  margin-right: auto;
}

.chat-message > header {
  display: flex;
  min-height: 2.5rem;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  border-bottom: 2px solid var(--neo-border);
  padding: 0.35rem 0.65rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.68rem;
  font-weight: 950;
  text-transform: uppercase;
}

.chat-message--assistant > header {
  background: var(--neo-cyan);
  color: #050505;
}

.chat-message__content {
  overflow-wrap: anywhere;
  padding: 0.85rem 1rem;
  font-size: 0.9rem;
  line-height: 1.65;
}

.chat-message__plain {
  white-space: pre-wrap;
}

.chat-message > footer {
  display: flex;
  justify-content: space-between;
  gap: 0.75rem;
  border-top: 2px solid var(--neo-border);
  padding: 0.4rem 0.65rem;
  color: var(--neo-muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 0.65rem;
  font-weight: 800;
}

.chat-message__error {
  color: #b91c1c;
}

.chat-composer {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: stretch;
  gap: 0.75rem;
  border-top: 2px solid var(--neo-border);
  padding: 0.9rem;
  background: var(--neo-panel);
}

.chat-composer__input {
  width: 100%;
  min-height: 4.5rem;
  resize: none;
  border: 2px solid var(--neo-border);
  border-radius: var(--neo-radius);
  padding: 0.75rem;
  background: var(--neo-panel-strong);
  color: var(--neo-ink);
  box-shadow: var(--neo-shadow-sm);
  font-size: 0.9rem;
  line-height: 1.5;
  outline: none;
}

.chat-composer__input:focus {
  transform: translate(3px, 3px);
  box-shadow: none;
}

.chat-composer__action {
  min-width: 7rem;
}

.chat-markdown :deep(*) {
  max-width: 100%;
}

.chat-markdown :deep(p) {
  margin: 0 0 0.75rem;
}

.chat-markdown :deep(p:last-child) {
  margin-bottom: 0;
}

.chat-markdown :deep(pre) {
  overflow-x: auto;
  border: 2px solid var(--neo-border);
  border-radius: var(--neo-radius);
  padding: 0.75rem;
  background: #111111;
  color: #f8f8f2;
  font-size: 0.78rem;
}

.chat-markdown :deep(code) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

.chat-markdown :deep(:not(pre) > code) {
  border: 1px solid var(--neo-border);
  border-radius: 3px;
  padding: 0.08rem 0.28rem;
  background: var(--neo-main);
  color: #050505;
}

.chat-markdown :deep(ul),
.chat-markdown :deep(ol) {
  margin: 0.65rem 0;
  padding-left: 1.4rem;
}

@media (max-width: 1024px) {
  .chat-page {
    min-height: auto;
  }

  .chat-workbench {
    grid-template-columns: 1fr;
  }

  .chat-config {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .chat-config__heading,
  .chat-field--grow,
  .chat-alert,
  .chat-config > .btn,
  .chat-config__status {
    grid-column: 1 / -1;
  }

  .chat-textarea {
    min-height: 7rem;
  }

  .chat-console {
    height: 44rem;
  }
}

@media (max-width: 640px) {
  .chat-page__header {
    align-items: flex-start;
  }

  .chat-page__header h1 {
    font-size: 1.4rem;
  }

  .chat-config {
    display: flex;
  }

  .chat-console {
    height: 39rem;
    min-height: 0;
  }

  .chat-console__toolbar div {
    align-items: flex-start;
    flex-direction: column;
    gap: 0.15rem;
  }

  .chat-message-list {
    padding: 0.75rem;
  }

  .chat-message {
    width: calc(100% - 0.35rem);
  }

  .chat-composer {
    grid-template-columns: 1fr;
  }

  .chat-composer__action {
    min-height: 2.75rem;
  }
}
</style>
