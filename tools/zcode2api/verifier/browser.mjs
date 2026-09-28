import { chromium } from 'playwright-core'

const origin = 'https://zcode.z.ai'
const sdkUrl = 'https://o.alicdn.com/captcha-frontend/aliyunCaptcha/AliyunCaptcha.js'
const verificationUrl = `${origin}/__start_plan_verification__`

export class VerificationError extends Error {
  constructor(code, status) {
    super(code)
    this.code = code
    this.status = status
  }
}

export async function fetchCaptchaConfig(headers, signal) {
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
  return { ...config, language: headers['x-client-language']?.startsWith('zh') ? 'cn' : 'en' }
}

// 只调用官方 SDK，不保存、伪造或重复使用验证结果。
export async function verifyInPage({ config, headless, timeoutMs = 120000, triggerDelayMs = 2000 }) {
  return await new Promise((resolve, reject) => {
    let settled = false
    let fallback
    let trigger
    let verificationStarted = false
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
          // SDK 打开或刷新挑战时会再次提供实例，当前请求不能重复发起无感验证。
          if (settled || verificationStarted || interactiveStarted) {
            return
          }
          verificationStarted = true
          trigger = setTimeout(() => {
            if (settled) return
            try {
              // 与官方客户端一致，先尝试无感验证，仅在 SDK 要求时升级为交互验证。
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
    this.page = undefined
    this.pageConfigKey = undefined
  }

  async verify(headers, signal) {
    signal.throwIfAborted()
    const config = await fetchCaptchaConfig(headers, signal)
    signal.throwIfAborted()
    if (!this.context) {
      this.context = await chromium.launchPersistentContext(this.profile, {
        executablePath: this.executablePath,
        headless: this.headless,
        viewport: { width: 960, height: 720 },
      })
    }
    const configKey = `${config.region}:${config.prefix}:${config.sceneId}`
    if (this.pageConfigKey !== configKey || this.page?.isClosed()) {
      await this.page?.close()
      this.page = undefined
    }
    const page = this.page || await this.context.newPage()
    const cancel = () => { page.close().catch(() => {}) }
    signal.addEventListener('abort', cancel, { once: true })
    try {
      signal.throwIfAborted()
      if (!this.page) {
        // 独立提供验证文档，避免官网的地区跳转和页面脚本覆盖正在进行的验证码。
        await page.route(verificationUrl, route => route.fulfill({
          contentType: 'text/html',
          body: '<!doctype html><html><head><title>Start Plan Verification</title></head><body style="font:16px system-ui;margin:40px"><main><h1 style="font-size:22px">Start Plan Verification</h1><div id="captcha-element"></div><button id="captcha-button" type="button">Verify</button></main></body></html>',
        }))
        await page.goto(verificationUrl, { waitUntil: 'domcontentloaded', timeout: 30000 })
        await page.addScriptTag({ url: sdkUrl })
        this.page = page
        this.pageConfigKey = configKey
      }
      const param = await page.evaluate(verifyInPage, {
        config,
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
      // 保留官方 SDK 的页面上下文，每次重新初始化验证，取消时仍关闭当前页。
      if (!page.isClosed() && this.page === page) {
        await page.evaluate(() => document.querySelector('#captcha-element')?.replaceChildren())
      } else {
        await page.close()
      }
    }
  }

  async close() {
    await this.context?.close()
    this.context = undefined
    this.page = undefined
    this.pageConfigKey = undefined
  }
}
