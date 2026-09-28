import assert from 'node:assert/strict'
import { afterEach, test } from 'node:test'
import { verifyInPage } from './browser.mjs'

afterEach(() => { delete globalThis.window; delete globalThis.document })

function sdk(run, click = () => {}) {
  globalThis.document = { querySelector: () => ({ click }) }
  globalThis.window = { initAliyunCaptcha(options) { run(options) } }
}

const options = { config: { region: 'cn', prefix: 'test', sceneId: 'test' }, headless: true, triggerDelayMs: 0, timeoutMs: 100 }

test('uses native traceless verification and returns a fresh proof per invocation', async () => {
  let calls = 0
  sdk(callbacks => callbacks.getInstance({ startTracelessVerification() { callbacks.success(`proof-${++calls}`) } }))
  assert.equal(await verifyInPage(options), 'proof-1')
  assert.equal(await verifyInPage(options), 'proof-2')
  assert.deepEqual(window.AliyunCaptchaConfig, { region: 'cn', prefix: 'test' })
})

test('headless verification reports an interactive challenge without solving it', async () => {
  let clicked = false
  sdk(callbacks => callbacks.getInstance({ startTracelessVerification() { callbacks.fail({ success: true, verifyResult: false }) } }), () => { clicked = true })
  await assert.rejects(verifyInPage(options), /interactive_verification_required/)
  assert.equal(clicked, false)
})

test('visible verification lets the SDK show its interactive challenge', async () => {
  let callbacks
  sdk(value => { callbacks = value; value.getInstance({}) }, () => callbacks.success('interactive-proof'))
  assert.equal(await verifyInPage({ ...options, headless: false }), 'interactive-proof')
})

test('visible verification tries the official traceless path before opening a challenge', async () => {
  let clicked = false
  sdk(callbacks => callbacks.getInstance({ startTracelessVerification() { callbacks.success('traceless-proof') } }), () => { clicked = true })
  assert.equal(await verifyInPage({ ...options, headless: false }), 'traceless-proof')
  assert.equal(clicked, false)
})

test('accepts the native terminal-pass callback and rejects duplicate submissions', async () => {
  sdk(callbacks => callbacks.fail({ verifyCode: 'T006', CaptchaVerifyParam: 'native-proof' }))
  assert.equal(await verifyInPage(options), 'native-proof')
  sdk(callbacks => callbacks.fail({ verifyCode: 'F008' }))
  await assert.rejects(verifyInPage(options), /verification_failed/)
})

test('a terminal-pass callback without proof requires the native interactive flow', async () => {
  sdk(callbacks => callbacks.fail({ verifyCode: 'T006' }))
  await assert.rejects(verifyInPage(options), /interactive_verification_required/)
})

test('repeated deferred callbacks open the interactive challenge once', async () => {
  let callbacks
  let clicks = 0
  sdk(value => {
    callbacks = value
    value.getInstance({ startTracelessVerification() {
      value.fail({ success: true, verifyResult: false })
      value.fail({ success: true, verifyResult: false })
    } })
  }, () => {
    clicks++
    callbacks.fail({ success: true, verifyResult: false })
    callbacks.fail({ success: true, verifyResult: false })
    callbacks.success('final-proof')
  })
  assert.equal(await verifyInPage({ ...options, headless: false }), 'final-proof')
  assert.equal(clicks, 1)
})

test('reinitialized SDK instances do not start another verification while a challenge is open', async () => {
  let callbacks
  let starts = 0
  let clicks = 0
  const instance = { startTracelessVerification() {
    starts++
    callbacks.fail({ verifyCode: starts === 1 ? 'F001' : 'F008', verifyResult: false })
  } }
  sdk(value => {
    callbacks = value
    callbacks.getInstance(instance)
  }, () => {
    clicks++
    callbacks.getInstance(instance)
    callbacks.fail({ verifyCode: 'F001', verifyResult: false })
    setTimeout(() => callbacks.success('interactive-proof'), 10)
  })
  assert.equal(await verifyInPage({ ...options, headless: false }), 'interactive-proof')
  assert.equal(starts, 1)
  assert.equal(clicks, 1)
})

test('verification with no SDK result times out instead of reusing a previous proof', async () => {
  sdk(() => {})
  await assert.rejects(verifyInPage({ ...options, timeoutMs: 5 }), /verification_timeout/)
})
