import { defineComponent, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DoubaoSessionProfiles from '../DoubaoSessionProfiles.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { listDoubaoSessionProfiles, uploadDoubaoSessionProfile, deleteDoubaoSessionProfile } from '@/api/admin/builtinAdapters'

vi.mock('@/api/admin/builtinAdapters', () => ({
  listDoubaoSessionProfiles: vi.fn(),
  uploadDoubaoSessionProfile: vi.fn(),
  deleteDoubaoSessionProfile: vi.fn(),
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess: vi.fn() }),
}))
vi.mock('vue-i18n', async importOriginal => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key }),
}))

const list = vi.mocked(listDoubaoSessionProfiles)
const upload = vi.mocked(uploadDoubaoSessionProfile)
const remove = vi.mocked(deleteDoubaoSessionProfile)

function mountProfiles(initial = 'default') {
  return mount(defineComponent({
    components: { DoubaoSessionProfiles },
    setup: () => ({ profile: ref(initial), busy: ref(false) }),
    template: '<DoubaoSessionProfiles v-model="profile" @busy="busy = $event" /><button data-testid="save" :disabled="busy">Save</button>',
  }), {
    global: { stubs: { ConfirmDialog: true, TotpStepUpDialog: true } },
  })
}

async function chooseFile(wrapper: ReturnType<typeof mountProfiles>, file = new File(['{}'], 'session.json', { type: 'application/json' })) {
  const input = wrapper.get('[data-testid="doubao-session-file"]')
  Object.defineProperty(input.element, 'files', { configurable: true, value: [file] })
  await input.trigger('change')
  return file
}

beforeEach(() => {
  vi.resetAllMocks()
  list.mockResolvedValue({ profiles: ['default', 'alice'] })
  upload.mockResolvedValue({ name: 'bob' })
  remove.mockResolvedValue(undefined)
})

describe('豆包网页会话管理', () => {
  it('加载已有会话，上传后刷新并选中新会话，草稿期间禁止保存账号', async () => {
    const wrapper = mountProfiles()
    await flushPromises()
    expect(wrapper.get('select').text()).toContain('alice')
    await wrapper.get('[data-testid="doubao-add-session"]').trigger('click')
    expect(wrapper.get('[data-testid="save"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="doubao-profile-name"]').setValue('bob')
    const file = await chooseFile(wrapper)
    list.mockResolvedValue({ profiles: ['default', 'alice', 'bob'] })
    await wrapper.get('[data-testid="doubao-upload-session"]').trigger('click')
    await flushPromises()
    expect(upload).toHaveBeenCalledWith('bob', file, expect.any(AbortSignal), 'admin')
    expect(list).toHaveBeenCalledTimes(2)
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('bob')
    expect(wrapper.get('[data-testid="save"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.find('input[type="file"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('拒绝保留名称、路径和过大的会话文件', async () => {
    const wrapper = mountProfiles()
    await flushPromises()
    await wrapper.get('[data-testid="doubao-add-session"]').trigger('click')
    await chooseFile(wrapper)
    for (const name of ['default', '../alice', '_alice']) {
      await wrapper.get('[data-testid="doubao-profile-name"]').setValue(name)
      await wrapper.get('[data-testid="doubao-upload-session"]').trigger('click')
      expect(wrapper.get('[role="alert"]').text()).toContain('profileNameHint')
    }
    await chooseFile(wrapper, new File(['x'.repeat(65537)], 'session.json'))
    expect(wrapper.get('[role="alert"]').text()).toContain('fileSizeError')
    expect(upload).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('替换需要确认，上传失败保留原选择和草稿', async () => {
    const wrapper = mountProfiles('alice')
    await flushPromises()
    await wrapper.get('[data-testid="doubao-update-session"]').trigger('click')
    await chooseFile(wrapper)
    await wrapper.get('[data-testid="doubao-upload-session"]').trigger('click')
    expect(upload).not.toHaveBeenCalled()
    expect(wrapper.findComponent(ConfirmDialog).props('show')).toBe(true)
    upload.mockRejectedValue({ message: 'snapshot rejected' })
    wrapper.findComponent(ConfirmDialog).vm.$emit('confirm')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('snapshot rejected')
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('alice')
    expect(wrapper.find('input[type="file"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('删除经确认后回到默认会话，默认会话没有删除入口', async () => {
    const wrapper = mountProfiles('alice')
    await flushPromises()
    await wrapper.get('[data-testid="doubao-delete-session"]').trigger('click')
    expect(remove).not.toHaveBeenCalled()
    wrapper.findComponent(ConfirmDialog).vm.$emit('confirm')
    await flushPromises()
    expect(remove).toHaveBeenCalledWith('alice', expect.any(AbortSignal), 'admin')
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('default')
    expect(wrapper.find('[data-testid="doubao-delete-session"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('已有账号的会话丢失时不悄悄改用默认账号', async () => {
    const wrapper = mountProfiles('deleted')
    await flushPromises()
    expect((wrapper.get('select').element as HTMLSelectElement).value).toBe('deleted')
    expect(wrapper.get('[data-testid="save"]').attributes('disabled')).toBeDefined()
    await wrapper.get('select').setValue('alice')
    expect(wrapper.get('[data-testid="save"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('支持二次验证后重试上传，关闭组件会取消在途请求', async () => {
    const wrapper = mountProfiles()
    await flushPromises()
    await wrapper.get('[data-testid="doubao-add-session"]').trigger('click')
    await wrapper.get('[data-testid="doubao-profile-name"]').setValue('bob')
    await chooseFile(wrapper)
    upload.mockRejectedValueOnce({ reason: 'STEP_UP_REQUIRED' })
    list.mockResolvedValue({ profiles: ['default', 'alice', 'bob'] })
    await wrapper.get('[data-testid="doubao-upload-session"]').trigger('click')
    await flushPromises()
    const stepUp = wrapper.findComponent(TotpStepUpDialog).props('controller')
    expect(stepUp.visible.value).toBe(true)
    stepUp.onVerified()
    await flushPromises()
    expect(upload).toHaveBeenCalledTimes(2)
    const signal = upload.mock.calls[0][2]
    wrapper.unmount()
    expect(signal?.aborted).toBe(true)
  })
})
