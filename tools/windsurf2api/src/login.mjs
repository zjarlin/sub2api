import crypto from "node:crypto";
import { WindSurfError } from "./protocol.mjs";

const LOGIN_TIMEOUT_MS = 20 * 60 * 1000;
const SESSION_TTL_MS = 30 * 60 * 1000;
const MAX_SESSIONS = 50;

// Windsurf 官方 OAuth2 隐式流（编辑器 "Provide Authentication Token" 备份登录用的
// 同一入口）。管理员在自己的浏览器完成授权后，windsurf.com 会把
// devin-session-token$<JWT> 回传到 show-auth-token 回调地址；这里把整段回调 URL 或
// 裸 Token 交给会话校验并换取账号凭据。不落盘，Token 仅在会话内存中短暂保留。
const SIGNIN_URL = "https://windsurf.com/windsurf/signin";
const OAUTH_CLIENT_ID = "3GUryQ7ldAeKEuD2obYnppsnmj58eP5u";
const OAUTH_REDIRECT_URI = "show-auth-token";

export class LoginSessions {
  constructor({ runtime, timeoutMs = LOGIN_TIMEOUT_MS, sessionTTLms = SESSION_TTL_MS } = {}) {
    this.runtime = runtime || {};
    this.timeoutMs = timeoutMs;
    this.sessionTTLms = sessionTTLms;
    this.sessions = new Map();
    // 每个管理员同时只保留一个进行中的登录会话；新会话直接替换旧会话，
    // 避免上一次未完成的授权把后续登录永久挡在 login_busy 上。
    this.activeByOwner = new Map();
  }

  start(owner) {
    const now = Date.now();
    for (const [id, session] of this.sessions) {
      if (session.expiresAt <= now) {
        this.sessions.delete(id);
        if (this.activeByOwner.get(session.owner) === session) this.activeByOwner.delete(session.owner);
      }
    }
    // 替换同一管理员尚未完成的旧会话。
    const existing = this.activeByOwner.get(owner);
    if (existing && this.sessions.get(existing.id) === existing) this.sessions.delete(existing.id);
    if (this.sessions.size >= MAX_SESSIONS) {
      throw new WindSurfError(429, "login_limit", "Too many Windsurf login attempts. Try again later.");
    }
    const state = crypto.randomBytes(24).toString("hex");
    const session = {
      id: crypto.randomBytes(32).toString("hex"),
      owner,
      mode: "callback",
      status: "pending",
      state,
      expiresAt: now + this.sessionTTLms,
    };
    session.authUrl = buildAuthURL(state);
    this.sessions.set(session.id, session);
    this.activeByOwner.set(owner, session);
    return session;
  }

  get(owner, id) {
    const session = this.sessions.get(id);
    if (!session || session.owner !== owner) {
      throw new WindSurfError(404, "login_not_found", "Login session not found.");
    }
    if (session.expiresAt <= Date.now() || session.status === "expired") {
      this.sessions.delete(id);
      if (this.activeByOwner.get(session.owner) === session) this.activeByOwner.delete(session.owner);
      throw new WindSurfError(410, "login_expired", "Windsurf login expired; start a new login.");
    }
    return session;
  }

  // 校验回调 URL / 裸 Token，并把换到的账号凭据写回会话。
  async complete(owner, id, callbackUrl) {
    const session = this.get(owner, id);
    if (session.status === "completed") return this.result(session);
    const token = extractToken(callbackUrl);
    if (!token) {
      throw new WindSurfError(400, "windsurf_invalid_callback", "Paste the full Windsurf redirect URL, or the session token it contains.");
    }
    if (typeof this.runtime.userStatus !== "function") {
      throw new WindSurfError(503, "windsurf_login_unavailable", "Windsurf sign-in is unavailable in this adapter build.");
    }
    let status;
    try {
      status = await this.runtime.userStatus(token);
    } catch (error) {
      if (error instanceof WindSurfError) throw error;
      throw new WindSurfError(502, "windsurf_login_failed", "Windsurf sign-in did not complete; restart the sign-in.");
    }
    const email = status.email || emailFromToken(token);
    session.status = "completed";
    session.apiKey = token;
    session.account = {
      uid: email || "windsurf",
      nickname: email || "windsurf",
      ...(status.plan ? { plan: status.plan } : {}),
    };
    if (this.activeByOwner.get(owner) === session) this.activeByOwner.delete(owner);
    return this.result(session);
  }

  cancel(owner, id) {
    const session = this.get(owner, id);
    this.sessions.delete(id);
    if (this.activeByOwner.get(owner) === session) this.activeByOwner.delete(owner);
  }

  result(session) {
    return {
      session_id: session.id,
      mode: session.mode,
      status: session.status,
      expires_at: session.expiresAt,
      ...(session.authUrl ? { auth_url: session.authUrl } : {}),
      ...(session.account ? { account: session.account } : {}),
      // 仅在授权完成后回传一次换得的账号 Token，供管理页面写入账号凭据。
      ...(session.status === "completed" && session.apiKey ? { api_key: session.apiKey } : {}),
    };
  }

  close() {
    this.activeByOwner.clear();
    this.sessions.clear();
  }
}

function buildAuthURL(state) {
  const params = new URLSearchParams({
    response_type: "token",
    client_id: OAUTH_CLIENT_ID,
    redirect_uri: OAUTH_REDIRECT_URI,
    state,
    prompt: "login",
    redirect_parameters_type: "query",
    workflow: "",
  });
  return `${SIGNIN_URL}?${params.toString()}`;
}

// 从回调 URL（query 或 hash）里取 token；也接受直接粘贴的裸 Token。
export function extractToken(input) {
  const raw = String(input ?? "").trim();
  if (!raw || raw.length > 8192) return "";
  if (!raw.includes("://")) {
    return /^(devin-session-token\$|auth1_|eyJ[A-Za-z0-9_-]+\.)/i.test(raw) ? raw : "";
  }
  try {
    const url = new URL(raw);
    const pick = (params) => params.get("token") || params.get("api_key") || params.get("access_token") || "";
    const fromQuery = pick(url.searchParams);
    if (fromQuery) return fromQuery.trim();
    if (url.hash) {
      const fromHash = pick(new URLSearchParams(url.hash.replace(/^#/, "")));
      if (fromHash) return fromHash.trim();
    }
  } catch {
    // 非 URL，忽略。
  }
  return "";
}

// 尽力从 devin-session-token$<JWT> 的 payload 里取邮箱，用于展示；失败返回空串。
export function emailFromToken(token) {
  const jwt = String(token || "").startsWith("devin-session-token$")
    ? String(token).slice("devin-session-token$".length)
    : String(token || "");
  const parts = jwt.split(".");
  if (parts.length < 2) return "";
  try {
    const payload = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"));
    for (const key of ["email", "preferred_username", "name", "sub"]) {
      const value = payload?.[key];
      if (typeof value === "string" && value.includes("@")) return value;
    }
  } catch {
    // payload 不是可解析的 JSON，忽略。
  }
  return "";
}
