import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AccountEditor from '../AccountEditor.vue'
import type { Account } from '@/types'
const { update, create } = vi.hoisted(() => ({ update: vi.fn(), create: vi.fn() }))
vi.mock('@/api/user/accounts', () => ({ default: { update, create } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn() }) }))
vi.mock('@/stores', () => ({ useAuthStore: () => ({ isSimpleMode: false }) }))
vi.mock('vue-i18n', async original => ({ ...await original<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
const account = {
  id: 12, name: 'Owned', platform: 'openai', type: 'apikey', status: 'active',
  priority: 0, concurrency: 3, group_ids: [4], load_factor: null, rate_multiplier: 0.25,
  credentials: { base_url: 'https://owned.example.test', vendor: 'custom', model_mapping: { alias: 'actual', exact: 'exact' } },
  credentials_status: { has_api_key: true }, extra: { custom: true }
} as unknown as Account
function editor(value: Account | null, groups: any[] = [], renderGroupSelector = false) {
  return mount(AccountEditor, { props: { account: value, groups }, global: { stubs: {
    BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' },
    Select: true,
    GroupSelector: renderGroupSelector ? false : true,
    GroupBadge: { props: ['name'], template: '<span>{{ name }}</span>' },
    Icon: true,
    ModelWhitelistSelector: true
  } } })
}
beforeEach(() => { vi.resetAllMocks(); update.mockResolvedValue(account) })
describe('我的账号编辑', () => {
  it('编辑保留隐藏凭证、供应商、混合映射和零优先级', async () => {
    const wrapper = editor(account)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(update).toHaveBeenCalledWith(12, expect.objectContaining({
      priority: 0, load_factor: 0, rate_multiplier: 0.25, expires_at: 0, auto_pause_on_expired: true,
      credentials: account.credentials, extra: { custom: true }, group_ids: [4], shared: false
    }))
    expect(update.mock.calls[0][1].credentials).not.toHaveProperty('api_key')
    expect(wrapper.emitted('saved')).toHaveLength(1)
  })
  it('仅在勾选共享后提交公共调度状态', async () => {
    const wrapper = editor(account)
    await wrapper.get('[data-testid="owned-account-shared"]').setValue(true)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(update).toHaveBeenCalledWith(12, expect.objectContaining({ shared: true }))
  })
  it('非法 JSON 阻止保存并展示错误', async () => {
    const wrapper = editor(account)
    await wrapper.findAll('textarea')[1].setValue('[')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(update).not.toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toContain('invalidCredentialsJson')
  })

  it('Kimi 可直接选择 Codex 分组并保留既有 extra', async () => {
    const kimiAccount = {
      ...account,
      platform: 'kimi',
      extra: { custom: true, mixed_scheduling: true },
      group_ids: [4]
    } as unknown as Account
    const groups = [
      { id: 4, name: 'Kimi', platform: 'kimi', status: 'active' },
      { id: 9, name: 'Codex', platform: 'openai', status: 'active' },
      { id: 10, name: 'Claude', platform: 'anthropic', status: 'active' }
    ] as any[]
    const wrapper = editor(kimiAccount, groups, true)

    expect(wrapper.find('[data-testid="mixed-scheduling-toggle"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="automatic-mixed-scheduling-hint"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('Codex')
    expect(wrapper.text()).not.toContain('Claude')

    await wrapper.get('input[type="checkbox"][value="9"]').setValue(true)
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(update).toHaveBeenCalledWith(12, expect.objectContaining({
      group_ids: [4, 9],
      extra: { custom: true, mixed_scheduling: true }
    }))
  })
})
