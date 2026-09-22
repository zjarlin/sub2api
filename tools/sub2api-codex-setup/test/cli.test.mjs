import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const cli = (...args) => execFileSync(process.execPath, ['dist/cli.mjs', ...args], {
  encoding: 'utf8',
  env: { ...process.env }
}).trim();

test('prints packaged version and help', () => {
  assert.equal(cli('--version'), JSON.parse(readFileSync('package.json', 'utf8')).version);
  assert.match(cli('--help'), /sub2api-codex-setup/);
  assert.match(cli('--help'), /--base-url/);
});

test('dry-run prints the current platform plan without writing config', () => {
  const output = cli(
    '--base-url', 'https://example.com/v1',
    '--api-key', 'sk-test',
    '--model', 'gpt-5.5',
    '--dry-run'
  );
  assert.match(output, /Detected platform: (macos|windows|linux)/);
  assert.match(output, /Base URL: https:\/\/example\.com\/v1/);
  assert.match(output, /Codex client/);
});

test('writes Codex config with inline API key under a custom HOME', () => {
  const home = mkdtempSync(join(tmpdir(), 'sub2api-codex-setup-'));
  try {
    execFileSync(process.execPath, ['dist/cli.mjs',
      '--base-url', 'https://example.com',
      '--api-key', 'sk-test',
      '--model', 'gpt-5.5',
      '--no-install'
    ], {
      encoding: 'utf8',
      env: { ...process.env, HOME: home, USERPROFILE: home }
    });

    const config = readFileSync(join(home, '.codex', 'config.toml'), 'utf8');
    assert.match(config, /base_url = "https:\/\/example\.com\/v1"/);
    assert.match(config, /experimental_bearer_token = "sk-test"/);
    assert.throws(() => readFileSync(join(home, '.codex', 'auth.json'), 'utf8'));
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
});

test('legacy auth mode writes auth.json', () => {
  const home = mkdtempSync(join(tmpdir(), 'sub2api-codex-setup-'));
  try {
    execFileSync(process.execPath, ['dist/cli.mjs',
      '--base-url', 'https://example.com/v1',
      '--api-key', 'sk-test',
      '--auth-mode', 'legacy',
      '--no-install'
    ], {
      encoding: 'utf8',
      env: { ...process.env, HOME: home, USERPROFILE: home }
    });

    const config = readFileSync(join(home, '.codex', 'config.toml'), 'utf8');
    const auth = JSON.parse(readFileSync(join(home, '.codex', 'auth.json'), 'utf8'));
    assert.match(config, /env_key = "SUB2API_API_KEY"/);
    assert.equal(auth.OPENAI_API_KEY, 'sk-test');
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
});
