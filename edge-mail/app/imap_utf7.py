"""IMAP4 修改版 UTF-7 编解码（RFC 3501 §5.1.3）。

中文/日文等非 ASCII 文件夹名（如「已发送」「垃圾邮件」）在 IMAP 协议里必须
编码成 modified UTF-7，否则 SEARCH/SELECT 会失败或选中不存在的邮箱。
stdlib 不提供该编解码，这里做一个最小实现。
"""
from __future__ import annotations

import base64

_B64_ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+,"
_STD_ALPHABET = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"


def _b64_encode(raw: bytes) -> str:
    return base64.b64encode(raw).decode("ascii").rstrip("=").translate(str.maketrans(_STD_ALPHABET, _B64_ALPHABET))


def _b64_decode(text: str) -> bytes:
    padded = text.translate(str.maketrans(_B64_ALPHABET, _STD_ALPHABET))
    padded += "=" * (-len(padded) % 4)
    return base64.b64decode(padded)


def encode(value: str) -> str:
    """把 Unicode 字符串编码为 IMAP modified UTF-7。"""
    out: list[str] = []
    pending: list[str] = []

    def flush() -> None:
        if pending:
            out.append("&" + _b64_encode("".join(pending).encode("utf-16-be")) + "-")
            pending.clear()

    for char in value:
        code = ord(char)
        if char == "&":
            flush()
            out.append("&-")
        elif 0x20 <= code <= 0x7E:
            flush()
            out.append(char)
        else:
            pending.append(char)
    flush()
    return "".join(out)


def decode(value: str) -> str:
    """把 IMAP modified UTF-7 解码回 Unicode 字符串。"""
    out: list[str] = []
    index = 0
    length = len(value)
    while index < length:
        char = value[index]
        if char != "&":
            out.append(char)
            index += 1
            continue
        end = value.find("-", index + 1)
        if end == -1:
            out.append(value[index:])
            break
        chunk = value[index + 1 : end]
        if chunk == "":
            out.append("&")
        else:
            out.append(_b64_decode(chunk).decode("utf-16-be", errors="replace"))
        index = end + 1
    return "".join(out)
