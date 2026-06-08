import { describe, it, expect } from 'vitest'
import {
  applyInterceptWarmup,
  buildBulkApiKeyAccountName,
  parseAccountApiKeys,
  parseQuickOpenAIInput
} from '../credentialsBuilder'

describe('applyInterceptWarmup', () => {
  it('create + enabled=true: should set intercept_warmup_requests to true', () => {
    const creds: Record<string, unknown> = { access_token: 'tok' }
    applyInterceptWarmup(creds, true, 'create')
    expect(creds.intercept_warmup_requests).toBe(true)
  })

  it('create + enabled=false: should not add the field', () => {
    const creds: Record<string, unknown> = { access_token: 'tok' }
    applyInterceptWarmup(creds, false, 'create')
    expect('intercept_warmup_requests' in creds).toBe(false)
  })

  it('edit + enabled=true: should set intercept_warmup_requests to true', () => {
    const creds: Record<string, unknown> = { api_key: 'sk' }
    applyInterceptWarmup(creds, true, 'edit')
    expect(creds.intercept_warmup_requests).toBe(true)
  })

  it('edit + enabled=false + field exists: should delete the field', () => {
    const creds: Record<string, unknown> = { api_key: 'sk', intercept_warmup_requests: true }
    applyInterceptWarmup(creds, false, 'edit')
    expect('intercept_warmup_requests' in creds).toBe(false)
  })

  it('edit + enabled=false + field absent: should not throw', () => {
    const creds: Record<string, unknown> = { api_key: 'sk' }
    applyInterceptWarmup(creds, false, 'edit')
    expect('intercept_warmup_requests' in creds).toBe(false)
  })

  it('should not affect other fields', () => {
    const creds: Record<string, unknown> = {
      api_key: 'sk',
      base_url: 'url',
      intercept_warmup_requests: true
    }
    applyInterceptWarmup(creds, false, 'edit')
    expect(creds.api_key).toBe('sk')
    expect(creds.base_url).toBe('url')
    expect('intercept_warmup_requests' in creds).toBe(false)
  })
})

describe('parseAccountApiKeys', () => {
  it('splits API keys from lines, commas, and whitespace', () => {
    expect(parseAccountApiKeys('sk-a\nsk-b, sk-c；sk-d')).toEqual([
      'sk-a',
      'sk-b',
      'sk-c',
      'sk-d'
    ])
  })

  it('filters empty separators without reordering or deduping keys', () => {
    expect(parseAccountApiKeys('  sk-a\n\nsk-a  ,  sk-b  ')).toEqual(['sk-a', 'sk-a', 'sk-b'])
  })
})

describe('buildBulkApiKeyAccountName', () => {
  it('preserves the original name for a single account', () => {
    expect(buildBulkApiKeyAccountName('main', 0, 1)).toBe('main')
  })

  it('adds numeric suffixes for multi-key account creation', () => {
    expect(buildBulkApiKeyAccountName('main', 0, 3)).toBe('main_1')
    expect(buildBulkApiKeyAccountName('main', 1, 3)).toBe('main_2')
    expect(buildBulkApiKeyAccountName('main', 2, 3)).toBe('main_3')
  })
})

describe('parseQuickOpenAIInput', () => {
  it('parses OpenAI-compatible quick add input regardless of key/url order', () => {
    expect(parseQuickOpenAIInput('sk-test-key-12345 https://api.example.com')).toEqual({
      apiKey: 'sk-test-key-12345',
      baseUrl: 'https://api.example.com'
    })
  })

  it('parses url-first input with Chinese separators', () => {
    expect(parseQuickOpenAIInput('https://api.example.com/v1，sk-test-key-67890')).toEqual({
      apiKey: 'sk-test-key-67890',
      baseUrl: 'https://api.example.com/v1'
    })
  })
})
