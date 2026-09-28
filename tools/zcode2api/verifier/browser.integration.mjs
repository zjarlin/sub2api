import assert from 'node:assert/strict'
import { test } from 'node:test'
import { chromium } from 'playwright-core'
import { BrowserVerifier } from './browser.mjs'

test('keeps the SDK page across requests while producing fresh proofs and isolating website redirects', async t => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.ZCODE_VERIFY_BROWSER || undefined })
  const context = await browser.newContext()
  t.after(() => browser.close())
  const config = { enabled: true, region: 'sgp', prefix: 'test-prefix', sceneId: 'test-scene' }
  t.mock.method(globalThis, 'fetch', async () => Response.json({ code: 0, data: { configs: { captcha: config } } }))

  let websiteRequests = 0
  await context.route('https://zcode.z.ai/**', route => {
    websiteRequests++
    return route.fulfill({ contentType: 'text/html', body: '<script>setTimeout(() => location.replace("/en"), 0)</script>' })
  })
  await context.route('https://o.alicdn.com/captcha-frontend/aliyunCaptcha/AliyunCaptcha.js', route => route.fulfill({
    contentType: 'application/javascript',
    body: `
      let attempt = 0;
      window.initAliyunCaptcha = callbacks => {
        if (window.AliyunCaptchaConfig?.region !== 'sgp' || window.AliyunCaptchaConfig?.prefix !== 'test-prefix') { callbacks.onError(); return; }
        callbacks.getInstance({ startTracelessVerification() { callbacks.success('fresh-test-proof-' + ++attempt); } });
      };
    `,
  }))

  const verifier = new BrowserVerifier({ headless: true })
  verifier.context = context
  const result = await verifier.verify({}, AbortSignal.timeout(5000))
  assert.deepEqual(result, { captcha_verify_param: 'fresh-test-proof-1', captcha_region: 'sgp' })
  const next = await verifier.verify({}, AbortSignal.timeout(5000))
  assert.deepEqual(next, { captcha_verify_param: 'fresh-test-proof-2', captcha_region: 'sgp' })
  assert.equal(websiteRequests, 0)
  assert.equal(context.pages().length, 1)
  const activePage = context.pages()[0]
  const controller = new AbortController()
  const cancelled = verifier.verify({}, controller.signal)
  const timer = setTimeout(() => controller.abort(new Error('test_cancelled')), 100)
  t.after(() => clearTimeout(timer))
  await assert.rejects(cancelled, /test_cancelled/)
  assert.equal(activePage.isClosed(), true)
  const recovered = await verifier.verify({}, AbortSignal.timeout(5000))
  assert.deepEqual(recovered, { captcha_verify_param: 'fresh-test-proof-1', captcha_region: 'sgp' })
  await verifier.close()
  assert.equal(context.pages().length, 0)
})
