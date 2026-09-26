import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import DocsView from '../DocsView.vue'

const { authState } = vi.hoisted(() => ({
  authState: {
    isAuthenticated: false,
    isAdmin: false,
  },
}))

const { listKeysMock } = vi.hoisted(() => ({
  listKeysMock: vi.fn(),
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
  'docs.codex.items.download.title': 'Install and configure Codex automatically',
  'docs.codex.items.download.body': 'Use the npm one-command setup.',
  'docs.codex.items.setupCommand.loading': 'Loading key.',
  'docs.codex.items.setupCommand.error': 'Key load failed.',
  'docs.codex.items.setupCommand.loginRequired': 'Login required.',
  'docs.codex.items.setupCommand.noKey': 'No available API key.',
  'docs.codex.items.setupCommand.createKey': 'Create API key',
  'docs.codex.items.setupCommand.usingKey': 'Using {name}',
  'docs.codex.items.setupCommand.manualKeyLabel': 'Paste your API key',
  'docs.codex.items.setupCommand.manualKeyPlaceholder': 'sk-...',
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
  'common.copy': 'Copy',
  'common.copied': 'Copied',
}

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, string>) => {
      const value = messages[key] ?? key
      return value.replace(/\{(\w+)\}/g, (_, name: string) => params?.[name] ?? `{${name}}`)
    },
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

vi.mock('@/api/keys', () => ({
  keysAPI: {
    list: listKeysMock,
  },
}))

describe('DocsView', () => {
  beforeEach(() => {
    listKeysMock.mockReset()
    listKeysMock.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 1, pages: 0 })
  })

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
    expect(wrapper.text()).toContain('Install and configure Codex automatically')
    expect(wrapper.text()).toContain('Login required.')
    expect(wrapper.text()).toContain('OpenCode')
    expect(wrapper.text()).toContain('Usage Query')
  })

  it('renders the current user key setup command for an authenticated user', async () => {
    authState.isAuthenticated = true
    listKeysMock.mockResolvedValue({
      items: [{ id: 1, name: 'Current key', key: 'sk-current' }],
      total: 1,
      page: 1,
      page_size: 1,
      pages: 1,
    })

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
    await flushPromises()

    expect(listKeysMock).toHaveBeenCalledWith(1, 1, {
      status: 'active',
      sort_by: 'created_at',
      sort_order: 'desc'
    })
    expect(wrapper.text()).toContain('npx -y sub2api-codex-setup')
    expect(wrapper.text()).toContain('--api-key sk-current')
    expect(wrapper.text()).toContain('Using Current key')
    authState.isAuthenticated = false
  })

  it('prompts authenticated users without available keys to create one', async () => {
    authState.isAuthenticated = true
    listKeysMock.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 1, pages: 0 })

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
    await flushPromises()

    expect(wrapper.text()).toContain('No available API key.')
    expect(wrapper.text()).toContain('Create API key')
    expect(wrapper.text()).toContain('Paste your API key')
    expect(wrapper.text()).toContain('--api-key sk-xxxx')
    const input = wrapper.find('input')
    expect(input.exists()).toBe(true)
    await input.setValue('sk-pasted')
    expect(wrapper.text()).toContain('--api-key sk-pasted')
    expect(wrapper.text()).not.toContain('Login required.')
    authState.isAuthenticated = false
  })

  it('lets anonymous users paste a key to build the setup command', async () => {
    authState.isAuthenticated = false

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
    await flushPromises()

    expect(wrapper.text()).toContain('Login required.')
    expect(wrapper.text()).toContain('Paste your API key')

    const input = wrapper.find('input')
    expect(input.exists()).toBe(true)
    await input.setValue('sk-manual')

    expect(wrapper.text()).toContain('--api-key sk-manual')
  })
})
