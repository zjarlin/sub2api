import { spawn, spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { homedir } from 'node:os';
import { join, posix, win32 } from 'node:path';

export type Platform = 'macos' | 'windows' | 'linux';
export type CodexInstallSource = 'official' | 'modified';
export type CodexClient = 'desktop' | 'cli';

export const MODIFIED_INSTALL_URL_ENV = 'SUB2API_CODEX_MODIFIED_INSTALL_URL';
export const DEFAULT_MODIFIED_INSTALL_REPO = 'zjarlin/sub2api';
export const MODIFIED_INSTALL_REPO_ENV = 'SUB2API_CODEX_MODIFIED_INSTALL_REPO';

const MODIFIED_INSTALL_ASSETS: Record<Platform, string[]> = {
  macos: ['codex-install.sh'],
  linux: ['codex-install.sh'],
  windows: ['codex-install.ps1']
};

export interface CommandPlan {
  command: string;
  args: string[];
}

export interface InstallPlan {
  platform: Platform;
  supported: boolean;
  installed: boolean;
  source: CodexInstallSource;
  install?: CommandPlan[];
}

export interface InstallOptions {
  platform?: NodeJS.Platform;
  source?: CodexInstallSource;
  modifiedInstallerUrl?: string;
  baseUrl?: string;
  client?: CodexClient;
  installDir?: string;
}

const CODEX_APP_CANDIDATES = [
  '/Applications/Codex.app',
  '/Applications/ChatGPT.app',
  join(homedir(), 'Applications', 'Codex.app'),
  join(homedir(), 'Applications', 'ChatGPT.app')
];

export function detectPlatform(platform = process.platform): Platform {
  if (platform === 'darwin') return 'macos';
  if (platform === 'win32') return 'windows';
  return 'linux';
}

export function resolveDirectory(value: string, platform = process.platform): string {
  if (!value.trim() || /[\x00-\x1f]/.test(value)) {
    throw new Error('Directory must be a non-empty path without control characters');
  }
  if (platform === 'win32') {
    if (!win32.isAbsolute(value) || !/^(?:[a-z]:[\\/]|\\\\)/i.test(value)) {
      throw new Error('Windows directory must be an absolute path, for example D:\\Codex');
    }
    return win32.normalize(value);
  }
  if (!posix.isAbsolute(value)) {
    throw new Error('Directory must be an absolute path');
  }
  return posix.resolve(value);
}

export function codexConfigDir(platform = process.platform, explicitDir?: string): string {
  if (explicitDir !== undefined) {
    return resolveDirectory(explicitDir, platform);
  }
  const configured = process.env.CODEX_HOME;
  if (configured) {
    return resolveDirectory(configured, platform);
  }
  if (detectPlatform(platform) === 'windows') {
    return join(process.env.USERPROFILE || homedir(), '.codex');
  }
  return join(homedir(), '.codex');
}

export function isCodexInstalled(platform = process.platform): boolean {
  const detected = detectPlatform(platform);
  if (detected === 'macos') {
    return CODEX_APP_CANDIDATES.some((candidate) => existsSync(candidate));
  }
  if (detected === 'windows') {
    const result = spawnSync('powershell.exe', ['-NoProfile', '-Command',
      "$ErrorActionPreference = 'Stop'; $app = Get-AppxPackage | Where-Object { $_.Name -match '^OpenAI[.](ChatGPT|Codex)' }; if ($app) { exit 0 } else { exit 1 }"], { stdio: 'ignore', shell: false });
    return result.status === 0;
  }

  return false;
}

export function parseCodexInstallSource(value: string | undefined): CodexInstallSource {
  const source = value || 'official';
  if (source === 'official' || source === 'modified') return source;
  throw new Error('--install-source must be official or modified');
}

export function modifiedInstallerUrl(explicitUrl?: string): string | undefined {
  const url = (explicitUrl || process.env[MODIFIED_INSTALL_URL_ENV] || '').trim();
  return url || undefined;
}

export function defaultModifiedInstallerUrl(
  platform: Platform,
  repo = process.env[MODIFIED_INSTALL_REPO_ENV] || DEFAULT_MODIFIED_INSTALL_REPO
): string {
  const asset = MODIFIED_INSTALL_ASSETS[platform][0];
  return `https://github.com/${repo}/releases/latest/download/${asset}`;
}

export function assertInstallerUrl(value: string | undefined): string {
  if (!value) throw new Error(`${MODIFIED_INSTALL_URL_ENV} is required`);
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    throw new Error('Modified Codex installer URL must be an http(s) URL');
  }
  if (parsed.protocol !== 'https:' && parsed.protocol !== 'http:') {
    throw new Error('Modified Codex installer URL must be an http(s) URL');
  }
  return parsed.toString();
}

export function planClientInstall(options: InstallOptions = {}): InstallPlan {
  const platform = options.platform || process.platform;
  const detected = detectPlatform(platform);
  const client = options.client || 'desktop';
  const installDir = options.installDir === undefined ? undefined : resolveDirectory(options.installDir, platform);
  const gatewayRoot = (options.baseUrl || '').replace(/\/+$/, '').replace(/\/v1$/i, '');

  if (options.source === 'modified' && (client === 'cli' || installDir)) {
    throw new Error('--client cli and --install-dir require --install-source official');
  }
  if (installDir && client === 'desktop' && detected !== 'macos') {
    throw new Error('Custom --install-dir is supported by --client cli. Windows Store desktop apps use Settings > System > Storage > Where new content is saved; move an existing app in Installed apps.');
  }

  if (client === 'cli') {
    const args = ['install', '--global', '@openai/codex', '--registry=https://registry.npmmirror.com'];
    if (installDir) {
      const cacheDir = (platform === 'win32' ? win32 : posix).join(installDir, 'npm-cache');
      args.push('--prefix', installDir, '--cache', cacheDir);
    }
    const install = detected === 'windows'
      ? [{ command: 'powershell.exe', args: ['-NoProfile', '-Command',
          `& npm.cmd ${args.map(powerShellLiteral).join(' ')}; if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }`] }]
      : [{ command: 'npm', args }];
    return { platform: detected, supported: true, installed: false, source: 'official', install };
  }

  if (options.source === 'modified') {
    const installerUrl = assertInstallerUrl(
      modifiedInstallerUrl(options.modifiedInstallerUrl) || defaultModifiedInstallerUrl(detected)
    );
    return {
      platform: detected,
      supported: true,
      installed: false,
      source: 'modified',
      install: [modifiedInstallCommand(installerUrl, detected)]
    };
  }

  if (!installDir && isCodexInstalled(platform)) {
    return { platform: detected, supported: true, installed: true, source: 'official' };
  }

  if (detected === 'macos') {
    return {
      platform: detected,
      supported: true,
      installed: false,
      source: 'official',
      install: [
        {
          command: 'bash',
          args: ['-lc', macosInstallScript(installDir, gatewayRoot ? `${gatewayRoot}/downloads/Codex.dmg` : undefined)]
        }
      ]
    };
  }

  if (detected === 'windows') {
    return {
      platform: detected,
      supported: true,
      installed: false,
      source: 'official',
      install: [
        {
          command: 'powershell.exe',
          args: ['-NoProfile', '-Command', windowsInstallScript(gatewayRoot ? `${gatewayRoot}/downloads/ChatGPT-Installer.exe` : undefined)]
        }
      ]
    };
  }

  return { platform: detected, supported: false, installed: false, source: 'official' };
}

function modifiedInstallCommand(installerUrl: string, platform: Platform): CommandPlan {
  if (platform === 'windows') {
    return {
      command: 'powershell.exe',
      args: [
        '-NoProfile',
        '-ExecutionPolicy',
        'Bypass',
        '-Command',
        `$ErrorActionPreference = 'Stop'; $u = $env:SUB2API_CODEX_MODIFIED_INSTALL_URL; if (-not $u) { $u = ${powerShellLiteral(installerUrl)} }; irm $u | iex`
      ]
    };
  }

  return {
    command: 'bash',
    args: [
      '-lc',
      `set -euo pipefail
installer_url=${shellLiteral(installerUrl)}
if [ -z "$installer_url" ]; then
  echo "Modified Codex installer URL is required" >&2
  exit 1
fi
case "$(uname -s)" in
  Darwin|Linux) ;;
  *) echo "Unsupported platform for modified Codex installer" >&2; exit 1 ;;
esac
tmp_installer="$(mktemp)"
cleanup() { rm -f "$tmp_installer"; }
trap cleanup EXIT
curl -fL "$installer_url" -o "$tmp_installer"
bash "$tmp_installer"`
    ]
  };
}

export function shellLiteral(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

export function powerShellLiteral(value: string): string {
  return `'${value.replace(/'/g, "''")}'`;
}

export function runCommand(plan: CommandPlan): Promise<void> {
  return new Promise((resolve, reject) => {
    const child = spawn(plan.command, plan.args, { stdio: 'inherit', shell: false });
    child.once('error', reject);
    child.once('exit', (code, signal) => {
      if (code === 0) {
        resolve();
        return;
      }
      reject(new Error(`${plan.command} exited with ${signal || code}`));
    });
  });
}

export function windowsInstallScript(cachedInstallerUrl?: string): string {
  const cachedUrl = cachedInstallerUrl ? `  $cachedInstallerUrl = ${powerShellLiteral(cachedInstallerUrl)}
` : '';
  return `$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
${cachedUrl}$tempDir = Join-Path ([IO.Path]::GetTempPath()) ('sub2api-codex-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tempDir | Out-Null
try {
  $installer = Join-Path $tempDir 'ChatGPT-Setup.exe'
  try {
    if ($cachedInstallerUrl) { Invoke-WebRequest -UseBasicParsing -Uri $cachedInstallerUrl -OutFile $installer -TimeoutSec 120 }
    else { throw 'No gateway cache URL configured' }
  } catch {
    Write-Warning 'Gateway cache unavailable; downloading from Microsoft.'
    Invoke-WebRequest -UseBasicParsing -Uri 'https://get.microsoft.com/installer/download/9PLM9XGG6VKS' -OutFile $installer -TimeoutSec 120
  }
  $process = Start-Process -FilePath $installer -PassThru
  if (-not $process.WaitForExit(90000)) {
    try { $process.Kill() } catch { }
    throw 'The Microsoft Store installer did not finish within 90 seconds.'
  }
  if ($process.ExitCode -ne 0) { throw "Windows installer exited with $($process.ExitCode)" }
} catch {
  Write-Warning $_.Exception.Message
  Write-Warning 'Falling back to the official Codex CLI from the npm mirror.'
  $npm = Get-Command npm.cmd -ErrorAction SilentlyContinue
  if (-not $npm) { $npm = Get-Command npm -ErrorAction SilentlyContinue }
  if (-not $npm) { throw 'Node.js 22.14 or newer with npm is required for the CLI fallback.' }
  & $npm.Source install --global '@openai/codex' '--registry=https://registry.npmmirror.com'
  if ($LASTEXITCODE -ne 0) { throw "npm install failed with exit code $LASTEXITCODE" }
} finally {
  Remove-Item -LiteralPath $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}`;
}

function macosInstallScript(installDir?: string, cachedDMGURL?: string): string {
  const cachedURL = cachedDMGURL ? `cached_url=${shellLiteral(cachedDMGURL)}
` : '';
  return `set -euo pipefail
tmpdir="$(mktemp -d)"
cleanup() {
  if [ -n "\${mount_dir:-}" ] && [ -d "$mount_dir" ]; then hdiutil detach "$mount_dir" -quiet || true; fi
  rm -rf "$tmpdir"
}
trap cleanup EXIT
dmg="$tmpdir/Codex.dmg"
${cachedURL}if [ -n "\${cached_url:-}" ] && curl -fL "$cached_url" -o "$dmg"; then
  :
else
  curl -fL "https://persistent.oaistatic.com/codex-app-prod/Codex.dmg" -o "$dmg"
fi
mount_dir="$tmpdir/mount"
mkdir -p "$mount_dir"
hdiutil attach "$dmg" -nobrowse -quiet -mountpoint "$mount_dir"
app_path="$(find "$mount_dir" -maxdepth 1 -name '*.app' -print -quit)"
if [ -z "$app_path" ]; then echo "No app found in Codex DMG" >&2; exit 1; fi
${installDir ? `target_dir=${shellLiteral(installDir)}\nmkdir -p "$target_dir"` : 'target_dir="/Applications"\nif [ ! -w "$target_dir" ]; then target_dir="$HOME/Applications"; mkdir -p "$target_dir"; fi'}
ditto "$app_path" "$target_dir/$(basename "$app_path")"
echo "Installed $(basename "$app_path") to $target_dir"`;
}
