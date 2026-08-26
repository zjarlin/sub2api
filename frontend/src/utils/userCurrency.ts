export const USER_CURRENCY_CODE = 'CNY'
export const USER_CURRENCY_SYMBOL = '¥'

export function formatUserCurrency(
  amount: number | null | undefined,
  fractionDigits = 2,
): string {
  const value = typeof amount === 'number' && Number.isFinite(amount) ? amount : 0
  return `${USER_CURRENCY_SYMBOL}${value.toFixed(fractionDigits)}`
}
