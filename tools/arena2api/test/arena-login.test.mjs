import assert from "node:assert/strict";
import { test } from "node:test";
import { arenaOutputRecaptchaRejected, loginArena, loginFailure } from "../src/arena-login.mjs";
import { ArenaError } from "../src/request.mjs";

const email = "owner@example.com";
const password = " private-password ";

function fixture(t, { status = 200, title = "Arena", navigationError, signInError } = {}) {
  const page = {
    goto: t.mock.fn(async () => {
      if (navigationError) {
        throw navigationError;
      }
      return { status: () => status };
    }),
    title: t.mock.fn(async () => title),
  };
  const context = { newPage: async () => page, close: t.mock.fn(async () => {}) };
  const chromium = { newContext: t.mock.fn(async () => context) };
  const browser = {
    userAgent: "",
    launch: t.mock.fn(async () => chromium),
    login: t.mock.fn(async (currentEmail, currentPassword) => {
      if (signInError) {
        throw signInError;
      }
      return { email: currentEmail, password: currentPassword, cookieHeader: "private-cookie" };
    }),
  };
  return { browser, chromium, context, page };
}

for (const [status, title] of [[403, "Attention Required! | Cloudflare"], [200, "Just a moment..."], [429, "Arena"]]) {
  test(`blocked homepage (${status}, ${title}) is rejected before credentials are submitted`, async (t) => {
    const f = fixture(t, { status, title });
    await assert.rejects(loginArena(f.browser, email, password), { code: "arena_access_blocked", stage: "arena_home", upstreamStatus: status });
    assert.equal(f.browser.login.mock.callCount(), 0);
    assert.equal(f.context.close.mock.callCount(), 1);
  });
}

test("accessible homepage permits the existing browser login with the exact password", async (t) => {
  const f = fixture(t);
  const result = await loginArena(f.browser, email, password, new AbortController().signal);
  assert.deepEqual(f.browser.login.mock.calls[0].arguments, [email, password]);
  assert.deepEqual(f.chromium.newContext.mock.calls[0].arguments, [{}]);
  assert.equal(result.password, password);
  assert.equal(f.context.close.mock.callCount(), 1);
});

test("homepage upstream failure is classified as network access and never attempts sign-in", async (t) => {
  const f = fixture(t, { status: 502 });
  await assert.rejects(loginArena(f.browser, email, password), { code: "arena_network_error", stage: "arena_home", upstreamStatus: 502 });
  assert.equal(f.browser.login.mock.callCount(), 0);
  assert.equal(f.context.close.mock.callCount(), 1);
});

test("browser launch errors are classified without exposing the browser stderr", async (t) => {
  const f = fixture(t);
  f.browser.launch = t.mock.fn(async () => { throw new Error(`Executable missing ${email} ${password}`); });
  await assert.rejects(loginArena(f.browser, email, password), (error) => {
    assert.equal(error.code, "browser_unavailable");
    assert.equal(error.stage, "browser_launch");
    assert.ok(!error.message.includes(email) && !error.message.includes(password));
    return true;
  });
  assert.equal(f.browser.login.mock.callCount(), 0);
});

test("credential rejection retains only a safe category and HTTP status", async (t) => {
  const f = fixture(t, { signInError: new Error(`Arena login failed (401): ${email} ${password} private-cookie`) });
  await assert.rejects(loginArena(f.browser, email, password), (error) => {
    assert.equal(error.code, "login_invalid_credentials");
    assert.equal(error.upstreamStatus, 401);
    assert.ok(!error.message.includes(email) && !error.message.includes("private"));
    return true;
  });
});

test("sign-in blocking is distinguished from a wrong password", async (t) => {
  const f = fixture(t, { signInError: new Error(`Arena login failed (403): Cloudflare ${email} ${password}`) });
  await assert.rejects(loginArena(f.browser, email, password), { code: "arena_access_blocked", stage: "email_sign_in", upstreamStatus: 403 });
});

test("chat reCAPTCHA rejection is distinguished from sign-in failure", () => {
  const failure = loginFailure(new Error(`recaptcha validation failed ${email} ${password}`), "session_probe");
  assert.equal(failure.code, "arena_verification_required");
  assert.equal(failure.stage, "session_probe");
  assert.ok(!failure.message.includes(email) && !failure.message.includes(password));
});

test("only an SSE error event can report chat reCAPTCHA rejection", () => {
  const text = { records: [{ body: JSON.stringify({ data: { type: "text-delta", delta: "recaptcha validation failed" } }) }] };
  const error = { records: [{ body: JSON.stringify({ data: { type: "error", errorText: "recaptcha validation failed" } }) }] };
  assert.equal(arenaOutputRecaptchaRejected(`data: ${JSON.stringify(text)}\n\n`), false);
  assert.equal(arenaOutputRecaptchaRejected(`data: ${JSON.stringify(error)}\n\n`), true);
});

test("network and timeout errors during homepage loading release the context", async (t) => {
  for (const [navigationError, code] of [[new Error("net::ERR_CONNECTION_RESET"), "arena_network_error"], [Object.assign(new Error("private timeout details"), { name: "TimeoutError" }), "arena_login_timeout"]]) {
    const f = fixture(t, { navigationError });
    await assert.rejects(loginArena(f.browser, email, password), { code, stage: "arena_home" });
    assert.equal(f.browser.login.mock.callCount(), 0);
    assert.equal(f.context.close.mock.callCount(), 1);
  }
});

test("cancellation during homepage inspection never submits credentials", async (t) => {
  const controller = new AbortController();
  const f = fixture(t);
  f.page.title = t.mock.fn(async () => { controller.abort(); return "Arena"; });
  await assert.rejects(loginArena(f.browser, email, password, controller.signal), { name: "AbortError" });
  assert.equal(f.browser.login.mock.callCount(), 0);
  assert.equal(f.context.close.mock.callCount(), 1);
});

test("public classification ignores unrecognized code, stage, and error text", () => {
  const failure = loginFailure(Object.assign(new Error(`${email} ${password} private-cookie`), { code: password, stage: email, status: password }));
  assert.equal(failure.code, "login_failed");
  assert.equal(failure.stage, "login");
  assert.equal(failure.upstreamStatus, undefined);
  assert.ok(!failure.message.includes(email) && !failure.message.includes("private"));
});

test("safe classifications stay idempotent and do not invent upstream HTTP status", () => {
  const failure = loginFailure(new ArenaError(403, "session_not_usable", `${email} private-cookie`), "email_sign_in");
  const repeated = loginFailure(failure);
  assert.equal(repeated.code, "session_not_usable");
  assert.equal(repeated.stage, "email_sign_in");
  assert.equal(repeated.upstreamStatus, undefined);
  assert.ok(!repeated.message.includes(email) && !repeated.message.includes("private"));
});
