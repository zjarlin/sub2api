// 旧日志仅有 stage，仍按视觉辅助用途展示，不套用请求顶层的主模型。
export function isVisionAttempt(entry: Record<string, unknown>): boolean {
  return entry.request_role === 'vision' || entry.stage === 'vision_helper'
}
