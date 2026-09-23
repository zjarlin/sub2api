// 保留未测试项与原有别名目标，只移除本轮已确认失败的项。
export function successfulModelMapping(
  existing: Record<string, unknown>, tested: string[], passed: string[]
): Record<string, string> {
  const mapping: Record<string, string> = {}
  for (const [key, value] of Object.entries(existing)) {
    if (typeof value === 'string' && !tested.includes(key)) {
      mapping[key] = value
    }
  }
  for (const id of passed) {
    mapping[id] = typeof existing[id] === 'string' ? existing[id] : id
  }
  return mapping
}
