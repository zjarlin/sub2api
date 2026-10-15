import crypto from "node:crypto";

const ORIGIN = "https://agent.teleai.com.cn";
const LOGIN_URL = `${ORIGIN}/meter/teleagent-login/login?source=01`;
const TTL_MS = 10 * 60_000;

export class AdapterError extends Error {
  constructor(status, code, message) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

// 登录完成后向官方用户接口核实令牌，账号身份不采用浏览器提交的字段。
export async function verifyToken(token, { fetchImpl = fetch, signal } = {}) {
  if (typeof token !== "string" || !token || token.length > 16_384) {
    throw new AdapterError(400, "invalid_token", "TeleAgent login did not return a valid token.");
  }
  const response = await fetchImpl(`${ORIGIN}/superCowork/sapi/cowork/user_info`, {
    headers: { "X-Token": token, "X-App-Version": "2.7.0" },
    signal: signal ?? AbortSignal.timeout(20_000),
    redirect: "error",
  });
  if (!response.ok) {
    throw new AdapterError(502, "teleagent_login_rejected", "TeleAgent rejected the login token.");
  }
  const envelope = await response.json();
  const user = envelope.data;
  const uid = String(user?.userId ?? user?.accountId ?? "");
  if (envelope.code !== 200 || !/^[A-Za-z0-9_-]{1,128}$/.test(uid)) {
    throw new AdapterError(502, "teleagent_login_rejected", "TeleAgent could not verify the account.");
  }
  let expires = 0;
  try {
    const payload = JSON.parse(Buffer.from(token.split(".").at(-2), "base64url").toString());
    expires = Number(payload.exp) || 0;
  } catch {
    // 有效性已由官方接口核实；非 JWT 令牌不推测过期时间。
  }
  return { uid, nickname: String(user.userName || user.accountName || uid), expires_at: expires };
}

export function tokenFromCallback(value) {
  try {
    const url = new URL(value);
    if (url.origin !== ORIGIN || url.pathname !== "/superCowork/sapi/cowork/auth" || !url.searchParams.get("code")) {
      return null;
    }
    return url.searchParams.get("token");
  } catch {
    return null;
  }
}

export class LoginSessions {
  constructor({ chromium, store, fetchImpl = fetch, now = Date.now, maxSessions = 4, ttlMs = TTL_MS }) {
    this.chromium = chromium;
    this.store = store;
    this.fetch = fetchImpl;
    this.now = now;
    this.maxSessions = maxSessions;
    this.ttlMs = ttlMs;
    this.sessions = new Map();
    this.browser = null;
  }

  async start(owner) {
    await this.cleanup();
    const pending = [...this.sessions.values()].find((s) => s.owner === owner && s.status === "pending");
    // 页面刷新会丢失前端会话 ID；同一归属恢复已有浏览器，不重复占用名额。
    if (pending?.page && !pending.failure) { return this.result(pending); }
    if (this.sessions.size >= this.maxSessions || pending) {
      throw new AdapterError(429, "login_busy", "A TeleAgent login is already pending; finish or cancel it first.");
    }
    const session = {
      id: crypto.randomBytes(32).toString("hex"), owner, status: "pending",
      expiresAt: this.now() + this.ttlMs, context: null, page: null, failure: null,
    };
    this.sessions.set(session.id, session);
    try {
      this.browser ??= this.chromium.launch({ headless: true, args: ["--disable-dev-shm-usage"] }).catch((error) => {
        this.browser = null;
        throw error;
      });
      const browser = await this.browser;
      session.context = await browser.newContext({ viewport: { width: 720, height: 760 }, locale: "zh-CN" });
      session.context.on("request", (request) => {
        const token = tokenFromCallback(request.url());
        if (token && !session.completing && session.status === "pending") {
          session.completing = this.complete(session, token).catch(() => {
            session.failure = new AdapterError(502, "login_failed", "TeleAgent login could not be saved; restart authorization.");
          });
        }
      });
      session.page = await session.context.newPage();
      await session.page.goto(LOGIN_URL, { waitUntil: "domcontentloaded", timeout: 35_000 });
      session.timer = setTimeout(() => { void this.remove(session.id); }, this.ttlMs);
      session.timer.unref();
      return this.result(session);
    } catch (error) {
      await this.remove(session.id);
      if (error instanceof AdapterError) {
        throw error;
      }
      throw new AdapterError(502, "login_browser_unavailable", "Unable to open the TeleAgent login page.");
    }
  }

  async complete(session, token) {
    const user = await verifyToken(token, { fetchImpl: this.fetch });
    if (!this.sessions.has(session.id) || session.expiresAt <= this.now()) {
      return;
    }
    const account = await this.store.save({
      ...user, token, owner: session.owner,
      device_id: crypto.randomBytes(6).toString("hex"), install_id: crypto.randomUUID(),
    });
    session.account = { uid: account.uid, nickname: account.nickname };
    session.apiKey = account.api_key;
    session.status = "completed";
    await session.context?.close();
    session.context = null;
    session.page = null;
  }

  get(owner, id) {
    const session = this.sessions.get(id);
    if (!session || session.owner !== owner) {
      throw new AdapterError(404, "login_not_found", "Login session not found.");
    }
    if (session.expiresAt <= this.now()) {
      void this.remove(id);
      throw new AdapterError(410, "login_expired", "Login session expired; start a new login.");
    }
    if (session.failure) {
      throw session.failure;
    }
    return session;
  }

  poll(owner, id) {
    return this.result(this.get(owner, id));
  }

  async view(owner, id) {
    const session = this.get(owner, id);
    if (session.status !== "pending" || !session.page) {
      throw new AdapterError(409, "login_complete", "Login no longer requires interaction.");
    }
    return session.page.screenshot({ type: "png" });
  }

  async input(owner, id, event) {
    const session = this.get(owner, id);
    if (session.status !== "pending" || !session.page) {
      throw new AdapterError(409, "login_complete", "Login no longer requires interaction.");
    }
    const page = session.page;
    const pointValid = Number.isFinite(event.x) && Number.isFinite(event.y) && event.x >= 0 && event.x <= 720 && event.y >= 0 && event.y <= 760;
    switch (event.type) {
      case "click":
        if (!pointValid) { throw new AdapterError(400, "invalid_input", "Invalid click position."); }
        await page.mouse.click(event.x, event.y);
        return;
      case "wheel": {
        const deltaX = event.delta_x ?? 0;
        const deltaY = event.delta_y ?? 0;
        if (!pointValid || !Number.isFinite(deltaY) || !Number.isFinite(deltaX)) {
          throw new AdapterError(400, "invalid_input", "Invalid scroll event.");
        }
        await page.mouse.move(event.x, event.y);
        await page.mouse.wheel(Math.max(-2000, Math.min(2000, deltaX)), Math.max(-2000, Math.min(2000, deltaY)));
        return;
      }
      case "text":
        if (typeof event.text !== "string" || event.text.length > 4096) { throw new AdapterError(400, "invalid_input", "Invalid input text."); }
        await page.keyboard.insertText(event.text);
        return;
      case "key":
        if (!["Enter", "Tab", "Backspace", "Escape", "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Delete", "Space"].includes(event.key)) {
          throw new AdapterError(400, "invalid_input", "Unsupported key.");
        }
        await page.keyboard.press(event.key);
        return;
      default:
        throw new AdapterError(400, "invalid_input", "Unsupported login interaction.");
    }
  }

  async cancel(owner, id) {
    const session = this.sessions.get(id);
    if (!session || session.owner !== owner) {
      throw new AdapterError(404, "login_not_found", "Login session not found.");
    }
    await this.remove(id);
  }

  result(session) {
    return {
      session_id: session.id, mode: "poll", status: session.status, expires_at: session.expiresAt,
      ...(session.status === "completed" ? { account: session.account, api_key: session.apiKey } : {}),
    };
  }

  async remove(id) {
    const session = this.sessions.get(id);
    if (!session) { return; }
    this.sessions.delete(id);
    clearTimeout(session.timer);
    await session.context?.close();
  }

  async cleanup() {
    for (const [id, session] of this.sessions) {
      if (session.expiresAt <= this.now()) { await this.remove(id); }
    }
  }

  async close() {
    for (const id of this.sessions.keys()) { await this.remove(id); }
    if (this.browser) { await (await this.browser).close(); }
  }
}
