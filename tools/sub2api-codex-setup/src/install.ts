import { spawn, spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

export type Platform = 'macos' | 'windows' | 'linux';
export type CodexInstallSource = 'official' | 'modified';

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

export function codexConfigDir(platform = process.platform): string {
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
    const result = spawnSync('where.exe', ['codex'], { stdio: 'ignore', shell: false });
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

  if (isCodexInstalled(platform)) {
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
          args: ['-lc', macosInstallScript()]
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
          command: 'winget',
          args: ['install', 'Codex', '-s', 'msstore', '--accept-package-agreements', '--accept-source-agreements']
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

function shellLiteral(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}

function powerShellLiteral(value: string): string {
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

function macosInstallScript(): string {
  return `set -euo pipefail
tmpdir="$(mktemp -d)"
cleanup() {
  if [ -n "\${mount_dir:-}" ] && [ -d "$mount_dir" ]; then hdiutil detach "$mount_dir" -quiet || true; fi
  rm -rf "$tmpdir"
}
trap cleanup EXIT
dmg="$tmpdir/Codex.dmg"
curl -fL "https://persistent.oaistatic.com/codex-app-prod/Codex.dmg" -o "$dmg"
mount_dir="$tmpdir/mount"
mkdir -p "$mount_dir"
hdiutil attach "$dmg" -nobrowse -quiet -mountpoint "$mount_dir"
app_path="$(find "$mount_dir" -maxdepth 1 -name '*.app' -print -quit)"
if [ -z "$app_path" ]; then echo "No app found in Codex DMG" >&2; exit 1; fi
target_dir="/Applications"
if [ ! -w "$target_dir" ]; then target_dir="$HOME/Applications"; mkdir -p "$target_dir"; fi
ditto "$app_path" "$target_dir/$(basename "$app_path")"
echo "Installed $(basename "$app_path") to $target_dir"`;
}
