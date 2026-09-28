import http from 'node:http'
import { timingSafeEqual } from 'node:crypto'
import { homedir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { BrowserVerifier, VerificationError } from './browser.mjs'
import { ElectronVerifier } from './electron.mjs'

const allowedHeaders = new Set(['x-zcode-app-version', 'x-platform', 'x-client-language'])

export function createServer({ key, verifier }) {
  if (!key) throw new Error('ZCODE_VERIFY_KEY is required')
  let queue = Promise.resolve()
  let queued = 0
  const json = (res, status, body) => {
    if (res.destroyed) return
    res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' })
    res.end(JSON.stringify(body))
  }
  return http.createServer(async (req, res) => {
    if (req.method === 'GET' && req.url === '/livez') {
      json(res, 200, { alive: true })
      return
    }
    const supplied = Buffer.from(req.headers.authorization || '')
    const expected = Buffer.from(`Bearer ${key}`)
    if (supplied.length !== expected.length || !timingSafeEqual(supplied, expected)) {
      json(res, 401, { code: 'unauthorized' })
      return
    }
    if (req.method !== 'POST' || req.url !== '/verify') {
      json(res, 404, { code: 'not_found' })
      return
    }
    if (queued >= 8) {
      json(res, 429, { code: 'verification_queue_full' })
      return
    }
    const controller = new AbortController()
    res.on('close', () => controller.abort())
    const timer = setTimeout(() => controller.abort(), 120000)
    queued++
    try {
      let size = 0
      const chunks = []
      for await (const chunk of req) {
        size += chunk.length
        if (size > 16384) throw new VerificationError('request_too_large', 413)
        chunks.push(chunk)
      }
      let body
      try { body = JSON.parse(Buffer.concat(chunks).toString()) }
      catch { throw new VerificationError('invalid_request', 400) }
      if (!body || !body.source_headers || typeof body.source_headers !== 'object' || Array.isArray(body.source_headers)) {
        throw new VerificationError('invalid_request', 400)
      }
      const headers = Object.fromEntries(Object.entries(body.source_headers || {})
        .filter(([name, value]) => allowedHeaders.has(name.toLowerCase()) && typeof value === 'string' && value.length <= 100)
        .map(([name, value]) => [name.toLowerCase(), value]))
      const task = queue.then(() => {
        controller.signal.throwIfAborted()
        return verifier.verify(headers, controller.signal)
      })
      queue = task.catch(() => {})
      const result = await task
      controller.signal.throwIfAborted()
      json(res, 200, result)
    } catch (error) {
      json(res, controller.signal.aborted ? 504 : error.status || 502, { code: controller.signal.aborted ? 'verification_cancelled' : error.code || 'verification_failed' })
    } finally {
      queued--
      clearTimeout(timer)
    }
  })
}

async function main() {
  const profileRoot = process.env.ZCODE_VERIFY_PROFILE || join(homedir(), '.cache', 'zcode-start-plan-verifier')
  const profile = process.versions.electron ? join(profileRoot, 'electron') : profileRoot
  const headless = process.env.ZCODE_VERIFY_HEADLESS === 'true'
  let verifier
  let exit = code => process.exit(code)
  if (process.versions.electron) {
    const { app, BrowserWindow } = await import('electron')
    exit = code => app.exit(code)
    app.setPath('userData', profile)
    app.on('window-all-closed', () => {})
    await app.whenReady()
    verifier = new ElectronVerifier({ BrowserWindow, profile, headless })
  } else {
    verifier = new BrowserVerifier({
      profile,
      executablePath: process.env.ZCODE_VERIFY_BROWSER || undefined,
      headless,
    })
  }
  const server = createServer({ key: process.env.ZCODE_VERIFY_KEY, verifier })
  server.listen(Number(process.env.ZCODE_VERIFY_PORT || 7866), process.env.ZCODE_VERIFY_HOST || '127.0.0.1', () => {
    console.log('Start Plan verification service listening')
  })
  for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => {
    server.close()
    verifier.close().then(() => exit(0), () => exit(1))
  })
}

if (process.argv.slice(1).some(argument => !argument.startsWith('-') && import.meta.url === pathToFileURL(argument).href)) {
  // Electron 的 ready 事件须等入口模块完成求值，不能在顶层等待它。
  main().catch(error => {
    console.error(error.message)
    process.exit(1)
  })
}
