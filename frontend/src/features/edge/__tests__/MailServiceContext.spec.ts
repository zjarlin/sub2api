import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import MailServiceContext from '../MailServiceContext.vue'

const { list, remove, proxies, groups, post } = vi.hoisted(() => ({
  list: vi.fn(),
  remove: vi.fn(),
  proxies: vi.fn(),
  groups: vi.fn(),
  post: vi.fn(),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: { list, delete: remove },
    proxies: { getAll: proxies },
    groups: { getAllIncludingInactive: groups },
  },
}))
vi.mock('@/api/client', () => ({ apiClient: { post } }))
vi.mock('@/utils/apiError', () => ({ extractApiErrorMessage: (_cause: unknown, fallback: string) => fallback }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => (params?.email ? `${key}:${params.email}` : key),
  }),
}))
vi.mock('@/components/icons/Icon.vue', () => ({ default: { template: '<span />' } }))
vi.mock('@/components/account/MailAccountModal.vue', () => ({
  default: {
    name: 'MailAccountModal',
    props: ['show', 'account', 'proxies', 'groups'],
    template: '<div data-testid="mail-modal" />',
  },
}))

function mailAccount(id: number, email: string, provider: string, extra: Record<string, unknown> = {}) {
  return {
    id,
    name: email,
    platform: 'mail',
    type: 'apikey',
    status: 'active',
    credentials: { email, mail_provider: provider },
    proxy_id: null,
    group_ids: [],
    ...extra,
  }
}

function mountContext() {
  return mount(MailServiceContext)
}

describe('MailServiceContext', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    list.mockResolvedValue({ items: [mailAccount(5, 'me@outlook.com', 'outlook')], total: 1, page: 1, page_size: 100, pages: 1 })
    proxies.mockResolvedValue([])
    groups.mockResolvedValue([])
    post.mockResolvedValue({ data: { ok: true } })
    remove.mockResolvedValue({ message: 'ok' })
    vi.stubGlobal('confirm', vi.fn(() => true))
  })

  it('loads mail accounts from the mail platform and hides credentials', async () => {
    const wrapper = mountContext()
    await flushPromises()

    expect(list).toHaveBeenCalledWith(1, 100, { platform: 'mail' })
    const row = wrapper.get('[data-testid="edge-mail-account-5"]')
    expect(row.text()).toContain('me@outlook.com')
    expect(row.text()).toContain('Outlook')
    // 凭据不进 DOM
    expect(wrapper.html()).not.toContain('api_key')
    expect(wrapper.find('[data-testid="edge-mail-add"]').exists()).toBe(true)
  })

  it('shows the empty state with an add entry when no mailbox is configured', async () => {
    list.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 100, pages: 0 })
    const wrapper = mountContext()
    await flushPromises()

    expect(wrapper.text()).toContain('admin.vision.mailContext.emptyTitle')
    expect(wrapper.find('[data-testid="edge-mail-empty-add"]').exists()).toBe(true)
  })

  it('runs a send/receive self-check through the account mail action', async () => {
    const wrapper = mountContext()
    await flushPromises()

    await wrapper.get('[data-testid="edge-mail-test"]').trigger('click')
    await flushPromises()

    expect(post.mock.calls[0][0]).toBe('/admin/accounts/5/mail/folders')
    const [sendPath, sendBody] = post.mock.calls[1]
    expect(sendPath).toBe('/admin/accounts/5/mail/send')
    expect((sendBody as { to: string[] }).to).toEqual(['me@outlook.com'])
    expect(wrapper.text()).toContain('admin.vision.mailContext.testPassed')
  })

  it('deletes a mailbox account after confirmation', async () => {
    const wrapper = mountContext()
    await flushPromises()

    await wrapper.get('[data-testid="edge-mail-delete"]').trigger('click')
    await flushPromises()

    expect(remove).toHaveBeenCalledWith(5)
    expect(list).toHaveBeenCalledTimes(2)
  })

  it('surfaces the failure message when the self-check fails', async () => {
    post.mockRejectedValue(new Error('boom'))
    const wrapper = mountContext()
    await flushPromises()

    await wrapper.get('[data-testid="edge-mail-test"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('admin.vision.mailContext.testFailed')
  })
})
