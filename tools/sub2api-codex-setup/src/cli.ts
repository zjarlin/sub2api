#!/usr/bin/env node
import { parseArgs } from 'node:util';
import { currentPlatformLabel, normalizeBaseUrl, writeCodexConfig } from './config.js';
import {
  DEFAULT_MODIFIED_INSTALL_REPO,
  MODIFIED_INSTALL_URL_ENV,
  MODIFIED_INSTALL_REPO_ENV,
  parseCodexInstallSource,
  planClientInstall,
  runCommand
} from './install.js';

declare const PACKAGE_VERSION: string;

const HELP = `sub2api-codex-setup

Install the Codex desktop client when needed, then write Codex CLI configuration for Sub2API.

Usage:
  npx -y sub2api-codex-setup --base-url <url> --api-key <key> [options]

Options:
  --base-url <url>       Sub2API base URL, with or without /v1
  --api-key <key>        Sub2API API key
  --model <model>        Codex model (default: gpt-5.5)
  --provider-name <name> Provider name written to config.toml (default: Sub2API)
  --auth-mode <mode>     api-key or legacy (default: api-key)
  --install-source <src> Codex client source: official or modified (default: official)
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

  console.log(`Detected platform: ${platform}`);
  console.log(`Codex install source: ${installSource}`);
  if (values['dry-run']) {
    console.log(`Config directory: ${platform === 'windows' ? '%USERPROFILE%\\.codex' : '~/.codex'}`);
    console.log(`Base URL: ${normalizedBaseUrl}`);
    console.log(`Model: ${values.model}`);
    console.log(`Auth mode: ${authMode}`);
    if (values['no-install']) {
      console.log('Codex client install: skipped by --no-install');
    } else {
      const installPlan = planClientInstall({
        source: installSource,
        modifiedInstallerUrl: values['modified-installer-url']
      });
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

  if (!values['no-install']) {
    const installPlan = planClientInstall({
      source: installSource,
      modifiedInstallerUrl: values['modified-installer-url']
    });
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
    modelCatalogJson: modelCatalog
  });
  console.log(`Wrote ${written.configPath}`);
  if (written.modelCatalogPath) console.log(`Wrote ${written.modelCatalogPath}`);
  if (written.authPath) console.log(`Wrote ${written.authPath}`);
  console.log('Codex setup complete. Restart Codex if it was already running.');
}

function formatInstallCommand(command: { command: string; args: string[] }): string {
  if (command.command === 'bash' && command.args[0] === '-lc' && command.args[1]?.includes('installer_url=')) {
    const match = command.args[1].match(/installer_url='((?:[^']|'\\'')*)'/);
    if (match) return `download and run ${match[1].replace(/'\\''/g, "'")}`;
  }
  if (command.command === 'powershell.exe') return 'run modified PowerShell installer from --modified-installer-url';
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

  const manifest = await response.json() as { models?: Array<{ slug?: string }> };
  const models = Array.isArray(manifest.models) ? manifest.models : [];
  if (models.length === 0) {
    console.warn('Model catalog fetch skipped: empty model list');
    return undefined;
  }

  const hasPreferredModel = models.some((model) => model.slug === preferredModel);
  if (!hasPreferredModel) {
    const first = models.find((model) => typeof model.slug === 'string' && model.slug);
    if (first?.slug) console.log(`Preferred model ${preferredModel} was not in the catalog; config still uses ${preferredModel}.`);
  }
  return JSON.stringify(manifest, null, 2);
}
