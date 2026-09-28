import fs from "node:fs";
import path from "node:path";
import { ArenaError } from "./request.mjs";

export function saveJSON(file, value) {
  fs.mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 });
  const temporary = `${file}.${process.pid}.tmp`;
  fs.writeFileSync(temporary, JSON.stringify(value, null, 2), { mode: 0o600 });
  fs.renameSync(temporary, file);
  fs.chmodSync(file, 0o600);
}

export function readJSON(file, fallback) {
  try {
    return JSON.parse(fs.readFileSync(file, "utf8"));
  } catch (error) {
    if (error.code === "ENOENT") {
      return fallback;
    }
    throw new ArenaError(503, "invalid_state", `State file is unreadable: ${path.basename(file)}.`);
  }
}

export class SessionState {
  constructor(file) {
    this.file = file;
    this.value = readJSON(file, { version: 1, sessions: {} });
    if (this.value.version !== 1 || !this.value.sessions || typeof this.value.sessions !== "object" || Array.isArray(this.value.sessions)) {
      throw new ArenaError(503, "invalid_state", "Unsupported Arena session state.");
    }
  }

  claim(sessionId, clientId) {
    const previous = this.value.sessions[sessionId];
    if (previous && previous.clientId !== clientId) {
      throw new ArenaError(409, "session_already_bound", "This Arena session belongs to another client conversation. Configure a separate session.");
    }
    if (!previous) {
      this.value.sessions[sessionId] = { clientId, requests: {}, claimedAt: new Date().toISOString() };
      saveJSON(this.file, this.value);
    }
    return this.value.sessions[sessionId];
  }

  begin(sessionId, key, digest) {
    const session = this.value.sessions[sessionId];
    const prior = key ? session.requests[key] : null;
    if (prior) {
      if (prior.digest !== digest) {
        throw new ArenaError(409, "idempotency_conflict", "This idempotency key was used for a different request.");
      }
      if (prior.status !== "completed") {
        throw new ArenaError(409, "turn_outcome_unknown", "The previous turn did not complete locally. Inspect the Arena session before retrying with a new key.");
      }
      return prior.payload;
    }
    if (session.pending) {
      throw new ArenaError(409, "turn_outcome_unknown", "A previous turn was interrupted. Inspect the Arena session and configure a fresh dedicated session before continuing.");
    }
    if (key && Object.keys(session.requests).length >= 500) {
      throw new ArenaError(409, "session_history_limit", "This session has reached its persisted request limit. Configure a new dedicated Arena session.");
    }
    session.pending = { key, digest };
    if (key) {
      session.requests[key] = { digest, status: "pending" };
    }
    saveJSON(this.file, this.value);
    return null;
  }

  finish(sessionId, key, payload) {
    const session = this.value.sessions[sessionId];
    if (key) {
      const request = session.requests[key];
      request.status = "completed";
      request.payload = payload;
    }
    delete session.pending;
    saveJSON(this.file, this.value);
  }
}
