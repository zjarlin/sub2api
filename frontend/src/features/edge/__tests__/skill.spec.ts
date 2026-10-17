import { describe, expect, it } from 'vitest'

import { EDGE_SERVICES } from '../catalog'
import { buildEdgeSkill, edgeSkillName, edgeSkillPath } from '../skill'

const service = (key: string) => EDGE_SERVICES.find(item => item.key === key)!
const endpointOf = (key: string) => ({ key, path: '/api/v1/example', method: 'POST', description: 'Example endpoint' })

function build(key: string, endpoints = [endpointOf(key)]) {
  return buildEdgeSkill({
    service: service(key),
    serviceTitle: '文本翻译',
    serviceDescription: '统一翻译接口，在线服务与离线模型按需选择。',
    endpoints,
    endpoint: endpoints[0],
    gatewayOrigin: 'http://gateway.example',
    example: "curl -X POST 'http://gateway.example/api/v1/example' \\\n  -H \"Authorization: Bearer $SUB2API_KEY\"",
  })
}

describe('edge skill generation', () => {
  it('keeps the frontmatter name identical to the install directory', () => {
    const translate = service('translate')
    expect(edgeSkillName(translate)).toBe('sub2api-edge-translate')
    expect(edgeSkillPath(translate)).toBe('~/.codex/skills/sub2api-edge-translate/SKILL.md')
  })

  it('embeds the frontmatter, gateway, endpoints and request example', () => {
    const skill = build('translate', [
      { key: 'translate', path: '/api/v1/translate', method: 'POST', description: '统一翻译' },
      { key: 'translate-providers', path: '/api/v1/translate/providers', method: 'GET', description: '服务商查询' },
    ])
    expect(skill.startsWith('---\nname: sub2api-edge-translate\n')).toBe(true)
    expect(skill).toContain('当用户需要通过 Sub2API 边缘计算网关调用文本翻译时使用。')
    expect(skill).toContain('http://gateway.example')
    expect(skill).toContain('| POST | `/api/v1/translate` | 统一翻译 |')
    expect(skill).toContain('| GET | `/api/v1/translate/providers` | 服务商查询 |')
    expect(skill).toContain('curl -X POST')
    expect(skill).toContain('$SUB2API_KEY')
  })

  it('escapes table separators so descriptions cannot break the table', () => {
    const skill = build('vision', [{ key: 'detect', path: '/vision/volcengine/detect', method: 'POST', description: 'a | b\nc' }])
    expect(skill).toContain('| POST | `/vision/volcengine/detect` | a \\| b c |')
  })

  it('documents async task polling only for asynchronous services', () => {
    const dub = build('dub', [{ key: 'dub', path: '/media/videos/dub', method: 'POST', description: '视频配音' }])
    expect(dub).toContain('POST /media/videos/dub')
    expect(dub).toContain('GET /media/tasks/{task_id}')
    expect(dub).toContain('GET /media/tasks/{task_id}/content')

    const generation = build('generation', [{ key: 'generation', path: '/v1/contents/generations/tasks', method: 'POST', description: '视频生成' }])
    expect(generation).toContain('GET /v1/contents/generations/tasks/{task_id}')
    expect(generation).not.toContain('GET /media/tasks/{task_id}')

    const translate = build('translate')
    expect(translate).not.toContain('## 异步任务')
    const tts = build('tts')
    expect(tts).not.toContain('## 异步任务')
  })

  it('marks multipart services with the right content type', () => {
    expect(build('dub')).toContain('`multipart/form-data`')
    expect(build('translate')).toContain('`application/json`')
  })

  it('does not claim a request body for GET-only services', () => {
    const providers = build('translate', [{ key: 'translate-providers', path: '/api/v1/translate/providers', method: 'GET', description: '服务商查询' }])
    expect(providers).toContain('不需要请求体')
    expect(providers).not.toContain('`application/json`')
  })
})
