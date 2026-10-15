"""出站代理支持（HTTP CONNECT / SOCKS5）。

部分邮箱服务商在特定网络下会被阻断（实测 252 到 smtp.gmail.com 的 465/587/25
全部超时），需要通过代理才能收发。imaplib/smtplib 原生不支持代理，这里用最小的
纯标准库实现，把「建立到目标主机的 TCP 连接」这一步替换为「经代理隧道连接」，
再由 stdlib 原有的逻辑在其上做 SSL / STARTTLS。

支持两种代理：
- http://host:port     —— 走 HTTP CONNECT 隧道
- socks5://host:port（或 socks5h）—— 走 SOCKS5（域名交给代理解析）

带认证时写 ``scheme://user:pass@host:port``。该模块只做连接建立，不做 TLS。
"""
from __future__ import annotations

import base64
import socket
from urllib.parse import urlsplit, unquote

_SUPPORTED_SCHEMES = {"http", "socks5", "socks5h", "socks"}


class ProxyError(OSError):
    """代理连接失败。"""


def proxy_tcp_connect(proxy_url: str, host: str, port: int, timeout: float | None) -> socket.socket:
    """经 ``proxy_url`` 建立一条到 ``host:port`` 的 TCP 连接。

    失败时抛 ``ProxyError``（OSError 子类），便于上层统一映射错误。
    """
    parsed = urlsplit(proxy_url)
    scheme = (parsed.scheme or "").lower()
    if scheme not in _SUPPORTED_SCHEMES:
        raise ProxyError(f"unsupported proxy scheme: {scheme or '(empty)'}")
    if not parsed.hostname:
        raise ProxyError("proxy host is required")

    try:
        sock = socket.create_connection((parsed.hostname, parsed.port), timeout)
    except OSError as exc:
        raise ProxyError(f"cannot reach proxy {parsed.hostname}:{parsed.port}: {exc}") from exc

    try:
        if scheme == "http":
            _http_connect(sock, host, port, unquote(parsed.username or ""), unquote(parsed.password or ""))
        else:
            _socks5_connect(sock, host, port, unquote(parsed.username or ""), unquote(parsed.password or ""))
    except ProxyError:
        sock.close()
        raise
    except OSError as exc:
        sock.close()
        raise ProxyError(f"proxy handshake failed: {exc}") from exc
    except Exception as exc:  # 协议异常一律归一化，避免泄漏内部细节
        sock.close()
        raise ProxyError(f"proxy handshake failed: {type(exc).__name__}") from exc
    return sock


def _http_connect(sock: socket.socket, host: str, port: int, user: str | None, password: str | None) -> None:
    lines = [f"CONNECT {host}:{port} HTTP/1.1", f"Host: {host}:{port}"]
    if user:
        token = base64.b64encode(f"{user}:{password or ''}".encode("utf-8")).decode("ascii")
        lines.append(f"Proxy-Authorization: Basic {token}")
    sock.sendall(("\r\n".join(lines) + "\r\n\r\n").encode("ascii"))

    buffer = b""
    while b"\r\n\r\n" not in buffer:
        chunk = sock.recv(1)
        if not chunk:
            raise ProxyError("proxy closed the connection during CONNECT")
        buffer += chunk
        if len(buffer) > 64 * 1024:
            raise ProxyError("proxy CONNECT response too large")
    status_line = buffer.split(b"\r\n", 1)[0].decode("latin-1", "replace")
    parts = status_line.split(" ", 2)
    if len(parts) < 2 or parts[1] != "200":
        raise ProxyError(f"proxy refused CONNECT: {status_line.strip()}")


def _socks5_connect(sock: socket.socket, host: str, port: int, user: str | None, password: str | None) -> None:
    # 1) 协商认证方式：有账号则同时提供「无认证」与「用户名/密码」
    sock.sendall(b"\x05\x02\x00\x02" if user else b"\x05\x01\x00")
    reply = _recv_exact(sock, 2)
    if reply[0] != 0x05:
        raise ProxyError("proxy is not a SOCKS5 server")
    method = reply[1]
    if method == 0x02:
        if not user:
            raise ProxyError("socks5 proxy requires authentication")
        ub = user.encode("utf-8")
        pb = (password or "").encode("utf-8")
        if len(ub) > 255 or len(pb) > 255:
            raise ProxyError("socks5 credentials too long")
        sock.sendall(b"\x01" + bytes([len(ub)]) + ub + bytes([len(pb)]) + pb)
        auth = _recv_exact(sock, 2)
        if auth[1] != 0x00:
            raise ProxyError("socks5 authentication failed")
    elif method != 0x00:
        raise ProxyError(f"socks5 proxy requires unsupported auth method {method}")

    # 2) 请求连接：用域名形式（ATYP=3），让代理解析 DNS
    hb = host.encode("idna")
    if len(hb) > 255:
        raise ProxyError("target host name too long")
    sock.sendall(b"\x05\x01\x00\x03" + bytes([len(hb)]) + hb + port.to_bytes(2, "big"))

    head = _recv_exact(sock, 4)
    if head[1] != 0x00:
        raise ProxyError(f"socks5 connect failed (code={head[1]})")
    atyp = head[3]
    if atyp == 0x01:
        _recv_exact(sock, 4)
    elif atyp == 0x03:
        _recv_exact(sock, _recv_exact(sock, 1)[0])
    elif atyp == 0x04:
        _recv_exact(sock, 16)
    else:
        raise ProxyError(f"socks5 returned unknown address type {atyp}")
    _recv_exact(sock, 2)  # 绑定端口


def _recv_exact(sock: socket.socket, count: int) -> bytes:
    buffer = b""
    while len(buffer) < count:
        chunk = sock.recv(count - len(buffer))
        if not chunk:
            raise ProxyError("proxy closed the connection unexpectedly")
        buffer += chunk
    return buffer


def normalize_proxy(value: str | None) -> str:
    """校验并规范化代理地址；空值返回空串。"""
    text = (value or "").strip()
    if not text:
        return ""
    parsed = urlsplit(text)
    scheme = (parsed.scheme or "").lower()
    if scheme not in _SUPPORTED_SCHEMES:
        raise ProxyError(f"unsupported proxy scheme: {scheme or '(empty)'}")
    if not parsed.hostname or not parsed.port:
        raise ProxyError("proxy must include host and port")
    return text
