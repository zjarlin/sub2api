import { describe, expect, it } from 'vitest'

import { generateEdgeCode } from '../codegen'

const base = {
  gatewayOrigin: 'https://company-ai.example',
  method: 'POST' as const,
  url: 'https://company-ai.example/vision/volcengine/detect',
  headers: [{ name: 'Content-Type', value: 'application/json' }],
  query: [{ name: 'trace', value: '1' }],
  body: '{"Action":"Detect","Version":"2022-08-31","ImageBase64":"aW1hZ2U="}',
  bodyMode: 'json' as const,
  apiKeyPlaceholder: '$SUB2API_KEY',
}

describe('generateEdgeCode', () => {
  it('generates cURL with the gateway URL and auth placeholder', () => {
    const code = generateEdgeCode({ ...base, language: 'curl' })
    expect(code).toContain("curl -X POST 'https://company-ai.example/vision/volcengine/detect?trace=1'")
    expect(code).toContain('Authorization: Bearer $SUB2API_KEY')
  })

  it('generates JavaScript, Python, Go, and Java clients', () => {
    for (const language of ['javascript', 'python', 'go', 'java'] as const) {
      const code = generateEdgeCode({ ...base, language })
      expect(code).toContain('/vision/volcengine/detect?trace=1')
      expect(code).toContain('SUB2API_KEY')
    }
  })
})
