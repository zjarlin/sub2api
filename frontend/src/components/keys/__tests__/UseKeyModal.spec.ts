import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard: vi.fn().mockResolvedValue(true)
  })
}))

vi.mock('@/api/keys', () => ({
  keysAPI: {
    getCodexModelCatalog: vi.fn().mockResolvedValue({ models: [] })
  }
}))

import UseKeyModal from '../UseKeyModal.vue'
import { keysAPI } from '@/api/keys'

describe('UseKeyModal', () => {
  it('renders GPT-5.5 and goals feature in OpenAI Codex config', () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'openai'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const codeBlocks = wrapper.findAll('pre code').map((code) => code.text())
    const configToml = codeBlocks.find((content) => content.includes('model_provider = "OpenAI"'))

    expect(configToml).toBeDefined()
    expect(configToml).toContain('model = "gpt-5.5"')
    expect(configToml).toContain('review_model = "gpt-5.5"')
    expect(configToml).not.toContain('model = "gpt-5.4"')
    expect(configToml).not.toContain('model_context_window')
    expect(configToml).not.toContain('model_auto_compact_token_limit')
    expect(configToml).toContain('[features]\ngoals = true')
  })

  it('renders a macOS/Linux Codex setup script', () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'openai'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const codeBlocks = wrapper.findAll('pre code').map((code) => code.text())
    const setupScript = codeBlocks.find((content) => content.includes('#!/usr/bin/env bash'))

    expect(setupScript).toBeDefined()
    expect(setupScript).toContain('mkdir -p "$config_dir"')
    expect(setupScript).toContain('cat > "$config_dir/config.toml"')
    expect(setupScript).toContain('cat > "$config_dir/auth.json"')
    expect(setupScript).toContain('model_provider = "OpenAI"')
    expect(setupScript).toContain('"OPENAI_API_KEY": "sk-test"')
  })

  it('writes Codex model catalog into the one-click setup script', async () => {
    vi.mocked(keysAPI.getCodexModelCatalog).mockResolvedValueOnce({
      models: [
        {
          slug: 'deepseek-v4-pro',
          display_name: 'DeepSeek V4 Pro',
          description: 'DeepSeek V4 Pro',
          context_window: 128000,
          max_context_window: 128000,
          visibility: 'list',
          supported_in_api: true,
          priority: 1000
        },
        {
          slug: 'minimax-m3',
          display_name: 'Minimax M3',
          description: 'Minimax M3',
          context_window: 128000,
          max_context_window: 128000,
          visibility: 'list',
          supported_in_api: true,
          priority: 1001
        }
      ]
    })
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKeyId: 123,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'openai'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    await vi.waitFor(() => {
      const codeBlocks = wrapper.findAll('pre code').map((code) => code.text())
      expect(codeBlocks.some((content) => content.includes('model_catalog_json = "sub2api-codex-model-catalog.json"'))).toBe(true)
    })

    const codeBlocks = wrapper.findAll('pre code').map((code) => code.text())
    const setupScript = codeBlocks.find((content) => content.includes('#!/usr/bin/env bash'))
    const catalogFile = codeBlocks.find((content) => content.includes('"slug": "deepseek-v4-pro"'))

    expect(keysAPI.getCodexModelCatalog).toHaveBeenCalledWith(123)
    expect(catalogFile).toContain('"slug": "minimax-m3"')
    expect(setupScript).toContain('cat > "$config_dir/sub2api-codex-model-catalog.json"')
    expect(setupScript).toContain('"slug": "deepseek-v4-pro"')
  })

  it('renders a Windows PowerShell Codex setup script', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'openai'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const windowsTab = wrapper.findAll('button').find((button) =>
      button.text().includes('Windows')
    )

    expect(windowsTab).toBeDefined()
    await windowsTab!.trigger('click')
    await nextTick()

    const codeBlocks = wrapper.findAll('pre code').map((code) => code.text())
    const setupScript = codeBlocks.find((content) => content.includes('$ErrorActionPreference = "Stop"'))

    expect(setupScript).toBeDefined()
    expect(setupScript).toContain('Join-Path $env:USERPROFILE ".codex"')
    expect(setupScript).toContain('[System.IO.File]::WriteAllText((Join-Path $configDir "config.toml")')
    expect(setupScript).toContain('[System.IO.File]::WriteAllText((Join-Path $configDir "auth.json")')
    expect(setupScript).toContain('model_provider = "OpenAI"')
    expect(setupScript).toContain('"OPENAI_API_KEY": "sk-test"')
  })

  it('renders OpenAI Responses Codex config for Gemini groups', () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'gemini'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const codeBlocks = wrapper.findAll('pre code').map((code) => code.text())
    const configToml = codeBlocks.find((content) => content.includes('model_provider = "Gemini"'))

    expect(configToml).toBeDefined()
    expect(configToml).toContain('model = "gemini-2.5-pro"')
    expect(configToml).toContain('wire_api = "responses"')
    expect(configToml).not.toContain('model = "gpt-5.5"')
  })

  it('renders OpenCode Responses config for Gemini groups', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'gemini'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const tab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.opencodeResponses')
    )

    expect(tab).toBeDefined()
    await tab!.trigger('click')
    await nextTick()

    const codeBlock = wrapper.find('pre code')
    expect(codeBlock.exists()).toBe(true)
    expect(codeBlock.text()).toContain('"openai"')
    expect(codeBlock.text()).toContain('"npm": "@ai-sdk/openai"')
    expect(codeBlock.text()).toContain('"Gemini 2.5 Pro"')
    expect(codeBlock.text()).toContain('"store": false')
  })

  it('renders GPT-5.5 and goals feature in OpenAI Codex WebSocket config', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'openai'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const wsTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.codexCliWs')
    )

    expect(wsTab).toBeDefined()
    await wsTab!.trigger('click')
    await nextTick()

    const codeBlocks = wrapper.findAll('pre code').map((code) => code.text())
    const configToml = codeBlocks.find((content) => content.includes('supports_websockets = true'))

    expect(configToml).toBeDefined()
    expect(configToml).toContain('model = "gpt-5.5"')
    expect(configToml).toContain('review_model = "gpt-5.5"')
    expect(configToml).not.toContain('model = "gpt-5.4"')
    expect(configToml).not.toContain('model_context_window')
    expect(configToml).not.toContain('model_auto_compact_token_limit')
    expect(configToml).toContain('[features]\nresponses_websockets_v2 = true\ngoals = true')
  })

  it('renders GPT-5.4 mini entry in OpenCode config', async () => {
    const wrapper = mount(UseKeyModal, {
      props: {
        show: true,
        apiKey: 'sk-test',
        baseUrl: 'https://example.com/v1',
        platform: 'openai'
      },
      global: {
        stubs: {
          BaseDialog: {
            template: '<div><slot /><slot name="footer" /></div>'
          },
          Icon: {
            template: '<span />'
          }
        }
      }
    })

    const opencodeTab = wrapper.findAll('button').find((button) =>
      button.text().includes('keys.useKeyModal.cliTabs.opencode')
    )

    expect(opencodeTab).toBeDefined()
    await opencodeTab!.trigger('click')
    await nextTick()

    const codeBlock = wrapper.find('pre code')
    expect(codeBlock.exists()).toBe(true)
    expect(codeBlock.text()).toContain('"name": "GPT-5.4 Mini"')
    expect(codeBlock.text()).not.toContain('"name": "GPT-5.4 Nano"')
  })
})
