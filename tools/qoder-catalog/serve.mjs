// Qoder 签名目录侧车（文件队列模式）
//
// 为什么不用 docker exec 的 stdout：`docker exec -i` 的 stdout 在 64KB 处
// 被截断，而 chat 目录响应约 77KB，JSON 会被截成半截。这里改为在共享目录
// 中交换文件，完整响应由主进程直接读文件，不受任何管道缓冲限制。
//
// 用法：
//   node serve.mjs /var/lib/qoder-catalog
//
// 协议（主进程侧，见 backend/internal/service/qoder.go）：
//   1. 在 <dir> 下写临时请求文件 <dir>/req-*.tmp，然后 rename 成 <dir>/req.json
//      （改名是原子的，避免侧车读到半个文件）
//   2. 侧车处理后写 <dir>/resp-*.tmp，再 rename 成 <dir>/resp.json
//   3. 主进程按 mtime 判断结果属于哪次请求，读取后删除 resp.json / req.json
//
// 同一时刻只处理一个请求（Qoder 目录同步是低频操作，串行足够）。
// 请求/响应目录必须对侧车可写；主进程以 sub2api 用户运行，目录权限由
// compose 的 0777 初始权限 + 双方各自 chmod 处理。
import fs from 'node:fs/promises';
import path from 'node:path';

const DIR = process.argv[2] || '/var/lib/qoder-catalog';
const POLL_MS = 100;
const MAX_OUTPUT_BYTES = 16 * 1024 * 1024;

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function readJson(file) {
  const raw = await fs.readFile(file, 'utf8');
  return JSON.parse(raw);
}

async function writeAtomic(target, data) {
  const tmp = path.join(DIR, `${path.basename(target)}.${process.pid}.tmp`);
  await fs.writeFile(tmp, data);
  await fs.rename(tmp, target);
}

async function ensureDir() {
  await fs.mkdir(DIR, { recursive: true });
  await fs.chmod(DIR, 0o777).catch(() => {});
}

async function main() {
  await ensureDir();
  const reqFile = path.join(DIR, 'req.json');
  const respFile = path.join(DIR, 'resp.json');
  let lastMtime = 0;
  let processing = false;

  // 启动时清掉上次进程崩溃留下的半成品。
  for (const name of await fs.readdir(DIR)) {
    if (/^(req|resp)\..*\.tmp$/.test(name)) {
      await fs.rm(path.join(DIR, name), { force: true });
    }
  }

  while (true) {
    if (!processing) {
      try {
        const stat = await fs.stat(reqFile);
        if (stat.mtimeMs !== lastMtime) {
          lastMtime = stat.mtimeMs;
          processing = true;
          const request = await readJson(reqFile);
          let result;
          try {
            const { fetchCatalog } = await import('./fetch.mjs');
            result = await fetchCatalog(request);
          } catch (err) {
            result = { ok: false, error: String(err?.message || err) };
          }
          let payload = JSON.stringify(result);
          if (Buffer.byteLength(payload) > MAX_OUTPUT_BYTES) {
            payload = JSON.stringify({ ok: false, error: 'sidecar response too large' });
          }
          await writeAtomic(respFile, payload);
          await fs.rm(reqFile, { force: true });
          processing = false;
        }
      } catch (err) {
        // req.json 还没出现（ENOENT）属于正常轮询。
        if (err?.code !== 'ENOENT') {
          await writeAtomic(respFile, JSON.stringify({ ok: false, error: String(err?.message || err) })).catch(() => {});
          lastMtime = 0;
          processing = false;
        }
      }
    }
    await sleep(POLL_MS);
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
