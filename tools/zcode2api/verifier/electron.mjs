import { mkdir, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { fetchCaptchaConfig, verifyInPage, VerificationError } from './browser.mjs'

// 使用官方客户端同系列的原生 Electron 页面，Linux 后台显示由 Xvfb 提供。
export class ElectronVerifier {
  constructor({ BrowserWindow, profile, headless = false }) {
    this.BrowserWindow = BrowserWindow
    this.profile = profile
    this.headless = headless
    this.window = undefined
    this.configKey = undefined
  }

  async verify(headers, signal) {
    signal.throwIfAborted()
    const config = await fetchCaptchaConfig(headers, signal)
    const configKey = `${config.region}:${config.prefix}:${config.sceneId}`
    if (this.configKey !== configKey) {
      await this.close()
    }
    signal.throwIfAborted()
    const window = this.window && !this.window.isDestroyed() ? this.window : new this.BrowserWindow({
      width: 960,
      height: 720,
      show: true,
      webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true, backgroundThrottling: false },
    })
    const cancel = () => { if (!window.isDestroyed()) window.destroy() }
    signal.addEventListener('abort', cancel, { once: true })
    try {
      signal.throwIfAborted()
      if (window !== this.window) {
        await mkdir(this.profile, { recursive: true })
        const documentPath = join(this.profile, 'verification.html')
        await writeFile(documentPath, '<!doctype html><html><head><title>Start Plan Verification</title></head><body style="font:16px system-ui;margin:40px"><h1>Start Plan Verification</h1><div id="captcha-element"></div><button id="captcha-button" type="button">Verify</button></body></html>')
        await window.loadFile(documentPath)
        await window.webContents.executeJavaScript(`new Promise((resolve, reject) => {
          const script = document.createElement('script');
          script.src = 'https://o.alicdn.com/captcha-frontend/aliyunCaptcha/AliyunCaptcha.js';
          script.onload = () => resolve();
          script.onerror = () => reject(new Error('captcha_script_load_failed'));
          document.head.appendChild(script);
        })`)
        this.window = window
        this.configKey = configKey
      }
      signal.throwIfAborted()
      const options = JSON.stringify({ config, headless: this.headless })
      const param = await window.webContents.executeJavaScript(`(${verifyInPage.toString()})(${options})`)
      signal.throwIfAborted()
      return { captcha_verify_param: param, captcha_region: config.region }
    } catch (error) {
      signal.throwIfAborted()
      if (error.message.includes('interactive_verification_required')) {
        throw new VerificationError('interactive_verification_required', 409)
      }
      throw new VerificationError('verification_failed', 502)
    } finally {
      signal.removeEventListener('abort', cancel)
      if (!window.isDestroyed() && window === this.window) {
        await window.webContents.executeJavaScript("document.querySelector('#captcha-element')?.replaceChildren()")
      } else {
        cancel()
      }
    }
  }

  async close() {
    if (this.window && !this.window.isDestroyed()) {
      this.window.destroy()
    }
    this.window = undefined
    this.configKey = undefined
  }
}
