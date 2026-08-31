<template>
  <div class="min-h-screen bg-slate-50 text-slate-950 dark:bg-dark-950 dark:text-white">
    <header class="border-b border-slate-200 bg-white/90 px-6 py-4 backdrop-blur dark:border-dark-800 dark:bg-dark-900/90">
      <nav class="mx-auto flex max-w-6xl items-center justify-between gap-4">
        <router-link to="/home" class="flex min-w-0 items-center gap-3">
          <div class="flex h-10 w-10 shrink-0 items-center justify-center overflow-hidden rounded-lg border border-slate-200 bg-slate-100 dark:border-dark-700 dark:bg-dark-800">
            <img v-if="siteLogo" :src="siteLogo" alt="" class="h-full w-full object-contain" />
            <span v-else class="text-sm font-bold">{{ siteNameInitial }}</span>
          </div>
          <span class="truncate text-base font-semibold">{{ siteName }}</span>
        </router-link>

        <div class="flex items-center gap-2">
          <LocaleSwitcher />
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm font-semibold text-slate-800 transition-colors hover:bg-slate-100 dark:border-dark-700 dark:bg-dark-900 dark:text-white dark:hover:bg-dark-800"
          >
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="mx-auto grid max-w-6xl gap-8 px-6 py-10 lg:grid-cols-[240px_minmax(0,1fr)]">
      <aside class="hidden lg:block">
        <div class="sticky top-24 space-y-2">
          <a
            v-for="section in sections"
            :key="section.id"
            :href="`#${section.id}`"
            class="block rounded-lg px-3 py-2 text-sm font-medium text-slate-600 transition-colors hover:bg-white hover:text-slate-950 dark:text-dark-300 dark:hover:bg-dark-900 dark:hover:text-white"
          >
            {{ section.title }}
          </a>
        </div>
      </aside>

      <article class="min-w-0 space-y-8">
        <section class="rounded-lg border border-slate-200 bg-white p-6 shadow-sm dark:border-dark-800 dark:bg-dark-900">
          <p class="mb-2 text-sm font-semibold uppercase tracking-wide text-primary-600 dark:text-primary-400">
            Sub2API
          </p>
          <h1 class="text-3xl font-bold tracking-tight sm:text-4xl">{{ t('docs.title') }}</h1>
          <p class="mt-4 max-w-3xl text-base leading-7 text-slate-600 dark:text-dark-300">
            {{ t('docs.subtitle') }}
          </p>
        </section>

        <section
          v-for="section in sections"
          :id="section.id"
          :key="section.id"
          class="scroll-mt-24 rounded-lg border border-slate-200 bg-white p-6 shadow-sm dark:border-dark-800 dark:bg-dark-900"
        >
          <div class="mb-5 flex items-start gap-3">
            <div class="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary-50 text-primary-600 dark:bg-primary-500/10 dark:text-primary-300">
              <Icon :name="section.icon" size="sm" />
            </div>
            <div>
              <h2 class="text-xl font-semibold tracking-tight">{{ section.title }}</h2>
              <p class="mt-1 text-sm leading-6 text-slate-500 dark:text-dark-400">{{ section.description }}</p>
            </div>
          </div>

          <div class="space-y-4">
            <div
              v-for="item in section.items"
              :key="item.title"
              class="rounded-lg border border-slate-200 bg-slate-50 p-4 dark:border-dark-800 dark:bg-dark-950"
            >
              <h3 class="text-sm font-semibold text-slate-950 dark:text-white">{{ item.title }}</h3>
              <p class="mt-2 text-sm leading-6 text-slate-600 dark:text-dark-300">{{ item.body }}</p>
              <div v-if="item.links?.length" class="mt-3 flex flex-wrap gap-2">
                <a
                  v-for="link in item.links"
                  :key="link.href"
                  :href="link.href"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="inline-flex items-center gap-1.5 rounded-lg border border-primary-200 bg-primary-50 px-3 py-1.5 text-xs font-semibold text-primary-700 transition-colors hover:border-primary-300 hover:bg-primary-100 dark:border-primary-500/30 dark:bg-primary-500/10 dark:text-primary-200 dark:hover:bg-primary-500/20"
                >
                  <Icon name="download" size="xs" />
                  <span>{{ link.label }}</span>
                </a>
              </div>
              <pre
                v-if="item.code"
                class="mt-3 overflow-x-auto rounded-lg bg-slate-950 p-4 text-sm text-slate-100"
              ><code>{{ item.code }}</code></pre>
            </div>
          </div>
        </section>
      </article>
    </main>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore, useAuthStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'

type DocIcon = 'book' | 'key' | 'document' | 'chart' | 'shield' | 'cog'

interface DocItem {
  title: string
  body: string
  code?: string
  links?: Array<{
    label: string
    href: string
  }>
}

interface DocSection {
  id: string
  icon: DocIcon
  title: string
  description: string
  items: DocItem[]
}

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || '++0 的 API')
const siteLogo = computed(() => appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '')
const siteNameInitial = computed(() => siteName.value.trim().charAt(0).toUpperCase() || 'S')
const isAuthenticated = computed(() => authStore.isAuthenticated)
const dashboardPath = computed(() => (authStore.isAdmin ? '/admin/dashboard' : '/dashboard'))

const sections = computed<DocSection[]>(() => [
  {
    id: 'quick-start',
    icon: 'book',
    title: t('docs.quickStart.title'),
    description: t('docs.quickStart.description'),
    items: [
      {
        title: t('docs.quickStart.items.createKey.title'),
        body: t('docs.quickStart.items.createKey.body'),
      },
      {
        title: t('docs.quickStart.items.assignGroup.title'),
        body: t('docs.quickStart.items.assignGroup.body'),
      },
      {
        title: t('docs.quickStart.items.useKey.title'),
        body: t('docs.quickStart.items.useKey.body'),
      },
    ],
  },
  {
    id: 'codex-cli',
    icon: 'key',
    title: t('docs.codex.title'),
    description: t('docs.codex.description'),
    items: [
      {
        title: t('docs.codex.items.files.title'),
        body: t('docs.codex.items.files.body'),
        code: '~/.codex/config.toml\n~/.codex/auth.json',
      },
      {
        title: t('docs.codex.items.script.title'),
        body: t('docs.codex.items.script.body'),
      },
      {
        title: t('docs.codex.items.download.title'),
        body: t('docs.codex.items.download.body'),
        links: [
          {
            label: t('docs.codex.items.download.links.official'),
            href: 'https://developers.openai.com/codex/app',
          },
          {
            label: t('docs.codex.items.download.links.mac'),
            href: 'https://persistent.oaistatic.com/codex-app-prod/Codex.dmg',
          },
          {
            label: t('docs.codex.items.download.links.macIntel'),
            href: 'https://persistent.oaistatic.com/codex-app-prod/Codex-latest-x64.dmg',
          },
          {
            label: t('docs.codex.items.download.links.windows'),
            href: 'https://get.microsoft.com/installer/download/9PLM9XGG6VKS?cid=website_cta_psi',
          },
        ],
        code: [
          '# Bash',
          'curl -L "https://persistent.oaistatic.com/codex-app-prod/Codex.dmg" -o "Codex.dmg"',
          'curl -L "https://persistent.oaistatic.com/codex-app-prod/Codex-latest-x64.dmg" -o "Codex-latest-x64.dmg"',
          '',
          '# PowerShell',
          'Invoke-WebRequest -Uri "https://get.microsoft.com/installer/download/9PLM9XGG6VKS?cid=website_cta_psi" -OutFile "$env:USERPROFILE\\Downloads\\Codex Installer.exe"',
          'winget install Codex -s msstore',
        ].join('\n'),
      },
      {
        title: t('docs.codex.items.windows.title'),
        body: t('docs.codex.items.windows.body'),
        code: '%USERPROFILE%\\.codex\\config.toml\n%USERPROFILE%\\.codex\\auth.json',
      },
    ],
  },
  {
    id: 'clients',
    icon: 'document',
    title: t('docs.clients.title'),
    description: t('docs.clients.description'),
    items: [
      {
        title: t('docs.clients.items.claude.title'),
        body: t('docs.clients.items.claude.body'),
        code: 'ANTHROPIC_BASE_URL\nANTHROPIC_AUTH_TOKEN',
      },
      {
        title: t('docs.clients.items.gemini.title'),
        body: t('docs.clients.items.gemini.body'),
        code: 'GOOGLE_GEMINI_BASE_URL\nGEMINI_API_KEY\nGEMINI_MODEL',
      },
      {
        title: t('docs.clients.items.opencode.title'),
        body: t('docs.clients.items.opencode.body'),
        code: '~/.config/opencode/opencode.json',
      },
    ],
  },
  {
    id: 'usage',
    icon: 'chart',
    title: t('docs.usage.title'),
    description: t('docs.usage.description'),
    items: [
      {
        title: t('docs.usage.items.query.title'),
        body: t('docs.usage.items.query.body'),
      },
      {
        title: t('docs.usage.items.quota.title'),
        body: t('docs.usage.items.quota.body'),
      },
    ],
  },
  {
    id: 'troubleshooting',
    icon: 'shield',
    title: t('docs.troubleshooting.title'),
    description: t('docs.troubleshooting.description'),
    items: [
      {
        title: t('docs.troubleshooting.items.noGroup.title'),
        body: t('docs.troubleshooting.items.noGroup.body'),
      },
      {
        title: t('docs.troubleshooting.items.baseUrl.title'),
        body: t('docs.troubleshooting.items.baseUrl.body'),
      },
      {
        title: t('docs.troubleshooting.items.secret.title'),
        body: t('docs.troubleshooting.items.secret.body'),
      },
    ],
  },
])
</script>
