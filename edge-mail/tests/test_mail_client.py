"""邮件客户端的离线单测：不联网，全部走假连接与纯解析函数。"""
from __future__ import annotations

import email
import imaplib

import pytest

from app import imap_utf7
from app import mail_client as mc
from tests.fake_servers import FakeIMAP, FakeSMTP, build_raw_message


# --------------------------------------------------------------------------
# IMAP modified UTF-7
# --------------------------------------------------------------------------


@pytest.mark.parametrize(
    "plain,encoded",
    [
        ("INBOX", "INBOX"),
        ("已发送", "&XfJT0ZAB-"),
        ("垃圾邮件", "&V4NXPpCuTvY-"),
        ("Sent Items", "Sent Items"),
        ("A&B", "A&-B"),
    ],
)
def test_imap_utf7_roundtrip(plain: str, encoded: str):
    assert imap_utf7.encode(plain) == encoded
    assert imap_utf7.decode(encoded) == plain


def test_imap_utf7_pure_ascii_untouched():
    assert imap_utf7.encode("Sent") == "Sent"
    assert imap_utf7.decode("Trash") == "Trash"


# --------------------------------------------------------------------------
# 账号构造
# --------------------------------------------------------------------------


def test_build_account_uses_provider_preset():
    account = mc.build_account("user@qq.com", "authcode")
    assert account.imap_host == "imap.qq.com"
    assert account.smtp_host == "smtp.qq.com"
    assert account.smtp_starttls is False
    assert account.provider.key == "qq"


def test_build_account_allows_host_override():
    account = mc.build_account(
        "user@corp.example.com",
        "secret",
        provider="outlook",
        imap_host="mail.corp.example.com",
        imap_port=143,
    )
    assert account.imap_host == "mail.corp.example.com"
    assert account.imap_port == 143


@pytest.mark.parametrize("address,credential", [("", "x"), ("noaddress", "x"), ("a@b.com", "")])
def test_build_account_validation(address: str, credential: str):
    with pytest.raises(mc.MailRequestError):
        mc.build_account(address, credential)


def test_display_from_uses_name_when_present():
    account = mc.build_account("user@gmail.com", "app-pw", from_name="张三")
    displayed = account.display_from()
    # formataddr 会把中文显示名按 RFC 2047 编码，而不是直接放 UTF-8 字节。
    assert "=?utf-8?" in displayed.lower()
    assert "user@gmail.com" in displayed
    from email.header import decode_header, make_header

    assert "张三" in str(make_header(decode_header(displayed)))


# --------------------------------------------------------------------------
# 解析纯函数
# --------------------------------------------------------------------------


def test_parse_list_line_decodes_modified_utf7_and_attributes():
    parsed = mc._parse_list_line(r'* LIST (\HasNoChildren \Sent) "/" "&XfJT0ZAB-"')
    assert parsed is not None
    assert parsed["name"] == "已发送"
    assert parsed["delimiter"] == "/"
    assert "Sent" in parsed["attributes"]


def test_parse_list_line_accepts_untagged_form():
    # imaplib.list() 返回的是去掉 "* LIST" 标签的 untagged response；
    # 解析器必须同时支持这一形态，否则生产上 list_folders 会返回空列表。
    parsed = mc._parse_list_line(r'(\HasNoChildren) "/" "INBOX"')
    assert parsed is not None
    assert parsed["name"] == "INBOX"


def test_parse_list_line_rejects_non_list():
    assert mc._parse_list_line("* OK ready") is None
    assert mc._parse_list_line("3 EXISTS") is None


def test_search_criteria_defaults_to_all():
    assert mc._search_criteria(False, "", "", "", "", "", "", 0) == ["ALL"]


def test_search_criteria_combines_filters():
    criteria = mc._search_criteria(
        unread_only=True,
        since="2026-10-01",
        before="2026-10-14",
        subject="发票",
        sender="a@b.com",
        recipient="",
        text="",
        newer_than_days=0,
    )
    assert "UNSEEN" in criteria
    assert "01-Oct-2026" in criteria
    assert "14-Oct-2026" in criteria
    assert "SUBJECT" in criteria


def test_imap_date_rejects_bad_input():
    with pytest.raises(mc.MailRequestError):
        mc._imap_date("not-a-date")


def test_parse_flags_extracts_keywords():
    assert mc._parse_flags("1 (FLAGS (\\Seen \\Flagged))") == ["Seen", "Flagged"]


def test_parse_message_extracts_body_and_attachments():
    raw = build_raw_message(
        "季度报表", "boss@example.com", "me@qq.com", "这是正文内容", attachment=b"%PDF-1.4 data"
    )
    message = email.message_from_bytes(raw)
    parsed = mc._parse_message(message, "7", include_body=True, max_bytes=None)
    assert parsed["uid"] == "7"
    assert parsed["subject"] == "季度报表"
    assert "这是正文内容" in parsed["body_text"]
    assert parsed["has_attachments"] is True
    assert parsed["attachments"][0]["filename"] == "report.pdf"
    assert parsed["from"][0]["address"] == "boss@example.com"


def test_parse_message_without_body_skips_content():
    raw = build_raw_message("hi", "a@b.com", "c@d.com", "secret body")
    parsed = mc._parse_message(email.message_from_bytes(raw), "1", include_body=False, max_bytes=None)
    assert parsed["body_text"] == ""


def test_address_list_handles_display_names_and_encoded_words():
    result = mc._address_list("=?utf-8?B?5byg5LiJ?= <zhangsan@example.com>, li@example.com")
    assert result[0]["address"] == "zhangsan@example.com"
    assert result[0]["name"] == "张三"
    assert result[1]["address"] == "li@example.com"


# --------------------------------------------------------------------------
# IMAP 交互（假连接）
# --------------------------------------------------------------------------


def _patch_imap(monkeypatch, fake: FakeIMAP):
    monkeypatch.setattr(mc.imaplib, "IMAP4_SSL", lambda *a, **k: fake)
    monkeypatch.setattr(mc.imaplib, "IMAP4", lambda *a, **k: fake)


def test_imap_login_failure_is_auth_error(monkeypatch):
    fake = FakeIMAP(login_ok=False)
    _patch_imap(monkeypatch, fake)
    account = mc.build_account("user@gmail.com", "wrong")
    with pytest.raises(mc.MailAuthError) as excinfo:
        with mc.ImapClient(account):
            pass
    assert "应用专用密码" in excinfo.value.message


def test_imap_readonly_select_is_used(monkeypatch):
    fake = FakeIMAP()
    _patch_imap(monkeypatch, fake)
    account = mc.build_account("user@qq.com", "code")
    with mc.ImapClient(account) as client:
        client.list_folders()
    select_calls = [call for call in fake.calls if call[0] == "select"]
    # list_folders 之前不应有写选择；所有 select 都是只读。
    assert all(call[2] is True for call in select_calls)


def test_imap_sends_id_for_netease(monkeypatch):
    fake = FakeIMAP()
    _patch_imap(monkeypatch, fake)
    account = mc.build_account("user@163.com", "code")
    with mc.ImapClient(account):
        pass
    assert any(call[0] == "xatom" and call[1] == "ID" for call in fake.calls)


def test_imap_list_messages_newest_first(monkeypatch):
    fake = FakeIMAP(search_result=["1", "2", "3"])
    fake.payloads = {
        "1": build_raw_message("old", "a@b.com", "me@qq.com", "1"),
        "2": build_raw_message("mid", "a@b.com", "me@qq.com", "2"),
        "3": build_raw_message("new", "a@b.com", "me@qq.com", "3"),
    }
    # summaries 路径直接 FETCH header；用 BODY.PEEK[] 返回整封也能解析 header。
    _patch_imap(monkeypatch, fake)
    account = mc.build_account("user@qq.com", "code")
    with mc.ImapClient(account) as client:
        messages = client.list_messages("INBOX", limit=2)
    assert [m["uid"] for m in messages] == ["3", "2"]


def test_imap_get_message_returns_parsed_body(monkeypatch):
    fake = FakeIMAP()
    fake.payloads = {"5": build_raw_message("hello", "a@b.com", "me@qq.com", "正文")}
    _patch_imap(monkeypatch, fake)
    account = mc.build_account("user@qq.com", "code")
    with mc.ImapClient(account) as client:
        message = client.get_message("INBOX", "5")
    assert message["subject"] == "hello"
    assert message["body_text"].strip() == "正文"


def test_imap_get_attachment_returns_bytes(monkeypatch):
    fake = FakeIMAP()
    fake.payloads = {
        "9": build_raw_message("with file", "a@b.com", "me@qq.com", "body", attachment=b"PDFDATA")
    }
    _patch_imap(monkeypatch, fake)
    account = mc.build_account("user@qq.com", "code")
    with mc.ImapClient(account) as client:
        message = client.get_message("INBOX", "9")
        part = message["attachments"][0]["part"]
        filename, content_type, payload = client.get_attachment("INBOX", "9", part)
    assert filename == "report.pdf"
    assert payload == b"PDFDATA"
    assert content_type == "application/pdf"


def test_imap_missing_attachment_raises(monkeypatch):
    fake = FakeIMAP()
    fake.payloads = {"9": build_raw_message("no file", "a@b.com", "me@qq.com", "body")}
    _patch_imap(monkeypatch, fake)
    account = mc.build_account("user@qq.com", "code")
    with mc.ImapClient(account) as client:
        with pytest.raises(mc.MailNotFound):
            client.get_attachment("INBOX", "9", "99")


def test_imap_delete_marks_deleted_and_expunges(monkeypatch):
    fake = FakeIMAP()
    _patch_imap(monkeypatch, fake)
    account = mc.build_account("user@qq.com", "code")
    with mc.ImapClient(account) as client:
        client.delete_message("INBOX", "5")
    assert any(call[0] == "+FLAGS" for call in fake.stored_commands)
    assert fake.expunged == 1


def test_imap_unknown_folder_raises_not_found(monkeypatch):
    fake = FakeIMAP()
    _patch_imap(monkeypatch, fake)
    account = mc.build_account("user@qq.com", "code")
    with mc.ImapClient(account) as client:
        with pytest.raises(mc.MailNotFound):
            client.list_messages("Nope", limit=5)


def test_imap_connect_failure_is_connection_error(monkeypatch):
    def boom(*args, **kwargs):
        raise OSError("no route to host")

    monkeypatch.setattr(mc.imaplib, "IMAP4_SSL", boom)
    account = mc.build_account("user@gmail.com", "pw")
    with pytest.raises(mc.MailConnectionError):
        with mc.ImapClient(account):
            pass


def test_imap_timeout_is_forwarded(monkeypatch):
    captured = {}

    def factory(host, port, timeout=None):
        captured["timeout"] = timeout
        return FakeIMAP()

    monkeypatch.setattr(mc.imaplib, "IMAP4_SSL", factory)
    account = mc.build_account("user@gmail.com", "pw")
    with mc.ImapClient(account, timeout=17):
        pass
    assert captured["timeout"] == 17


# --------------------------------------------------------------------------
# SMTP 发件（假连接）
# --------------------------------------------------------------------------


def test_smtp_ssl_login_and_send(monkeypatch):
    FakeSMTP.instances.clear()
    monkeypatch.setattr(mc.smtplib, "SMTP_SSL", FakeSMTP)
    account = mc.build_account("user@qq.com", "code")
    result = mc.send_message(account, ["a@b.com"], "主题", "正文")
    instance = FakeSMTP.instances[-1]
    assert instance.logged_in == ("user@qq.com", "code")
    assert instance.quit_called is True
    assert result["accepted"] is True
    assert result["recipients"] == ["a@b.com"]


def test_smtp_starttls_used_for_outlook(monkeypatch):
    FakeSMTP.instances.clear()
    monkeypatch.setattr(mc.smtplib, "SMTP", FakeSMTP)
    account = mc.build_account("user@outlook.com", "app-pw")
    mc.send_message(account, ["a@b.com"], "s", "b")
    assert FakeSMTP.instances[-1].tls is True
    assert FakeSMTP.instances[-1].port == 587


def test_smtp_auth_failure_is_auth_error(monkeypatch):
    FakeSMTP.instances.clear()

    class AuthFailSMTP(FakeSMTP):
        def __init__(self, *args, **kwargs):
            super().__init__(*args, **kwargs)
            self.fail_auth = True

    monkeypatch.setattr(mc.smtplib, "SMTP_SSL", AuthFailSMTP)
    account = mc.build_account("user@qq.com", "bad")
    with pytest.raises(mc.MailAuthError):
        mc.send_message(account, ["a@b.com"], "s", "b")


def test_smtp_requires_recipient(monkeypatch):
    monkeypatch.setattr(mc.smtplib, "SMTP_SSL", FakeSMTP)
    account = mc.build_account("user@qq.com", "code")
    with pytest.raises(mc.MailRequestError):
        mc.send_message(account, [], "s", "b")


def test_smtp_invalid_recipient_rejected(monkeypatch):
    monkeypatch.setattr(mc.smtplib, "SMTP_SSL", FakeSMTP)
    account = mc.build_account("user@qq.com", "code")
    with pytest.raises(mc.MailRequestError):
        mc.send_message(account, ["not-an-address"], "s", "b")


def test_smtp_multipart_alternative_and_attachment(monkeypatch):
    FakeSMTP.instances.clear()
    monkeypatch.setattr(mc.smtplib, "SMTP_SSL", FakeSMTP)
    account = mc.build_account("user@qq.com", "code")
    mc.send_message(
        account,
        ["a@b.com"],
        "主题",
        "纯文本正文",
        html="<p>HTML 正文</p>",
        attachments=[("f.txt", "text/plain", b"data")],
    )
    sent = FakeSMTP.instances[-1].sent[-1]
    content_types = {part.get_content_type() for part in sent.walk()}
    assert "text/plain" in content_types
    assert "text/html" in content_types
    assert any(part.get_filename() == "f.txt" for part in sent.walk())

# --------------------------------------------------------------------------
# 出站代理接线
# --------------------------------------------------------------------------


def test_build_account_accepts_and_normalizes_proxy():
    account = mc.build_account("user@gmail.com", "pw", proxy="socks5://p.local:1080")
    assert account.proxy == "socks5://p.local:1080"
    # 空值保持直连
    assert mc.build_account("user@gmail.com", "pw").proxy == ""
    assert mc.build_account("user@gmail.com", "pw", proxy="").proxy == ""


def test_build_account_rejects_invalid_proxy():
    with pytest.raises(mc.MailRequestError):
        mc.build_account("user@gmail.com", "pw", proxy="ftp://p.local:21")


def test_imap_uses_proxy_factory_when_configured(monkeypatch):
    """配置代理时，IMAP 必须走代理工厂而不是直连。"""
    calls = {}

    def fake_factory(account, timeout):
        calls["account"] = account
        calls["timeout"] = timeout
        return FakeIMAP()

    monkeypatch.setattr(mc, "_proxied_imap", fake_factory)
    account = mc.build_account("user@gmail.com", "pw", proxy="http://p.local:3128")
    with mc.ImapClient(account, timeout=9):
        pass
    assert calls["account"] is account
    assert calls["timeout"] == 9


def test_imap_direct_when_no_proxy(monkeypatch):
    used = {"proxy": False}

    def fake_factory(account, timeout):
        used["proxy"] = True
        return FakeIMAP()

    monkeypatch.setattr(mc, "_proxied_imap", fake_factory)
    fake = FakeIMAP()
    monkeypatch.setattr(mc.imaplib, "IMAP4_SSL", lambda *a, **k: fake)
    account = mc.build_account("user@gmail.com", "pw")
    with mc.ImapClient(account):
        pass
    assert used["proxy"] is False


def test_proxied_smtp_uses_ssl_wrapper_for_implicit_tls(monkeypatch):
    """隐式 TLS（465）经代理时必须由 stdlib 在隧道之上再 wrap。"""
    seen = {}

    class DummySock:
        def close(self): seen["closed"] = True

    class IdentityContext:
        def wrap_socket(self, sock, server_hostname=None):
            seen["server_hostname"] = server_hostname
            return sock

    def fake_connect(url, host, port, timeout):
        seen["proxy_url"] = url
        seen["target"] = (host, port)
        return DummySock()

    monkeypatch.setattr(mc, "proxy_tcp_connect", fake_connect)
    cls = mc._proxied_smtp_class("socks5://p.local:1080", starttls=False)
    obj = cls.__new__(cls)          # 不触发真实连接
    obj.context = IdentityContext()
    obj._host = "smtp.gmail.com"
    obj._get_socket("smtp.gmail.com", 465, 5)
    assert seen["proxy_url"] == "socks5://p.local:1080"
    assert seen["target"] == ("smtp.gmail.com", 465)
    assert seen["server_hostname"] == "smtp.gmail.com"


def test_proxied_smtp_starttls_uses_plain_tunnel(monkeypatch):
    seen = {}

    class DummySock:
        def close(self): pass

    def fake_connect(url, host, port, timeout):
        seen["target"] = (host, port)
        return DummySock()

    monkeypatch.setattr(mc, "proxy_tcp_connect", fake_connect)
    cls = mc._proxied_smtp_class("http://p.local:3128", starttls=True)
    obj = cls.__new__(cls)
    obj._get_socket("smtp.office365.com", 587, 5)
    assert seen["target"] == ("smtp.office365.com", 587)


def test_invalid_proxy_is_request_error():
    from app.mail_client import MailRequestError
    with pytest.raises(MailRequestError):
        mc.build_account("test@gmail.com", "secret", proxy="http://proxy:not-a-port")
