// 从 Qoder CLI worker runtime 中提取官方签名 WASM，生成侧车用的 wasm-lib.mjs。
//
//   node build-wasm-lib.mjs <qoder-worker-runtime.obf.mjs> [wasm-lib.mjs]
//
// 来源：https://download.qoder.com/qodercli/releases/<ver>/qodercli-worker-runtime-darwin-arm64.tgz
// 该包在 qoder-worker-runtime/qoder-worker-runtime.obf.mjs 内以 base64 内嵌
// `abs="..."`（约 297KB，magic 0061736d）。运行时入口被截掉并改为导出，
// 以便侧车 `import` 后直接调用 __initWasm / __getWasm / QoderContext。
import fs from 'node:fs';
import path from 'node:path';

const TRAILER =
  'process.env.QODERCLI_RUNTIME_PACKAGING="worker_mjs",iZn(process.argv),cVd().catch(A=>{aVd(A)});';
const EXPORTS =
  '\nexport { rN as __initWasm, yI as __getWasm, x7A as QoderContext, ubs as __decryptServerResponse, dbs as __credEncrypt, lbs as __credDecrypt };\n';

const [, , src, dst = path.join(path.dirname(new URL(import.meta.url).pathname), 'wasm-lib.mjs')] = process.argv;
if (!src) {
  console.error('usage: node build-wasm-lib.mjs <qoder-worker-runtime.obf.mjs> [wasm-lib.mjs]');
  process.exit(2);
}
let s = fs.readFileSync(src, 'utf8');

const b64Start = s.indexOf('abs="');
const b64End = s.indexOf('"}),LXn={}');
if (b64Start < 0 || b64End < 0 || b64End <= b64Start) {
  console.error('embedded wasm base64 not found; runtime version changed?');
  process.exit(3);
}
const wasm = Buffer.from(s.slice(b64Start + 5, b64End), 'base64');
if (wasm.length < 8 || wasm.readUInt32BE(0) !== 0x0061736d) {
  console.error('embedded blob is not a wasm module');
  process.exit(4);
}

const cut = s.lastIndexOf(TRAILER);
if (cut < 0) {
  console.error('runtime trailer not found; runtime version changed?');
  process.exit(5);
}
s = s.slice(0, cut) + EXPORTS;

fs.mkdirSync(path.dirname(dst), { recursive: true });
fs.writeFileSync(dst, s);
console.log(JSON.stringify({ ok: true, wasmLib: dst, wasmBytes: wasm.length, libBytes: s.length }));
