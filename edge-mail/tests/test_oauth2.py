"""Outlook OAuth2/XOAUTH2 支持的单测：离线，不联网。

覆盖三部分：
1. XOAUTH2 SASL 载荷纯函数（build_xoauth2 / xoauth2_b64）。
2. refresh_token 换 access_token（伪造令牌端点）。
3. IMAP / SMTP 在 OAuth2 预设下走 XOAUTH2（假连接 + 假令牌端点）。
"""
from __future__ import annotations

import base64
import json
import urllib.error
import urllib.request

import pytest

from app import mail_client as mc
from app import oauth2 as oauth
from app.providers import PROVIDERS, resolve_provider
from tests.fake_servers import FakeIMAP, FakeSMTP


# --------------------------------------------------------------------------
# XOAUTH2 SASL 纯函数
# --------------------------------------------------------------------------


def test_build_xoauth2_payload_format():
    payload = oauth.build_xoauth2("zjarlin@outlook.com", "TOKEN123")
    decoded = payload.decode()
    assert decoded.startswith("user=zjarlin@outlook.com\x01auth=Bearer TOKEN123\x01\x01")
    assert decoded.endswith("\x01\x01")


def test_xoauth2_b64_matches_raw_payload():
    b64 = oauth.xoauth2_b64("a@outlook.com", "tok")
    raw = oauth.build_xoauth2("a@outlook.com", "tok")
    assert base64.b64decode(b64) == raw


# --------------------------------------------------------------------------
# refresh_token -> access_token 交换
# --------------------------------------------------------------------------


def _outlook_provider() -> object:
    return resolve_provider("outlook", "user@outlook.com")


def _patch_token_endpoint(monkeypatch, payload, status=200):
    """把令牌端点替换成假响应，返回被调用参数供断言。"""
    import io

    captured = {}

    def fake_urlopen(request, timeout=None):
        captured["url"] = request.full_url
        captured["body"] = request.data
        body_bytes = request.data or b""

        class Resp:
            def __enter__(self_):
                return self_

            def __exit__(self_, *args):
                return False

            def read(self_):
                if status >= 400:
                    err = urllib.error.HTTPError(
                        request.full_url, status, "err", {}, io.BytesIO(json.dumps(payload).encode())
                    )
                    raise err
                return json.dumps(payload).encode()

        return Resp()

    monkeypatch.setattr(urllib.request, "urlopen", fake_urlopen)
    return captured


def test_exchange_refresh_token_returns_tokens(monkeypatch):
    captured = _patch_token_endpoint(
        monkeypatch, {"access_token": "ACC", "expires_in": 3599}, status=200
    )
    provider = _outlook_provider()
    result = oauth.exchange_refresh_token(provider, "REFRESH")
    assert result["access_token"] == "ACC"
    assert "grant_type=refresh_token" in captured["body"].decode()
    assert "client_id=9e5f94bc" in captured["body"].decode()
    assert "refresh_token=REFRESH" in captured["body"].decode()


def test_get_access_token_returns_string(monkeypatch):
    _patch_token_endpoint(monkeypatch, {"access_token": "ACC"}, status=200)
    provider = _outlook_provider()
    assert oauth.get_access_token(provider, "REFRESH") == "ACC"


def test_get_access_token_missing_raises(monkeypatch):
    _patch_token_endpoint(monkeypatch, {"expires_in": 1}, status=200)
    provider = _outlook_provider()
    with pytest.raises(oauth.OAuth2Error):
        oauth.get_access_token(provider, "REFRESH")


def test_token_exchange_http_error_raises(monkeypatch):
    _patch_token_endpoint(
        monkeypatch, {"error": "invalid_grant", "error_description": "bad"}, status=400
    )
    provider = _outlook_provider()
    with pytest.raises(oauth.OAuth2Error) as excinfo:
        oauth.exchange_refresh_token(provider, "REFRESH")
    assert "invalid_grant" in str(excinfo.value)


def test_outlook_preset_is_oauth2():
    provider = _outlook_provider()
    assert provider.key == "outlook"
    assert provider.auth == "oauth2"
    assert provider.oauth2_client_id == "9e5f94bc-e8a4-4e73-b8be-63364c29d753"
    assert provider.oauth2_token_endpoint.startswith("https://login.microsoftonline.com/")


# --------------------------------------------------------------------------
# 假连接 + 假令牌端点：IMAP / SMTP XOAUTH2 全路径
# --------------------------------------------------------------------------

# 给 FakeIMAP 增加 authenticate 方法
def _patch_imap_oauth(monkeypatch, fake: FakeIMAP, access_token: str):
    class ExtraFake(FakeIMAP):
        def authenticate(self, mechanism, authobject):
            # imaplib 会调用 authobject(None) 拿到响应
            sasl = authobject(None)
            assert sasl == f"user=user@outlook.com\x01auth=Bearer {access_token}\x01\x01".encode()
            fake.calls.append(("authenticate", mechanism))
            if not self.login_ok:
                raise mc._IMAP_ERROR("AUTHENTICATIONFAILED Invalid credentials")
            return ("OK", [b"authenticated"])

    monkeypatch.setattr(mc.imaplib, "IMAP4_SSL", lambda *a, **k: ExtraFake(**fake.__dict__.copy()))
    monkeypatch.setattr(mc.imaplib, "IMAP4", lambda *a, **k: ExtraFake(**fake.__dict__.copy()))


def test_imap_xoauth2_flow_uses_authenticate(monkeypatch):
    # 返回令牌的假端点
    _patch_token_endpoint(monkeypatch, {"access_token": "ACC_IMAP"}, status=200)
    fake = FakeIMAP(login_ok=True)
    _patch_imap_oauth(monkeypatch, fake, "ACC_IMAP")
    account = mc.build_account("user@outlook.com", "REFRESH")
    with mc.ImapClient(account):
        pass
    auth_calls = [c for c in fake.calls if c[0] == "authenticate"]
    assert len(auth_calls) == 1
    assert auth_calls[0][1] == "XOAUTH2"


def test_imap_xoauth2_failure_is_auth_error(monkeypatch):
    _patch_token_endpoint(monkeypatch, {"access_token": "ACC"}, status=200)
    fake = FakeIMAP(login_ok=False)
    _patch_imap_oauth(monkeypatch, fake, "ACC")
    account = mc.build_account("user@outlook.com", "REFRESH")
    with pytest.raises(mc.MailAuthError):
        with mc.ImapClient(account):
            pass


# SMTP XOAUTH2：FakeSMTP 需要支持 docmd
def _patch_smtp(monkeypatch, fake: FakeSMTP):
    monkeypatch.setattr(mc.smtplib, "SMTP", lambda *a, **k: fake)
    monkeypatch.setattr(mc.smtplib, "SMTP_SSL", lambda *a, **k: fake)


def test_smtp_xoauth2_flow(monkeypatch):
    _patch_token_endpoint(monkeypatch, {"access_token": "ACC_SMTP"}, status=200)

    class OAuthFakeSMTP(FakeSMTP):
        def __init__(self, *a, **k):
            super().__init__(*a, **k)
            self.auth_used = None
            self.auth_sasl = None

        def docmd(self, verb, arg=None):
            if verb == "AUTH" and arg.startswith("XOAUTH2 "):
                self.auth_used = "XOAUTH2"
                self.auth_sasl = arg[len("XOAUTH2 "):]
                return (235, b"ok")
            return (250, b"ok")

    fake = OAuthFakeSMTP("smtp.office365.com", 587)
    _patch_smtp(monkeypatch, fake)

    account = mc.build_account("user@outlook.com", "REFRESH")
    result = mc.send_message(
        account=account,
        to=["user@outlook.com"],
        subject="t",
        body="hello",
        timeout=30,
    )
    assert fake.auth_used == "XOAUTH2"
    # 校验 base64 载荷里的 access_token
    decoded = base64.b64decode(fake.auth_sasl).decode()
    assert "Bearer ACC_SMTP" in decoded
    assert result["accepted"] is True


def test_smtp_login_still_uses_password_for_non_oauth(monkeypatch):
    fake = FakeSMTP("smtp.qq.com", 465)
    _patch_smtp(monkeypatch, fake)
    account = mc.build_account("user@qq.com", "authcode")
    mc.send_message(account=account, to=["user@qq.com"], subject="t", body="x", timeout=30)
    assert fake.logged_in == ("user@qq.com", "authcode")
