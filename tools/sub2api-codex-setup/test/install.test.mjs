import { test } from 'node:test';
import assert from 'node:assert/strict';
import { build } from 'esbuild';

const output = await build({ entryPoints: ['src/install.ts'], bundle: true, platform: 'node', format: 'esm', write: false });
const { planClientInstall, resolveDirectory } = await import('data:text/javascript;base64,' + Buffer.from(output.outputFiles[0].text).toString('base64'));

test('Windows CLI installs in a quoted custom prefix with its own npm cache', () => {
  const plan = planClientInstall({ platform: 'win32', client: 'cli', installDir: "D:\\AI tools\\O'Brien" });
  const script = plan.install[0].args.at(-1);
  assert.equal(plan.install[0].command, 'powershell.exe');
  assert.ok(script.includes("'--prefix' 'D:\\AI tools\\O''Brien'"));
  assert.ok(script.includes("'--cache' 'D:\\AI tools\\O''Brien\\npm-cache'"));
  assert.match(script, /exit \$LASTEXITCODE/);
});

test('Store desktop installation rejects an unsupported custom directory', () => {
  assert.throws(() => planClientInstall({ platform: 'win32', installDir: 'D:\\Codex' }), /--client cli/);
});

test('Linux CLI installation is supported and keeps spaces in a single argument', () => {
  const plan = planClientInstall({ platform: 'linux', client: 'cli', installDir: '/mnt/AI tools' });
  assert.equal(plan.supported, true);
  assert.deepEqual(plan.install, [{ command: 'npm', args: ['install', '--global', '@openai/codex', '--prefix', '/mnt/AI tools', '--cache', '/mnt/AI tools/npm-cache'] }]);
});

test('macOS desktop honors a requested app directory instead of skipping another installation', () => {
  const plan = planClientInstall({ platform: 'darwin', installDir: '/Volumes/Data/AI apps' });
  assert.equal(plan.installed, false);
  assert.ok(plan.install[0].args[1].includes("target_dir='/Volumes/Data/AI apps'"));
});

test('ambiguous Windows drive-relative paths fail validation', () => {
  for (const path of ['D:Codex', '\\Codex', 'relative', 'D:\\bad\npath']) {
    assert.throws(() => resolveDirectory(path, 'win32'));
  }
  assert.equal(resolveDirectory('D:/AI tools/data', 'win32'), 'D:\\AI tools\\data');
});
