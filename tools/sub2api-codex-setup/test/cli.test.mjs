import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFile, execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);

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

test('modified install source uses the provided installer URL in dry-run', () => {
  const output = cli(
    '--base-url', 'https://example.com/v1',
    '--api-key', 'sk-test',
    '--install-source', 'modified',
    '--modified-installer-url', 'https://example.com/install-codex.sh',
    '--dry-run'
  );
  assert.match(output, /Codex install source: modified/);
  assert.match(output, /install-codex\.sh/);
});

test('modified install source defaults to the GitHub release asset', () => {
  const output = cli(
    '--base-url', 'https://example.com/v1',
    '--api-key', 'sk-test',
    '--install-source', 'modified',
    '--dry-run'
  );
  assert.match(output, /github\.com\/zjarlin\/sub2api\/releases\/latest\/download\/codex-install\.(sh|ps1)/);
});

test('no-install skips modified installer resolution', () => {
  const output = cli(
    '--base-url', 'https://example.com/v1',
    '--api-key', 'sk-test',
    '--install-source', 'modified',
    '--no-install',
    '--dry-run'
  );
  assert.match(output, /Codex client install: skipped by --no-install/);
});

test('modified install source always plans its installer even when Codex exists', () => {
  const output = cli(
    '--base-url', 'https://example.com/v1',
    '--api-key', 'sk-test',
    '--install-source', 'modified',
    '--modified-installer-url', 'https://example.com/install-codex.sh',
    '--dry-run'
  );
  assert.match(output, /Codex install source: modified/);
  assert.match(output, /install-codex\.sh/);
  assert.doesNotMatch(output, /already installed; install step skipped/);
});

for (const scenario of [
  { name: 'default directory', options: [], directory: '.codex', environment: '' },
  { name: 'legacy auth', options: ['--auth-mode', 'legacy'], directory: '.codex', environment: '' },
  { name: 'CODEX_HOME environment', options: [], directory: 'environment data', environment: 'environment data' },
  { name: 'explicit directory overrides CODEX_HOME', options: ['--codex-home'], directory: 'custom data', environment: 'unused' }
]) {
  test('writes configuration and catalog in ' + scenario.name, async () => {
    const sandboxRoot = mkdtempSync(join(tmpdir(), 'sub2api-codex-setup-'));
    const destination = join(sandboxRoot, scenario.directory);
    const manifest = { models: [{ slug: 'gpt-5.5' }] };
    const server = createServer((request, response) => {
      assert.match(request.url, /^\/v1\/models\?/);
      assert.equal(request.headers.authorization, 'Bearer sk-test');
      response.writeHead(200, { 'content-type': 'application/json' });
      response.end(JSON.stringify(manifest));
    });
    try {
      await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
      const options = scenario.options[0] === '--codex-home' ? ['--codex-home', destination] : scenario.options;
      await execFileAsync(process.execPath, ['dist/cli.mjs',
        '--base-url', 'http://127.0.0.1:' + server.address().port,
        '--api-key', 'sk-test', '--no-install', ...options
      ], {
        encoding: 'utf8',
        env: { ...process.env, HOME: sandboxRoot, USERPROFILE: sandboxRoot,
          CODEX_HOME: scenario.environment ? join(sandboxRoot, scenario.environment) : '' }
      });
      const config = readFileSync(join(destination, 'config.toml'), 'utf8');
      const catalogLine = config.split('\n').find((line) => line.startsWith('model_catalog_json = '));
      assert.equal(JSON.parse(catalogLine.slice('model_catalog_json = '.length)), join(destination, 'codex-models.json'));
      assert.deepEqual(JSON.parse(readFileSync(join(destination, 'codex-models.json'), 'utf8')), manifest);
      if (scenario.name === 'legacy auth') {
        assert.equal(JSON.parse(readFileSync(join(destination, 'auth.json'), 'utf8')).OPENAI_API_KEY, 'sk-test');
      } else {
        assert.match(config, /experimental_bearer_token = "sk-test"/);
        assert.throws(() => readFileSync(join(destination, 'auth.json')));
      }
      if (scenario.directory !== '.codex') assert.throws(() => readFileSync(join(sandboxRoot, '.codex', 'config.toml')));
      if (scenario.environment === 'unused') assert.throws(() => readFileSync(join(sandboxRoot, 'unused', 'config.toml')));
    } finally {
      await new Promise((resolve) => server.close(resolve));
      rmSync(sandboxRoot, { recursive: true, force: true });
    }
  });
}

test('rejects a model catalog that disables Auto image input', async () => {
  const sandboxRoot = mkdtempSync(join(tmpdir(), 'sub2api-codex-image-'));
  const destination = join(sandboxRoot, '.codex');
  const server = createServer((request, response) => {
    response.writeHead(200, { 'content-type': 'application/json' });
    response.end(JSON.stringify({ models: [
      { slug: 'gpt-5.5', input_modalities: ['text'] },
      { slug: 'auto', input_modalities: ['text'] }
    ] }));
  });
  try {
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    await assert.rejects(execFileAsync(process.execPath, ['dist/cli.mjs',
      '--base-url', 'http://127.0.0.1:' + server.address().port,
      '--api-key', 'sk-test', '--no-install', '--codex-home', destination
    ], { encoding: 'utf8' }), /auto as text-only/);
  } finally {
    await new Promise((resolve) => server.close(resolve));
    rmSync(sandboxRoot, { recursive: true, force: true });
  }
});

test('accepts Auto image input and keeps the catalog available for image requests', async () => {
  const sandboxRoot = mkdtempSync(join(tmpdir(), 'sub2api-codex-image-ok-'));
  const destination = join(sandboxRoot, '.codex');
  const server = createServer((request, response) => {
    response.writeHead(200, { 'content-type': 'application/json' });
    response.end(JSON.stringify({ models: [
      { slug: 'gpt-5.5', input_modalities: ['text', 'image'] },
      { slug: 'auto', input_modalities: ['text', 'image'] }
    ] }));
  });
  try {
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    await execFileAsync(process.execPath, ['dist/cli.mjs',
      '--base-url', 'http://127.0.0.1:' + server.address().port,
      '--api-key', 'sk-test', '--no-install', '--codex-home', destination
    ], { encoding: 'utf8' });
    const catalog = JSON.parse(readFileSync(join(destination, 'codex-models.json'), 'utf8'));
    assert.deepEqual(catalog.models.find((model) => model.slug === 'auto').input_modalities, ['text', 'image']);
  } finally {
    await new Promise((resolve) => server.close(resolve));
    rmSync(sandboxRoot, { recursive: true, force: true });
  }
});

test('dry-run reports custom directories and never writes or exposes the API key', () => {
  const sandboxRoot = mkdtempSync(join(tmpdir(), 'sub2api-codex-dry-'));
  const destination = join(sandboxRoot, 'data folder');
  try {
    const output = cli('--base-url', 'https://example.com', '--api-key', 'sk-secret',
      '--client', 'cli', '--install-dir', join(sandboxRoot, 'app folder'),
      '--codex-home', destination, '--dry-run');
    assert.ok(output.includes(destination));
    assert.match(output, /--prefix/);
    assert.doesNotMatch(output, /sk-secret/);
    assert.throws(() => readFileSync(join(destination, 'config.toml')));
  } finally {
    rmSync(sandboxRoot, { recursive: true, force: true });
  }
});

test('invalid options fail before installation or writing configuration', () => {
  for (const options of [
    ['--client', 'other'],
    ['--codex-home', 'relative-path'],
    ['--install-dir', join(tmpdir(), 'app'), '--no-install'],
    ['--persist-home'],
    ['--install-source', 'modified', '--client', 'cli']
  ]) {
    assert.throws(() => cli('--base-url', 'https://example.com', '--api-key', 'sk-test', '--dry-run', ...options));
  }
});
