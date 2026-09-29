import { describe, expect, it } from 'vitest'
import { buildCodexSetupCommand, defaultCodexSetupOptions } from '../codexSetup'

describe('Codex setup command quoting', () => {
  it('uses the Windows command shim so PowerShell does not execute npx.ps1', () => {
    const options = defaultCodexSetupOptions()
    expect(buildCodexSetupCommand('https://example.com', 'sk-test', options, true))
      .toBe('npx.cmd -y sub2api-codex-setup --base-url https://example.com --api-key sk-test')
    expect(buildCodexSetupCommand('https://example.com', 'sk-test', options))
      .toBe('npx -y sub2api-codex-setup --base-url https://example.com --api-key sk-test')
  })

  it('keeps PowerShell metacharacters in a literal argument', () => {
    const options = { ...defaultCodexSetupOptions(), codexHome: "D:\\O'Brien\\$data;name" }
    const command = buildCodexSetupCommand('https://example.com', 'sk-test', options, true)
    expect(command).toContain("--codex-home 'D:\\O''Brien\\$data;name' --persist-home")
  })

  it.each(['D', 'G'])('quotes %s drive paths for both CMD and PowerShell', drive => {
    const options = { ...defaultCodexSetupOptions(), codexHome: `${drive}:\\Codex\\data` }
    const command = buildCodexSetupCommand('https://example.com', 'sk-test', options, true)
    expect(command).toContain(`--codex-home "${drive}:\\Codex\\data" --persist-home`)
    expect(command).not.toContain("--codex-home '")
  })

  it('preserves spaces and apostrophes in Windows paths shared by CMD and PowerShell', () => {
    const options = { ...defaultCodexSetupOptions(), client: 'cli' as const, installDir: "G:\\AI tools\\O'Brien", codexHome: 'G:\\AI tools\\data' }
    const command = buildCodexSetupCommand('https://example.com', 'sk-test', options, true)
    expect(command).toContain('--install-dir "G:\\AI tools\\O\'Brien"')
    expect(command).toContain('--codex-home "G:\\AI tools\\data" --persist-home')
  })

  it('keeps POSIX command substitution and quotes inside literal arguments', () => {
    const options = { ...defaultCodexSetupOptions(), codexHome: "/data/O'Brien/$(touch marker)" }
    const command = buildCodexSetupCommand('https://example.com', 'sk-test', options)
    expect(command).toContain("--codex-home '/data/O'\\''Brien/$(touch marker)'")
    expect(command).not.toContain('--persist-home')
  })
})
