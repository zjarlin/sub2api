export interface ModelProbePolicy {
  enabled: boolean
  intervalHours: number
}

export function readModelProbePolicy(extra?: Record<string, unknown> | null): ModelProbePolicy {
  const hours = extra?.model_health_probe_interval_hours
  return {
    enabled: extra?.model_health_probe_enabled !== false,
    intervalHours: typeof hours === 'number' && Number.isInteger(hours) && hours >= 24 && hours <= 8760 ? hours : 168
  }
}

export function writeModelProbePolicy(policy: ModelProbePolicy, extra: Record<string, unknown>) {
  return {
    ...extra,
    model_health_probe_enabled: policy.enabled,
    model_health_probe_interval_hours: policy.intervalHours
  }
}
