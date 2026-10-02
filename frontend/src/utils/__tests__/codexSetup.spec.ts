import { describe, expect, it } from 'vitest'
import { buildCodexSetupCommand, defaultCodexSetupOptions } from '../codexSetup'

describe('Codex setup command quoting', () => {
  it('uses the gateway-hosted Windows setup script instead of npm or Microsoft Store', () => {
    const options = defaultCodexSetupOptions()
    const command = buildCodexSetupCommand('https://example.com', 'sk-test', options, true)
    expect(command).toContain('powershell.exe -NoProfile -ExecutionPolicy Bypass -Command')
    expect(command).toContain("https://example.com/downloads/codex-setup.ps1")
    expect(command).toContain("-BaseUrl https://example.com -ApiKey sk-test")
    expect(command).not.toContain('npx.cmd')
    expect(command).not.toContain('get.microsoft.com')
    expect(buildCodexSetupCommand('https://example.com', 'sk-test', options))
      .toBe('npx --registry=https://registry.npmmirror.com -y sub2api-codex-setup --base-url https://example.com --api-key sk-test')
  })

  it('keeps PowerShell metacharacters in a literal argument', () => {
    const options = { ...defaultCodexSetupOptions(), codexHome: "D:\\O'Brien\\$data;name" }
    const command = buildCodexSetupCommand('https://example.com', 'sk-test', options, true)
    expect(command).toContain("-CodexHome 'D:\\O''Brien\\$data;name' -PersistHome")
  })

  it.each(['D', 'G'])('quotes %s drive paths for both CMD and PowerShell', drive => {
    const options = { ...defaultCodexSetupOptions(), codexHome: `${drive}:\\Codex\\data` }
    const command = buildCodexSetupCommand('https://example.com', 'sk-test', options, true)
    expect(command).toContain(`-CodexHome "${drive}:\\Codex\\data" -PersistHome`)
    expect(command).not.toContain("-CodexHome '")
  })

  it('preserves spaces and apostrophes in Windows paths shared by CMD and PowerShell', () => {
    const options = { ...defaultCodexSetupOptions(), client: 'cli' as const, installDir: "G:\\AI tools\\O'Brien", codexHome: 'G:\\AI tools\\data' }
    const command = buildCodexSetupCommand('https://example.com', 'sk-test', options, true)
    expect(command).toContain('-Client cli -InstallDir "G:\\AI tools\\O\'Brien"')
    expect(command).toContain('-CodexHome "G:\\AI tools\\data" -PersistHome')
  })

  it('keeps POSIX command substitution and quotes inside literal arguments', () => {
    const options = { ...defaultCodexSetupOptions(), codexHome: "/data/O'Brien/$(touch marker)" }
    const command = buildCodexSetupCommand('https://example.com', 'sk-test', options)
    expect(command).toContain("--codex-home '/data/O'\\''Brien/$(touch marker)'")
    expect(command).not.toContain('--persist-home')
  })
})
