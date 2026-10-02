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
    if (windows && !/["%!$`]/.test(value) && [...value].every(character => character.charCodeAt(0) >= 32)) {
      return `"${value.replace(/(\\+)$/, '$1$1')}"`
    }
    return windows ? `'${value.replace(/'/g, "''")}'` : `'${value.replace(/'/g, "'\\''")}'`
  }
  if (windows) {
    const root = baseUrl.replace(/\/+$/, '')
    const downloadRoot = root.replace(/\/v1$/i, '')
    const commandParts = [`Invoke-WebRequest -UseBasicParsing -Uri '${downloadRoot}/downloads/codex-setup.ps1' -OutFile codex-setup.ps1;`, '& .\\codex-setup.ps1', '-BaseUrl', quote(root), '-ApiKey', quote(apiKey)]
    if (authMode === 'legacy') commandParts.push('-AuthMode', 'legacy')
    if (options.client === 'cli') commandParts.push('-Client', 'cli')
    if (options.client === 'cli' && options.installDir.trim()) commandParts.push('-InstallDir', quote(options.installDir.trim()))
    if (options.codexHome.trim()) commandParts.push('-CodexHome', quote(options.codexHome.trim()), '-PersistHome')
    return ['powershell.exe', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', commandParts.join(' ')].join(' ')
  }
  const args = ['npx', '--registry=https://registry.npmmirror.com', '-y', 'sub2api-codex-setup', '--base-url', quote(baseUrl), '--api-key', quote(apiKey)]
  if (authMode === 'legacy') args.push('--auth-mode', 'legacy')
  if (options.client === 'cli') args.push('--client', 'cli')
  if (options.client === 'cli' && options.installDir.trim()) {
    args.push('--install-dir', quote(options.installDir.trim()))
  }
  if (options.codexHome.trim()) {
    args.push('--codex-home', quote(options.codexHome.trim()))
  }
  return args.join(' ')
}
