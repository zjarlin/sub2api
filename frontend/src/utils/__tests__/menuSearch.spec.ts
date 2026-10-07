import { describe, expect, it } from 'vitest'
import { filterMenuItems } from '../menuSearch'

const items = [
  { path: '/admin/proxies', label: '代理管理', searchKeywords: 'IP proxy proxies' },
  { path: '/admin/accounts', label: '账号管理' },
  { path: '/admin/orders', label: '订单', children: [
    { path: '/admin/orders/plans', label: '支付套餐' },
    { path: '/admin/orders/history', label: '历史订单' }
  ] }
]

describe('menu search', () => {
  it('preserves the menu when the query is blank', () => {
    expect(filterMenuItems(items, '  ')).toBe(items)
  })
  it('finds proxies by name, path and the former IP label', () => {
    for (const query of ['代理', 'PROXY', '/admin/proxies', ' IP ']) {
      expect(filterMenuItems(items, query)).toEqual([items[0]])
    }
  })
  it('keeps the parent and only matching children without mutating the menu', () => {
    expect(filterMenuItems(items, '支付')).toEqual([
      { ...items[2], children: [items[2].children![0]] }
    ])
    expect(items[2].children).toHaveLength(2)
  })
  it('keeps all children when the parent matches', () => {
    expect(filterMenuItems(items, '订单')).toEqual([items[2]])
  })
  it('requires every search term and returns no matches for unrelated text', () => {
    expect(filterMenuItems(items, '代理 IP')).toEqual([items[0]])
    expect(filterMenuItems(items, '代理 支付')).toEqual([])
  })
})
