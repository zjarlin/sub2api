import { describe, expect, it } from 'vitest'
import {
  USER_CURRENCY_CODE,
  USER_CURRENCY_SYMBOL,
  formatUserCurrency,
} from '../userCurrency'

describe('userCurrency', () => {
  it('uses CNY for user wallet values', () => {
    expect(USER_CURRENCY_CODE).toBe('CNY')
    expect(USER_CURRENCY_SYMBOL).toBe('¥')
    expect(formatUserCurrency(12.5)).toBe('¥12.50')
  })

  it('supports detailed usage precision and invalid values', () => {
    expect(formatUserCurrency(0.123456, 4)).toBe('¥0.1235')
    expect(formatUserCurrency(Number.NaN)).toBe('¥0.00')
    expect(formatUserCurrency(undefined)).toBe('¥0.00')
  })
})
