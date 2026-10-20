import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import type { OpsDashboardOverview } from '@/api/admin/ops'
import { flushPromises, mount, shallowMount } from '@vue/test-utils'
import OpsDashboardHeader from '../OpsDashboardHeader.vue'
import OpsRequestDetailsModal from '../OpsRequestDetailsModal.vue'
import { resetAccountNameCacheForTests } from '../../utils/accountNameMap'

const { listRequestDetails, listAccounts, viewport } = vi.hoisted(() => ({
  listRequestDetails: vi.fn(),
  listAccounts: vi.fn(),
  viewport: { desktop: true },
}))

vi.mock('@vueuse/core', () => ({ useMediaQuery: () => ref(viewport.desktop) }))
vi.mock('@/api/admin/ops', () => ({ opsAPI: { listRequestDetails } }))
vi.mock('@/api/admin/accounts', () => ({ accountsAPI: { list: listAccounts } }))
vi.mock('@/api', () => ({ adminAPI: { groups: { getAll: vi.fn().mockResolvedValue([]) } } }))
vi.mock('@/stores', () => ({
  useAppStore: () => ({ showError: vi.fn() }),
  useAdminSettingsStore: () => ({ opsRealtimeMonitoringEnabled: false }),
}))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

async function openDetails(sort: 'duration_desc' | 'ttft_desc') {
  const wrapper = mount(OpsRequestDetailsModal, {
    props: { modelValue: false, timeRange: '1h', preset: { title: 'Details', sort } },
    global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, Pagination: true } },
  })
  await wrapper.setProps({ modelValue: true })
  await flushPromises()
  return wrapper
}

describe('Ops request latency details', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    resetAccountNameCacheForTests()
    viewport.desktop = true
    listRequestDetails.mockResolvedValue({
      items: [
        { kind: 'success', created_at: '2026-09-10T00:00:00Z', duration_ms: 12000, first_token_ms: 800, account_id: 101 },
        { kind: 'success', created_at: '2026-09-10T00:00:01Z', duration_ms: 9000, first_token_ms: 0, account_id: 102 },
        { kind: 'success', created_at: '2026-09-10T00:00:02Z', duration_ms: 5000, first_token_ms: null, account_id: 999 },
      ],
      total: 3,
    })
    listAccounts.mockResolvedValue({
      items: [
        { id: 101, name: 'acc-alpha' },
        { id: 102, name: 'acc-beta' },
      ],
      total: 2,
    })
  })

  it('shows recovered requests separately and opens their dedicated list', async () => {
    const wrapper = shallowMount(OpsDashboardHeader, {
      props: { overview: { recovered_success_count: 12 } as OpsDashboardOverview, platform: '', groupId: null, timeRange: '1h', queryMode: 'auto', loading: false, lastUpdated: null },
    })
    await flushPromises()
    const card = wrapper.get('[data-testid="recovered-success-card"]')
    expect(card.text()).toContain('12')
    expect(card.text()).toContain('admin.ops.recoveredSuccess')
    await card.get('button').trigger('click')
    expect(wrapper.emitted('openRecoveredDetails')).toEqual([[]])
    expect(wrapper.emitted('openErrorDetails')).toBeUndefined()
    wrapper.unmount()
  })

  it('opens the TTFT card with first-token sorting and successful requests', async () => {
    const wrapper = shallowMount(OpsDashboardHeader, {
      props: { overview: {} as OpsDashboardOverview, platform: '', groupId: null, timeRange: '1h', queryMode: 'auto', loading: false, lastUpdated: null },
    })
    await flushPromises()
    const button = wrapper.findAll('button').find((item) =>
      item.text() === 'admin.ops.requestDetails.details' &&
      item.element.parentElement?.textContent?.includes('TTFT'),
    )
    expect(button).toBeDefined()
    await button!.trigger('click')
    expect(wrapper.emitted('openRequestDetails')).toEqual([[
      { title: 'admin.ops.ttftLabel', kind: 'success', sort: 'ttft_desc' },
    ]])
    wrapper.unmount()
  })

  it.each([true, false])('shows TTFT rather than total duration (desktop: %s)', async (desktop) => {
    viewport.desktop = desktop
    const wrapper = await openDetails('ttft_desc')
    expect(listRequestDetails).toHaveBeenCalledWith(expect.objectContaining({ sort: 'ttft_desc' }))
    expect(wrapper.text()).toContain('admin.ops.ttftLabel')
    expect(wrapper.text()).toContain('800 ms')
    expect(wrapper.text()).toContain('0 ms')
    expect(wrapper.text()).not.toContain('12000 ms')
    expect(wrapper.text()).not.toContain('9000 ms')
    expect(wrapper.text()).not.toContain('5000 ms')
    // fork 双列（登录账号 + 调度账号）下延迟列是第 7 列；第三行 first_token_ms 为 null。
    if (desktop) expect(wrapper.findAll('tbody tr')[2].findAll('td')[6].text()).toBe('-')
    else expect(wrapper.text()).toContain('admin.ops.ttftLabel: -')
    wrapper.unmount()
  })

  // fork 保留「登录账号 + 调度账号」双列（上游为单列 account）。
  it('keeps the fork login/dispatch account columns with id fallback', async () => {
    const wrapper = await openDetails('ttft_desc')
    expect(wrapper.text()).toContain('admin.ops.requestDetails.table.loginAccount')
    expect(wrapper.text()).toContain('admin.ops.requestDetails.table.dispatchAccount')
    // 桌面表格：第 5 列登录账号、第 6 列调度账号。
    // 明细行只带 account_id 时，调度账号回退到 lite 账号名映射，缺项再回退 #id。
    const dispatchCells = wrapper.findAll('tbody tr').map(tr => tr.findAll('td')[5].text())
    expect(dispatchCells).toEqual(['acc-alpha', 'acc-beta', '#999'])
    // 999 不在账号映射里，应触发一次强制刷新重建缓存
    expect(listAccounts).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  // 移动端卡片用 dispatchAccount(row) || accountLabel(row)：账号名来自 lite 账号映射，
  // 缺项时回退到 #id。
  it('falls back to the account-name map and then the raw id on small screens', async () => {
    viewport.desktop = false
    const wrapper = await openDetails('ttft_desc')
    expect(wrapper.text()).toContain('acc-alpha')
    expect(wrapper.text()).toContain('acc-beta')
    expect(wrapper.text()).toContain('#999')
    expect(wrapper.text()).toContain('admin.ops.requestDetails.table.dispatchAccount')
    expect(listAccounts).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('keeps total duration for duration details', async () => {
    const wrapper = await openDetails('duration_desc')
    expect(listRequestDetails).toHaveBeenCalledWith(expect.objectContaining({ sort: 'duration_desc' }))
    expect(wrapper.text()).toContain('admin.ops.requestDetails.table.duration')
    expect(wrapper.text()).toContain('12000 ms')
    expect(wrapper.text()).not.toContain('800 ms')
    wrapper.unmount()
  })

  it('searches request details by model and dispatch account', async () => {
    const wrapper = await openDetails('duration_desc')
    const modelInput = wrapper.get('[data-testid="model-search"]')
    await modelInput.setValue('deepseek-v4.1')
    await modelInput.trigger('keyup.enter')
    await flushPromises()
    expect(listRequestDetails).toHaveBeenLastCalledWith(expect.objectContaining({ model_query: 'deepseek-v4.1' }))

    const dispatchInput = wrapper.get('[data-testid="dispatch-search"]')
    await dispatchInput.setValue('yueyue_workbuddy')
    await dispatchInput.trigger('keyup.enter')
    await flushPromises()
    expect(listRequestDetails).toHaveBeenLastCalledWith(expect.objectContaining({
      model_query: 'deepseek-v4.1',
      account_name: 'yueyue_workbuddy'
    }))
    wrapper.unmount()
  })

  it('clears each search filter independently', async () => {
    const wrapper = await openDetails('duration_desc')
    const modelInput = wrapper.get('[data-testid="model-search"]')
    const dispatchInput = wrapper.get('[data-testid="dispatch-search"]')
    await modelInput.setValue('deepseek')
    await dispatchInput.setValue('yueyue')
    await dispatchInput.trigger('keyup.enter')
    await flushPromises()

    await wrapper.get('[data-testid="model-search"] + button').trigger('click')
    await dispatchInput.trigger('keyup.enter')
    await flushPromises()
    expect(listRequestDetails).toHaveBeenLastCalledWith(expect.not.objectContaining({ model_query: expect.anything() }))
    expect(listRequestDetails).toHaveBeenLastCalledWith(expect.objectContaining({ account_name: 'yueyue' }))
    wrapper.unmount()
  })

  it('shows the concrete Auto failure model below the requested model', async () => {
    listRequestDetails.mockResolvedValue({
      items: [{
        kind: 'error',
        created_at: '2026-09-10T00:00:02Z',
        request_id: 'req-auto',
        platform: 'openai',
        model: 'glm-5.2',
        requested_model: 'auto',
        upstream_model: 'glm-5.2',
        error_model: 'glm-5.2',
        account_id: 851,
        account_name: 'zjarlin_commandcode',
        status_code: 502,
      }],
      total: 1,
    })

    const wrapper = await openDetails('duration_desc')
    const modelCell = wrapper.findAll('tbody tr')[0].findAll('td')[3]
    expect(modelCell.text()).toContain('auto')
    expect(modelCell.text()).toContain('glm-5.2')
    expect(modelCell.find('.text-red-600').exists()).toBe(true)
    expect(wrapper.text()).toContain('admin.ops.requestDetails.table.dispatchAccount')
    expect(wrapper.text()).toContain('zjarlin_commandcode #851')
    wrapper.unmount()
  })
})
