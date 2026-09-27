export interface CodexSetupOptions {
  client: 'desktop' | 'cli'
  installDir: string
  codexHome: string
}

export function defaultCodexSetupOptions(): CodexSetupOptions {
  return { client: 'desktop', installDir: '', codexHome: '' }
}

export function buildCodexSetupCommand(
  baseUrl: string,
  apiKey: string,
  options: CodexSetupOptions,
  windows = false,
  authMode: 'legacy' | 'api-key' = 'api-key'
): string {
  const quote = (value: string) => {
    if (/^[a-zA-Z0-9_./:@=-]+$/.test(value)) return value
    return windows ? `'${value.replace(/'/g, "''")}'` : `'${value.replace(/'/g, "'\\''")}'`
  }
  const args = ['npx', '-y', 'sub2api-codex-setup', '--base-url', quote(baseUrl), '--api-key', quote(apiKey)]
  if (authMode === 'legacy') args.push('--auth-mode', 'legacy')
  if (options.client === 'cli') args.push('--client', 'cli')
  if (options.client === 'cli' && options.installDir.trim()) {
    args.push('--install-dir', quote(options.installDir.trim()))
  }
  if (options.codexHome.trim()) {
    args.push('--codex-home', quote(options.codexHome.trim()))
    if (windows) args.push('--persist-home')
  }
  return args.join(' ')
}
