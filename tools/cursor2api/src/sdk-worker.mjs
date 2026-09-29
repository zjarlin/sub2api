import { parentPort, workerData } from "node:worker_threads";
import { Agent, Cursor, JsonlLocalAgentStore } from "@cursor/sdk";
import { generateWithSDK } from "./runtime.mjs";
import { publicError } from "./request.mjs";

const acknowledgements = new Map();
let nextID = 0;
parentPort.on("message", (message) => {
  if (message.type !== "ack") return;
  acknowledgements.get(message.id)?.();
  acknowledgements.delete(message.id);
});

function delta(type, value) {
  const id = nextID++;
  const acknowledged = new Promise((resolve) => acknowledgements.set(id, resolve));
  parentPort.postMessage({ type, id, value });
  return acknowledged;
}

try {
  const { operation, apiKey, request, model, directory } = workerData;
  const value = operation === "models"
    ? await Cursor.models.list({ apiKey })
    : await generateWithSDK({ Agent, Cursor, JsonlLocalAgentStore }, apiKey, request, model, directory, {
      onText: (text) => delta("text", text),
      onThinking: (text) => delta("thinking", text),
    });
  parentPort.postMessage({ type: "result", value });
} catch (error) {
  const detail = publicError(error);
  parentPort.postMessage({ type: "error", status: detail.status, code: detail.code, message: detail.message });
}
