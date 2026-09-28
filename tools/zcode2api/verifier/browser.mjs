import { chromium } from 'playwright-core'

const origin = 'https://zcode.z.ai'
const sdkUrl = 'https://o.alicdn.com/captcha-frontend/aliyunCaptcha/AliyunCaptcha.js'

export class VerificationError extends Error {
  constructor(code, status) {
    super(code)
    this.code = code
    this.status = status
  }
}

// 只调用官方 SDK，不保存、伪造或重复使用验证结果。
export async function verifyInPage({ config, headless, timeoutMs = 120000, triggerDelayMs = 2000 }) {
  return await new Promise((resolve, reject) => {
    let settled = false
    let fallback
    let trigger
    let interactiveStarted = false
    const finish = (value, code) => {
      if (settled) return
      settled = true
      clearTimeout(deadline)
      clearTimeout(fallback)
      clearTimeout(trigger)
      if (code) reject(new Error(code))
      else resolve(value)
    }
    const deadline = setTimeout(() => finish(null, 'verification_timeout'), timeoutMs)
    const button = document.querySelector('#captcha-button')
    const interactive = () => {
      if (settled) return
      if (headless) finish(null, 'interactive_verification_required')
      else if (!interactiveStarted) {
        interactiveStarted = true
        button.click()
      }
    }
    window.AliyunCaptchaConfig = { region: config.region, prefix: config.prefix }
    try {
      window.initAliyunCaptcha({
        SceneId: config.sceneId,
        mode: 'popup',
        language: config.language,
        showErrorTip: true,
        element: '#captcha-element',
        button: '#captcha-button',
        getInstance(instance) {
          trigger = setTimeout(() => {
            if (settled) return
            try {
              if (typeof instance.startTracelessVerification === 'function') {
                instance.startTracelessVerification()
                if (!settled) fallback = setTimeout(interactive, 8000)
              } else interactive()
            } catch { finish(null, 'verification_failed') }
          }, triggerDelayMs)
        },
        success(param) {
          if (typeof param === 'string' && param.trim()) finish(param)
          else finish(null, 'verification_failed')
        },
        fail(result) {
          const code = result?.verifyCode ?? result?.VerifyCode
          const passed = (result?.success === true && result?.verifyResult === true) || code === 'T006'
          const param = result?.captchaVerifyParam ?? result?.CaptchaVerifyParam
          if (passed && typeof param === 'string' && param.trim()) finish(param)
          else if (code === 'F008') finish(null, 'verification_failed')
          else interactive()
        },
        onError() { finish(null, 'verification_failed') },
      })
    } catch { finish(null, 'verification_failed') }
  })
}

export class BrowserVerifier {
  constructor({ profile, executablePath, headless = false }) {
    this.profile = profile
    this.executablePath = executablePath
    this.headless = headless
    this.context = undefined
  }

  async verify(headers, signal) {
    signal.throwIfAborted()
    const appVersion = headers['x-zcode-app-version'] || '3.14.3'
    const url = new URL('/api/v1/client/configs', origin)
    url.searchParams.set('app_version', appVersion)
    url.searchParams.set('platform', headers['x-platform'] || `${process.platform}-${process.arch}`)
    const response = await fetch(url, { signal, headers: { 'User-Agent': `ZCode/${appVersion}` } })
    if (!response.ok) throw new VerificationError('configuration_unavailable', 502)
    const body = await response.json()
    const config = body.data?.configs?.captcha
    if (body.code !== 0 || !config || config.enabled === false || !config.sceneId || !config.prefix || !['cn', 'sgp'].includes(config.region)) {
      throw new VerificationError('configuration_unavailable', 502)
    }
    signal.throwIfAborted()
    if (!this.context) {
      this.context = await chromium.launchPersistentContext(this.profile, {
        executablePath: this.executablePath,
        headless: this.headless,
        viewport: { width: 960, height: 720 },
      })
    }
    const page = await this.context.newPage()
    const cancel = () => { page.close().catch(() => {}) }
    signal.addEventListener('abort', cancel, { once: true })
    try {
      signal.throwIfAborted()
      await page.goto(origin, { waitUntil: 'domcontentloaded', timeout: 30000 })
      await page.setContent('<!doctype html><html><head><title>Start Plan Verification</title></head><body style="font:16px system-ui;margin:40px"><main><h1 style="font-size:22px">Start Plan Verification</h1><div id="captcha-element"></div><button id="captcha-button" type="button">Verify</button></main></body></html>')
      await page.addScriptTag({ url: sdkUrl })
      const param = await page.evaluate(verifyInPage, {
        config: { ...config, language: headers['x-client-language']?.startsWith('zh') ? 'cn' : 'en' },
        headless: this.headless,
      })
      signal.throwIfAborted()
      return { captcha_verify_param: param, captcha_region: config.region }
    } catch (error) {
      signal.throwIfAborted()
      if (error.message.includes('interactive_verification_required')) throw new VerificationError('interactive_verification_required', 409)
      throw new VerificationError('verification_failed', 502)
    } finally {
      signal.removeEventListener('abort', cancel)
      await page.close()
    }
  }

  async close() {
    await this.context?.close()
    this.context = undefined
  }
}
