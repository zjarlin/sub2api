import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import BuiltinAdapterLogin from '../BuiltinAdapterLogin.vue'

const { start, complete, cancel } = vi.hoisted(() => ({ start: vi.fn(), complete: vi.fn(), cancel: vi.fn() }))
vi.mock('@/api/admin/builtinAdapters', () => ({ startBuiltinLogin: start, completeBuiltinLogin: complete, cancelBuiltinLogin: cancel }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))

const pending = (mode: 'poll' | 'callback') => ({ session_id: 'abc', mode, status: 'pending', auth_url: 'https://example.com/login', expires_at: Date.now() + 600000 })

describe('BuiltinAdapterLogin', () => {
  beforeEach(() => { vi.useFakeTimers(); vi.clearAllMocks(); cancel.mockResolvedValue(undefined) })
  afterEach(() => { vi.useRealTimers() })

  it('submits the TRAE callback and clears credentials after success', async () => {
    start.mockResolvedValue(pending('callback'))
    complete.mockResolvedValue({ ...pending('callback'), status: 'completed', account: { uid: 'u1' } })
    const wrapper = mount(BuiltinAdapterLogin, { props: { platform: 'traework' } })
    await wrapper.get('button').trigger('click'); await flushPromises()
    expect(wrapper.get('a').attributes('href')).toBe('https://example.com/login')
    await wrapper.get('input').setValue('http://127.0.0.1:18080/authorize?refreshToken=secret')
    await wrapper.get('button.btn-primary').trigger('click'); await flushPromises()
    expect(complete).toHaveBeenCalledWith('traework', expect.anything(), expect.stringContaining('refreshToken=secret'), expect.any(AbortSignal))
    expect(wrapper.text()).toContain('admin.accounts.builtinLogin.success')
    expect(wrapper.find('input').exists()).toBe(false)
    wrapper.unmount()
  })

  it('polls WorkBuddy and stops after success', async () => {
    start.mockResolvedValue(pending('poll'))
    complete.mockResolvedValueOnce(pending('poll')).mockResolvedValueOnce({ ...pending('poll'), status: 'completed', account: { uid: 'u2' } })
    const wrapper = mount(BuiltinAdapterLogin, { props: { platform: 'workbuddy' } })
    await wrapper.get('button').trigger('click'); await flushPromises()
    expect(wrapper.find('input').exists()).toBe(false)
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(complete).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(10000)
    expect(complete).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('does not leave polling active after unmount', async () => {
    start.mockResolvedValue(pending('poll'))
    const wrapper = mount(BuiltinAdapterLogin, { props: { platform: 'workbuddy' } })
    await wrapper.get('button').trigger('click'); await flushPromises()
    wrapper.unmount(); await vi.advanceTimersByTimeAsync(10000)
    expect(complete).not.toHaveBeenCalled()
  })

  it('shows errors and permits a new authorization', async () => {
    start.mockRejectedValue(new Error('Adapter unavailable'))
    const wrapper = mount(BuiltinAdapterLogin, { props: { platform: 'workbuddy' } })
    await wrapper.get('button').trigger('click'); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('Adapter unavailable')
    expect(wrapper.get('button').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })
})
