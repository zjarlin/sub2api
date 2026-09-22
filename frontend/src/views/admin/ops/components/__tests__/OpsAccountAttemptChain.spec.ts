import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import OpsAccountAttemptChain from '../OpsAccountAttemptChain.vue'

function render(raw: string) {
  return mount(OpsAccountAttemptChain, {
    props: { raw },
    global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: {} })] }
  })
}

describe('OpsAccountAttemptChain', () => {
  it('keeps account order, repeated attempts, and local versus upstream failures', () => {
    const wrapper = render(JSON.stringify([
      { account_id: 837, account_name: 'aaawinn', upstream_status_code: 429, message: 'RPM 15' },
      null,
      { account_id: 832, account_name: 'r4', stage: 'routing', status_code: 429, message: 'queue full' },
      { account_id: 832, account_name: 'r4', upstream_status_code: 503, message: 'busy' }
    ]))
    expect(wrapper.findAll('li')).toHaveLength(3)
    expect(wrapper.findAll('details')).toHaveLength(3)
    expect(wrapper.text()).toContain('aaawinn')
    expect(wrapper.text()).toContain('#837')
    expect(wrapper.text()).toContain('attemptChain.routing')
  })

  it('uses amber accordions for recovered attempts and red only for the final failure', () => {
    const recovered = mount(OpsAccountAttemptChain, {
      props: { raw: JSON.stringify([{ account_id: 1, account_name: 'a', upstream_status_code: 503 }]), finalStatusCode: 200 },
      global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: {} })] }
    })
    expect(recovered.get('[data-testid="attempt-chain-accordion-0"]').classes()).toContain('bg-amber-50')
    expect(recovered.get('[data-testid="attempt-chain-accordion-0"]').classes()).not.toContain('bg-red-50')

    const failed = mount(OpsAccountAttemptChain, {
      props: { raw: JSON.stringify([{ account_id: 1, account_name: 'a', upstream_status_code: 503 }, { kind: 'model_fallback', from_model: 'a', model: 'b' }]), finalStatusCode: 502 },
      global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: {} })] }
    })
    expect(failed.get('[data-testid="attempt-chain-accordion-0"]').classes()).toContain('bg-amber-50')
    expect(failed.get('[data-testid="attempt-chain-accordion-1"]').classes()).toContain('bg-red-50')
    expect(failed.get('[data-testid="attempt-chain-accordion-1"]').attributes('open')).toBeDefined()
  })
})
