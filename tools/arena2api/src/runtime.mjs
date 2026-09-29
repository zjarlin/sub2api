import crypto from "node:crypto";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { loadConfig, loadDotEnv } from "arena-local-bridge/src/config.mjs";
import { CredentialStore } from "arena-local-bridge/src/credentials.mjs";
import { Bridge } from "arena-local-bridge/src/bridge.mjs";
import { requireSecret } from "arena-local-bridge/src/secret.mjs";
import { readEntries, sessionIdFromUrl } from "arena-local-bridge/src/archive.mjs";
import { ArenaError } from "./request.mjs";
import { parseAgentOutput } from "arena-local-bridge/src/parser.mjs";
import { readJSON, saveJSON } from "./state.mjs";

export function adapterConfig(env = process.env) {
  const dataDir = path.resolve(env.DATA_DIR || path.join(os.homedir(), ".sub2api-arena"));
  fs.mkdirSync(dataDir, { recursive: true, mode: 0o700 });
  const envFile = path.join(dataDir, ".env");
  const merged = { ...loadDotEnv(envFile), ...env };
  for (const key of ["ARENA_AGENT_BRIDGE_KEY", "STORAGE_ENCRYPTION_KEY"]) {
    if (!merged[key]) {
      merged[key] = crypto.randomBytes(32).toString("hex");
      fs.appendFileSync(envFile, `${key}=${merged[key]}\n`, { mode: 0o600 });
      fs.chmodSync(envFile, 0o600);
    }
  }
  const port = Number(merged.ARENA_PORT || 7867);
  const timeoutMs = Number(merged.ARENA_REQUEST_TIMEOUT_MS || 300_000);
  if (!Number.isInteger(port) || port < 1 || port > 65535 || !Number.isFinite(timeoutMs) || timeoutMs < 1000 || timeoutMs > 900_000) {
    throw new Error("Invalid ARENA_PORT or ARENA_REQUEST_TIMEOUT_MS (1000-900000).");
  }
  const core = loadConfig({
    ...merged,
    DATA_DIR: dataDir,
    ARENA_TOOL_POLICY: "block",
    ARENA_MAX_QUEUE: "1",
    ARENA_READ_BUDGET_MS: String(timeoutMs),
    ARENA_TOOL_ALLOW_BUDGET_MS: String(timeoutMs),
  });
  core.mcpEndpointFile = "";
  core.profile = { enabled: false };
  requireSecret(merged);
  return {
    host: merged.ARENA_HOST || "127.0.0.1",
    port,
    timeoutMs,
    apiKey: merged.ARENA_AGENT_BRIDGE_KEY,
    secret: merged.STORAGE_ENCRYPTION_KEY,
    dataDir,
    stateFile: path.join(dataDir, "client-sessions.json"),
    modelsFile: path.join(dataDir, "models.json"),
    core,
  };
}

export function readModels(config) {
  const configured = readJSON(config.modelsFile, []);
  if (!Array.isArray(configured)) {
    throw new ArenaError(503, "invalid_models", "models.json must be an array.");
  }
  const archived = readEntries(config.core.archiveDir).filter((entry) => entry.Model && entry.Email).map((entry) => ({
    id: sessionIdFromUrl(entry.Url),
    sessionId: sessionIdFromUrl(entry.Url),
    accountEmail: entry.Email,
    name: entry.Model,
  }));
  const models = new Map();
  for (const entry of [...configured, ...archived]) {
    if (!entry || typeof entry.id !== "string" || !entry.id.trim() || typeof entry.accountEmail !== "string" || !entry.accountEmail.trim() || !/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(entry.sessionId || "")) {
      throw new ArenaError(503, "invalid_models", "Each model needs id, sessionId (UUID) and accountEmail.");
    }
    if (!models.has(entry.id)) {
      models.set(entry.id, { ...entry, sessionId: entry.sessionId.toLowerCase() });
    }
  }
  return [...models.values()];
}

export function createRuntime(config) {
  const credentials = new CredentialStore({ filePath: config.core.credentialsFile, secret: config.secret }).load();
  const bridge = new Bridge({ config: config.core, credentials, recaptcha: null });
  let activeSignal = null;
  let submissionStarted = false;
  const append = bridge.appendAgentMessage.bind(bridge);
  bridge.appendAgentMessage = async (...args) => {
    activeSignal?.throwIfAborted();
    // 浏览器断开后的上游自动重试不能再次提交结果不确定的同一轮。
    if (submissionStarted) {
      throw new ArenaError(502, "arena_submission_uncertain", "Arena submission was interrupted; inspect the session before continuing.");
    }
    submissionStarted = true;
    try {
      return await append(...args);
    } catch (error) {
      activeSignal?.throwIfAborted();
      throw error;
    }
  };
  const read = bridge.readLatestTurn.bind(bridge);
  bridge.readLatestTurn = async (...args) => {
    let result;
    try {
      result = await read(...args);
    } catch (error) {
      activeSignal?.throwIfAborted();
      throw error;
    }
    activeSignal?.throwIfAborted();
    if (result.errorText) {
      throw new ArenaError(502, "arena_stream_error", "Arena reported a generation error.");
    }
    if (result.timing?.breakReason === "budget") {
      throw new ArenaError(504, "arena_timeout", "Arena did not finish within the configured time limit.");
    }
    return result;
  };
  return {
    models: () => readModels(config),
    health() {
      credentials.load();
      const models = readModels(config);
      const accounts = credentials.list();
      const ready = models.some((model) => accounts.some((account) => !account.disabled && account.email.toLowerCase() === model.accountEmail.toLowerCase()));
      return { status: ready ? "configured" : "not_ready", ready, models: models.length, accounts: accounts.length, verification: "configuration_only", usage: "unavailable" };
    },
    async generate(model, request, { onDelta, signal, idempotencyKey }) {
      credentials.load();
      const account = credentials.forSession(model.accountEmail);
      if (!account) {
        throw new ArenaError(503, "login_required", "No usable Arena account. Run npm run manage -- login.");
      }
      activeSignal = signal;
      submissionStarted = false;
      let cancellation = Promise.resolve();
      const cancel = () => {
        cancellation = bridge.browser.close();
        cancellation.catch(() => undefined);
      };
      signal.addEventListener("abort", cancel, { once: true });
      try {
        signal.throwIfAborted();
        if (credentials.needsRefresh(account, config.core.refreshMarginSec)) {
          const login = credentials.loginSecretFor(account);
          if (!login?.password) {
            throw new ArenaError(401, "login_expired", "Arena login expired. Run npm run manage -- login.");
          }
          const refreshed = await bridge.browser.login(login.email, login.password);
          signal.throwIfAborted();
          credentials.replaceCookie(refreshed.email, refreshed.cookieHeader);
          await bridge.browser.close();
        }
        const owner = credentials.forSession(model.accountEmail);
        return await bridge.converse(model.sessionId, { model: model.id, messages: [{ role: "user", content: request.prompt }] }, {
          account: owner,
          accountEmail: model.accountEmail,
          onDelta,
          injectMcp: false,
          idempotencyKey,
        });
      } finally {
        signal.removeEventListener("abort", cancel);
        await cancellation;
        if (signal.aborted) {
          await bridge.browser.close();
        }
        activeSignal = null;
      }
    },
    async loginAndPrepare(email, password, signal) {
      let cancellation = Promise.resolve();
      const cancel = () => {
        cancellation = bridge.browser.close();
        cancellation.catch(() => undefined);
      };
      signal.addEventListener("abort", cancel, { once: true });
      try {
        signal.throwIfAborted();
        const result = await bridge.browser.login(email, password);
        signal.throwIfAborted();
        const page = await bridge.browser.getPage(result.cookieHeader, crypto.randomUUID());
        signal.throwIfAborted();
        // 自动创建全新会话并完成一次文本探测，不复用其他客户端的历史。
        const state = await bridge.createAgentSession(page, "Reply with READY only. Do not use tools or ask questions.");
        signal.throwIfAborted();
        state.readBudgetMs = config.timeoutMs;
        const raw = await bridge.readAgentOutput(page, state);
        signal.throwIfAborted();
        const reply = parseAgentOutput(raw);
        if (!reply.text.trim() || !reply.lastNodeId || reply.requiresReview || reply.nativeCalls.length > 0) {
          throw new ArenaError(502, "session_not_ready", "Arena could not prepare a text session.");
        }
        if (!/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(state.id)) {
          throw new ArenaError(502, "invalid_session", "Arena returned an invalid session.");
        }
        const models = readJSON(config.modelsFile, []);
        if (!Array.isArray(models)) {
          throw new ArenaError(503, "invalid_models", "models.json must be an array.");
        }
        const model = { id: `arena-session-${state.id}`, sessionId: state.id.toLowerCase(), accountEmail: result.email, name: "Arena Agent session" };
        credentials.load();
        credentials.upsert({ email: result.email, cookieHeader: result.cookieHeader, password: result.password });
        saveJSON(config.modelsFile, [...models, model]);
        return { uid: result.email, nickname: result.email, model_id: model.id };
      } finally {
        signal.removeEventListener("abort", cancel);
        await cancellation;
        await bridge.browser.close();
      }
    },
    async login(email, password) {
      const result = await bridge.browser.login(email, password);
      credentials.upsert({ email: result.email, cookieHeader: result.cookieHeader, password: result.password });
      await bridge.browser.close();
      return { loggedIn: true };
    },
    close: () => bridge.browser.close(),
  };
}
