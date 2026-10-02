import { WindSurfRuntime } from "./runtime.mjs";
import { createServer } from "./server.mjs";

function integer(name, fallback, minimum, maximum) {
  const value = process.env[name] === undefined ? fallback : Number(process.env[name]);
  if (!Number.isSafeInteger(value) || value < minimum || value > maximum) {
    throw new Error(`${name} must be an integer between ${minimum} and ${maximum}.`);
  }
  return value;
}

const port = integer("WINDSURF_PORT", 7869, 1, 65535);
const host = process.env.WINDSURF_HOST || "127.0.0.1";
const server = createServer({
  runtime: new WindSurfRuntime(),
  adapterKey: process.env.WINDSURF_ADAPTER_KEY?.trim(),
  timeoutMs: integer("WINDSURF_REQUEST_TIMEOUT_MS", 600_000, 1000, 900_000),
  maxConcurrent: integer("WINDSURF_MAX_CONCURRENT", 4, 1, 32),
  maxBodyBytes: integer("WINDSURF_MAX_BODY_BYTES", 4 << 20, 1024, 32 << 20),
});
server.listen(port, host, () => console.log(`Windsurf adapter listening on ${host}:${port}`));
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
