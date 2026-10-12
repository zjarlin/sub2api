import { afterEach, describe, expect, it, vi } from 'vitest'
import apiClient from '@/api/client'
import { deleteDoubaoSessionProfile, listDoubaoSessionProfiles, uploadDoubaoSessionProfile } from '@/api/admin/builtinAdapters'

afterEach(() => vi.restoreAllMocks())

describe('豆包会话 API', () => {
  it('上传使用 multipart，覆盖默认 JSON 请求头且仅提交名称与文件', async () => {
    const requests: unknown[] = []
    const original = apiClient.defaults.adapter
    apiClient.defaults.adapter = async config => {
      requests.push(config)
      return { config, status: 201, statusText: 'Created', headers: {}, data: { code: 0, data: { name: 'alice' } } }
    }
    try {
      const file = new File(['{"cookies":{"sessionid":"fixture"}}'], 'session.json', { type: 'application/json' })
      expect(await uploadDoubaoSessionProfile('alice', file)).toEqual({ name: 'alice' })
      const request = requests[0] as { data: FormData; headers: Record<string, string>; url: string }
      expect(request.url).toBe('/admin/builtin-adapters/doubao/profiles')
      expect(request.data).toBeInstanceOf(FormData)
      expect(request.data.get('profile')).toBe('alice')
      expect(request.data.get('session')).toBeInstanceOf(File)
      expect(request.headers['Content-Type']).toBe('multipart/form-data')
    } finally {
      apiClient.defaults.adapter = original
    }
  })

  it('列举支持取消，删除时编码会话名称', async () => {
    const signal = new AbortController().signal
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: { profiles: ['default', 'alice'] } })
    const remove = vi.spyOn(apiClient, 'delete').mockResolvedValue({})
    expect(await listDoubaoSessionProfiles(signal)).toEqual({ profiles: ['default', 'alice'] })
    expect(get).toHaveBeenCalledWith('/admin/builtin-adapters/doubao/profiles', { signal })
    await deleteDoubaoSessionProfile('a/b', signal)
    expect(remove).toHaveBeenCalledWith('/admin/builtin-adapters/doubao/profiles/a%2Fb', { signal })
  })
})
