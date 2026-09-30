import crypto from "node:crypto";
import { CursorError } from "./request.mjs";

const LOGIN_TIMEOUT_MS = 20 * 60 * 1000;
const SESSION_TTL_MS = 30 * 60 * 1000;
const AUTH_URL_WAIT_MS = 8000;

// Cursor 官方 SDK 的浏览器登录：SDK 生成 PKCE 挑战、托管轮询，管理员在浏览器完成
// 授权后它铸造一个可撤销、默认 90 天有效的账号 Key。这里不落盘，Key 只在会话中
// 短暂保留，由主服务写入账号凭据。
export class LoginSessions {
  constructor({ sdk, timeoutMs = LOGIN_TIMEOUT_MS, sessionTTLms = SESSION_TTL_MS } = {}) {
    if (!sdk?.Cursor?.auth?.login) throw new Error("Cursor SDK auth.login is required.");
    this.sdk = sdk;
    this.timeoutMs = timeoutMs;
    this.sessionTTLms = sessionTTLms;
    this.sessions = new Map();
    this.active = null;
  }

  start(owner) {
    if (this.active) {
      throw new CursorError(429, "login_busy", "A Cursor login is already running.");
    }
    for (const [id, session] of this.sessions) {
      if (session.expiresAt <= Date.now()) this.sessions.delete(id);
    }
    if (this.sessions.size >= 50) {
      throw new CursorError(429, "login_limit", "Too many Cursor login attempts. Try again later.");
    }
    const controller = new AbortController();
    const session = {
      id: crypto.randomBytes(32).toString("hex"),
      owner,
      mode: "poll",
      status: "pending",
      expiresAt: Date.now() + this.sessionTTLms,
      controller,
    };
    this.sessions.set(session.id, session);
    this.active = session;
    session.timer = setTimeout(
      () => controller.abort(new CursorError(410, "login_expired", "Cursor login expired; start a new login.")),
      this.timeoutMs,
    );
    session.timer.unref();
    let resolveAuthUrl;
    session.authUrlReady = new Promise((resolve) => { resolveAuthUrl = resolve; });
    session.task = this.sdk.Cursor.auth.login({
      openBrowser: false,
      store: null,
      apiKeyName: "Sub2API",
      signal: controller.signal,
      onLoginUrl: (url) => {
        session.authUrl = url;
        resolveAuthUrl();
      },
    }).then((result) => {
      if (controller.signal.aborted) return;
      session.status = "completed";
      session.apiKey = result.apiKey;
      session.account = { uid: result.email || "cursor", nickname: result.email };
    }).catch((error) => {
      if (controller.signal.aborted) {
        session.status = "expired";
        return;
      }
      session.status = "failed";
      session.failure = loginFailure(error);
      console.warn(JSON.stringify({ event: "cursor_login_failed", code: session.failure.code }));
    }).finally(() => {
      clearTimeout(session.timer);
      resolveAuthUrl();
      this.active = null;
    });
    return session;
  }

  async startResult(owner) {
    const session = this.start(owner);
    // 首屏尽量带回授权链接；拿不到也不阻塞，前端轮询会补齐。
    await Promise.race([session.authUrlReady, sleep(AUTH_URL_WAIT_MS)]);
    return this.result(session);
  }

  get(owner, id) {
    const session = this.sessions.get(id);
    if (!session || session.owner !== owner) {
      throw new CursorError(404, "login_not_found", "Login session not found.");
    }
    if (session.expiresAt <= Date.now()) {
      session.controller.abort();
      this.sessions.delete(id);
      throw new CursorError(410, "login_expired", "Login session expired.");
    }
    return session;
  }

  poll(owner, id) {
    const session = this.get(owner, id);
    if (session.status === "failed") throw session.failure;
    if (session.status === "expired") {
      throw new CursorError(410, "login_expired", "Cursor login expired; start a new login.");
    }
    return this.result(session);
  }

  cancel(owner, id) {
    const session = this.get(owner, id);
    session.controller.abort();
    this.sessions.delete(id);
  }

  result(session) {
    return {
      session_id: session.id,
      mode: session.mode,
      status: session.status,
      expires_at: session.expiresAt,
      ...(session.authUrl ? { auth_url: session.authUrl } : {}),
      ...(session.account ? { account: session.account } : {}),
      // 仅在授权完成后回传一次铸造出的账号 Key，供管理页面写入账号凭据。
      ...(session.status === "completed" && session.apiKey ? { api_key: session.apiKey } : {}),
    };
  }

  async close() {
    if (this.active) {
      this.active.controller.abort();
      await this.active.task;
    }
    this.sessions.clear();
  }
}

function sleep(ms) {
  return new Promise((resolve) => { const timer = setTimeout(resolve, ms); timer.unref(); });
}

function loginFailure(error) {
  const name = error?.name ?? "";
  if (name === "AuthenticationError") {
    return new CursorError(401, "cursor_login_failed", "Cursor rejected the login attempt; restart the sign-in.");
  }
  return new CursorError(502, "cursor_login_failed", "Cursor sign-in did not complete; restart the sign-in.");
}
