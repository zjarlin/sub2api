import crypto from "node:crypto";
import { ArenaError } from "./request.mjs";
import { loginFailure } from "./arena-login.mjs";

// 登录任务只保存状态；密码仅在调用期间传给浏览器，结果按管理员身份隔离。
export class LoginSessions {
  constructor(runtime, timeoutMs = 600_000) {
    this.runtime = runtime;
    this.timeoutMs = timeoutMs;
    this.sessions = new Map();
    this.active = null;
  }

  start(owner, input) {
    if (this.active) {
      throw new ArenaError(429, "login_busy", "An Arena login is already running.");
    }
    const email = typeof input?.email === "string" ? input.email.trim() : "";
    const password = input?.password;
    if (!email || email.length > 320 || typeof password !== "string" || !password || password.length > 4096) {
      throw new ArenaError(400, "invalid_login", "Arena email and password are required.");
    }
    for (const [id, session] of this.sessions) {
      if (session.expires_at <= Date.now()) {
        this.sessions.delete(id);
      }
    }
    if (this.sessions.size >= 50) {
      throw new ArenaError(429, "login_limit", "Too many login attempts. Try again later.");
    }
    const session = {
      session_id: crypto.randomBytes(32).toString("hex"),
      owner,
      mode: "poll",
      status: "pending",
      expires_at: Date.now() + this.timeoutMs,
      controller: new AbortController(),
    };
    this.sessions.set(session.session_id, session);
    this.active = session;
    session.timer = setTimeout(() => session.controller.abort(), this.timeoutMs);
    session.timer.unref();
    session.task = this.runtime.loginAndPrepare(email, password, session.controller.signal)
      .then((account) => {
        if (!session.controller.signal.aborted) {
          session.account = account;
          session.status = "completed";
        }
      })
      .catch((error) => {
        session.error = loginFailure(error);
        session.status = "failed";
        if (!session.controller.signal.aborted) {
          console.warn(JSON.stringify({ event: "arena_login_failed", code: session.error.code, stage: session.error.stage, ...(session.error.upstreamStatus ? { upstream_status: session.error.upstreamStatus } : {}) }));
        }
      })
      .finally(() => {
        clearTimeout(session.timer);
        this.active = null;
      });
    return this.result(session);
  }

  get(owner, id) {
    const session = this.sessions.get(id);
    if (!session || session.owner !== owner) {
      throw new ArenaError(404, "login_not_found", "Login session not found.");
    }
    if (session.expires_at <= Date.now()) {
      session.controller.abort();
      this.sessions.delete(id);
      throw new ArenaError(410, "login_expired", "Login session expired.");
    }
    return session;
  }

  poll(owner, id) {
    const session = this.get(owner, id);
    if (session.status === "failed") {
      throw session.error;
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
      session_id: session.session_id,
      mode: session.mode,
      status: session.status,
      expires_at: session.expires_at,
      ...(session.account ? { account: session.account } : {}),
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
