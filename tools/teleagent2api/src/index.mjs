import { chromium } from "playwright";
import { AccountStore } from "./accounts.mjs";
import { LoginSessions } from "./login.mjs";
import { createServer } from "./server.mjs";
import { startWelfareSchedule } from "./welfare.mjs";

function integer(name, fallback, min, max) {
  const value = process.env[name] === undefined ? fallback : Number(process.env[name]);
  if (!Number.isSafeInteger(value) || value < min || value > max) { throw new Error(`Invalid ${name}.`); }
  return value;
}

const store = new AccountStore({ dataDir: process.env.TELEAGENT_DATA_DIR || "./data" });
await store.load();
const login = new LoginSessions({ chromium, store });
const server = createServer({
  store, login, adapterKey: process.env.TELEAGENT_ADAPTER_KEY,
  timeoutMs: integer("TELEAGENT_REQUEST_TIMEOUT_MS", 300_000, 1000, 900_000),
  maxConcurrent: integer("TELEAGENT_MAX_CONCURRENT", 4, 1, 32),
});
const stopWelfare = process.env.TELEAGENT_WELFARE_ENABLED === "0" ? () => {} : startWelfareSchedule(store);
const host = process.env.TELEAGENT_HOST || "127.0.0.1";
const port = integer("TELEAGENT_PORT", 7872, 1, 65535);
server.listen(port, host, () => console.log(`TeleAgent adapter listening on ${host}:${port}`));
let stopping = false;
async function stop() {
  if (stopping) { return; }
  stopping = true;
  try { await Promise.all([stopWelfare(), server.stop()]); } catch { process.exitCode = 1; }
}
process.once("SIGTERM", stop);
process.once("SIGINT", stop);
