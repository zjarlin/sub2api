import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import Field from './ExhaustionPolicyField.vue'
import { exhaustionModelOptions, readExhaustionPolicy } from './policy'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('account exhaustion configuration', () => {
  it('offers configured text models without expanding an explicit whitelist', () => {
    const extra = { upstream_supported_models: { models: ['glm', 'qwen', 'image-gen'] } }
    expect(exhaustionModelOptions({ glm: 'glm', '*': '*', auto: 'auto', 'image-gen': 'image-gen' }, extra)).toEqual(['glm'])
    expect(exhaustionModelOptions(null, extra)).toEqual(['glm', 'qwen'])
    expect(readExhaustionPolicy({ exhaustion_policy: { enabled: 'yes', models: [2, 'glm'], group_ids: [null, -1, '2', 3] } })).toEqual({ enabled: false, models: ['glm'], group_ids: [3] })
  })
  it('supports keyboard ordering, group restriction, removal and toggling', async () => {
    const Host = defineComponent({
      components: { Field },
      setup: () => ({ policy: ref({ enabled: true, models: ['glm', 'qwen'], group_ids: [] as number[] }) }),
      template: `<Field v-model="policy" :models="['glm', 'qwen']" :groups="[{id: 7, name: 'codex'}]" />`
    })
    const wrapper = mount(Host)
    await wrapper.findAll('li')[1].get('button[aria-label="admin.exhaustion.up"]').trigger('click')
    expect(wrapper.findAll('li').map(row => row.text())).toEqual([expect.stringContaining('qwen'), expect.stringContaining('glm')])
    await wrapper.get('fieldset label:last-child input').setValue(true)
    expect(wrapper.vm.policy.group_ids).toEqual([7])
    await wrapper.findAll('li')[1].get('button[aria-label="admin.exhaustion.remove"]').trigger('click')
    expect(wrapper.vm.policy.models).toEqual(['qwen'])
    await wrapper.get('input[type="checkbox"]').setValue(false)
    expect(wrapper.find('ol').exists()).toBe(false)
    await wrapper.get('input[type="checkbox"]').setValue(true)
    expect(wrapper.vm.policy.models).toEqual(['qwen'])
    wrapper.unmount()
  })
})
