import { ArenaError } from "./request.mjs";

const failures = new Map([
  ["arena_access_blocked", [503, "Arena blocked access from the server browser. Check the server network or proxy before retrying."]],
  ["arena_verification_required", [503, "Arena rejected security verification during chat. Confirm that the account can send messages on the Arena website."]],
  ["login_invalid_credentials", [401, "Arena rejected the email or password."]],
  ["session_not_usable", [403, "Arena accepted sign-in but the account session is unavailable."]],
  ["session_not_ready", [502, "Arena signed in but did not return a usable text session."]],
  ["browser_unavailable", [503, "The Arena browser could not start. Check the browser installation."]],
  ["arena_network_error", [502, "The server browser could not reach Arena. Check the server network or proxy."]],
  ["arena_login_timeout", [504, "Arena login or session preparation timed out. Retry later."]],
  ["arena_session_prepare_failed", [502, "Arena signed in but could not create or verify an Agent session."]],
  ["login_failed", [502, "Arena login failed. Retry or check the server login diagnostics."]],
]);
const stages = new Set(["login", "browser_launch", "arena_home", "email_sign_in", "agent_session", "session_probe", "save_session"]);

export function arenaRecaptchaRejected(value) {
  return /recaptcha validation failed/i.test(String(value || ""));
}

export function arenaOutputRecaptchaRejected(raw) {
  for (const line of String(raw || "").split(/\r?\n/)) {
    if (!line.startsWith("data: ")) {
      continue;
    }
    let event;
    try {
      event = JSON.parse(line.slice(6));
    } catch {
      continue;
    }
    for (const record of Array.isArray(event?.records) ? event.records : []) {
      let body;
      try {
        body = JSON.parse(record.body);
      } catch {
        continue;
      }
      const data = body?.data;
      if (data?.type !== "error") {
        continue;
      }
      const message = data.errorText || data.message || data.error;
      if (arenaRecaptchaRejected(typeof message === "string" ? message : JSON.stringify(message))) {
        return true;
      }
    }
  }
  return false;
}

// 仅保留静态分类、阶段和 HTTP 状态，原始错误可能包含密码、Cookie 或邮箱。
export function loginFailure(error, fallbackStage = "login") {
  const stage = stages.has(error?.stage) ? error.stage : stages.has(fallbackStage) ? fallbackStage : "login";
  const message = typeof error?.message === "string" ? error.message : "";
  const loginStatus = /^Arena login failed \((\d{3})\):/.exec(message);
  const adapterFailure = error instanceof ArenaError && failures.has(error.code);
  const status = Number(error?.upstreamStatus || (adapterFailure ? undefined : error?.status) || loginStatus?.[1]);
  const upstreamStatus = Number.isInteger(status) && status >= 100 && status <= 599 ? status : undefined;
  let code = failures.has(error?.code) ? error.code : "login_failed";
  if (code === "login_failed") {
    if (arenaRecaptchaRejected(message) && (stage === "agent_session" || stage === "session_probe")) {
      code = "arena_verification_required";
    } else if (/just a moment|attention required|cloudflare|cf-chl-|recaptcha validation failed/i.test(message) || (stage === "arena_home" && (upstreamStatus === 403 || upstreamStatus === 429))) {
      code = "arena_access_blocked";
    } else if (stage === "email_sign_in" && (upstreamStatus === 401 || (upstreamStatus === 400 && /invalid.*(?:email|password|credential)|incorrect.*password/i.test(message)))) {
      code = "login_invalid_credentials";
    } else if (error?.name === "TimeoutError" || /(?:timeout|timed out|timeout.*exceeded)/i.test(message)) {
      code = "arena_login_timeout";
    } else if (stage === "browser_launch") {
      code = "browser_unavailable";
    } else if (/net::ERR_|ECONN(?:RESET|REFUSED|ABORTED)|ENOTFOUND|EAI_AGAIN|ETIMEDOUT|fetch failed/i.test(message)) {
      code = "arena_network_error";
    } else if (stage === "agent_session" || stage === "session_probe") {
      code = "arena_session_prepare_failed";
    }
  }
  const [publicStatus, publicMessage] = failures.get(code);
  const failure = new ArenaError(publicStatus, code, publicMessage);
  failure.stage = stage;
  if (upstreamStatus !== undefined) {
    failure.upstreamStatus = upstreamStatus;
  }
  return failure;
}

// 使用与依赖登录一致的新浏览器上下文，在发送凭据前确认首页可访问。
export async function loginArena(browser, email, password, signal) {
  let stage = "browser_launch";
  try {
    signal?.throwIfAborted();
    const chromium = await browser.launch();
    signal?.throwIfAborted();
    const options = browser.userAgent ? { userAgent: browser.userAgent } : {};
    const context = await chromium.newContext(options);
    try {
      stage = "arena_home";
      const page = await context.newPage();
      const response = await page.goto("https://arena.ai/", { waitUntil: "domcontentloaded", timeout: 60_000 });
      signal?.throwIfAborted();
      const status = response?.status();
      const title = await page.title();
      signal?.throwIfAborted();
      if (status === 403 || status === 429 || /just a moment|attention required|checking your browser|security verification/i.test(title)) {
        const failure = new ArenaError(503, "arena_access_blocked", "Arena blocked the server browser before sign-in.");
        failure.upstreamStatus = status;
        throw failure;
      }
      if (!response || status >= 400) {
        const failure = new ArenaError(502, "arena_network_error", "Arena could not be reached before sign-in.");
        failure.upstreamStatus = status;
        throw failure;
      }
    } finally {
      await context.close().catch(() => undefined);
    }
    signal?.throwIfAborted();
    stage = "email_sign_in";
    const result = await browser.login(email, password);
    signal?.throwIfAborted();
    return result;
  } catch (error) {
    signal?.throwIfAborted();
    throw loginFailure(error, stage);
  }
}
