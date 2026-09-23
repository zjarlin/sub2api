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
  it('distinguishes failed vision helpers from the primary model and shows recovery', async () => {
    const wrapper = render(JSON.stringify([{ account_id: 3, model: 'preferred-vision', stage: 'vision_helper', image_index: 2, upstream_status_code: 504, recovered_by_model: 'backup-vision', recovered_by_account_id: 4 }]))
    await wrapper.setProps({ finalStatusCode: 502 })
    expect(wrapper.text()).toContain('preferred-vision')
    expect(wrapper.text()).toContain('attemptChain.visionHelper')
    expect(wrapper.text()).toContain('attemptChain.visionImage')
    expect(wrapper.text()).toContain('attemptChain.visionRecovered')
    expect(wrapper.get('details').classes()).not.toContain('bg-red-50')
  })
  it('keeps account order, repeated attempts, and local versus upstream failures', () => {
    const wrapper = render(JSON.stringify([
      { account_id: 837, account_name: 'aaawinn', upstream_status_code: 429, message: 'RPM 15' },
      null,
      { account_id: 832, account_name: 'r4', stage: 'routing', status_code: 429, message: 'queue full' },
      { account_id: 832, account_name: 'r4', upstream_status_code: 503, message: 'busy' }
    ]))
    const attempts = wrapper.findAll('li')
    expect(attempts).toHaveLength(3)
    expect(wrapper.findAll('details')).toHaveLength(3)
    expect(attempts[0].text()).toContain('aaawinn')
    expect(attempts[0].text()).toContain('#837')
    expect(attempts[0].text()).toContain('RPM 15')
    expect(attempts[1].text()).toContain('r4')
    expect(attempts[1].text()).toContain('#832')
    expect(attempts[1].text()).toContain('attemptChain.routing')
    expect(attempts[1].text()).toContain('429')
    expect(attempts[2].text()).toContain('r4')
    expect(attempts[2].text()).toContain('attemptChain.upstream')
    expect(attempts[2].text()).toContain('503')
  })

  it('shows model transitions and the model used by each failed account', () => {
    const wrapper = render(JSON.stringify([
      { account_id: 1, account_name: 'first', model: 'requested', upstream_status_code: 429 },
      { kind: 'model_fallback', stage: 'routing', from_model: 'requested', model: 'peer', model_tier: 'same-tier' },
      { account_id: 2, account_name: 'second', model: 'peer', upstream_status_code: 503 }
    ]))
    const attempts = wrapper.findAll('li')
    expect(attempts[0].text()).toContain('requested')
    expect(attempts[1].text()).toContain('requested → peer')
    expect(attempts[1].text()).toContain('same-tier')
    expect(attempts[1].text()).not.toContain('common.unknown')
    expect(attempts[2].text()).toContain('peer')
  })

  it('shows incomplete legacy entries and retention notices', () => {
    const wrapper = render('[{"account_id":42,"dropped_earlier_attempts":4}]')
    expect(wrapper.text()).toContain('#42')
    expect(wrapper.text()).toContain('common.unknown')
    expect(wrapper.text()).toContain('attemptChain.dropped')
  })

  it('uses the recovered outcome for legacy logs without treating an upstream status as the final status', () => {
    const wrapper = mount(OpsAccountAttemptChain, {
      props: {
        raw: JSON.stringify([{ account_id: 1, upstream_status_code: 503 }]),
        finalStatusCode: 503,
        finalSucceeded: true,
      },
      global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: {} })] },
    })

    const success = wrapper.get('[data-testid="attempt-chain-success"]')
    expect(success.text()).toContain('attemptChain.finalSuccess')
    expect(success.text()).not.toContain('503')
    expect(success.text()).not.toContain('#1')
    expect(wrapper.get('[data-testid="attempt-chain-accordion-0"]').classes()).not.toContain('bg-red-50')
  })

  it('uses amber accordions for recovered attempts and red only for the final failure', () => {
    const recovered = mount(OpsAccountAttemptChain, {
      props: { raw: JSON.stringify([{ account_id: 1, account_name: 'a', upstream_status_code: 503 }]), finalStatusCode: 200, finalAccountId: 2, finalAccountName: 'recovered-account', finalModel: 'fallback-model' },
      global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: {} })] }
    })
    expect(recovered.get('[data-testid="attempt-chain-accordion-0"]').classes()).toContain('bg-amber-50')
    expect(recovered.get('[data-testid="attempt-chain-accordion-0"]').classes()).not.toContain('bg-red-50')
    const success = recovered.get('[data-testid="attempt-chain-success"]')
    expect(success.text()).toContain('recovered-account')
    expect(success.text()).toContain('fallback-model')
    expect(success.text()).toContain('200')
    expect(success.classes()).toContain('bg-emerald-50')

    const failed = mount(OpsAccountAttemptChain, {
      props: {
        raw: JSON.stringify([
          { account_id: 1, account_name: 'a', upstream_status_code: 503 },
          { kind: 'model_fallback', from_model: 'a', model: 'b' }
        ]),
        finalStatusCode: 502
      },
      global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: {} })] }
    })
    expect(failed.get('[data-testid="attempt-chain-accordion-0"]').classes()).toContain('bg-amber-50')
    expect(failed.get('[data-testid="attempt-chain-accordion-1"]').classes()).toContain('bg-red-50')
    expect(failed.get('[data-testid="attempt-chain-accordion-1"]').attributes('open')).toBeDefined()
  })

  it.each(['', 'null', '{}', '[null]', 'invalid json'])('handles missing or malformed payload: %s', raw => {
    expect(render(raw).find('section').exists()).toBe(false)
  })
})
