export function applyInterceptWarmup(
  credentials: Record<string, unknown>,
  enabled: boolean,
  mode: 'create' | 'edit'
): void {
  if (enabled) {
    credentials.intercept_warmup_requests = true
  } else if (mode === 'edit') {
    delete credentials.intercept_warmup_requests
  }
}

export function parseAccountApiKeys(input: string): string[] {
  return input
    .split(/[\s,，;；]+/)
    .map((key) => key.trim())
    .filter((key) => key.length > 0)
}

export function buildBulkApiKeyAccountName(baseName: string, index: number, total: number): string {
  if (total <= 1) {
    return baseName
  }
  return `${baseName.trim()}_${index + 1}`
}
