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
    authState.isAuthenticated = false
    Object.defineProperty(navigator, 'platform', { configurable: true, value: 'MacIntel' })
    listKeysMock.mockReset()
    listKeysMock.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 1, pages: 0 })
  })

  it('renders official documentation, platform downloads and one-command setup', () => {
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
    expect(wrapper.find('a[href="https://learn.chatgpt.com/docs/app"]').exists()).toBe(true)
    expect(wrapper.find('#quick-start').exists()).toBe(true)
    expect(wrapper.text()).toContain('curl -fL')
    expect(wrapper.text()).toContain('Codex.dmg')
    expect(wrapper.find('[data-testid="download-windows"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="download-linux"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('npx -y sub2api-codex-setup')
    expect(wrapper.text()).toContain('Login required.')
    expect(wrapper.find('#clients').exists()).toBe(false)
  })

  it('shows and copies the Windows installer on a Windows browser', async () => {
    Object.defineProperty(navigator, 'platform', { configurable: true, value: 'Win32' })
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const wrapper = mount(DocsView, {
      global: { stubs: { RouterLink: { template: '<a><slot /></a>' }, Icon: { template: '<span />' } } }
    })
    const download = wrapper.get('[data-testid="download-windows"]')
    const command = download.get('code').text()
    expect(command).toContain('powershell.exe -NoProfile -Command')
    expect(command).toContain('https://get.microsoft.com/installer/download/9PLM9XGG6VKS')
    expect(command).toContain('Start-Process')
    expect(command).not.toContain('$LASTEXITCODE')
    expect(command).not.toContain('\n')
    expect(wrapper.text()).not.toContain('Codex.dmg')
    expect(wrapper.get('[data-testid="setup-command"]').text()).toContain('npx.cmd -y sub2api-codex-setup')
    await download.get('button').trigger('click')
    expect(writeText).toHaveBeenCalledWith(command)
    await wrapper.get('[data-testid="platform-macos"]').trigger('click')
    expect(wrapper.find('[data-testid="download-windows"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="download-macos"]').text()).toContain('Codex.dmg')
    await wrapper.get('[data-testid="platform-linux"]').trigger('click')
    expect(wrapper.find('[data-testid="download-macos"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="download-linux"]').text()).toContain('https://chatgpt.com/codex/install.sh')
    wrapper.unmount()
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

  it('builds and copies a Windows command for installation and data on another drive', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    const wrapper = mount(DocsView, {
      global: { stubs: { RouterLink: { template: '<a><slot /></a>' }, Icon: { template: '<span />' } } }
    })
    await wrapper.get('[data-testid="platform-windows"]').trigger('click')
    await wrapper.get('[data-testid="setup-client-cli"]').trigger('click')
    expect(wrapper.get('[data-testid="setup-install-dir"]').attributes('placeholder')).toBe('D:\\Codex\\app')
    await wrapper.get('[data-testid="setup-install-dir"]').setValue('D:\\AI tools\\app')
    await wrapper.get('[data-testid="setup-codex-home"]').setValue('D:\\AI tools\\data')
    const command = wrapper.get('[data-testid="setup-command"]').text()
    expect(command).toContain('npx.cmd -y sub2api-codex-setup')
    expect(command).toContain('--client cli')
    expect(command).toContain('--install-dir "D:\\AI tools\\app"')
    expect(command).toContain('--codex-home "D:\\AI tools\\data" --persist-home')
    await wrapper.get('#codex-cli > button').trigger('click')
    expect(writeText).toHaveBeenCalledWith(command)
    await wrapper.get('[data-testid="setup-client-desktop"]').trigger('click')
    expect(wrapper.get('[data-testid="setup-command"]').text()).not.toContain('--install-dir')
    await wrapper.get('[data-testid="platform-linux"]').trigger('click')
    expect(wrapper.get('[data-testid="setup-command"]').text()).toContain('--client cli')
    expect(wrapper.get('[data-testid="setup-command"]').text()).not.toContain('--persist-home')
    wrapper.unmount()
  })

  it('allows manual configuration after key retrieval fails', async () => {
    authState.isAuthenticated = true
    listKeysMock.mockRejectedValue(new Error('offline'))
    const wrapper = mount(DocsView, {
      global: { stubs: { RouterLink: { template: '<a><slot /></a>' }, Icon: { template: '<span />' } } }
    })
    await flushPromises()
    expect(wrapper.text()).toContain('Key load failed.')
    await wrapper.get('[data-testid="setup-api-key"]').setValue('sk-recovery')
    expect(wrapper.get('[data-testid="setup-command"]').text()).toContain('--api-key sk-recovery')
  })
})
