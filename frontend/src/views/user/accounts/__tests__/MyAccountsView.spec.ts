import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import MyAccountsView from '../MyAccountsView.vue'
import type { Account } from '@/types'

const { list, update, getAvailable, showError } = vi.hoisted(() => ({
  list: vi.fn(), update: vi.fn(), getAvailable: vi.fn(), showError: vi.fn()
}))
vi.mock('@/api/user/accounts', () => ({ default: { list, update } }))
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
      props: ['data'],
      template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-shared" :row="row" /></div></div>'
    },
    SearchInput: true, Select: true, AccountTableActions: true, Pagination: true,
    AccountEditor: true, Icon: true
  } } })
}

beforeEach(() => {
  vi.resetAllMocks()
  list.mockResolvedValue({ items: [{ ...account }], total: 1, page: 1, page_size: 20, pages: 1 })
  getAvailable.mockResolvedValue([])
})

describe('我的账号共享开关', () => {
  it('提交当前账号的共享状态', async () => {
    update.mockResolvedValue({ ...account, shared: true })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await flushPromises()
    expect(update).toHaveBeenCalledWith(12, { shared: true })
    expect(wrapper.get('input[type="checkbox"]').element.checked).toBe(true)
    wrapper.unmount()
  })

  it('提交失败后恢复原状态', async () => {
    update.mockRejectedValue(new Error('offline'))
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await flushPromises()
    expect(wrapper.get('input[type="checkbox"]').element.checked).toBe(false)
    expect(showError).toHaveBeenCalled()
    wrapper.unmount()
  })
})
