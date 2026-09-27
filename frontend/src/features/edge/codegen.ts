import { buildEdgeCurl, edgeFormFields, sanitizeHeaders, type BuildEdgeCurlOptions } from './curl'

export type EdgeCodeLanguage = 'curl' | 'javascript' | 'python' | 'go' | 'java'
export interface GenerateEdgeCodeOptions extends BuildEdgeCurlOptions { language: EdgeCodeLanguage }
export const EDGE_CODE_LANGUAGES: Array<{ value: EdgeCodeLanguage; label: string }> = [
  { value: 'curl', label: 'cURL' }, { value: 'javascript', label: 'JavaScript (Node 20+)' },
  { value: 'python', label: 'Python' }, { value: 'go', label: 'Go' }, { value: 'java', label: 'Java 17+' },
]

const quote = (value: string): string => JSON.stringify(value)

function requestParts(options: GenerateEdgeCodeOptions) {
  const target = new URL(options.url, options.gatewayOrigin)
  const url = new URL(target.pathname + target.search, options.gatewayOrigin)
  options.query.forEach(({ name, value }) => { if (name.trim()) url.searchParams.set(name.trim(), value) })
  const hasBody = options.method !== 'GET' && options.bodyMode !== 'none'
  const multipart = hasBody && options.bodyMode === 'form'
  const headers = sanitizeHeaders(options.headers).filter(h => !/^authorization$/i.test(h.name)
    && !(multipart && /^content-type$/i.test(h.name)))
  headers.unshift({ name: 'Authorization', value: 'Bearer $SUB2API_KEY' })
  if (hasBody && !multipart && !headers.some(h => /^content-type$/i.test(h.name))) {
    headers.push({ name: 'Content-Type', value: 'application/json' })
  }
  return { url: url.toString(), headers, hasBody, multipart, form: multipart ? edgeFormFields(options) : [] }
}

function headerValue(value: string, language: Exclude<EdgeCodeLanguage, 'curl'>): string {
  const env = { javascript: 'process.env.SUB2API_KEY', python: 'os.environ["SUB2API_KEY"]',
    go: 'os.Getenv("SUB2API_KEY")', java: 'System.getenv("SUB2API_KEY")' }[language]
  if (value === 'Bearer $SUB2API_KEY') return quote('Bearer ') + ' + ' + env
  if (value === '$SUB2API_KEY') return env
  return quote(value)
}

function generateJavaScript(options: GenerateEdgeCodeOptions): string {
  const { url, headers, hasBody, multipart, form } = requestParts(options)
  const lines = ['import { writeFile } from "node:fs/promises"']
  if (multipart) lines.push('import { openAsBlob } from "node:fs"')
  lines.push('', 'if (!process.env.SUB2API_KEY) throw new Error("Set SUB2API_KEY")')
  if (multipart) {
    lines.push('const body = new FormData()')
    for (const field of form) {
      lines.push('body.append(' + quote(field.name) + ', ' + (field.kind === 'file'
        ? 'await openAsBlob(' + quote(field.value) + '), ' + quote(field.value.split(/[\\/]/).pop() || 'input.mp4')
        : quote(field.value)) + ')')
    }
  }
  lines.push('const response = await fetch(' + quote(url) + ', {', '  method: ' + quote(options.method) + ',', '  headers: {')
  headers.forEach(h => lines.push('    ' + quote(h.name) + ': ' + headerValue(h.value, 'javascript') + ','))
  lines.push('  },')
  if (hasBody) lines.push('  body: ' + (multipart ? 'body' : quote(options.body)) + ',')
  lines.push('})', 'if (!response.ok) throw new Error(await response.text())',
    'if (/(json|text)/i.test(response.headers.get("content-type") || "")) {',
    '  console.log(await response.text())', '} else {',
    '  await writeFile(' + quote(options.outputFile ?? 'response.bin') + ', new Uint8Array(await response.arrayBuffer()))', '}')
  return lines.join('\n')
}

function generatePython(options: GenerateEdgeCodeOptions): string {
  const { url, headers, hasBody, multipart, form } = requestParts(options)
  const lines = ['import os', 'from contextlib import ExitStack', 'from pathlib import Path', 'import requests', '', 'headers = {']
  headers.forEach(h => lines.push('    ' + quote(h.name) + ': ' + headerValue(h.value, 'python') + ','))
  lines.push('}', 'with ExitStack() as stack:')
  if (multipart) {
    lines.push('    files = [')
    for (const field of form) {
      const value = field.kind === 'file'
        ? '(' + quote(field.value.split(/[\\/]/).pop() || 'input.mp4') + ', stack.enter_context(open(' + quote(field.value) + ', "rb")), "application/octet-stream")'
        : '(None, ' + quote(field.value) + ')'
      lines.push('        (' + quote(field.name) + ', ' + value + '),')
    }
    lines.push('    ]')
  }
  lines.push('    response = requests.request(' + quote(options.method) + ', ' + quote(url) + ', headers=headers'
    + (hasBody ? multipart ? ', files=files' : ', data=' + quote(options.body) + '.encode("utf-8")' : '') + ', timeout=7200)',
    '    response.raise_for_status()', '    content_type = response.headers.get("content-type", "")',
    '    if "json" in content_type or "text" in content_type:', '        print(response.text)', '    else:',
    '        Path(' + quote(options.outputFile ?? 'response.bin') + ').write_bytes(response.content)')
  return lines.join('\n')
}

function generateGo(options: GenerateEdgeCodeOptions): string {
  const { url, headers, hasBody, multipart, form } = requestParts(options)
  const lines = ['package main', '', 'import (', '    "fmt"', '    "io"', '    "net/http"', '    "os"', '    "strings"']
  if (multipart) lines.push('    "bytes"', '    "mime/multipart"')
  lines.push(')', '', 'func check(err error) { if err != nil { panic(err) } }', '', 'func main() {',
    '    if os.Getenv("SUB2API_KEY") == "" { panic("Set SUB2API_KEY") }')
  if (multipart) {
    lines.push('    var payload bytes.Buffer', '    writer := multipart.NewWriter(&payload)')
    form.forEach((field, index) => {
      if (field.kind === 'text') {
        lines.push('    check(writer.WriteField(' + quote(field.name) + ', ' + quote(field.value) + '))')
      } else {
        const id = String(index)
        lines.push('    file' + id + ', err := os.Open(' + quote(field.value) + ')', '    check(err)',
          '    part' + id + ', err := writer.CreateFormFile(' + quote(field.name) + ', ' + quote(field.value.split(/[\\/]/).pop() || 'input.mp4') + ')', '    check(err)',
          '    _, err = io.Copy(part' + id + ', file' + id + ')', '    check(err)', '    check(file' + id + '.Close())')
      }
    })
    lines.push('    check(writer.Close())')
  }
  const body = !hasBody ? 'nil' : multipart ? '&payload' : 'strings.NewReader(' + quote(options.body) + ')'
  lines.push('    req, err := http.NewRequest(' + quote(options.method) + ', ' + quote(url) + ', ' + body + ')', '    check(err)')
  headers.forEach(h => lines.push('    req.Header.Set(' + quote(h.name) + ', ' + headerValue(h.value, 'go') + ')'))
  if (multipart) lines.push('    req.Header.Set("Content-Type", writer.FormDataContentType())')
  lines.push('    resp, err := http.DefaultClient.Do(req)', '    check(err)', '    defer resp.Body.Close()',
    '    fmt.Println(resp.Status)', '    kind := resp.Header.Get("Content-Type")',
    '    if resp.StatusCode >= 300 || strings.Contains(kind, "json") || strings.Contains(kind, "text") {',
    '        _, err = io.Copy(os.Stdout, resp.Body)', '        check(err)', '    } else {',
    '        output, err := os.Create(' + quote(options.outputFile ?? 'response.bin') + ')', '        check(err)',
    '        defer output.Close()', '        _, err = io.Copy(output, resp.Body)', '        check(err)', '    }', '}')
  return lines.join('\n')
}

function generateJava(options: GenerateEdgeCodeOptions): string {
  const { url, headers, hasBody, multipart, form } = requestParts(options)
  const lines = ['import java.net.URI;', 'import java.net.http.*;', 'import java.nio.file.*;', 'import java.nio.charset.StandardCharsets;',
    'import java.util.*;', '', 'public class EdgeRequest {', '  public static void main(String[] args) throws Exception {',
    '    if (System.getenv("SUB2API_KEY") == null) throw new IllegalStateException("Set SUB2API_KEY");']
  if (multipart) {
    lines.push('    String boundary = UUID.randomUUID().toString();', '    List<HttpRequest.BodyPublisher> parts = new ArrayList<>();')
    for (const field of form) {
      const disposition = 'Content-Disposition: form-data; name="' + field.name.replace(/["\r\n]/g, '_') + '"'
        + (field.kind === 'file' ? '; filename="' + (field.value.split(/[\\/]/).pop() || 'input.mp4').replace(/["\r\n]/g, '_') + '"' : '')
        + '\r\n\r\n'
      lines.push('    parts.add(HttpRequest.BodyPublishers.ofString("--" + boundary + "\\r\\n" + ' + quote(disposition) + '));',
        '    parts.add(HttpRequest.BodyPublishers.' + (field.kind === 'file' ? 'ofFile(Path.of(' + quote(field.value) + '))' : 'ofString(' + quote(field.value) + ')') + ');',
        '    parts.add(HttpRequest.BodyPublishers.ofString("\\r\\n"));')
    }
    lines.push('    parts.add(HttpRequest.BodyPublishers.ofString("--" + boundary + "--\\r\\n"));',
      '    HttpRequest.BodyPublisher body = HttpRequest.BodyPublishers.concat(parts.toArray(HttpRequest.BodyPublisher[]::new));')
  }
  const body = !hasBody ? 'HttpRequest.BodyPublishers.noBody()'
    : multipart ? 'body' : 'HttpRequest.BodyPublishers.ofString(' + quote(options.body) + ')'
  lines.push('    HttpRequest request = HttpRequest.newBuilder(URI.create(' + quote(url) + '))',
    '        .method(' + quote(options.method) + ', ' + body + ')')
  headers.forEach(h => lines.push('        .header(' + quote(h.name) + ', ' + headerValue(h.value, 'java') + ')'))
  if (multipart) lines.push('        .header("Content-Type", "multipart/form-data; boundary=" + boundary)')
  lines.push('        .build();', '    HttpResponse<byte[]> response = HttpClient.newHttpClient().send(request, HttpResponse.BodyHandlers.ofByteArray());',
    '    System.out.println(response.statusCode());', '    String kind = response.headers().firstValue("content-type").orElse("");',
    '    if (response.statusCode() >= 300 || kind.contains("json") || kind.contains("text")) {',
    '      System.out.println(new String(response.body(), StandardCharsets.UTF_8));', '    } else {',
    '      Files.write(Path.of(' + quote(options.outputFile ?? 'response.bin') + '), response.body());', '    }', '  }', '}')
  return lines.join('\n')
}

export function generateEdgeCode(options: GenerateEdgeCodeOptions): string {
  if (options.language === 'curl') return buildEdgeCurl(options)
  if (options.language === 'javascript') return generateJavaScript(options)
  if (options.language === 'python') return generatePython(options)
  if (options.language === 'go') return generateGo(options)
  return generateJava(options)
}
