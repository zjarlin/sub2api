"""出站代理模块的离线单测：不访问真实网络，用内存套接字替身驱动协议。"""
from __future__ import annotations

import pytest

from app import proxy as proxy_mod


class FakeSock:
    """记录发送内容并按脚本返回接收数据的套接字替身。"""

    def __init__(self, script: list[bytes]):
        self.script = list(script)
        self.sent = bytearray()
        self.closed = False

    def sendall(self, data: bytes) -> None:
        self.sent += data

    def recv(self, count: int) -> bytes:
        if not self.script:
            return b""
        head = self.script[0]
        if len(head) <= count:
            self.script.pop(0)
            return head
        self.script[0] = head[count:]
        return head[:count]

    def close(self) -> None:
        self.closed = True


@pytest.fixture()
def patch_connect(monkeypatch):
    def _install(sock: FakeSock) -> dict:
        captured: dict = {}

        def fake_create_connection(address, timeout=None):
            captured["address"] = address
            captured["timeout"] = timeout
            return sock

        monkeypatch.setattr(proxy_mod.socket, "create_connection", fake_create_connection)
        return captured

    return _install


def test_normalize_proxy_accepts_supported_schemes():
    assert proxy_mod.normalize_proxy("http://127.0.0.1:7890") == "http://127.0.0.1:7890"
    assert proxy_mod.normalize_proxy("socks5://user:pass@host:1080").startswith("socks5://")
    assert proxy_mod.normalize_proxy("") == ""
    assert proxy_mod.normalize_proxy(None) == ""
    assert proxy_mod.normalize_proxy("   ") == ""


@pytest.mark.parametrize("value", ["ftp://host:21", "host:1080", "http://host", "socks5://"])
def test_normalize_proxy_rejects_bad_values(value: str):
    with pytest.raises(proxy_mod.ProxyError):
        proxy_mod.normalize_proxy(value)


def test_http_connect_sends_connect_and_accepts_200(patch_connect):
    sock = FakeSock([b"HTTP/1.1 200 Connection established\r\n\r\n"])
    captured = patch_connect(sock)
    result = proxy_mod.proxy_tcp_connect("http://proxy.local:3128", "imap.gmail.com", 993, 10)
    assert result is sock
    assert captured["address"] == ("proxy.local", 3128)
    assert captured["timeout"] == 10
    sent = bytes(sock.sent).decode("ascii")
    assert sent.startswith("CONNECT imap.gmail.com:993 HTTP/1.1\r\n")
    assert "Host: imap.gmail.com:993" in sent


def test_http_connect_sends_basic_auth(patch_connect):
    sock = FakeSock([b"HTTP/1.1 200 OK\r\n\r\n"])
    patch_connect(sock)
    proxy_mod.proxy_tcp_connect("http://user:secret@proxy.local:3128", "h", 443, 5)
    sent = bytes(sock.sent).decode("ascii")
    # base64("user:secret") == dXNlcjpzZWNyZXQ=
    assert "Proxy-Authorization: Basic dXNlcjpzZWNyZXQ=" in sent


def test_http_connect_rejects_non_200(patch_connect):
    sock = FakeSock([b"HTTP/1.1 403 Forbidden\r\n\r\n"])
    patch_connect(sock)
    with pytest.raises(proxy_mod.ProxyError) as exc:
        proxy_mod.proxy_tcp_connect("http://proxy.local:3128", "h", 443, 5)
    assert "403" in str(exc.value)
    assert sock.closed is True


def test_socks5_no_auth_success(patch_connect):
    # 协商回复 05 00；连接回复 05 00 00 01 + IPv4 + 端口
    sock = FakeSock([b"\x05\x00", b"\x05\x00\x00\x01\x7f\x00\x00\x01\x1f\x90"])
    patch_connect(sock)
    result = proxy_mod.proxy_tcp_connect("socks5://proxy.local:1080", "imap.qq.com", 993, 8)
    assert result is sock
    sent = bytes(sock.sent)
    assert sent.startswith(b"\x05\x01\x00")
    # 请求体：VER CMD RSV ATYP=3 + 长度 + 域名 + 端口
    assert b"\x05\x01\x00\x03" in sent
    assert b"imap.qq.com" in sent
    assert sent.endswith((993).to_bytes(2, "big"))


def test_socks5_username_password_auth(patch_connect):
    sock = FakeSock([b"\x05\x02", b"\x01\x00", b"\x05\x00\x00\x01\x00\x00\x00\x00\x00\x00"])
    patch_connect(sock)
    proxy_mod.proxy_tcp_connect("socks5://bob:pw@proxy.local:1080", "h", 25, 5)
    sent = bytes(sock.sent)
    assert sent.startswith(b"\x05\x02\x00\x02")  # 同时提供无认证与用户密码
    assert b"\x01\x03bob\x02pw" in sent


def test_socks5_auth_failure(patch_connect):
    sock = FakeSock([b"\x05\x02", b"\x01\x01"])
    patch_connect(sock)
    with pytest.raises(proxy_mod.ProxyError) as exc:
        proxy_mod.proxy_tcp_connect("socks5://bob:bad@proxy.local:1080", "h", 25, 5)
    assert "authentication failed" in str(exc.value)
    assert sock.closed is True


def test_socks5_connect_refused(patch_connect):
    sock = FakeSock([b"\x05\x00", b"\x05\x05\x00\x01\x00\x00\x00\x00\x00\x00"])
    patch_connect(sock)
    with pytest.raises(proxy_mod.ProxyError) as exc:
        proxy_mod.proxy_tcp_connect("socks5://proxy.local:1080", "h", 25, 5)
    assert "code=5" in str(exc.value)


def test_proxy_unreachable_is_proxy_error(monkeypatch):
    def boom(address, timeout=None):
        raise OSError("network is unreachable")

    monkeypatch.setattr(proxy_mod.socket, "create_connection", boom)
    with pytest.raises(proxy_mod.ProxyError) as exc:
        proxy_mod.proxy_tcp_connect("http://proxy.local:3128", "h", 443, 5)
    assert "cannot reach proxy" in str(exc.value)


def test_unsupported_scheme_rejected():
    with pytest.raises(proxy_mod.ProxyError):
        proxy_mod.proxy_tcp_connect("ftp://proxy.local:21", "h", 443, 5)


def test_http_connect_preserves_smtp_banner(patch_connect):
    sock = FakeSock([b"HTTP/1.1 200 OK\r\n\r\n220 ready\r\n"])
    patch_connect(sock)
    proxy_mod.proxy_tcp_connect("http://proxy:80", "smtp.test", 587, 5)
    assert sock.recv(100) == b"220 ready\r\n"


def test_proxy_password_percent_decoding(patch_connect):
    import base64
    sock = FakeSock([b"HTTP/1.1 200 OK\r\n\r\n"])
    patch_connect(sock)
    proxy_mod.proxy_tcp_connect("http://user:p%40ss@proxy:80", "smtp.test", 465, 5)
    assert base64.b64encode(b"user:p@ss") in sock.sent


def test_https_proxy_rejected_until_nested_tls_supported():
    with pytest.raises(proxy_mod.ProxyError):
        proxy_mod.normalize_proxy("https://proxy:443")
