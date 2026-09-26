import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(
  resolve(process.cwd(), 'src/components/account/CreateAccountModal.vue'),
  'utf8'
)

const qoderApi = readFileSync(resolve(process.cwd(), 'src/api/admin/qoder.ts'), 'utf8')

describe('CreateAccountModal Qoder OAuth (device flow)', () => {
  it('offers OAuth alongside API-key and defaults Qoder to the device flow', () => {
    expect(source).toContain('data-testid="qoder-account-type-oauth"')
    expect(source).toContain('data-testid="qoder-account-type-api-key"')
    expect(source).toMatch(/function selectQoderPlatform\(\)[\s\S]*?form\.platform = 'qoder'[\s\S]*?accountCategory\.value = 'oauth-based'[\s\S]*?form\.type = 'oauth'/)
  })

  it('renders the device-flow panel and polls until authorization completes', () => {
    expect(source).toContain("form.platform === 'qoder'")
    expect(source).toContain('startQoderAuth')
    expect(source).toContain('adminAPI.qoder.generateAuthURL()')
    expect(source).toContain('adminAPI.qoder.pollToken(qoderSessionId.value)')
    expect(source).toContain("createAccountAndFinish('qoder', 'oauth'")
  })

  it('creates the OAuth account with the device token and refresh token', () => {
    expect(source).toContain('access_token: token.access_token')
    expect(source).toContain('credentials.refresh_token = token.refresh_token')
  })

  it('exposes the Qoder OAuth admin endpoints', () => {
    expect(qoderApi).toContain("'/admin/qoder/oauth/auth-url'")
    expect(qoderApi).toContain("'/admin/qoder/oauth/poll-token'")
    expect(qoderApi).toContain("'/admin/qoder/oauth/refresh-token'")
  })
})
