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
      template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-shared" :row="row" /></div></div>'
    },
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
