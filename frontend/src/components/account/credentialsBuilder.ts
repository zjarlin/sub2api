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

export type QuickOpenAIParseErrorKey =
  | 'inputRequired'
  | 'baseUrlRequired'
  | 'invalidBaseUrl'
  | 'apiKeyRequired'

export interface QuickOpenAIParseResult {
  baseUrl?: string
  apiKey?: string
  errorKey?: QuickOpenAIParseErrorKey
}

const quickOpenAIUrlPattern = /https?:\/\/[^\s"'<>`,，;；]+/i
const quickOpenAISkPattern = /\bsk-[^\s"'<>`,，;；]+/i

function trimQuickOpenAIToken(value: string): string {
  return value
    .trim()
    .replace(/^[`"'(<[{]+/, '')
    .replace(/[`"')>\]};,，；。]+$/, '')
}

function stripQuickOpenAILabel(value: string): string {
  return trimQuickOpenAIToken(value).replace(
    /^(?:api[_-]?key|key|token|authorization|bearer|base[_-]?url|url)\s*[:=]\s*/i,
    ''
  )
}

function isQuickOpenAIKeyCandidate(value: string): boolean {
  const normalized = value.replace(/[:=]$/, '')
  if (/^(?:api[_-]?key|key|token|authorization|bearer|base[_-]?url|url)$/i.test(normalized)) {
    return false
  }
  return value.length >= 8 && /^[A-Za-z0-9][A-Za-z0-9._:-]*$/.test(value)
}

export function parseQuickOpenAIInput(raw: string): QuickOpenAIParseResult {
  const input = raw.trim()
  if (!input) {
    return { errorKey: 'inputRequired' }
  }

  const urlMatch = input.match(quickOpenAIUrlPattern)
  if (!urlMatch) {
    return { errorKey: 'baseUrlRequired' }
  }

  const baseUrl = trimQuickOpenAIToken(urlMatch[0])
  try {
    const parsed = new URL(baseUrl)
    if (!['http:', 'https:'].includes(parsed.protocol) || !parsed.hostname) {
      return { errorKey: 'invalidBaseUrl' }
    }
  } catch {
    return { errorKey: 'invalidBaseUrl' }
  }

  const remaining = input.replace(urlMatch[0], ' ')
  const skMatch = remaining.match(quickOpenAISkPattern)
  const apiKey = skMatch
    ? trimQuickOpenAIToken(skMatch[0])
    : remaining
        .split(/[\s,，;；]+/)
        .map(stripQuickOpenAILabel)
        .map(trimQuickOpenAIToken)
        .find(isQuickOpenAIKeyCandidate)

  if (!apiKey) {
    return { errorKey: 'apiKeyRequired' }
  }

  return { baseUrl, apiKey }
}
