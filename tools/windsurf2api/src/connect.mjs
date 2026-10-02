// Connect-RPC envelope framing. Frame: [1 byte flags][4 byte BE length][payload].
// flags: 0x01 gzip, 0x02 end-of-stream trailer (JSON).
import { gzipSync, gunzipSync } from "node:zlib";

const MAX_FRAME_SIZE = 16 * 1024 * 1024;

export function wrapEnvelope(protoBuf, { compress = true } = {}) {
  let payload = protoBuf;
  let flags = 0;
  if (compress && payload.length > 0) {
    payload = gzipSync(payload);
    flags |= 0x01;
  }
  const frame = Buffer.alloc(5 + payload.length);
  frame[0] = flags;
  frame.writeUInt32BE(payload.length, 1);
  payload.copy(frame, 5);
  return frame;
}

export function decodeFrames(buf) {
  const out = [];
  let pos = 0;
  while (pos + 5 <= buf.length) {
    const flags = buf[pos];
    const len = buf.readUInt32BE(pos + 1);
    if (len > MAX_FRAME_SIZE) throw new Error("Connect frame exceeds size limit");
    if (pos + 5 + len > buf.length) break;
    let payload = buf.subarray(pos + 5, pos + 5 + len);
    if (flags & 0x01) payload = gunzipSync(payload, { maxOutputLength: MAX_FRAME_SIZE });
    out.push({ flags, isEndStream: !!(flags & 0x02), payload });
    pos += 5 + len;
  }
  return { frames: out, rest: buf.subarray(pos) };
}

// Streaming parser that buffers partial frames across chunk boundaries.
export class FrameParser {
  constructor() {
    this.buffer = Buffer.alloc(0);
  }

  push(chunk) {
    this.buffer = this.buffer.length ? Buffer.concat([this.buffer, chunk]) : Buffer.from(chunk);
    const { frames, rest } = decodeFrames(this.buffer);
    this.buffer = rest;
    return frames;
  }
}

export function connectHeaders(extra = {}) {
  return {
    "Content-Type": "application/connect+proto",
    "Connect-Protocol-Version": "1",
    "Connect-Accept-Encoding": "gzip",
    "User-Agent": "connect-es/2.0.0",
    ...extra,
  };
}
