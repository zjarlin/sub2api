import fs from "node:fs/promises";
import crypto from "node:crypto";
import { AccountStore } from "./accounts.mjs";
import { verifyToken } from "./login.mjs";

// 仅运维命令读取已有账号的受保护文件，凭据不出现在参数或日志中。
if (!process.argv[2] || !process.env.TELEAGENT_LOGIN_OWNER) {
  throw new Error("Usage: TELEAGENT_DATA_DIR=... TELEAGENT_LOGIN_OWNER=user:<id> node src/import.mjs /secure/token.json");
}
const input = JSON.parse(await fs.readFile(process.argv[2], "utf8"));
const user = await verifyToken(input.token);
const store = new AccountStore({ dataDir: process.env.TELEAGENT_DATA_DIR || "./data" });
await store.load();
const account = await store.save({
  ...user, token: input.token, owner: process.env.TELEAGENT_LOGIN_OWNER,
  device_id: input.device_id || crypto.randomBytes(6).toString("hex"),
  install_id: input.install_id || crypto.randomUUID(),
});
console.log(JSON.stringify({ uid: account.uid, nickname: account.nickname, imported: true }));
