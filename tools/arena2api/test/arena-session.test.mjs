import assert from "node:assert/strict";
import { test } from "node:test";
import { createArenaSession } from "../src/arena-session.mjs";

const sessionId = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";
const modelId = "11111111-2222-4333-8444-555555555555";
const prompt = "Reply with READY only.";

function fixture(t, { states = ["ready"], status = 200, created = { id: sessionId, modelId }, cookieVisible = false, clickError } = {}) {
  const editor = {
    fill: t.mock.fn(async () => {}),
    filter: t.mock.fn(function () { return this; }),
    last: t.mock.fn(function () { return this; }),
  };
  const send = {
    click: t.mock.fn(async () => {
      if (clickError) {
        throw clickError;
      }
    }),
    filter: t.mock.fn(function () { return this; }),
    last: t.mock.fn(function () { return this; }),
  };
  const cookies = {
    first: t.mock.fn(function () { return this; }),
    isVisible: t.mock.fn(async () => cookieVisible),
    click: t.mock.fn(async () => {}),
  };
  const response = { status: () => status, text: t.mock.fn(async () => JSON.stringify(created)) };
  const page = {
    goto: t.mock.fn(async () => {}),
    reload: t.mock.fn(async () => {}),
    waitForFunction: t.mock.fn(async (_predicate, _arg, _options) => ({
      jsonValue: async () => states.shift() || "fallback",
      dispose: async () => {},
    })),
    getByRole: t.mock.fn(() => cookies),
    locator: t.mock.fn((selector) => selector === '[contenteditable="true"]' ? editor : send),
    waitForResponse: t.mock.fn(async () => response),
    evaluate: t.mock.fn(async () => ({ status: 200, html: JSON.stringify({ publicAccessToken: "private-agent-token" }) })),
  };
  return { page, editor, send, cookies, response };
}

test("a new Agent session recovers from one fallback page and submits exactly once", async (t) => {
  const f = fixture(t, { states: ["fallback", "ready"], cookieVisible: true });
  const state = await createArenaSession(f.page, prompt, new AbortController().signal);
  assert.equal(f.page.reload.mock.callCount(), 1);
  assert.equal(f.page.waitForFunction.mock.callCount(), 2);
  for (const call of f.page.waitForFunction.mock.calls) {
    assert.equal(call.arguments.length, 3);
    assert.equal(call.arguments[1], undefined);
    assert.equal(call.arguments[2].timeout, 60_000);
  }
  assert.equal(f.cookies.click.mock.callCount(), 1);
  assert.equal(f.editor.fill.mock.callCount(), 1);
  assert.equal(f.editor.fill.mock.calls[0].arguments[0], prompt);
  assert.deepEqual(f.editor.filter.mock.calls[0].arguments, [{ visible: true }]);
  assert.equal(f.send.click.mock.callCount(), 1);
  assert.equal(f.page.waitForResponse.mock.callCount(), 1);
  const responseMatcher = f.page.waitForResponse.mock.calls[0].arguments[0];
  assert.ok(responseMatcher({ url: () => "https://arena.ai/nextjs-api/stream/create-chat" }));
  assert.equal(responseMatcher({ url: () => "https://arena.ai/agent" }), false);
  assert.equal(f.page.evaluate.mock.calls[0].arguments[1], sessionId);
  assert.equal(state.id, sessionId);
  assert.equal(state.token, "private-agent-token");
  assert.equal(state.lastNodeId, null);
  assert.equal(state.requiresReview, false);
  assert.equal(state.toolsInitialized, false);
  assert.ok(Number.isFinite(state.updatedAt));
});

test("persistent fallback stops before filling or sending the prompt", async (t) => {
  const f = fixture(t, { states: ["fallback", "fallback", "fallback"] });
  await assert.rejects(createArenaSession(f.page, prompt), { code: "arena_session_prepare_failed", stage: "agent_session" });
  assert.equal(f.page.waitForFunction.mock.callCount(), 3);
  assert.equal(f.page.reload.mock.callCount(), 2);
  assert.equal(f.editor.fill.mock.callCount(), 0);
  assert.equal(f.send.click.mock.callCount(), 0);
  assert.equal(f.page.waitForResponse.mock.callCount(), 0);
});

test("cancellation during composer hydration does not submit a prompt", async (t) => {
  const controller = new AbortController();
  const f = fixture(t);
  f.page.waitForFunction = t.mock.fn(async () => {
    controller.abort();
    return { jsonValue: async () => "ready", dispose: async () => {} };
  });
  await assert.rejects(createArenaSession(f.page, prompt, controller.signal), { name: "AbortError" });
  assert.equal(f.page.reload.mock.callCount(), 0);
  assert.equal(f.editor.fill.mock.callCount(), 0);
  assert.equal(f.send.click.mock.callCount(), 0);
  assert.equal(f.page.waitForResponse.mock.callCount(), 0);
});

test("a send failure never reloads or retries a potentially created session", async (t) => {
  const f = fixture(t, { clickError: new Error("private-password private-cookie") });
  await assert.rejects(createArenaSession(f.page, prompt), (error) => {
    assert.equal(error.code, "arena_session_prepare_failed");
    assert.ok(!error.message.includes("private"));
    return true;
  });
  assert.equal(f.page.goto.mock.callCount(), 1);
  assert.equal(f.page.reload.mock.callCount(), 0);
  assert.equal(f.send.click.mock.callCount(), 1);
  assert.equal(f.page.waitForResponse.mock.callCount(), 1);
  assert.equal(f.page.evaluate.mock.callCount(), 0);
});

test("failed creation response retains a safe status without parsing or retrying", async (t) => {
  const f = fixture(t, { status: 503, created: { id: sessionId, secret: "private-password private-cookie" } });
  await assert.rejects(createArenaSession(f.page, prompt), (error) => {
    assert.equal(error.code, "arena_session_prepare_failed");
    assert.equal(error.upstreamStatus, 503);
    assert.ok(!error.message.includes("private"));
    return true;
  });
  assert.equal(f.response.text.mock.callCount(), 0);
  assert.equal(f.page.reload.mock.callCount(), 0);
  assert.equal(f.send.click.mock.callCount(), 1);
  assert.equal(f.page.evaluate.mock.callCount(), 0);
});

test("invalid session UUID is rejected before fetching a token even with a valid model UUID", async (t) => {
  const f = fixture(t, { created: { id: "invalid-session", modelId } });
  await assert.rejects(createArenaSession(f.page, prompt), { code: "arena_session_prepare_failed" });
  assert.equal(f.page.reload.mock.callCount(), 0);
  assert.equal(f.send.click.mock.callCount(), 1);
  assert.equal(f.page.evaluate.mock.callCount(), 0);
});

test("an already cancelled preparation never navigates or sends", async (t) => {
  const controller = new AbortController();
  controller.abort();
  const f = fixture(t);
  await assert.rejects(createArenaSession(f.page, prompt, controller.signal), { name: "AbortError" });
  assert.equal(f.page.goto.mock.callCount(), 0);
  assert.equal(f.send.click.mock.callCount(), 0);
});
