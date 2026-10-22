// Qoder 签名目录侧车
//
// 用法：node fetch.mjs   （stdio，一行请求 → 一行响应）
//   stdin : {"access_token":"dt-...","uid":"...","machine_id":"...","scene":"chat"}
//   stdout: {"ok":true,"body":"<原始目录 JSON>"} 或 {"ok":false,"error":"..."}
//
// 为什么要侧车：Qoder 的 /api/v2/model/list 只接受官方客户端签名
// （Authorization: Bearer COSY.* + Cosy-* 头 + /algo 路径前缀），签名逻辑是
// Rust 编译的 WASM。Go 侧不引入 WASM 运行时，改为把这一步放在这里执行。
import { createRequire } from 'node:module';
import crypto from 'node:crypto';
import fs from 'node:fs';

const require = createRequire(import.meta.url);

// 默认从 Qoder CLI worker runtime 里取内嵌 WASM；也可用 QODER_WASM_LIB 指向
// 已经切分好的 lib（导出 __initWasm / QoderContext / __getWasm）。
const DEFAULT_LIB = '/opt/qoder-catalog/wasm-lib.mjs';

async function loadWasm(libPath) {
  const mod = await import(libPath);
  await mod.__initWasm();
  return mod;
}

function readStdinLine() {
  return new Promise((resolve) => {
    let buf = '';
    process.stdin.setEncoding('utf8');
    process.stdin.on('data', (chunk) => {
      buf += chunk;
      const idx = buf.indexOf('\n');
      if (idx >= 0) {
        resolve(buf.slice(0, idx));
        process.stdin.pause();
      }
    });
    process.stdin.on('end', () => resolve(buf.trim()));
  });
}

export async function fetchCatalog(req) {
  const libPath = process.env.QODER_WASM_LIB || DEFAULT_LIB;
  const lib = await loadWasm(libPath);
  const wasm = lib.__getWasm();

  const uid = String(req.uid || '');
  const accessToken = String(req.access_token || '');
  const machineId = String(req.machine_id || '') || crypto.randomUUID();
  const scene = String(req.scene || 'chat') || 'chat';

  // encrypt_user_info / key 必须由 WASM 生成；只放 uid 会被上游拒绝为
  // 403 {"code":"101","message":"Signature invalid"}。
  const authFields = JSON.parse(
    wasm.generate_runtime_auth_fields(
      JSON.stringify({
        access_token: accessToken,
        uid,
        organization_id: req.organization_id || '',
        organization_tags: [],
        data_policy_agreed: false,
      }),
    ),
  );

  const context = new lib.QoderContext(
    machineId,
    '1.1.49',
    JSON.stringify({
      uid,
      encrypt_user_info: authFields.encrypt_user_info || '',
      key: authFields.key || '',
      organization_id: req.organization_id || '',
      data_policy_agreed: false,
    }),
    JSON.stringify({
      client_type: '5',
      business_product: 'cli',
      business_type: 'agent',
      scene,
    }),
  );

  const prepared = context.prepareRequest(
    'https://api2.qoder.sh',
    '/api/v2/model/list?Encode=1',
    'GET',
    'auth',
    undefined,
    undefined,
  );

  try {
    const headers = Object.fromEntries(prepared.headers);
    const resp = await fetch(prepared.url, { method: 'GET', headers });
    const body = await resp.text();
    if (!resp.ok) {
      return { ok: false, error: `HTTP ${resp.status}: ${body.slice(0, 300)}`, status: resp.status };
    }
    return { ok: true, body, url: prepared.url, machine_id: machineId };
  } finally {
    prepared.free();
    context.free?.();
  }
}

async function runMachineToken() {
  const bin = process.env.QODER_UMID_BIN || '/opt/qoder-catalog/umid';
  if (!fs.existsSync(bin)) return '';
  try {
    const { execFileSync } = require('node:child_process');
    const out = execFileSync(bin, [], { timeout: 15000, encoding: 'utf8' });
    const parsed = JSON.parse(out.trim().split('\n').pop());
    return parsed.machineToken || '';
  } catch {
    return '';
  }
}

const raw = await readStdinLine();
if (raw) {
  let req;
  try {
    req = JSON.parse(raw);
  } catch {
    process.stdout.write(JSON.stringify({ ok: false, error: 'invalid JSON request' }) + '\n');
    process.exit(0);
  }
  try {
    if (!req.machine_token) {
      req.machine_token = await runMachineToken();
    }
    const result = await fetchCatalog(req);
    process.stdout.write(JSON.stringify(result) + '\n');
  } catch (err) {
    process.stdout.write(JSON.stringify({ ok: false, error: String(err?.message || err) }) + '\n');
  }
  process.exit(0);
}

// 被 serve.mjs import 时没有 stdin：只提供 fetchCatalog，不执行 CLI 分支。
export const sidecarReady = true;
