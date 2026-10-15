import { beforeEach, describe, expect, it, vi } from 'vitest'
import { cancelBuiltinLogin, completeBuiltinLogin, getBuiltinLoginView, sendBuiltinLoginInput, startBuiltinLogin, type BuiltinLoginSession } from '../admin/builtinAdapters'

const { post, get, remove } = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn(), remove: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { post, get, delete: remove } }))

describe('TeleAgent login API scopes', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    post.mockResolvedValue({ data: {} })
    get.mockResolvedValue({ data: new Blob(['png'], { type: 'image/png' }) })
    remove.mockResolvedValue(undefined)
  })

  it.each(['admin', 'user'] as const)('routes every login operation through %s', async scope => {
    const base = `/${scope}/builtin-adapters/teleagent/login-sessions`
    const signal = new AbortController().signal
    const session: BuiltinLoginSession = { session_id: 'login-1', mode: 'poll', status: 'pending', expires_at: Date.now() + 600000 }
    const result = { ...session, status: 'completed', api_key: `ta-${'d'.repeat(64)}`, account: { uid: 'user-1', nickname: 'Test' } }
    post.mockResolvedValueOnce({ data: session }).mockResolvedValueOnce({ data: result })
    expect(await startBuiltinLogin('teleagent', signal, undefined, scope)).toBe(session)
    expect(post).toHaveBeenLastCalledWith(base, {}, { signal, timeout: 50000 })
    expect(await completeBuiltinLogin('teleagent', session, '', signal, scope)).toBe(result)
    expect(post).toHaveBeenLastCalledWith(`${base}/login-1/poll`, { callback_url: '' }, { signal, timeout: 50000 })
    const view = await getBuiltinLoginView('teleagent', 'login-1', signal, scope)
    expect(view.type).toBe('image/png')
    expect(get).toHaveBeenLastCalledWith(`${base}/login-1/view`, { signal, timeout: 20000, responseType: 'blob' })
    await sendBuiltinLoginInput('teleagent', 'login-1', { type: 'text', text: '123456' }, signal, scope)
    expect(post).toHaveBeenLastCalledWith(`${base}/login-1/input`, { type: 'text', text: '123456' }, { signal, timeout: 20000 })
    await cancelBuiltinLogin('teleagent', 'login-1', scope)
    expect(remove).toHaveBeenCalledWith(`${base}/login-1`)
  })
})
