#!/usr/bin/env node
import { parseArgs } from 'node:util';
import { join } from 'node:path';
import { currentPlatformLabel, normalizeBaseUrl, writeCodexConfig } from './config.js';
import {
  DEFAULT_MODIFIED_INSTALL_REPO,
  MODIFIED_INSTALL_REPO_ENV,
  parseCodexInstallSource,
  codexConfigDir,
  resolveDirectory,
  powerShellLiteral,
  shellLiteral,
  planClientInstall,
  runCommand
} from './install.js';

declare const PACKAGE_VERSION: string;

const HELP = `sub2api-codex-setup

Install the Codex desktop app or CLI, then write Sub2API configuration.

Usage:
  npx -y sub2api-codex-setup --base-url <url> --api-key <key> [options]

Options:
  --base-url <url>       Sub2API base URL, with or without /v1
  --api-key <key>        Sub2API API key
  --model <model>        Codex model (default: gpt-5.5)
  --provider-name <name> Provider name written to config.toml (default: Sub2API)
  --auth-mode <mode>     api-key or legacy (default: api-key)
  --install-source <src> Codex client source: official or modified (default: official)
  --client <type>        desktop or cli (default: desktop)
  --install-dir <dir>    Absolute npm prefix for CLI, or app folder for macOS desktop
  --codex-home <dir>     Absolute config/data directory (default: CODEX_HOME or ~/.codex)
  --persist-home        Save CODEX_HOME in Windows user environment; reopen terminals/apps
  --modified-installer-url <url>
                          Override the modified installer URL; default is the latest GitHub
                          Release asset from ${DEFAULT_MODIFIED_INSTALL_REPO}
                          (override repository with ${MODIFIED_INSTALL_REPO_ENV})
  --no-install           Only write configuration; do not install Codex
  --dry-run              Print actions without installing or writing files
  --help                 Show this help
  --version              Print version`;

function parseCli() {
  return parseArgs({
    options: {
      version: { type: 'boolean' },
      help: { type: 'boolean' },
      'base-url': { type: 'string' },
      'api-key': { type: 'string' },
      model: { type: 'string', default: 'gpt-5.5' },
      'provider-name': { type: 'string', default: 'Sub2API' },
      'auth-mode': { type: 'string', default: 'api-key' },
      'install-source': { type: 'string', default: 'official' },
      client: { type: 'string', default: 'desktop' },
      'install-dir': { type: 'string' },
      'codex-home': { type: 'string' },
      'persist-home': { type: 'boolean', default: false },
      'modified-installer-url': { type: 'string' },
      'no-install': { type: 'boolean', default: false },
      'dry-run': { type: 'boolean', default: false }
    },
    allowPositionals: false
  });
}

async function main(): Promise<void> {
  const { values } = parseCli();
  if (values.version) {
    console.log(PACKAGE_VERSION);
    return;
  }
  if (values.help) {
    console.log(HELP);
    return;
  }

  const baseUrl = values['base-url'];
  const apiKey = values['api-key'];
  if (!baseUrl || !apiKey) {
    console.error('--base-url and --api-key are required. Run with --help for usage.');
    process.exitCode = 1;
    return;
  }

  const authMode = values['auth-mode'] === 'legacy' ? 'legacy' : 'api-key';
  if (values['auth-mode'] && values['auth-mode'] !== 'legacy' && values['auth-mode'] !== 'api-key') {
    throw new Error('--auth-mode must be legacy or api-key');
  }

  const normalizedBaseUrl = normalizeBaseUrl(baseUrl);
  const platform = currentPlatformLabel();
  const installSource = parseCodexInstallSource(values['install-source']);
  const client = values.client;
  if (client !== 'desktop' && client !== 'cli') {
    throw new Error('--client must be desktop or cli');
  }
  const directory = codexConfigDir(process.platform, values['codex-home']);
  const installDir = values['install-dir'] === undefined ? undefined : resolveDirectory(values['install-dir']);
  if (values['persist-home'] && (process.platform !== 'win32' || !values['codex-home'])) {
    throw new Error('--persist-home requires Windows and an explicit --codex-home');
  }
  if (values['no-install'] && installDir) {
    throw new Error('--install-dir cannot be used with --no-install');
  }
  const installPlan = values['no-install'] ? undefined : planClientInstall({
    source: installSource,
    baseUrl: normalizedBaseUrl,
    client,
    installDir,
    modifiedInstallerUrl: values['modified-installer-url']
  });

  console.log(`Detected platform: ${platform}`);
  console.log(`Codex install source: ${installSource}`);
  if (values['dry-run']) {
    console.log(`Config directory: ${directory}`);
    console.log(`Client: ${client}`);
    if (installDir) console.log(`Install directory: ${installDir}`);
    if (installDir && client === 'cli' && platform === 'windows') console.log(`Windows user PATH: prepend ${installDir}`);
    if (values['persist-home']) console.log(`Windows user CODEX_HOME: ${directory}`);
    console.log(`Base URL: ${normalizedBaseUrl}`);
    console.log(`Model: ${values.model}`);
    console.log(`Auth mode: ${authMode}`);
    if (values['no-install']) {
      console.log('Codex client install: skipped by --no-install');
    } else {
      if (!installPlan) throw new Error('Missing install plan');
      if (installPlan.installed) console.log('Codex client: already installed; install step skipped');
      else if (installPlan.install) {
        for (const command of installPlan.install) {
          console.log(`Codex client install: ${formatInstallCommand(command)}`);
        }
      }
      else console.log('Codex client install: unsupported on this platform; configuration will still be written');
    }
    return;
  }

  if (installPlan) {
    if (installPlan.installed) {
      console.log('Codex client already installed; skipping install.');
    } else if (installPlan.install) {
      for (const command of installPlan.install) {
        console.log(`Installing Codex client: ${formatInstallCommand(command)}`);
        await runCommand(command);
      }
    } else {
      console.log('Codex client installer is not available for this platform; writing configuration only.');
    }
  }

  const modelCatalog = await loadCodexModelCatalog(normalizedBaseUrl, apiKey, values.model);
  const written = await writeCodexConfig({
    baseUrl: normalizedBaseUrl,
    apiKey,
    model: values.model,
    providerName: values['provider-name'],
    authMode,
    modelCatalogJson: modelCatalog,
    codexHome: directory
  });
  console.log(`Wrote ${written.configPath}`);
  if (written.modelCatalogPath) console.log(`Wrote ${written.modelCatalogPath}`);
  if (written.authPath) console.log(`Wrote ${written.authPath}`);
  if (values['persist-home']) {
    await runCommand({ command: 'powershell.exe', args: ['-NoProfile', '-Command',
      `$ErrorActionPreference = 'Stop'; [Environment]::SetEnvironmentVariable('CODEX_HOME', ${powerShellLiteral(directory)}, 'User')`] });
    console.log('Saved Windows user CODEX_HOME. Reopen terminals and restart Codex; sign out of Windows if an existing launcher keeps the old environment.');
  } else if (values['codex-home']) {
    console.log(process.platform === 'win32'
      ? `Before starting Codex in PowerShell: $env:CODEX_HOME = ${powerShellLiteral(directory)}`
      : `Before starting Codex (also add to your shell profile): export CODEX_HOME=${shellLiteral(directory)}`);
  }
  if (installDir && client === 'cli') {
    const binDir = process.platform === 'win32' ? installDir : join(installDir, 'bin');
    if (process.platform === 'win32') {
      await runCommand({ command: 'powershell.exe', args: ['-NoProfile', '-Command',
        `$ErrorActionPreference = 'Stop'; $binDir = ${powerShellLiteral(binDir)}; $userPath = [Environment]::GetEnvironmentVariable('Path', 'User'); if (($userPath -split ';') -notcontains $binDir) { [Environment]::SetEnvironmentVariable('Path', ($binDir + ';' + $userPath), 'User') }`] });
      console.log('Saved Windows user PATH. Reopen your terminal to run codex by name.');
    }
    console.log(`Codex CLI executable: ${join(binDir, process.platform === 'win32' ? 'codex.cmd' : 'codex')}`);
    if (process.platform !== 'win32') console.log(`Add to your shell profile: export PATH=${shellLiteral(binDir)}:"$PATH"`);
  }
  console.log('Codex setup complete. Restart Codex if it was already running.');
}

function formatInstallCommand(command: { command: string; args: string[] }): string {
  if (command.command === 'bash' && command.args[0] === '-lc' && command.args[1]?.includes('installer_url=')) {
    const match = command.args[1].match(/installer_url='((?:[^']|'\\'')*)'/);
    if (match) return `download and run ${match[1].replace(/'\\''/g, "'")}`;
  }
  return `${command.command} ${command.args.join(' ')}`;
}

main().catch((error: unknown) => {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
});

async function loadCodexModelCatalog(baseUrl: string, apiKey: string, preferredModel: string): Promise<string | undefined> {
  const response = await fetch(`${baseUrl}/models?client_version=0.147.0`, {
    headers: { Authorization: `Bearer ${apiKey}` },
    signal: AbortSignal.timeout(30000)
  });
  if (!response.ok) {
    console.warn(`Model catalog fetch skipped: HTTP ${response.status}`);
    return undefined;
  }

  const manifest = await response.json() as { models?: Array<{ slug?: string; input_modalities?: string[] }> };
  const models = Array.isArray(manifest.models) ? manifest.models : [];
  if (models.length === 0) {
    console.warn('Model catalog fetch skipped: empty model list');
    return undefined;
  }

  const auto = models.find((model) => model.slug === 'auto');
  if (auto && !Array.isArray(auto.input_modalities)) {
    throw new Error('Model catalog is missing auto input modalities; upgrade the Sub2API gateway before running setup.');
  }
  if (auto && !auto.input_modalities?.includes('image')) {
    throw new Error('Model catalog declares auto as text-only; image input would be disabled. Update the Sub2API gateway catalog before running setup.');
  }

  const hasPreferredModel = models.some((model) => model.slug === preferredModel);
  if (!hasPreferredModel) {
    const first = models.find((model) => typeof model.slug === 'string' && model.slug);
    if (first?.slug) console.log(`Preferred model ${preferredModel} was not in the catalog; config still uses ${preferredModel}.`);
  }
  return JSON.stringify(manifest, null, 2);
}
