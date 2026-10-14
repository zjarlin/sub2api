"""内存版 IMAP/SMTP 假服务器，用于端到端验证协议交互而不联网。

只实现 edge-mail 实际使用到的那部分 IMAP 命令，够精确地驱动 UID SEARCH /
UID FETCH / LIST / STORE / COPY / EXPUNGE 与登录失败分支。

注意：真实 IMAP 服务器返回的邮箱名是 modified UTF-7，这里同样编码，
否则中文文件夹名测试会与生产行为不一致。
"""
from __future__ import annotations

import email
from dataclasses import dataclass, field

from app import imap_utf7

# 保存真实异常类型，避免测试中 monkeypatch 掉 imaplib.IMAP4 后
# `imaplib.IMAP4.error` 变成属性错误。
IMAP_ERROR = __import__("imaplib").IMAP4.error
SMTP_AUTH_ERROR = __import__("smtplib").SMTPAuthenticationError


def build_raw_message(
    subject: str, sender: str, to: str, body: str, attachment: bytes | None = None
) -> bytes:
    from email.message import EmailMessage

    message = EmailMessage()
    message["From"] = sender
    message["To"] = to
    message["Subject"] = subject
    message["Date"] = "Tue, 14 Oct 2026 10:00:00 +0800"
    message["Message-ID"] = f"<{abs(hash(subject))}@example.com>"
    message.set_content(body)
    if attachment is not None:
        message.add_attachment(
            attachment, maintype="application", subtype="pdf", filename="report.pdf"
        )
    return message.as_bytes()


@dataclass
class FakeIMAP:
    """记录调用并按脚本返回结果的假 IMAP 连接。"""

    login_ok: bool = True
    folders: list[str] = field(default_factory=lambda: ["INBOX", "已发送", "Trash"])
    search_result: list[str] = field(default_factory=lambda: ["1", "2", "3"])
    payloads: dict[str, bytes] = field(default_factory=dict)
    calls: list[tuple] = field(default_factory=list)
    selected: str = ""
    selected_readonly: bool = True
    stored_commands: list[tuple] = field(default_factory=list)
    expunged: int = 0

    # -- 协议方法 --------------------------------------------------------
    def login(self, user: str, password: str):
        self.calls.append(("login", user, password))
        if not self.login_ok:
            raise IMAP_ERROR("AUTHENTICATIONFAILED Invalid credentials")
        return ("OK", [b"LOGIN completed"])

    def xatom(self, name: str, *args):
        self.calls.append(("xatom", name, *args))
        return ("OK", [b"ID completed"])

    def list(self, *args):
        self.calls.append(("list", *args))
        rows = []
        for folder in self.folders:
            encoded = imap_utf7.encode(folder).encode("ascii")
            rows.append(b'(\\HasNoChildren) "/" "' + encoded + b'"')
        return ("OK", rows)

    def select(self, mailbox, readonly=False):
        self.calls.append(("select", mailbox, readonly))
        self.selected = mailbox.strip('"')
        self.selected_readonly = readonly
        if imap_utf7.decode(self.selected) not in self.folders:
            return ("NO", [b"Mailbox does not exist"])
        return ("OK", [b"3"])

    def uid(self, command, *args):
        self.calls.append(("uid", command, *args))
        upper = command.upper()
        if upper == "SEARCH":
            payload = " ".join(self.search_result).encode()
            return ("OK", [payload])
        if upper == "FETCH":
            uid = args[0]
            data = self.payloads.get(uid, b"")
            return ("OK", [(f"{uid} (BODY[] {{{len(data)}}}".encode(), data), b")"])
        if upper == "STORE":
            # args[1] 是 "+FLAGS"/"-FLAGS"
            self.stored_commands.append((args[1], args[2], args[0]))
            return ("OK", [b"done"])
        if upper == "COPY":
            self.stored_commands.append(("COPY", args[0], args[1]))
            return ("OK", [b"done"])
        return ("OK", [b"done"])

    def close(self):
        self.calls.append(("close",))
        return ("OK", [b"done"])

    def logout(self):
        self.calls.append(("logout",))
        return ("BYE", [b"done"])

    def expunge(self):
        self.expunged += 1
        return ("OK", [b"done"])


class FakeSMTP:
    """记录发信内容的假 SMTP 连接。"""

    instances: list["FakeSMTP"] = []

    def __init__(self, host, port, timeout=None, context=None):
        self.host = host
        self.port = port
        self.timeout = timeout
        self.tls = False
        self.logged_in = None
        self.sent: list = []
        self.quit_called = False
        self.fail_auth = False
        FakeSMTP.instances.append(self)

    def ehlo(self):
        return (250, b"ok")

    def starttls(self, context=None):
        self.tls = True
        return (220, b"ready")

    def login(self, user, password):
        if self.fail_auth:
            raise SMTP_AUTH_ERROR(535, b"auth failed")
        self.logged_in = (user, password)
        return (235, b"ok")

    def send_message(self, message):
        self.sent.append(message)
        return {}

    def quit(self):
        self.quit_called = True
        return (221, b"bye")

    def close(self):
        pass


__all__ = ["FakeIMAP", "FakeSMTP", "build_raw_message", "IMAP_ERROR", "SMTP_AUTH_ERROR"]
