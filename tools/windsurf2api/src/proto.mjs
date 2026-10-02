// Minimal schema-less protobuf wire codec. Wire types: 0 varint, 1 fixed64,
// 2 length-delimited, 5 fixed32. Only what the Windsurf Connect protocol needs.

export function encodeVarint(value) {
  const bytes = [];
  if (typeof value === "bigint" || value < 0 || value > 0x7fffffff) {
    let b = (typeof value === "bigint" ? value : BigInt(value)) & 0xffffffffffffffffn;
    for (;;) {
      const byte = Number(b & 0x7fn);
      b >>= 7n;
      if (b === 0n) { bytes.push(byte); break; }
      bytes.push(byte | 0x80);
    }
    return Buffer.from(bytes);
  }
  let v = Number(value);
  do {
    let byte = v & 0x7f;
    v >>>= 7;
    if (v > 0) byte |= 0x80;
    bytes.push(byte);
  } while (v > 0);
  return Buffer.from(bytes);
}

export function decodeVarint(buf, offset = 0) {
  let result = 0, shift = 0, pos = offset;
  while (pos < buf.length && shift < 28) {
    const byte = buf[pos++];
    result |= (byte & 0x7f) << shift;
    if (!(byte & 0x80)) return { value: result >>> 0, length: pos - offset };
    shift += 7;
  }
  if (pos >= buf.length) throw new Error("Truncated varint");
  let big = BigInt(result >>> 0);
  let bigShift = BigInt(shift);
  while (pos < buf.length) {
    const byte = buf[pos++];
    big |= BigInt(byte & 0x7f) << bigShift;
    if (!(byte & 0x80)) {
      const asNum = Number(big);
      return { value: Number.isSafeInteger(asNum) ? asNum : big, length: pos - offset };
    }
    bigShift += 7n;
    if (bigShift >= 64n) throw new Error("Varint overflow");
  }
  throw new Error("Truncated varint");
}

function tag(field, wireType) {
  return encodeVarint((field << 3) | wireType);
}

export function writeVarintField(field, value) {
  return Buffer.concat([tag(field, 0), encodeVarint(value)]);
}

export function writeStringField(field, str) {
  if (!str && str !== "") return Buffer.alloc(0);
  const data = Buffer.from(String(str), "utf8");
  return Buffer.concat([tag(field, 2), encodeVarint(data.length), data]);
}

export function writeBytesField(field, data) {
  const buf = Buffer.isBuffer(data) ? data : Buffer.from(data);
  return Buffer.concat([tag(field, 2), encodeVarint(buf.length), buf]);
}

export function writeMessageField(field, msgBuf) {
  if (!msgBuf || msgBuf.length === 0) return Buffer.alloc(0);
  return Buffer.concat([tag(field, 2), encodeVarint(msgBuf.length), msgBuf]);
}

export function writeFixed64Field(field, buf8) {
  return Buffer.concat([tag(field, 1), buf8]);
}

export function f64le(value) {
  const b = Buffer.alloc(8);
  b.writeDoubleLE(value, 0);
  return b;
}

export function parseFields(buf) {
  const fields = [];
  let pos = 0;
  while (pos < buf.length) {
    const t = decodeVarint(buf, pos);
    pos += t.length;
    const fieldNum = Number(t.value) >>> 3;
    const wireType = Number(t.value) & 0x07;
    let value;
    switch (wireType) {
      case 0: {
        const v = decodeVarint(buf, pos);
        pos += v.length;
        value = v.value;
        break;
      }
      case 1:
        if (pos + 8 > buf.length) throw new Error("truncated fixed64");
        value = buf.subarray(pos, pos + 8);
        pos += 8;
        break;
      case 2: {
        const len = decodeVarint(buf, pos);
        pos += len.length;
        const size = Number(len.value);
        if (size < 0 || pos + size > buf.length) throw new Error("truncated len-delimited");
        value = buf.subarray(pos, pos + size);
        pos += size;
        break;
      }
      case 5:
        if (pos + 4 > buf.length) throw new Error("truncated fixed32");
        value = buf.subarray(pos, pos + 4);
        pos += 4;
        break;
      default:
        throw new Error(`Unknown wire type ${wireType}`);
    }
    fields.push({ field: fieldNum, wireType, value });
  }
  return fields;
}

export function getField(fields, num, wireType) {
  return fields.find((f) => f.field === num && (wireType === undefined || f.wireType === wireType)) || null;
}

export function getAllFields(fields, num) {
  return fields.filter((f) => f.field === num);
}
