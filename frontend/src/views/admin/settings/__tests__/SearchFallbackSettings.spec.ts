import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import SearchFallbackSettings from '../SearchFallbackSettings.vue'
import { getSearchFallbackPolicy, updateSearchFallbackPolicy, getSearchProbeCandidates, probeSearchCapability } from '@/api/admin/settings'
vi.mock('@/api/admin/settings', () => ({ getSearchFallbackPolicy: vi.fn(), updateSearchFallbackPolicy: vi.fn(), getSearchProbeCandidates: vi.fn(), probeSearchCapability: vi.fn() }))
vi.mock('@/api/admin/groups', () => ({ list: vi.fn(async () => ({ items: [{ id: 6, name: 'codex' }] })) }))
const evidence = { account_id: 820, account_name: 'source', model: 'search-model', upstream_model: 'search-model', route_fingerprint: 'hash', status: 'supported' as const, checked_at: '2026-10-05T08:00:00Z', source_urls: ['https://go.dev', 'javascript:alert(1)'] }
const initial = { enabled: true, models: [], require_verified: false, candidate_timeout_seconds: 45, timeout_seconds: 120, probe_results: [] }
async function render() {
  const wrapper = mount(SearchFallbackSettings, { global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: {}, missingWarn: false, fallbackWarn: false })] } })
  await flushPromises()
  return wrapper
}
beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getSearchFallbackPolicy).mockResolvedValue(structuredClone(initial))
  vi.mocked(updateSearchFallbackPolicy).mockImplementation(async policy => policy)
  vi.mocked(getSearchProbeCandidates).mockResolvedValue([{ account_id: 820, account_name: 'source', model: 'search-model', upstream_model: 'search-model' }])
  vi.mocked(probeSearchCapability).mockResolvedValue(evidence)
})
describe('SearchFallbackSettings', () => {
  it('probes real candidates and configures only successful results', async () => {
    const wrapper = await render()
    await wrapper.get('select').setValue(6)
    vi.mocked(getSearchFallbackPolicy).mockResolvedValueOnce({ ...initial, probe_results: [evidence, { ...evidence, account_id: 877, model: 'plain-answer', status: 'unverified' }] })
    await wrapper.findAll('button').find(button => button.text().includes('searchFallback.probe'))!.trigger('click')
    await flushPromises()
    expect(probeSearchCapability).toHaveBeenCalledWith(6, expect.objectContaining({ account_id: 820 }), expect.any(AbortSignal))
    expect(updateSearchFallbackPolicy).toHaveBeenCalledWith(expect.objectContaining({ require_verified: true, models: ['search-model'] }))
    expect(wrapper.findAll('a').map(link => link.attributes('href'))).toEqual(['https://go.dev', 'https://go.dev'])
    expect(wrapper.text()).toContain('searchFallback.unverified')
  })
  it('does not save invalid model order or timeout and retains errors', async () => {
    const wrapper = await render()
    await wrapper.get('textarea').setValue('a\na')
    expect(wrapper.get('.btn-primary').attributes('disabled')).toBeDefined()
    await wrapper.get('textarea').setValue('a')
    vi.mocked(updateSearchFallbackPolicy).mockRejectedValueOnce(new Error('offline'))
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    expect(wrapper.get('textarea').element.value).toBe('a')
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
  })
})
