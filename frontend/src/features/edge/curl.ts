export type EdgeRequestMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
export type EdgeBodyMode = 'none' | 'json' | 'form'

export interface EdgeKeyValue {
  name: string
  value: string
}

export interface ParsedEdgeCurl {
  method: EdgeRequestMethod
  url: string
  headers: EdgeKeyValue[]
  query: EdgeKeyValue[]
  body: string
  bodyMode: EdgeBodyMode
  model: string
  warnings: string[]
}

export interface BuildEdgeCurlOptions {
  gatewayOrigin: string
  method: EdgeRequestMethod
  url: string
  headers: EdgeKeyValue[]
  query: EdgeKeyValue[]
  body: string
  bodyMode: EdgeBodyMode
  apiKeyPlaceholder?: string
  injectAuthorization?: boolean
}

const REQUEST_METHODS = new Set<EdgeRequestMethod>(['GET', 'POST', 'PUT', 'PATCH', 'DELETE'])
const VALUE_FLAGS = new Set([
  '-A', '--user-agent',
  '-b', '--cookie',
  '-c', '--cookie-jar',
  '-e', '--referer',
  '--connect-timeout',
  '--max-time',
  '--proxy',
  '--resolve',
  '--retry',
  '--retry-delay',
  '--retry-max-time',
])
const IGNORED_FLAGS = new Set([
  '-4', '-6', '-f', '-g', '-i', '-k', '-L', '-N', '-s', '-S', '-v', '--compressed',
  '--fail', '--fail-with-body', '--insecure', '--location', '--no-buffer', '--silent', '--show-error',
])
const HEADER_VALUE = /^([^:]+):\s*(.*)$/
const SENSITIVE_HEADERS = new Set(['authorization', 'proxy-authorization', 'x-api-key', 'api-key', 'x-goog-api-key'])
const UNSTABLE_HEADERS = new Set(['content-length', 'host', 'connection', 'transfer-encoding'])

function stripTrailingShellContinuations(input: string): string {
  return input.replace(/\\(?:\r\n|\n|\r)/g, ' ')
}

/**
 * Tokenize the useful curl subset without invoking a shell. It intentionally
 * handles the quoting used by browser "Copy as cURL" commands, while unknown
 * flags are ignored rather than interpreted as request data.
 */
export function tokenizeCurl(input: string): string[] {
  const source = stripTrailingShellContinuations(input).trim()
  const tokens: string[] = []
  let token = ''
  let quote: '"' | "'" | null = null
  let escaping = false
  let tokenStarted = false

  const push = () => {
    if (tokenStarted) {
      tokens.push(token)
      token = ''
      tokenStarted = false
    }
  }

  for (let index = 0; index < source.length; index += 1) {
    const char = source[index]
    if (escaping) {
      token += char
      escaping = false
      tokenStarted = true
      continue
    }
    if (char === '\\' && quote !== "'") {
      escaping = true
      tokenStarted = true
      continue
    }
    if (quote) {
      if (char === quote) {
        quote = null
      } else {
        token += char
      }
      tokenStarted = true
      continue
    }
    if (char === '"' || char === "'") {
      quote = char
      tokenStarted = true
      continue
    }
    if (/\s/.test(char)) {
      push()
      continue
    }
    token += char
    tokenStarted = true
  }

  if (escaping) {
    token += '\\'
  }
  push()
  return tokens
}

function parseHeader(value: string): EdgeKeyValue | null {
  const match = value.match(HEADER_VALUE)
  if (!match) return null
  const name = match[1].trim()
  if (!name) return null
  return { name, value: match[2].trim() }
}

function parseKeyValue(value: string): EdgeKeyValue {
  const separator = value.indexOf('=')
  if (separator < 0) return { name: value.trim(), value: '' }
  return {
    name: value.slice(0, separator).trim(),
    value: value.slice(separator + 1),
  }
}

function parseQuery(url: URL): EdgeKeyValue[] {
  const query: EdgeKeyValue[] = []
  url.searchParams.forEach((value, name) => query.push({ name, value }))
  return query
}

function inferMethod(explicitMethod: string | null, hasBody: boolean, forceGet: boolean): EdgeRequestMethod {
  if (forceGet) return 'GET'
  const normalized = explicitMethod?.trim().toUpperCase()
  if (normalized && REQUEST_METHODS.has(normalized as EdgeRequestMethod)) {
    return normalized as EdgeRequestMethod
  }
  return hasBody ? 'POST' : 'GET'
}

function inferBodyMode(contentType: string, multipart: boolean): EdgeBodyMode {
  if (multipart) return 'form'
  return /(?:^|;)\s*application\/(?:[\w.+-]*\+)?json/i.test(contentType) ? 'json' : 'none'
}

function extractModel(body: string, contentType: string): string {
  if (!body.trim() || !/(?:^|;)\s*application\/(?:[\w.+-]*\+)?json/i.test(contentType)) return ''
  try {
    const parsed = JSON.parse(body) as { model?: unknown }
    return typeof parsed.model === 'string' ? parsed.model.trim() : ''
  } catch {
    return ''
  }
}

export function parseEdgeCurl(input: string): ParsedEdgeCurl {
  const tokens = tokenizeCurl(input)
  if (tokens.length === 0) {
    throw new Error('curl command is empty')
  }

  const start = tokens[0].toLowerCase() === 'curl' ? 1 : 0
  let explicitMethod: string | null = null
  let urlText = ''
  let body = ''
  let forceGet = false
  let multipart = false
  const headers: EdgeKeyValue[] = []
  const form: EdgeKeyValue[] = []
  const warnings: string[] = []

  const addData = (value: string) => {
    body = body ? `${body}&${value}` : value
  }

  for (let index = start; index < tokens.length; index += 1) {
    const token = tokens[index]
    if (!token) continue
    if (token === '--') {
      if (index + 1 < tokens.length && !urlText) urlText = tokens[index + 1]
      break
    }

    const longEquals = token.match(/^(--[\w-]+)=(.*)$/)
    const flag = longEquals?.[1] ?? token
    const inlineValue = longEquals?.[2]
    const nextValue = (): string | null => {
      if (inlineValue !== undefined) return inlineValue
      if (index + 1 >= tokens.length) return null
      index += 1
      return tokens[index]
    }

    if (flag === '-X' || flag === '--request') {
      explicitMethod = nextValue()
      continue
    }
    if (flag === '-H' || flag === '--header') {
      const header = parseHeader(nextValue() ?? '')
      if (header) headers.push(header)
      continue
    }
    if (flag === '-d' || flag === '--data' || flag === '--data-raw' || flag === '--data-binary') {
      addData(nextValue() ?? '')
      continue
    }
    if (flag === '--data-urlencode') {
      addData(nextValue() ?? '')
      continue
    }
    if (flag === '-F' || flag === '--form' || flag === '--form-string') {
      multipart = true
      form.push(parseKeyValue(nextValue() ?? ''))
      continue
    }
    if (flag === '-G' || flag === '--get') {
      forceGet = true
      continue
    }
    if (flag === '--url') {
      urlText = nextValue() ?? urlText
      continue
    }
    if (flag === '-u' || flag === '--user') {
      const credentials = nextValue() ?? ''
      if (credentials) headers.push({ name: 'Authorization', value: `Basic ${credentials}` })
      continue
    }
    if (flag === '-b' || flag === '--cookie') {
      const cookie = nextValue() ?? ''
      if (cookie) headers.push({ name: 'Cookie', value: cookie })
      continue
    }
    if (flag === '-A' || flag === '--user-agent') {
      const userAgent = nextValue() ?? ''
      if (userAgent) headers.push({ name: 'User-Agent', value: userAgent })
      continue
    }
    if (flag === '-e' || flag === '--referer') {
      const referer = nextValue() ?? ''
      if (referer) headers.push({ name: 'Referer', value: referer })
      continue
    }
    if (flag === '-o' || flag === '--output') {
      void nextValue()
      warnings.push(`ignored curl output option: ${flag}`)
      continue
    }
    if (VALUE_FLAGS.has(flag)) {
      void nextValue()
      warnings.push(`ignored curl option: ${flag}`)
      continue
    }
    if (IGNORED_FLAGS.has(flag) || flag.startsWith('--')) {
      continue
    }
    if (flag.startsWith('-') && flag.length > 2) {
      const short = flag.slice(0, 2)
      if (short === '-X') {
        explicitMethod = flag.slice(2)
        continue
      }
      if (short === '-H') {
        const header = parseHeader(flag.slice(2))
        if (header) headers.push(header)
        continue
      }
      if (short === '-d') {
        addData(flag.slice(2))
        continue
      }
    }
    if (!urlText && /^[a-z][a-z\d+.-]*:\/\//i.test(token)) {
      urlText = token
      continue
    }
    if (!urlText && token.startsWith('/')) {
      urlText = token
    }
  }

  if (!urlText) {
    throw new Error('curl URL is missing')
  }
  let parsedUrl: URL
  try {
    parsedUrl = new URL(urlText)
  } catch {
    throw new Error('curl URL is invalid')
  }

  const query = parseQuery(parsedUrl)
  parsedUrl.search = ''
  const method = inferMethod(explicitMethod, Boolean(body || form.length), forceGet)
  if (forceGet && body) {
    for (const item of body.split('&')) query.push(parseKeyValue(item))
    body = ''
  }
  const contentType = headers.find(header => header.name.toLowerCase() === 'content-type')?.value ?? ''
  if (!contentType && body.trim()) headers.push({ name: 'Content-Type', value: 'application/json' })

  const normalizedHeaders = headers.filter(header => !UNSTABLE_HEADERS.has(header.name.toLowerCase()))
  let model = extractModel(body, contentType || 'application/json')
  if (!model) {
    model = query
      .find(item => item.name.toLowerCase() === 'model')
      ?.value.trim() ?? ''
  }
  if (!model && form.length > 0) {
    model = form.find(item => item.name.toLowerCase() === 'model')?.value.trim() ?? ''
  }

  return {
    method,
    url: parsedUrl.toString(),
    headers: normalizedHeaders,
    query,
    body: multipart ? form.map(item => `${item.name}=${item.value}`).join('&') : body,
    bodyMode: inferBodyMode(contentType, multipart),
    model,
    warnings,
  }
}

export function sanitizeHeaders(headers: EdgeKeyValue[], apiKeyPlaceholder = '$SUB2API_KEY'): EdgeKeyValue[] {
  return headers
    .map(header => ({
      name: header.name.trim(),
      value: SENSITIVE_HEADERS.has(header.name.trim().toLowerCase()) ? apiKeyPlaceholder : header.value,
    }))
    .filter(header => header.name)
}

function quoteShell(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`
}

function appendQuery(urlText: string, query: EdgeKeyValue[]): string {
  let url: URL
  try {
    url = new URL(urlText)
  } catch {
    return urlText
  }
  url.search = ''
  for (const item of query) {
    if (item.name.trim()) url.searchParams.append(item.name.trim(), item.value)
  }
  return url.toString()
}

function gatewayUrl(gatewayOrigin: string, urlText: string): string {
  let imported: URL
  try {
    imported = new URL(urlText)
  } catch {
    return urlText
  }
  let origin: URL
  try {
    origin = new URL(gatewayOrigin)
  } catch {
    return urlText
  }
  origin.pathname = imported.pathname
  origin.search = imported.search
  origin.hash = imported.hash
  return origin.toString()
}

export function buildEdgeCurl(options: BuildEdgeCurlOptions): string {
  const url = appendQuery(gatewayUrl(options.gatewayOrigin, options.url), options.query)
  const headers = sanitizeHeaders(options.headers, options.apiKeyPlaceholder)
  const hasAuthentication = headers.some(header => SENSITIVE_HEADERS.has(header.name.toLowerCase()))
  if (options.injectAuthorization !== false && !hasAuthentication) {
    headers.unshift({ name: 'Authorization', value: options.apiKeyPlaceholder ?? '$SUB2API_KEY' })
  }

  const lines = [`curl -X ${options.method} ${quoteShell(url)}`]
  for (const header of headers) {
    lines.push(`  -H ${quoteShell(`${header.name}: ${header.value}`)}`)
  }
  if (options.bodyMode === 'form') {
    for (const item of options.body.split('&').filter(Boolean)) {
      lines.push(`  -F ${quoteShell(item)}`)
    }
  } else if (options.body.trim() && options.method !== 'GET') {
    lines.push(`  -d ${quoteShell(options.body)}`)
  }
  return lines.join(' \\\n')
}

export function replaceBodyModel(body: string, model: string): string {
  const value = model.trim()
  if (!value) return body
  try {
    const parsed = JSON.parse(body) as Record<string, unknown>
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return body
    parsed.model = value
    return JSON.stringify(parsed, null, 2)
  } catch {
    return body
  }
}

export function applyGatewayPath(urlText: string, gatewayOrigin: string, path: string): string {
  let origin: URL
  try {
    origin = new URL(gatewayOrigin)
  } catch {
    return path
  }
  const normalizedPath = path.startsWith('/') ? path : `/${path}`
  origin.pathname = normalizedPath
  origin.search = ''
  origin.hash = ''
  if (urlText) {
    try {
      const imported = new URL(urlText)
      origin.search = imported.search
    } catch {
      // Keep the preset path when the imported URL has no parsable query.
    }
  }
  return origin.toString()
}
