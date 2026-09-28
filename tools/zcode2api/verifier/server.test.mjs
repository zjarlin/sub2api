import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createServer } from './server.mjs'

async function fixture(t, verify) {
  const server = createServer({ key: 'local-verifier-key', verifier: { verify } })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(() => new Promise(resolve => { server.closeAllConnections(); server.close(resolve) }))
  const url = `http://127.0.0.1:${server.address().port}`
  const request = (body, headers = {}, signal) => fetch(`${url}/verify`, {
    method: 'POST',
    headers: { Authorization: 'Bearer local-verifier-key', ...headers },
    body: typeof body === 'string' ? body : JSON.stringify(body), signal,
  })
  return { url, request, server }
}

test('requires authentication and filters out credentials and unrelated headers', async t => {
  let calls = 0
  const { request } = await fixture(t, async headers => {
    calls++
    assert.deepEqual(headers, { 'x-zcode-app-version': '3.14.3' })
    return { captcha_verify_param: 'proof', captcha_region: 'cn' }
  })
  assert.equal((await request({ source_headers: {} }, { Authorization: 'Bearer wrong' })).status, 401)
  const result = await request({ source_headers: { 'X-ZCode-App-Version': '3.14.3', Authorization: 'sensitive-token', 'x-api-key': 'sensitive-key' } })
  assert.equal(result.status, 200)
  assert.deepEqual(await result.json(), { captcha_verify_param: 'proof', captcha_region: 'cn' })
  assert.equal(calls, 1)
})

test('serializes SDK verification instead of sharing proofs between concurrent requests', async t => {
  let active = 0
  let sequence = 0
  const { request } = await fixture(t, async () => {
    assert.equal(active++, 0)
    const id = ++sequence
    await new Promise(resolve => setTimeout(resolve, 5))
    active--
    return { captcha_verify_param: `proof-${id}`, captcha_region: 'cn' }
  })
  const results = await Promise.all([request({ source_headers: {} }), request({ source_headers: {} })])
  const bodies = await Promise.all(results.map(r => r.json()))
  assert.equal(new Set(bodies.map(body => body.captcha_verify_param)).size, 2)
})

test('client disconnection cancels verification and releases the queue', async t => {
  let started
  const ready = new Promise(resolve => { started = resolve })
  let calls = 0
  const { request } = await fixture(t, async (_headers, signal) => {
    if (++calls === 1) {
      started()
      await new Promise(resolve => signal.addEventListener('abort', resolve, { once: true }))
      signal.throwIfAborted()
    }
    return { captcha_verify_param: 'next-proof', captcha_region: 'cn' }
  })
  const controller = new AbortController()
  const first = request({ source_headers: {} }, {}, controller.signal)
  await ready
  controller.abort()
  await assert.rejects(first, { name: 'AbortError' })
  assert.equal((await request({ source_headers: {} })).status, 200)
})

test('does not expose verifier error details or accept invalid request shapes', async t => {
  const { request } = await fixture(t, async () => { throw new Error('sensitive upstream response') })
  assert.equal((await request('invalid json')).status, 400)
  assert.equal((await request({ source_headers: [] })).status, 400)
  assert.equal((await request({ source_headers: null })).status, 400)
  const response = await request({ source_headers: {} })
  assert.equal(response.status, 502)
  assert.deepEqual(await response.json(), { code: 'verification_failed' })
})

test('bounds the queue and skips a queued request that disconnects', async t => {
  let release
  let started
  const ready = new Promise(resolve => { started = resolve })
  const gate = new Promise(resolve => { release = resolve })
  let calls = 0
  const { request, server } = await fixture(t, async () => {
    if (++calls === 1) {
      started()
      await gate
    }
    return { captcha_verify_param: `proof-${calls}`, captcha_region: 'cn' }
  })
  t.after(release)
  const disconnected = new Promise(resolve => server.on('request', (req, res) => {
    if (req.headers['x-check-cancel'] === '1') res.on('close', resolve)
  }))
  const first = request({ source_headers: {} })
  await ready
  const controller = new AbortController()
  const cancelled = request({ source_headers: {} }, { 'X-Check-Cancel': '1' }, controller.signal)
  const rejected = assert.rejects(cancelled, { name: 'AbortError' })
  const pending = Array.from({ length: 6 }, () => request({ source_headers: {} }))
  await new Promise(resolve => setTimeout(resolve, 30))
  assert.equal((await request({ source_headers: {} })).status, 429)
  controller.abort()
  await disconnected
  release()
  await rejected
  const responses = await Promise.all([first, ...pending])
  assert.ok(responses.every(response => response.status === 200))
  assert.equal(calls, 7)
})
