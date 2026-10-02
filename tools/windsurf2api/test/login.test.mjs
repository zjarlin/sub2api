import assert from "node:assert/strict";
import test from "node:test";
import { LoginSessions, extractToken, emailFromToken } from "../src/login.mjs";
import { WindSurfError } from "../src/protocol.mjs";

function token(payload) {
  const body = Buffer.from(JSON.stringify(payload)).toString("base64url");
  return `devin-session-token$eyJhbGciOiJIUzI1NiJ9.${body}.sig`;
}

test("extractToken pulls the token from a redirect URL, hash, or bare value", () => {
  assert.equal(extractToken("https://windsurf.com/show-auth-token?token=abc.def"), "abc.def");
  assert.equal(extractToken("https://windsurf.com/show-auth-token#access_token=abc.def"), "abc.def");
  assert.equal(extractToken(token({ email: "a@b.com" })), token({ email: "a@b.com" }));
  assert.equal(extractToken("https://windsurf.com/show-auth-token?state=x"), "");
  assert.equal(extractToken("nonsense"), "");
});

test("emailFromToken reads the email out of the JWT payload", () => {
  assert.equal(emailFromToken(token({ email: "user@example.com" })), "user@example.com");
  assert.equal(emailFromToken("auth1_abc"), "");
});

test("starts a callback session with the official OAuth2 authorize URL", () => {
  const logins = new LoginSessions({ runtime: { userStatus: async () => ({ plan: "Pro" }) } });
  const session = logins.start("owner-1");
  assert.equal(session.mode, "callback");
  assert.equal(session.status, "pending");
  const url = new URL(session.authUrl);
  assert.equal(url.origin + url.pathname, "https://windsurf.com/windsurf/signin");
  assert.equal(url.searchParams.get("response_type"), "token");
  assert.equal(url.searchParams.get("redirect_uri"), "show-auth-token");
  assert.equal(url.searchParams.get("client_id"), "3GUryQ7ldAeKEuD2obYnppsnmj58eP5u");
  assert.equal(url.searchParams.get("state"), session.state);
});

test("completing with a valid callback returns the account credential once", async () => {
  const calls = [];
  const logins = new LoginSessions({ runtime: { userStatus: async (t) => { calls.push(t); return { plan: "Pro", email: "user@example.com" }; } } });
  const session = logins.start("owner-1");
  const result = await logins.complete("owner-1", session.id, `https://windsurf.com/show-auth-token?token=${encodeURIComponent(token({ email: "user@example.com" }))}`);
  assert.equal(result.status, "completed");
  assert.equal(result.account.uid, "user@example.com");
  assert.equal(result.api_key, token({ email: "user@example.com" }));
  assert.equal(calls.length, 1);
});

test("a callback without a token is rejected", async () => {
  const logins = new LoginSessions({ runtime: { userStatus: async () => ({}) } });
  const session = logins.start("owner-1");
  await assert.rejects(
    logins.complete("owner-1", session.id, "https://windsurf.com/show-auth-token?state=x"),
    (error) => error instanceof WindSurfError && error.code === "windsurf_invalid_callback",
  );
});

test("sessions are isolated by owner", () => {
  const logins = new LoginSessions({ runtime: { userStatus: async () => ({}) } });
  const session = logins.start("owner-1");
  assert.throws(() => logins.get("owner-2", session.id), (error) => error.status === 404);
});

test("only one Windsurf login may run at a time", () => {
  const logins = new LoginSessions({ runtime: { userStatus: async () => ({}) } });
  logins.start("owner-1");
  assert.throws(() => logins.start("owner-2"), (error) => error.status === 429);
});
