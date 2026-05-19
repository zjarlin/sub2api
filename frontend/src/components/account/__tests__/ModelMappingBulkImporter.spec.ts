import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

const copyToClipboardMock = vi.fn()

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copied: { value: false },
    copyToClipboard: copyToClipboardMock
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

import ModelMappingBulkImporter from '../ModelMappingBulkImporter.vue'

describe('ModelMappingBulkImporter', () => {
  it('copies current mappings when copy text is provided', async () => {
    copyToClipboardMock.mockReset()

    const wrapper = mount(ModelMappingBulkImporter, {
      props: {
        copyText: 'gpt-5.4 => deepseek-v4-pro[1m]'
      }
    })

    await wrapper.get('[data-testid="copy-mappings"]').trigger('click')

    expect(copyToClipboardMock).toHaveBeenCalledWith(
      'gpt-5.4 => deepseek-v4-pro[1m]',
      'admin.accounts.currentMappingsCopied'
    )
  })

  it('hides the copy button when there are no current mappings', () => {
    const wrapper = mount(ModelMappingBulkImporter, {
      props: {
        copyText: ''
      }
    })

    expect(wrapper.find('[data-testid="copy-mappings"]').exists()).toBe(false)
  })
})
