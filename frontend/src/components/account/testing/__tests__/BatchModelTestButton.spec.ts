import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import BatchModelTestButton from '../BatchModelTestButton.vue'
import type { Account } from '@/types'
const { run, get, update } = vi.hoisted(() => ({ run: vi.fn(), get: vi.fn(), update: vi.fn() }))
vi.mock('@/api/accountTest', () => ({ runModelTest: run }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { getById: get, update } } }))
vi.mock('vue-i18n', async original => ({ ...await original<typeof import('vue-i18n')>(), useI18n: () => ({ t: (key: string) => key }) }))
const account = { id: 1, credentials: { base_url: 'https://upstream.test', vendor: 'custom', model_mapping: { good: 'real', bad: 'bad' } } } as unknown as Account
function modal() { return mount(BatchModelTestButton, { props: { show: true, disabled: false, account, models: [{ id: 'good', display_name: 'Good' }, { id: 'bad', display_name: 'Bad' }] }, global: { stubs: { Icon: true } } }) }
beforeEach(() => { vi.resetAllMocks(); get.mockResolvedValue(account); update.mockResolvedValue(account) })
describe('批量模型测试恢复', () => {
  it('移除失败模型且保留 Base URL、厂商和成功别名', async () => {
    run.mockResolvedValueOnce({ success: true }).mockResolvedValueOnce({ success: false })
    const wrapper = modal(); await wrapper.find('button').trigger('click'); await flushPromises()
    expect(update).toHaveBeenCalledWith(1, { credentials: { base_url: 'https://upstream.test', vendor: 'custom', model_mapping: { good: 'real' } } })
    expect(wrapper.emitted('updated')).toHaveLength(1)
  })
  it('全部失败不清空模型', async () => {
    run.mockResolvedValue({ success: false })
    const wrapper = modal(); await wrapper.find('button').trigger('click'); await flushPromises()
    expect(update).not.toHaveBeenCalled()
  })
  it('部分成功后断流也不提交配置', async () => {
    run.mockResolvedValueOnce({ success: true }).mockRejectedValueOnce(new Error('Incomplete stream'))
    const wrapper = modal(); await wrapper.find('button').trigger('click'); await flushPromises()
    expect(update).not.toHaveBeenCalled()
  })
  it('关闭弹窗会取消请求且不提交配置', async () => {
    let finish!: (value: {success: boolean}) => void
    run.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const wrapper = modal(); await wrapper.find('button').trigger('click'); await wrapper.setProps({ show: false })
    expect(run.mock.calls[0][2].aborted).toBe(true)
    finish({ success: true }); await flushPromises()
    expect(update).not.toHaveBeenCalled()
    expect(run).toHaveBeenCalledTimes(1)
  })
  it('测试等待期间可直接取消，立即恢复按钮并保留配置', async () => {
    run.mockImplementation((_path, _payload, signal: AbortSignal) => new Promise((_resolve, reject) => {
      signal.addEventListener('abort', () => reject(signal.reason))
    }))
    const wrapper = modal()
    await wrapper.find('button').trigger('click')
    const cancel = wrapper.findAll('button').find(button => button.text().includes('cancelTest'))!
    expect(cancel.attributes('disabled')).toBeUndefined()
    await cancel.trigger('click')
    await flushPromises()
    expect(wrapper.find('button').attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).toContain('testCancelledPreserved')
    expect(update).not.toHaveBeenCalled()
    wrapper.unmount()
  })

})
