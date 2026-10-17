import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import OpsDashboardHeader from '../OpsDashboardHeader.vue'
import type { OpsDashboardOverview } from '@/api/admin/ops'

vi.mock('@/api', () => ({ adminAPI: { groups: { getAll: vi.fn().mockResolvedValue([]) } } }))
vi.mock('@/api/admin/ops', () => ({ opsAPI: { getRealtimeTrafficSummary: vi.fn().mockResolvedValue({ enabled: true, summary: null }) } }))
vi.mock('@/stores', () => ({ useAdminSettingsStore: () => ({ opsRealtimeMonitoringEnabled: true, setOpsRealtimeMonitoringEnabledLocal: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => params ? `${key}:${JSON.stringify(params)}` : key })
}))

const overview = { tps: { avg: 242.7 } } as OpsDashboardOverview

// 组件 setup 里的 useClipboard 会取 app store（绝运行期由 app.use(pinia) 提供），
// 测试里显式安装 pinia，否则报 getActivePinia() 无活动实例。
beforeEach(() => {
  setActivePinia(createPinia())
})

function mountHeader(value = overview) {
  return mount(OpsDashboardHeader, {
    props: { overview: value, platform: '', groupId: null, timeRange: '1h', queryMode: 'auto', loading: false, lastUpdated: null },
    global: { stubs: { Select: true, HelpTooltip: true, BaseDialog: true, Icon: true } }
  })
}

describe('OpsDashboardHeader output TPS', () => {

  it('shows per-request P50, slower-tail percentiles and sample count separately from traffic TPS', async () => {
    const wrapper = mountHeader({ ...overview, output_tps: { p5: 2.35, p10: 3.7, p50: 55, avg: 277.75, sample_count: 4 } })
    await flushPromises()
    const card = wrapper.get('[data-testid="output-tps-card"]')
    expect(card.get('[data-testid="output-tps-p50"]').text()).toBe('55.0')
    expect(card.get('[data-testid="output-tps-p5"]').text()).toBe('2.4')
    expect(card.get('[data-testid="output-tps-p10"]').text()).toBe('3.7')
    expect(card.get('[data-testid="output-tps-avg"]').text()).toBe('277.8')
    expect(card.get('[data-testid="output-tps-samples"]').text()).toContain('4')
    expect(card.text()).toContain('tok/s (P50)')
    expect(card.text()).not.toContain('242.7')
    await wrapper.setProps({ fullscreen: true, overview: { ...overview, output_tps: { p5: 0.01, p10: 0.02, p50: 0.04, avg: 0.05, sample_count: 1 } } })
    expect(card.get('[data-testid="output-tps-p50"]').text()).toBe('0.0')
    expect(card.get('[data-testid="output-tps-samples"]').text()).toContain('1')
    wrapper.unmount()
  })

  it.each([undefined, null, { p5: null, p10: null, p50: null, avg: null, sample_count: 0 }])('shows missing output TPS as unavailable, not zero: %s', async (output_tps) => {
    const wrapper = mountHeader({ ...overview, output_tps })
    await flushPromises()
    for (const metric of ['p5', 'p10', 'p50', 'avg']) {
      expect(wrapper.get(`[data-testid="output-tps-${metric}"]`).text()).toBe('—')
    }
    expect(wrapper.get('[data-testid="output-tps-samples"]').text()).toContain(output_tps == null ? '—' : '0')
    wrapper.unmount()
  })

})
