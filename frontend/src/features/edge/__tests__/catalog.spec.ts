import { describe, expect, it } from 'vitest'
import { EDGE_SERVICES, edgeQuery, edgeSelection } from '../catalog'
import type { LocationQuery } from 'vue-router'

describe('edge deep links', () => {
  it('preserves unrelated query values and round trips adapter context', () => {
    const translate = EDGE_SERVICES[0]
    const query = edgeQuery({ tracking: 'keep' }, translate, 'context', undefined, 'hymt') as LocationQuery
    expect(query).toEqual({ tracking: 'keep', service: 'translate', tab: 'context', adapter: 'hymt' })
    expect(edgeSelection(query)).toMatchObject({ service: translate, tab: 'context', endpoint: 'translate', adapter: 'hymt' })
    expect(edgeQuery(query, translate, 'context', undefined, 'hymt')).toEqual(query)
    expect(edgeQuery(query)).toEqual({ tracking: 'keep' })
  })
  it('ignores invalid detail parameters and never serializes credential drafts', () => {
    expect(edgeSelection({ service: 'invalid', tab: 'invalid', adapter: ['hymt', 'baidu'] })).toMatchObject({ service: undefined, tab: 'docs', adapter: 'baidu' })
    const vision = EDGE_SERVICES[1]
    expect(edgeSelection({ service: 'vision', endpoint: 'translate' }).endpoint).toBe('detect')
    expect(edgeQuery({}, vision, 'debug', 'ocr', 'hymt')).toEqual({ service: 'vision', tab: 'debug', endpoint: 'ocr' })
  })
})
