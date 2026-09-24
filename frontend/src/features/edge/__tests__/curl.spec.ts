import { describe, expect, it } from 'vitest'

import {
  applyGatewayPath,
  buildEdgeCurl,
  parseEdgeCurl,
  replaceBodyModel,
  sanitizeHeaders,
} from '../curl'

describe('parseEdgeCurl', () => {
  it('parses a browser-style JSON curl with shell continuations', () => {
    const parsed = parseEdgeCurl(`curl 'https://upstream.example/v1/chat/completions' \\
  -H 'accept: application/json' \\
  -H 'authorization: Bearer secret-token' \\
  -H 'content-type: application/json' \\
  --data-raw '{"model":"upstream-chat","messages":[{"role":"user","content":"hello"}]}'`)

    expect(parsed.method).toBe('POST')
    expect(parsed.url).toBe('https://upstream.example/v1/chat/completions')
    expect(parsed.model).toBe('upstream-chat')
    expect(parsed.bodyMode).toBe('json')
    expect(parsed.headers).toEqual([
      { name: 'accept', value: 'application/json' },
      { name: 'authorization', value: 'Bearer secret-token' },
      { name: 'content-type', value: 'application/json' },
    ])
  })

  it('moves -G data into editable query parameters', () => {
    const parsed = parseEdgeCurl(`curl -G 'https://upstream.example/search?limit=20' \\
  --data-urlencode 'q=hello world' \\
  --data-urlencode 'model=gpt-test'`)

    expect(parsed.method).toBe('GET')
    expect(parsed.body).toBe('')
    expect(parsed.query).toEqual([
      { name: 'limit', value: '20' },
      { name: 'q', value: 'hello world' },
      { name: 'model', value: 'gpt-test' },
    ])
    expect(parsed.model).toBe('gpt-test')
  })

  it('parses multipart form fields and ignores transport-only headers on output', () => {
    const parsed = parseEdgeCurl(`curl -X POST 'http://edge-vision:18081/detect' \\
  -H 'Host: edge-vision:18081' \\
  -H 'Content-Length: 120' \\
  -F 'image=@photo.jpg' \\
  -F 'confidence_threshold=0.5'`)

    expect(parsed.bodyMode).toBe('form')
    expect(parsed.body).toBe('image=@photo.jpg&confidence_threshold=0.5')
    expect(parsed.headers).toEqual([])
  })

  it('supports escaped double quotes inside JSON', () => {
    const parsed = parseEdgeCurl(`curl -X POST https://example.test/v1/responses -H "Content-Type: application/json" -d "{\\"model\\":\\"responses-model\\",\\"input\\":\\"say \\\\\\"hi\\\\\\"\\"}"`)

    expect(parsed.model).toBe('responses-model')
    expect(parsed.body).toBe('{"model":"responses-model","input":"say \\"hi\\""}')
  })
})

describe('edge curl output', () => {
  it('replaces authentication headers with a placeholder and targets the configured gateway', () => {
    const output = buildEdgeCurl({
      gatewayOrigin: 'https://company-ai.example',
      method: 'POST',
      url: 'https://upstream.example/v1/chat/completions',
      headers: [
        { name: 'Authorization', value: 'Bearer secret-token' },
        { name: 'Content-Type', value: 'application/json' },
        { name: 'X-Api-Key', value: 'another-secret' },
      ],
      query: [],
      body: '{"model":"upstream-model"}',
      bodyMode: 'json',
      apiKeyPlaceholder: '$SUB2API_KEY',
    })

    expect(output).toContain("curl -X POST 'https://company-ai.example/v1/chat/completions'")
    expect(output).toContain("-H 'Authorization: $SUB2API_KEY'")
    expect(output).toContain("-H 'X-Api-Key: $SUB2API_KEY'")
    expect(output).not.toContain('secret-token')
    expect(output).not.toContain('another-secret')
  })

  it('adds a placeholder authorization header when the imported curl had none', () => {
    const output = buildEdgeCurl({
      gatewayOrigin: 'https://company-ai.example',
      method: 'GET',
      url: 'https://upstream.example/v1/models',
      headers: [],
      query: [{ name: 'type', value: 'codex' }],
      body: '',
      bodyMode: 'none',
    })

    expect(output).toContain("-H 'Authorization: $SUB2API_KEY'")
    expect(output).toContain("'https://company-ai.example/v1/models?type=codex'")
  })

  it('supports a preset gateway path for JEV and Laya', () => {
    const url = applyGatewayPath('https://upstream.example/v1/systemone?source=legacy', 'https://company-ai.example', '/v1/systemone')
    expect(url).toBe('https://company-ai.example/v1/systemone?source=legacy')
  })

  it('replaces only a JSON model field', () => {
    expect(replaceBodyModel('{"model":"old","input":"model: keep"}', 'laya-multilingual')).toBe(
      JSON.stringify({ model: 'laya-multilingual', input: 'model: keep' }, null, 2),
    )
  })

  it('never carries sensitive header values through sanitization', () => {
    expect(sanitizeHeaders([
      { name: 'Authorization', value: 'Bearer secret' },
      { name: 'x-api-key', value: 'secret-2' },
      { name: 'X-Trace', value: 'trace-1' },
    ])).toEqual([
      { name: 'Authorization', value: '$SUB2API_KEY' },
      { name: 'x-api-key', value: '$SUB2API_KEY' },
      { name: 'X-Trace', value: 'trace-1' },
    ])
  })
})
