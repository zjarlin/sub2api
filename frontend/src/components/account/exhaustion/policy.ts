export interface ExhaustionPolicy {
  enabled: boolean
  models: string[]
  group_ids: number[]
}

export function readExhaustionPolicy(extra?: Record<string, unknown>): ExhaustionPolicy {
  const raw = extra?.exhaustion_policy as Partial<ExhaustionPolicy> | undefined
  return {
    enabled: raw?.enabled === true,
    models: Array.isArray(raw?.models) ? raw.models.filter((id): id is string => typeof id === 'string') : [],
    group_ids: Array.isArray(raw?.group_ids) ? raw.group_ids.filter((id): id is number => typeof id === 'number' && Number.isInteger(id) && id > 0) : []
  }
}

export function exhaustionModelOptions(mapping: Record<string, string> | null | undefined, extra?: Record<string, unknown>): string[] {
  const configured = Object.keys(mapping ?? {})
  const catalog = extra?.upstream_supported_models as { models?: string[] } | undefined
  const models = configured.length ? configured : (catalog?.models ?? [])
  return [...new Set(models)].filter(id => id && !id.includes('*') &&
    !/embed|image|audio|video|tts-|whisper|transcri|dall-e|translate|translation|safety|guard|calibration|rerank|re-rank|classifier|reward-model|parser|decision-model/i.test(id) &&
    !/^(auto|ask|free|tier-\d+)$/.test(id)).sort()
}
