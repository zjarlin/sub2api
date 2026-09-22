import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { codexConfigDir, detectPlatform } from './install.js';

export interface SetupConfig {
  baseUrl: string;
  apiKey: string;
  model: string;
  providerName: string;
  authMode: 'legacy' | 'api-key';
  modelCatalogJson?: string;
  platform?: NodeJS.Platform;
}

export interface WrittenConfig {
  directory: string;
  configPath: string;
  authPath?: string;
  modelCatalogPath?: string;
}

function escapeTomlBasicString(value: string): string {
  return value.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
}

export function normalizeBaseUrl(value: string): string {
  const trimmed = value.trim().replace(/\/+$/, '');
  if (!trimmed) throw new Error('baseUrl is required');
  return trimmed.endsWith('/v1') ? trimmed : `${trimmed}/v1`;
}

export function buildConfigToml(config: SetupConfig): string {
  const baseUrl = normalizeBaseUrl(config.baseUrl);
  const providerName = config.providerName || 'Sub2API';
  const modelCatalogLine = config.modelCatalogJson
    ? `model_catalog_json = "${escapeTomlBasicString(config.modelCatalogJson)}"\n`
    : ''
  const authLines = config.authMode === 'api-key'
    ? `requires_openai_auth = false
experimental_bearer_token = "${escapeTomlBasicString(config.apiKey)}"`
    : `env_key = "SUB2API_API_KEY"
requires_openai_auth = false`

  return `# Codex CLI -> ${providerName}
model_provider = "sub2api"
model = "${config.model}"
review_model = "${config.model}"
${modelCatalogLine}disable_response_storage = true

[model_providers.sub2api]
name = "${providerName}"
base_url = "${baseUrl}"
${authLines}
wire_api = "responses"
supports_websockets = false`
}

export async function writeCodexConfig(config: SetupConfig): Promise<WrittenConfig> {
  const platform = config.platform || process.platform;
  const directory = codexConfigDir(platform);
  const configPath = join(directory, 'config.toml');
  const modelCatalogPath = config.modelCatalogJson ? join(directory, 'codex-models.json') : undefined;
  await mkdir(directory, { recursive: true });
  await writeFile(configPath, buildConfigToml(config), { encoding: 'utf8', mode: 0o600 });

  if (config.modelCatalogJson && modelCatalogPath) {
    await writeFile(modelCatalogPath, config.modelCatalogJson, { encoding: 'utf8', mode: 0o600 });
  }

  if (config.authMode === 'api-key') {
    return { directory, configPath, modelCatalogPath };
  }

  const authPath = join(directory, 'auth.json');
  await writeFile(authPath, `${JSON.stringify({ OPENAI_API_KEY: config.apiKey }, null, 2)}\n`, {
    encoding: 'utf8',
    mode: 0o600
  });
  return { directory, configPath, authPath, modelCatalogPath };
}

export function currentPlatformLabel(platform = process.platform): string {
  return detectPlatform(platform);
}
