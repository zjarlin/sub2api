import { spawn, spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

export type Platform = 'macos' | 'windows' | 'linux';

export interface CommandPlan {
  command: string;
  args: string[];
}

export interface InstallPlan {
  platform: Platform;
  supported: boolean;
  installed: boolean;
  install?: CommandPlan[];
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

export function planClientInstall(platform = process.platform): InstallPlan {
  const detected = detectPlatform(platform);
  if (isCodexInstalled(platform)) {
    return { platform: detected, supported: true, installed: true };
  }

  if (detected === 'macos') {
    return {
      platform: detected,
      supported: true,
      installed: false,
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
      install: [
        {
          command: 'winget',
          args: ['install', 'Codex', '-s', 'msstore', '--accept-package-agreements', '--accept-source-agreements']
        }
      ]
    };
  }

  return { platform: detected, supported: false, installed: false };
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
