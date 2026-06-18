import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import DocsView from '../DocsView.vue'

const { authState } = vi.hoisted(() => ({
  authState: {
    isAuthenticated: false,
    isAdmin: false,
  },
}))

const messages: Record<string, string> = {
  'docs.title': 'Documentation',
  'docs.subtitle': 'Complete client setup flow.',
  'docs.quickStart.title': 'Quick Start',
  'docs.quickStart.description': 'Start here.',
  'docs.quickStart.items.createKey.title': 'Create an API key',
  'docs.quickStart.items.createKey.body': 'Create a key from API Keys.',
  'docs.quickStart.items.assignGroup.title': 'Assign a group',
  'docs.quickStart.items.assignGroup.body': 'Assign a group before setup.',
  'docs.quickStart.items.useKey.title': 'Open Use Key',
  'docs.quickStart.items.useKey.body': 'Copy generated config.',
  'docs.codex.title': 'Codex CLI Configuration',
  'docs.codex.description': 'Codex setup.',
  'docs.codex.items.files.title': 'Config file locations',
  'docs.codex.items.files.body': 'Write config files.',
  'docs.codex.items.script.title': 'One-click setup script',
  'docs.codex.items.script.body': 'Use the generated script.',
  'docs.codex.items.windows.title': 'Windows paths',
  'docs.codex.items.windows.body': 'Use PowerShell.',
  'docs.clients.title': 'Other Clients',
  'docs.clients.description': 'Client setup.',
  'docs.clients.items.claude.title': 'Claude Code',
  'docs.clients.items.claude.body': 'Claude env vars.',
  'docs.clients.items.gemini.title': 'Gemini CLI',
  'docs.clients.items.gemini.body': 'Gemini env vars.',
  'docs.clients.items.opencode.title': 'OpenCode',
  'docs.clients.items.opencode.body': 'OpenCode config.',
  'docs.usage.title': 'Usage Query',
  'docs.usage.description': 'Usage page.',
  'docs.usage.items.query.title': 'Query entry',
  'docs.usage.items.query.body': 'Open usage page.',
  'docs.usage.items.quota.title': 'Quota and limits',
  'docs.usage.items.quota.body': 'Inspect limits.',
  'docs.troubleshooting.title': 'Troubleshooting',
  'docs.troubleshooting.description': 'Check basics.',
  'docs.troubleshooting.items.noGroup.title': 'Assign a group first',
  'docs.troubleshooting.items.noGroup.body': 'Bind a group.',
  'docs.troubleshooting.items.baseUrl.title': 'Client cannot connect',
  'docs.troubleshooting.items.baseUrl.body': 'Check base_url.',
  'docs.troubleshooting.items.secret.title': 'Key safety',
  'docs.troubleshooting.items.secret.body': 'Do not commit keys.',
  'home.dashboard': 'Dashboard',
  'home.login': 'Login',
}

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => messages[key] ?? key,
  }),
}))

vi.mock('@/components/common/LocaleSwitcher.vue', () => ({
  default: {
    name: 'LocaleSwitcher',
    template: '<div />',
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    cachedPublicSettings: {
      site_name: 'Sub2API',
      site_logo: '',
    },
    siteName: 'Sub2API',
    siteLogo: '',
  }),
  useAuthStore: () => authState,
}))

describe('DocsView', () => {
  it('renders built-in API key and client setup documentation', () => {
    const wrapper = mount(DocsView, {
      global: {
        stubs: {
          RouterLink: {
            props: ['to'],
            template: '<a><slot /></a>',
          },
          LocaleSwitcher: {
            template: '<div />',
          },
          Icon: {
            template: '<span />',
          },
        },
      },
    })

    expect(wrapper.text()).toContain('Documentation')
    expect(wrapper.text()).toContain('Create an API key')
    expect(wrapper.text()).toContain('Codex CLI Configuration')
    expect(wrapper.text()).toContain('~/.codex/config.toml')
    expect(wrapper.text()).toContain('setup script')
    expect(wrapper.text()).toContain('OpenCode')
    expect(wrapper.text()).toContain('Usage Query')
  })
})
