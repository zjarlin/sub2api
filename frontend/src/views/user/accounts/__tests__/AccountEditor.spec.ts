import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AccountEditor from '../AccountEditor.vue'
import type { Account } from '@/types'
const { update, create } = vi.hoisted(() => ({ update: vi.fn(), create: vi.fn() }))
vi.mock('@/api/user/accounts', () => ({ default: { update, create } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', async original => ({ ...await original<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
const account = {
  id: 12, name: 'Owned', platform: 'openai', type: 'apikey', status: 'active',
  priority: 0, concurrency: 3, group_ids: [4],
  credentials: { base_url: 'https://owned.example.test', vendor: 'custom', model_mapping: { alias: 'actual', exact: 'exact' } },
  credentials_status: { has_api_key: true }, extra: { custom: true }
} as unknown as Account
function editor(value: Account | null) {
  return mount(AccountEditor, { props: { account: value, groups: [] }, global: { stubs: {
    BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' },
    Select: true, GroupSelector: true, ModelWhitelistSelector: true
  } } })
}
beforeEach(() => { vi.resetAllMocks(); update.mockResolvedValue(account) })
describe('我的账号编辑', () => {
  it('编辑保留隐藏凭证、供应商、混合映射和零优先级', async () => {
    const wrapper = editor(account)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(update).toHaveBeenCalledWith(12, expect.objectContaining({
      priority: 0, credentials: account.credentials, extra: { custom: true }, group_ids: [4], shared: false
    }))
    expect(update.mock.calls[0][1].credentials).not.toHaveProperty('api_key')
    expect(wrapper.emitted('saved')).toHaveLength(1)
  })
  it('仅在勾选共享后提交公共调度状态', async () => {
    const wrapper = editor(account)
    await wrapper.get('input[type="checkbox"]').setValue(true)
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
})
