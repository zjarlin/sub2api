import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import MyAccountsView from '../MyAccountsView.vue'
import type { Account } from '@/types'

const { list, update, getAvailable, showError } = vi.hoisted(() => ({
  list: vi.fn(), update: vi.fn(), getAvailable: vi.fn(), showError: vi.fn()
}))
vi.mock('@/api/user/accounts', () => ({ default: {
  list, update, getAccountRebateSummary: vi.fn().mockResolvedValue({})
} }))
vi.mock('@/api/groups', () => ({ userGroupsAPI: { getAvailable } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', async original => ({
  ...await original<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))

const account = { id: 12, name: 'Owned', shared: false } as Account

function mountView() {
  return mount(MyAccountsView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    TablePageLayout: { template: '<div><slot name="actions"/><slot name="filters"/><slot name="table"/><slot name="pagination"/></div>' },
    DataTable: {
      name: 'DataTable',
      props: ['data', 'columns'],
      template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-shared" :row="row" /><slot name="cell-capacity" :row="row" /></div></div>'
    },
    BuiltinCheckinCell: { name: 'BuiltinCheckinCell', props: ['account', 'scope'], template: '<div data-test="builtin-checkin-cell" />' },
    BuiltinCheckinDialog: { name: 'BuiltinCheckinDialog', props: ['show', 'account', 'scope'], template: '<div data-test="builtin-checkin-dialog" />' },
    SearchInput: true, Select: true, AccountTableActions: true, Pagination: true,
    CreateAccountModal: true, EditAccountModal: true, Icon: true
  } } })
}

beforeEach(() => {
  vi.resetAllMocks()
  list.mockResolvedValue({ items: [{ ...account }], total: 1, page: 1, page_size: 20, pages: 1 })
  getAvailable.mockResolvedValue([])
})

describe('我的账号默认共享', () => {
  it('移除共享列和开关', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'DataTable' }).props('columns')).not.toEqual(
      expect.arrayContaining([expect.objectContaining({ key: 'shared' })])
    )
    expect(update).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})

describe('我的账号内置适配器积分', () => {
  const workbuddy = { id: 905, name: 'yueyue_workbuddy', platform: 'workbuddy', type: 'apikey' } as Account

  it('为 WorkBuddy / TRAE Work 账号渲染用户侧积分单元格与明细弹窗', async () => {
    list.mockResolvedValue({ items: [{ ...workbuddy }], total: 1, page: 1, page_size: 20, pages: 1 })
    const wrapper = mountView()
    await flushPromises()

    const cell = wrapper.findComponent({ name: 'BuiltinCheckinCell' })
    expect(cell.exists()).toBe(true)
    // 普通用户只能读自己的账号池积分，必须走 /user/accounts/:id/sidecar/checkins。
    expect(cell.props('scope')).toBe('user')
    expect((cell.props('account') as Account).id).toBe(905)

    cell.vm.$emit('open', wrapper.findComponent({ name: 'DataTable' }).props('data')[0])
    await flushPromises()
    const dialog = wrapper.findComponent({ name: 'BuiltinCheckinDialog' })
    expect(dialog.props('show')).toBe(true)
    expect(dialog.props('scope')).toBe('user')
    expect((dialog.props('account') as Account).id).toBe(905)
    wrapper.unmount()
  })

  it('其它平台不渲染积分单元格', async () => {
    list.mockResolvedValue({ items: [{ id: 12, name: 'Owned', platform: 'openai', type: 'apikey' } as Account], total: 1, page: 1, page_size: 20, pages: 1 })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.findComponent({ name: 'BuiltinCheckinCell' }).exists()).toBe(false)
    expect(wrapper.findComponent({ name: 'BuiltinCheckinDialog' }).props('show')).toBe(false)
    wrapper.unmount()
  })
})
