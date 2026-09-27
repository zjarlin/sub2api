import { buildEdgeCurl, type EdgeBodyMode, type EdgeKeyValue, type EdgeRequestMethod } from './curl'

export type EdgeCodeLanguage = 'curl' | 'javascript' | 'python' | 'go' | 'java'

export interface GenerateEdgeCodeOptions {
  language: EdgeCodeLanguage
  gatewayOrigin: string
  method: EdgeRequestMethod
  url: string
  headers: EdgeKeyValue[]
  query: EdgeKeyValue[]
  body: string
  bodyMode: EdgeBodyMode
  apiKeyPlaceholder?: string
}

export interface EdgeCodeOption {
  value: EdgeCodeLanguage
  label: string
}

export const EDGE_CODE_LANGUAGES: EdgeCodeOption[] = [
  { value: 'curl', label: 'cURL' },
  { value: 'javascript', label: 'JavaScript' },
  { value: 'python', label: 'Python' },
  { value: 'go', label: 'Go' },
  { value: 'java', label: 'Java' },
]

function quote(value: string): string {
  return `'${value.replace(/\\/g, '\\\\').replace(/'/g, "\\'").replace(/\n/g, '\\n')}'`
}

function appendQuery(url: string, query: EdgeKeyValue[]): string {
  let parsed: URL
  try {
    parsed = new URL(url)
  } catch {
    return url
  }
  parsed.search = ''
  for (const item of query) {
    if (item.name.trim()) parsed.searchParams.append(item.name.trim(), item.value)
  }
  return parsed.toString()
}

function gatewayUrl(origin: string, pathOrUrl: string): string {
  try {
    const target = new URL(pathOrUrl, origin)
    const base = new URL(origin)
    base.pathname = target.pathname
    base.search = target.search
    base.hash = ''
    return base.toString()
  } catch {
    return pathOrUrl
  }
}

function requestParts(options: GenerateEdgeCodeOptions) {
  const url = appendQuery(gatewayUrl(options.gatewayOrigin, options.url), options.query)
  const headers = [{ name: 'Authorization', value: options.apiKeyPlaceholder ?? '$SUB2API_KEY' }, ...options.headers.filter(header => header.name.trim() && !/^authorization$/i.test(header.name))]
  const hasContentType = headers.some(header => header.name.toLowerCase() === 'content-type')
  if (!hasContentType && options.body.trim() && options.bodyMode !== 'none') {
    headers.push({ name: 'Content-Type', value: options.bodyMode === 'json' ? 'application/json' : 'application/x-www-form-urlencoded' })
  }
  return { url, headers, body: options.body.trim() }
}

function generateJavaScript(options: GenerateEdgeCodeOptions): string {
  const { url, headers, body } = requestParts(options)
  const lines = [
    `const response = await fetch(${quote(url)}, {`,
    `  method: ${quote(options.method)},`,
    '  headers: {',
    ...headers.map(header => `    ${quote(header.name)}: ${quote(header.value)},`),
    '  },',
  ]
  if (body && options.method !== 'GET' && options.bodyMode !== 'none') {
    lines.push(`  body: ${quote(body)},`)
  }
  lines.push('})', '', 'const data = await response.json()', 'console.log(data)')
  return lines.join('\n')
}

function generatePython(options: GenerateEdgeCodeOptions): string {
  const { url, headers, body } = requestParts(options)
  const lines = [
    'import requests',
    '',
    `url = ${quote(url)}`,
    'headers = {',
    ...headers.map(header => `    ${quote(header.name)}: ${quote(header.value)},`),
    '}',
  ]
  if (body && options.method !== 'GET' && options.bodyMode !== 'none') {
    lines.push(`payload = ${quote(body)}`, `response = requests.request(${quote(options.method)}, url, headers=headers, data=payload)`)
  } else {
    lines.push(`response = requests.request(${quote(options.method)}, url, headers=headers)`)
  }
  lines.push('print(response.status_code)', 'print(response.text)')
  return lines.join('\n')
}

function generateGo(options: GenerateEdgeCodeOptions): string {
  const { url, headers, body } = requestParts(options)
  const bodyArg = body && options.method !== 'GET' && options.bodyMode !== 'none' ? `strings.NewReader(${quote(body)})` : 'nil'
  const lines = [
    'package main',
    '',
    'import (',
    '    "fmt"',
    '    "io"',
    '    "net/http"',
    '    "strings"',
    ')',
    '',
    'func main() {',
  ]
  if (bodyArg !== 'nil') lines.push(`    payload := ${bodyArg}`)
  lines.push(`    req, err := http.NewRequest(${quote(options.method)}, ${quote(url)}, payload)`, '    if err != nil { panic(err) }')
  for (const header of headers) lines.push(`    req.Header.Set(${quote(header.name)}, ${quote(header.value)})`)
  lines.push('    resp, err := http.DefaultClient.Do(req)', '    if err != nil { panic(err) }', '    defer resp.Body.Close()', '    data, _ := io.ReadAll(resp.Body)', '    fmt.Println(resp.Status)', '    fmt.Println(string(data))', '}')
  return lines.join('\n')
}

function generateJava(options: GenerateEdgeCodeOptions): string {
  const { url, headers, body } = requestParts(options)
  const bodyPublisher = body && options.method !== 'GET' && options.bodyMode !== 'none' ? `HttpRequest.BodyPublishers.ofString(${quote(body)})` : 'HttpRequest.BodyPublishers.noBody()'
  const lines = [
    'import java.net.URI;',
    'import java.net.http.HttpClient;',
    'import java.net.http.HttpRequest;',
    'import java.net.http.HttpResponse;',
    '',
    'public class EdgeRequest {',
    '  public static void main(String[] args) throws Exception {',
    '    HttpRequest request = HttpRequest.newBuilder()',
    `        .uri(URI.create(${quote(url)}))`,
    `        .method(${quote(options.method)}, ${bodyPublisher})`,
  ]
  for (const header of headers) lines.push(`        .header(${quote(header.name)}, ${quote(header.value)})`)
  lines.push('        .build();', '    HttpResponse<String> response = HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofString());', '    System.out.println(response.statusCode());', '    System.out.println(response.body());', '  }', '}')
  return lines.join('\n')
}

export function generateEdgeCode(options: GenerateEdgeCodeOptions): string {
  if (options.language === 'curl') {
    return buildEdgeCurl({ ...options, apiKeyPlaceholder: options.apiKeyPlaceholder ?? '$SUB2API_KEY' })
  }
  if (options.language === 'javascript') return generateJavaScript(options)
  if (options.language === 'python') return generatePython(options)
  if (options.language === 'go') return generateGo(options)
  return generateJava(options)
}
