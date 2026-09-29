import { setTimeout } from "node:timers/promises";
import { parsePublicToken } from "arena-local-bridge/src/parser.mjs";
import { loginFailure } from "./arena-login.mjs";
import { ArenaError } from "./request.mjs";

const sessionIdPattern = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i;

function preparationFailure(message, upstreamStatus) {
  const failure = new ArenaError(502, "arena_session_prepare_failed", message);
  if (upstreamStatus !== undefined) {
    failure.upstreamStatus = upstreamStatus;
  }
  return failure;
}

export async function createArenaSession(page, prompt, signal) {
  try {
    signal?.throwIfAborted();
    await page.goto("https://arena.ai/agent", { waitUntil: "domcontentloaded", timeout: 60_000 });
    // 仅在提交前恢复明确的加载失败页，避免结果未知时重复创建会话。
    for (let attempt = 0; attempt < 3; attempt++) {
      signal?.throwIfAborted();
      const ready = await page.waitForFunction(() => {
        const visibleEditor = [...document.querySelectorAll('[contenteditable="true"]')].some((editor) => {
          const style = getComputedStyle(editor);
          const bounds = editor.getBoundingClientRect();
          return style.visibility !== "hidden" && style.display !== "none" && bounds.width > 0 && bounds.height > 0;
        });
        if (visibleEditor) {
          return "ready";
        }
        const text = document.body?.innerText || "";
        return /taking longer than expected|Reload the page/i.test(text) ? "fallback" : false;
      }, undefined, { timeout: 60_000 });
      signal?.throwIfAborted();
      const state = await ready.jsonValue();
      await ready.dispose();
      signal?.throwIfAborted();
      if (state === "ready") {
        break;
      }
      if (state !== "fallback" || attempt === 2) {
        throw preparationFailure("Arena Agent composer is unavailable.");
      }
      await page.reload({ waitUntil: "domcontentloaded", timeout: 60_000 });
    }

    const acceptCookies = page.getByRole("button", { name: "Accept Cookies", exact: true }).first();
    if (await acceptCookies.isVisible()) {
      signal?.throwIfAborted();
      await acceptCookies.click({ timeout: 10_000 });
    }
    signal?.throwIfAborted();
    const editor = page.locator('[contenteditable="true"]').filter({ visible: true }).last();
    await editor.fill(prompt, { timeout: 60_000 });
    signal?.throwIfAborted();
    const createResponse = page.waitForResponse(
      (response) => response.url().includes("/nextjs-api/stream/create-chat"),
      { timeout: 60_000 },
    );
    // 点击失败或取消时，关闭浏览器会拒绝已注册的响应等待。
    createResponse.catch(() => undefined);
    signal?.throwIfAborted();
    await page.locator('button[aria-label="Send message"]').filter({ visible: true }).last().click({ timeout: 60_000 });
    signal?.throwIfAborted();
    const response = await createResponse;
    signal?.throwIfAborted();
    if (response.status() !== 200) {
      throw preparationFailure("Arena Agent session creation failed.", response.status());
    }
    const created = JSON.parse(await response.text());
    signal?.throwIfAborted();
    if (typeof created?.id !== "string" || !sessionIdPattern.test(created.id)) {
      throw preparationFailure("Arena returned an invalid Agent session id.");
    }
    const id = created.id.toLowerCase();
    let token = "";
    for (let attempt = 0; attempt < 8 && !token; attempt++) {
      signal?.throwIfAborted();
      const result = await page.evaluate(async (sessionId) => {
        const response = await fetch(`/agent/${sessionId}`, { credentials: "same-origin" });
        return { status: response.status, html: await response.text() };
      }, id);
      signal?.throwIfAborted();
      if (result.status !== 200) {
        throw preparationFailure("Arena Agent session could not be opened.", result.status);
      }
      token = parsePublicToken(result.html);
      if (!token && attempt < 7) {
        await setTimeout(350, undefined, { signal });
      }
    }
    if (!token) {
      throw preparationFailure("Arena Agent session token is unavailable.");
    }
    signal?.throwIfAborted();
    return { id, token, lastNodeId: null, requiresReview: false, toolsInitialized: false, updatedAt: Date.now() };
  } catch (error) {
    signal?.throwIfAborted();
    throw loginFailure(error, "agent_session");
  }
}
