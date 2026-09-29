import { CursorRuntime } from "./runtime.mjs";
import { createServer } from "./server.mjs";

function integer(name, fallback, minimum, maximum) {
  const value = process.env[name] === undefined ? fallback : Number(process.env[name]);
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) {
    throw new Error(`${name} must be an integer between ${minimum} and ${maximum}.`);
  }
  return value;
}

const port = integer("CURSOR_PORT", 7868, 1, 65535);
const host = process.env.CURSOR_HOST || "127.0.0.1";
const server = createServer({
  runtime: new CursorRuntime(),
  adapterKey: process.env.CURSOR_ADAPTER_KEY?.trim(),
  timeoutMs: integer("CURSOR_REQUEST_TIMEOUT_MS", 300_000, 1000, 900_000),
  maxConcurrent: integer("CURSOR_MAX_CONCURRENT", 4, 1, 32),
  maxBodyBytes: integer("CURSOR_MAX_BODY_BYTES", 4 << 20, 1024, 32 << 20),
});
server.listen(port, host, () => console.log(`Cursor adapter listening on ${host}:${port}`));
let stopping = false;
async function stop() {
  if (stopping) return;
  stopping = true;
  try {
    await server.stop();
  } catch {
    process.exitCode = 1;
  }
}
process.once("SIGTERM", stop);
process.once("SIGINT", stop);
