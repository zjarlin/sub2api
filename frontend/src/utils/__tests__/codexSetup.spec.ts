import { describe, expect, it } from 'vitest'
import { buildCodexSetupCommand, defaultCodexSetupOptions } from '../codexSetup'

describe('Codex setup command quoting', () => {
  it('keeps PowerShell metacharacters in a literal argument', () => {
    const options = { ...defaultCodexSetupOptions(), codexHome: "D:\\O'Brien\\$data;name" }
    const command = buildCodexSetupCommand('https://example.com', 'sk-test', options, true)
    expect(command).toContain("--codex-home 'D:\\O''Brien\\$data;name' --persist-home")
  })

  it('keeps POSIX command substitution and quotes inside literal arguments', () => {
    const options = { ...defaultCodexSetupOptions(), codexHome: "/data/O'Brien/$(touch marker)" }
    const command = buildCodexSetupCommand('https://example.com', 'sk-test', options)
    expect(command).toContain("--codex-home '/data/O'\\''Brien/$(touch marker)'")
    expect(command).not.toContain('--persist-home')
  })
})
