import { createInterface, emitKeypressEvents } from "node:readline";
import { parseArgs } from "node:util";
import { adapterConfig, createRuntime } from "./runtime.mjs";
import { readJSON, saveJSON } from "./state.mjs";
import { ArenaError } from "./request.mjs";

function invalidCommand(message) {
  return new ArenaError(400, "invalid_command", message);
}

async function question(label) {
  const reader = createInterface({ input: process.stdin, output: process.stderr });
  try {
    return await new Promise((resolve) => reader.question(label, resolve));
  } finally {
    reader.close();
  }
}

async function password() {
  if (!process.stdin.isTTY) {
    throw invalidCommand("Interactive login needs a terminal, or set ARENA_LOGIN_PASSWORD for this process.");
  }
  process.stderr.write("Password: ");
  emitKeypressEvents(process.stdin);
  process.stdin.setRawMode(true);
  process.stdin.resume();
  return new Promise((resolve, reject) => {
    let value = "";
    const cleanup = () => {
      process.stdin.off("keypress", input);
      process.stdin.setRawMode(false);
      process.stdin.pause();
      process.stderr.write("\n");
    };
    const input = (text, key = {}) => {
      if (key.ctrl && key.name === "c") {
        cleanup();
        reject(invalidCommand("Login cancelled."));
        return;
      }
      if (key.name === "return") {
        cleanup();
        resolve(value);
        return;
      }
      if (key.name === "backspace") {
        value = value.slice(0, -1);
        return;
      }
      if (text && !key.ctrl && !key.meta) {
        value += text;
      }
    };
    process.stdin.on("keypress", input);
  });
}

async function main() {
  let args;
  try {
    args = parseArgs({ allowPositionals: true, options: { email: { type: "string" }, session: { type: "string" }, model: { type: "string" }, name: { type: "string" } } });
  } catch {
    throw invalidCommand("Invalid command options. Supported options: --email, --session, --model, --name.");
  }
  const { positionals, values } = args;
  if (positionals.length !== 1) {
    throw invalidCommand("Choose one command: status, models, login, add-session.");
  }
  const config = adapterConfig();
  const runtime = createRuntime(config);
  try {
    switch (positionals[0]) {
      case "status":
        console.log(JSON.stringify(runtime.health(), null, 2));
        break;
      case "models":
        console.log(JSON.stringify(runtime.models(), null, 2));
        break;
      case "add-session": {
        if (!values.email?.trim() || !values.session || !values.model?.trim()) {
          throw invalidCommand("add-session requires --email, --session UUID and --model ALIAS.");
        }
        const entries = readJSON(config.modelsFile, []);
        if (!Array.isArray(entries)) {
          throw invalidCommand("models.json must be an array.");
        }
        const model = values.model.trim();
        entries.push({ id: model, sessionId: values.session, accountEmail: values.email.trim(), name: values.name || model });
        if (!/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(values.session)) {
          throw invalidCommand("--session must be an Arena session UUID.");
        }
        if (entries.filter((entry) => entry.id === model).length !== 1) {
          throw invalidCommand("This model alias is already configured.");
        }
        saveJSON(config.modelsFile, entries);
        console.log(JSON.stringify({ configured: true, model }));
        break;
      }
      case "login": {
        const email = values.email || process.env.ARENA_LOGIN_EMAIL || await question("Arena email: ");
        const secret = process.env.ARENA_LOGIN_PASSWORD || await password();
        console.log(JSON.stringify(await runtime.login(email, secret)));
        break;
      }
      default:
        throw invalidCommand("Usage: npm run manage -- status | models | login [--email EMAIL] | add-session --model ALIAS --session UUID --email EMAIL");
    }
  } finally {
    await runtime.close();
  }
}

main().catch((error) => {
  console.error(error instanceof ArenaError ? error.message : "Arena account setup failed. Check the local configuration and login; no credentials were printed.");
  process.exitCode = 1;
});
