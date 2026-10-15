"""IMAP/SMTP 收发件客户端。

只依赖 Python 标准库（imaplib / smtplib / email），因此可以完全离线单测，
也不给镜像引入额外依赖。设计原则：

- 服务本身不持久化任何账号和邮件；每次调用都由请求体携带邮箱地址与凭据。
- 所有连接都显式设置超时，避免 IMAP 半开连接把 worker 卡死。
- 只读操作统一用 ``readonly=True`` 选择邮箱，避免意外改动 Seen 状态。
"""
from __future__ import annotations

import email
import imaplib
import smtplib
import ssl
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from email.header import decode_header, make_header
from email.message import EmailMessage, Message
from email.utils import formataddr, getaddresses, parsedate_to_datetime

from app import imap_utf7
from app import oauth2 as oauth
from app.providers import MailProvider, resolve_provider
from app.proxy import ProxyError, normalize_proxy, proxy_tcp_connect

# 在导入期绑定异常类。若写成 `except imaplib.IMAP4.error`，一旦 imaplib.IMAP4
# 被替换（测试替身或未来重构），属性查找会在异常处理路径上二次失败，掩盖真实错误。
_IMAP_ERROR = imaplib.IMAP4.error
_SMTP_AUTH_ERROR = smtplib.SMTPAuthenticationError


class MailError(Exception):
    """邮箱服务错误基类，``kind`` 供上层映射 HTTP 状态码。"""

    kind = "mail_error"
    status = 502

    def __init__(self, message: str):
        super().__init__(message)
        self.message = message


class MailAuthError(MailError):
    kind = "auth_failed"
    status = 401


class MailConnectionError(MailError):
    kind = "connection_failed"
    status = 502


class MailNotFound(MailError):
    kind = "not_found"
    status = 404


class MailRequestError(MailError):
    kind = "invalid_request_error"
    status = 400


@dataclass(frozen=True)
class MailAccount:
    """一次调用使用的账号连接信息；不落盘、不缓存。"""

    address: str
    credential: str
    provider: MailProvider
    imap_host: str
    imap_port: int
    imap_ssl: bool
    smtp_host: str
    smtp_port: int
    smtp_starttls: bool
    from_name: str = ""
    # 出站代理（http(s):// 或 socks5://）；空串表示直连。
    proxy: str = ""

    def display_from(self) -> str:
        return formataddr((self.from_name or self.address, self.address))


def build_account(
    email_address: str,
    credential: str,
    provider: str | None = None,
    from_name: str = "",
    imap_host: str = "",
    imap_port: int | None = None,
    smtp_host: str = "",
    smtp_port: int | None = None,
    proxy: str = "",
) -> MailAccount:
    """按服务商预设构造账号；允许显式覆盖主机与端口以接入自建邮箱。"""
    address = (email_address or "").strip()
    if not address or "@" not in address:
        raise MailRequestError("email address is required")
    if not credential:
        raise MailRequestError("credential is required")

    try:
        normalized_proxy = normalize_proxy(proxy)
    except (ProxyError, ValueError) as exc:
        raise MailRequestError("proxy must be a valid HTTP CONNECT or SOCKS5 URL with host and port") from exc

    preset = resolve_provider(provider, address)
    host = imap_host.strip() or preset.imap_host
    port = imap_port or preset.imap_port
    out_host = smtp_host.strip() or preset.smtp_host
    out_port = smtp_port or preset.smtp_port
    return MailAccount(
        address=address,
        credential=credential,
        provider=preset,
        imap_host=host,
        imap_port=int(port),
        imap_ssl=preset.imap_ssl,
        smtp_host=out_host,
        smtp_port=int(out_port),
        smtp_starttls=preset.smtp_starttls,
        from_name=from_name.strip(),
        proxy=normalized_proxy,
    )


# --------------------------------------------------------------------------
# IMAP 收件
# --------------------------------------------------------------------------


def _decode(value: str | None) -> str:
    if not value:
        return ""
    try:
        return str(make_header(decode_header(value)))
    except (ValueError, UnicodeDecodeError):
        return value


def _to_local_iso(raw: str | None) -> str:
    if not raw:
        return ""
    try:
        parsed = parsedate_to_datetime(raw)
    except (TypeError, ValueError):
        return ""
    if parsed is None:
        return ""
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return parsed.astimezone(timezone.utc).isoformat()


def _quote_mailbox(name: str) -> str:
    encoded = imap_utf7.encode(name)
    return '"' + encoded.replace("\\", "\\\\").replace('"', '\\"') + '"'


def _use_oauth2(account: MailAccount) -> bool:
    """该服务商是否走 OAuth2/XOAUTH2 认证。"""
    return account.provider.auth == "oauth2"


def _imap_authenticate(conn, account: MailAccount, timeout: int) -> None:
    """对 IMAP 连接做登录：OAuth2 走 XOAUTH2，否则走密码 LOGIN。"""
    if _use_oauth2(account):
        access_token = oauth.get_access_token(account.provider, account.credential, timeout=timeout)
        sasl = oauth.build_xoauth2(account.address, access_token)
        conn.authenticate("XOAUTH2", lambda _: sasl)
    else:
        conn.login(account.address, account.credential)


def _proxied_imap(account: MailAccount, timeout: int):
    """经代理建立 IMAP 连接；SSL 由 stdlib 在隧道之上完成。"""
    base = imaplib.IMAP4_SSL if account.imap_ssl else imaplib.IMAP4
    proxy_url = account.proxy

    class Proxied(base):  # type: ignore[misc, valid-type]
        def _create_socket(self, timeout=None):
            plain = proxy_tcp_connect(proxy_url, self.host, self.port, timeout or 30)
            if account.imap_ssl:
                return self.ssl_context.wrap_socket(plain, server_hostname=self.host)
            return plain

    options = {"ssl_context": ssl.create_default_context()} if account.imap_ssl else {}
    return Proxied(account.imap_host, account.imap_port, timeout=timeout, **options)


def _proxied_smtp_class(proxy_url: str, starttls: bool):
    """构造经代理建连的 SMTP 类；SSL 由 stdlib 在隧道之上完成。

    单独抽出类工厂，便于在不真正建连的情况下验证代理接线。
    """
    if starttls:
        class ProxiedStartTLS(smtplib.SMTP):  # type: ignore[misc, valid-type]
            def _get_socket(self, host, port, timeout):
                return proxy_tcp_connect(proxy_url, host, port, timeout or 30)

        return ProxiedStartTLS

    class ProxiedSSL(smtplib.SMTP_SSL):  # type: ignore[misc, valid-type]
        def _get_socket(self, host, port, timeout):
            plain = proxy_tcp_connect(proxy_url, host, port, timeout or 30)
            return self.context.wrap_socket(plain, server_hostname=self._host or host)

    return ProxiedSSL


def _proxied_smtp(account: MailAccount, timeout: int):
    cls = _proxied_smtp_class(account.proxy, account.smtp_starttls)
    options = {} if account.smtp_starttls else {"context": ssl.create_default_context()}
    return cls(account.smtp_host, account.smtp_port, timeout=timeout, **options)


class ImapClient:
    """IMAP 收件操作的薄封装，作为上下文管理器使用。"""

    def __init__(self, account: MailAccount, timeout: int = 60, readonly: bool = True):
        self.account = account
        self.timeout = timeout
        self.readonly = readonly
        self._conn: imaplib.IMAP4 | None = None

    # -- 连接生命周期 ----------------------------------------------------
    def __enter__(self) -> "ImapClient":
        account = self.account
        try:
            if account.proxy:
                self._conn = _proxied_imap(account, self.timeout)
            elif account.imap_ssl:
                self._conn = imaplib.IMAP4_SSL(
                    account.imap_host, account.imap_port, timeout=self.timeout
                )
            else:
                self._conn = imaplib.IMAP4(
                    account.imap_host, account.imap_port, timeout=self.timeout
                )
        except ProxyError as exc:
            raise MailConnectionError(f"proxy error for {account.imap_host}: {exc}") from exc
        except (OSError, ssl.SSLError) as exc:
            raise MailConnectionError(f"cannot reach {account.imap_host}: {exc}") from exc

        try:
            _imap_authenticate(self._conn, account, self.timeout)
        except _IMAP_ERROR as exc:
            raise _auth_error("IMAP", account, exc) from exc
        except oauth.OAuth2Error as exc:
            raise MailAuthError(f"IMAP OAuth2 failed for {account.address}: {exc}") from exc
        except (OSError, ssl.SSLError) as exc:
            raise MailConnectionError(f"IMAP login failed on the network layer: {exc}") from exc

        # 网易系（163/126/yeah）与部分服务商要求客户端先声明 IMAP ID，
        # 否则 SELECT/SEARCH 会被拒绝（返回 "Unsafe Login"）。
        self._send_id()
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        if self._conn is None:
            return
        try:
            if self._selected is not None:
                self._conn.close()
        except _IMAP_ERROR:
            pass
        try:
            self._conn.logout()
        except (_IMAP_ERROR, OSError):
            pass
        finally:
            self._conn = None

    _selected: str | None = None

    def _send_id(self) -> None:
        assert self._conn is not None
        try:
            self._conn.xatom(
                "ID",
                '("name" "edge-mail" "version" "1.0.0" "vendor" "sub2api")',
            )
        except (_IMAP_ERROR, AttributeError, OSError):
            # 不是所有服务商都支持 ID；失败不阻断后续操作。
            pass

    def _select(self, folder: str) -> None:
        assert self._conn is not None
        mailbox = folder or "INBOX"
        status, data = self._conn.select(_quote_mailbox(mailbox), readonly=self.readonly)
        if status != "OK":
            raise MailNotFound(f"mailbox not found: {mailbox}")
        self._selected = mailbox

    # -- 操作 ------------------------------------------------------------
    def list_folders(self) -> list[dict[str, object]]:
        assert self._conn is not None
        status, data = self._conn.list()
        if status != "OK":
            raise MailConnectionError("LIST command failed")
        folders: list[dict[str, object]] = []
        for raw in data or []:
            if not isinstance(raw, bytes):
                continue
            decoded = raw.decode("ascii", errors="replace")
            parsed = _parse_list_line(decoded)
            if parsed is not None:
                folders.append(parsed)
        return folders

    def list_messages(
        self,
        folder: str,
        limit: int,
        unread_only: bool = False,
        since: str = "",
        before: str = "",
        subject: str = "",
        sender: str = "",
        recipient: str = "",
        text: str = "",
        newer_than_days: int = 0,
        include_body_preview: bool = False,
    ) -> list[dict[str, object]]:
        assert self._conn is not None
        self._select(folder)
        criteria = _search_criteria(
            unread_only=unread_only,
            since=since,
            before=before,
            subject=subject,
            sender=sender,
            recipient=recipient,
            text=text,
            newer_than_days=newer_than_days,
        )
        uids = self._search(criteria)
        if not uids:
            return []
        # UID 升序，取最后（最新）的 limit 封，再反转为新到旧。
        selected = uids[-limit:]
        selected.reverse()
        return self._fetch_summaries(selected, include_body_preview)

    def get_message(self, folder: str, uid: str, include_body: bool = True) -> dict[str, object]:
        assert self._conn is not None
        self._select(folder)
        raw = self._fetch_raw(uid)
        message = email.message_from_bytes(raw)
        return _parse_message(message, uid, include_body=include_body, max_bytes=None)

    def get_attachment(self, folder: str, uid: str, part: str) -> tuple[str, str, bytes]:
        assert self._conn is not None
        self._select(folder)
        raw = self._fetch_raw(uid)
        message = email.message_from_bytes(raw)
        for part_id, mime_part in _walk_parts(message):
            if part_id == part:
                filename = _attachment_filename(mime_part, part_id)
                content_type = mime_part.get_content_type()
                payload = mime_part.get_payload(decode=True) or b""
                return filename, content_type, payload
        raise MailNotFound(f"attachment part not found: {part}")

    def set_seen(self, folder: str, uid: str, seen: bool) -> None:
        assert self._conn is not None
        mailbox = folder or "INBOX"
        status, _ = self._conn.select(_quote_mailbox(mailbox), readonly=False)
        if status != "OK":
            raise MailNotFound(f"mailbox not found: {mailbox}")
        self._selected = mailbox
        flags = "(\\Seen)" if seen else "(\\Seen)"
        command = "+FLAGS" if seen else "-FLAGS"
        result, _ = self._conn.uid("STORE", uid, command, flags)
        if result != "OK":
            raise MailError("failed to update flags")

    def move_message(self, folder: str, uid: str, target: str) -> None:
        assert self._conn is not None
        self._select(folder)
        result, _ = self._conn.uid("COPY", uid, _quote_mailbox(target))
        if result != "OK":
            raise MailError("failed to copy message")
        self._conn.uid("STORE", uid, "+FLAGS", "(\\Deleted)")
        self._conn.expunge()

    def delete_message(self, folder: str, uid: str) -> None:
        assert self._conn is not None
        mailbox = folder or "INBOX"
        status, _ = self._conn.select(_quote_mailbox(mailbox), readonly=False)
        if status != "OK":
            raise MailNotFound(f"mailbox not found: {mailbox}")
        self._selected = mailbox
        result, _ = self._conn.uid("STORE", uid, "+FLAGS", "(\\Deleted)")
        if result != "OK":
            raise MailError("failed to mark message deleted")
        self._conn.expunge()

    # -- 低层辅助 --------------------------------------------------------
    def _search(self, criteria: list[str]) -> list[str]:
        assert self._conn is not None
        needs_charset = any(_is_ascii(item) is False for item in criteria)
        if needs_charset:
            status, data = self._conn.uid("SEARCH", "CHARSET", "UTF-8", *criteria)
        else:
            status, data = self._conn.uid("SEARCH", *criteria)
        if status != "OK":
            raise MailError("SEARCH command failed")
        payload = b" ".join(item for item in (data or []) if isinstance(item, bytes))
        return [token.decode("ascii") for token in payload.split() if token]

    def _fetch_raw(self, uid: str) -> bytes:
        assert self._conn is not None
        status, data = self._conn.uid("FETCH", uid, "(BODY.PEEK[])")
        if status != "OK":
            raise MailNotFound(f"message not found: {uid}")
        for item in data or []:
            if isinstance(item, tuple) and len(item) >= 2 and isinstance(item[1], bytes):
                return item[1]
        raise MailNotFound(f"message not found: {uid}")

    def _fetch_summaries(self, uids: list[str], include_preview: bool) -> list[dict[str, object]]:
        if include_preview:
            return [self.get_message(self._selected or "INBOX", uid) for uid in uids]
        assert self._conn is not None
        summaries: list[dict[str, object]] = []
        for uid in uids:
            status, data = self._conn.uid(
                "FETCH",
                uid,
                "(FLAGS BODY.PEEK[HEADER.FIELDS (FROM TO SUBJECT DATE MESSAGE-ID)])",
            )
            if status != "OK":
                continue
            flags: list[str] = []
            header_bytes = b""
            for item in data or []:
                if isinstance(item, tuple) and len(item) >= 2:
                    prefix = item[0].decode("ascii", errors="replace") if isinstance(item[0], bytes) else str(item[0])
                    if "FLAGS" in prefix:
                        flags = _parse_flags(prefix)
                    if isinstance(item[1], bytes):
                        header_bytes = item[1]
            message = email.message_from_bytes(header_bytes)
            summaries.append(_summarize(message, uid, flags))
        return summaries


# --------------------------------------------------------------------------
# SMTP 发件
# --------------------------------------------------------------------------


def send_message(
    account: MailAccount,
    to: list[str],
    subject: str,
    body: str,
    html: str = "",
    cc: list[str] | None = None,
    bcc: list[str] | None = None,
    reply_to: str = "",
    attachments: list[tuple[str, str, bytes]] | None = None,
    timeout: int = 60,
) -> dict[str, object]:
    """通过 SMTP 发信。``attachments`` 为 (文件名, MIME 类型, 内容) 列表。"""
    recipients = _clean_addresses(to) + _clean_addresses(cc or []) + _clean_addresses(bcc or [])
    if not recipients:
        raise MailRequestError("at least one recipient is required")

    message = EmailMessage()
    message["From"] = account.display_from()
    message["To"] = ", ".join(_clean_addresses(to))
    if cc:
        message["Cc"] = ", ".join(_clean_addresses(cc))
    if reply_to:
        message["Reply-To"] = reply_to
    message["Subject"] = subject or ""
    if html:
        message.set_content(body or "")
        message.add_alternative(html, subtype="html")
    else:
        message.set_content(body or "")

    for filename, content_type, content in attachments or []:
        maintype, _, subtype = (content_type or "application/octet-stream").partition("/")
        message.add_attachment(
            content, maintype=maintype or "application", subtype=subtype or "octet-stream", filename=filename
        )

    try:
        if account.proxy:
            client: smtplib.SMTP = _proxied_smtp(account, timeout)
            try:
                if account.smtp_starttls:
                    client.ehlo()
                    client.starttls(context=ssl.create_default_context())
                    client.ehlo()
                _smtp_authenticate(client, account, timeout)
                client.send_message(message)
            finally:
                _close_smtp(client)
        elif account.smtp_starttls:
            client = smtplib.SMTP(account.smtp_host, account.smtp_port, timeout=timeout)
            try:
                client.ehlo()
                client.starttls(context=ssl.create_default_context())
                client.ehlo()
                _smtp_authenticate(client, account, timeout)
                client.send_message(message)
            finally:
                _close_smtp(client)
        else:
            client = smtplib.SMTP_SSL(
                account.smtp_host, account.smtp_port, timeout=timeout, context=ssl.create_default_context()
            )
            try:
                _smtp_authenticate(client, account, timeout)
                client.send_message(message)
            finally:
                _close_smtp(client)
    except oauth.OAuth2Error as exc:
        raise MailAuthError(f"SMTP OAuth2 failed for {account.address}: {exc}") from exc
    except _SMTP_AUTH_ERROR as exc:
        raise MailAuthError(
            f"SMTP authentication failed for {account.address}: "
            f"{exc.smtp_code} {exc.smtp_error.decode(errors='replace') if isinstance(exc.smtp_error, bytes) else exc.smtp_error}"
        ) from exc
    except (smtplib.SMTPException, OSError) as exc:
        raise MailConnectionError(f"SMTP send failed: {exc}") from exc

    return {
        "accepted": True,
        "from": account.address,
        "recipients": recipients,
        "subject": subject or "",
    }


def _smtp_authenticate(client: smtplib.SMTP, account: MailAccount, timeout: int) -> None:
    """对 SMTP 连接做登录：OAuth2 走 XOAUTH2，否则走密码 LOGIN。"""
    if _use_oauth2(account):
        access_token = oauth.get_access_token(account.provider, account.credential, timeout=timeout)
        sasl = oauth.xoauth2_b64(account.address, access_token)
        code, resp = client.docmd("AUTH", "XOAUTH2 " + sasl)
        if code != 235:
            raise _SMTP_AUTH_ERROR(code, resp)
    else:
        client.login(account.address, account.credential)


def _close_smtp(client: smtplib.SMTP) -> None:
    try:
        client.quit()
    except (smtplib.SMTPException, OSError):
        try:
            client.close()
        except OSError:
            pass


# --------------------------------------------------------------------------
# 纯函数解析辅助（便于离线单测）
# --------------------------------------------------------------------------


def _auth_error(protocol: str, account: MailAccount, exc: Exception) -> MailAuthError:
    text = str(exc)
    hint = account.provider.credential_hint
    return MailAuthError(
        f"{protocol} login failed for {account.address} ({account.provider.label}): {text}. {hint}"
    )


def _is_ascii(value: str) -> bool:
    try:
        value.encode("ascii")
        return True
    except UnicodeEncodeError:
        return False


def _parse_list_line(line: str) -> dict[str, object] | None:
    """解析一行 LIST 响应。

    ``imaplib.list()`` 返回的是**去掉标签**的 untagged response，
    真实形态是 ``(\\HasNoChildren) "/" "INBOX"``，不带 ``* LIST`` 前缀；
    但原始协议行（以及部分测试替身）会带 ``* LIST``。两种都要能解析，
    否则生产上 ``list_folders`` 会静默返回空列表。
    """
    line = line.strip()
    if line.startswith("* LIST"):
        line = line[len("* LIST") :].strip()
    elif line.startswith("LIST"):
        line = line[len("LIST") :].strip()
    if not line.startswith("("):
        return None
    remainder = line
    depth = 0
    end = -1
    for index, char in enumerate(remainder):
        if char == "(":
            depth += 1
        elif char == ")":
            depth -= 1
            if depth == 0:
                end = index
                break
    if end == -1:
        return None
    attributes = remainder[1:end].split()
    tail = remainder[end + 1 :].strip()
    # 分隔符 + 邮箱名
    if not tail:
        return None
    if tail.startswith('"'):
        close = tail.find('"', 1)
        delimiter = tail[1:close] if close != -1 else "/"
        tail = tail[close + 1 :].strip() if close != -1 else ""
    else:
        pieces = tail.split(None, 1)
        delimiter = pieces[0] if pieces else "/"
        tail = pieces[1] if len(pieces) > 1 else ""
    tail = tail.strip()
    if tail.startswith('"') and tail.endswith('"') and len(tail) >= 2:
        tail = tail[1:-1]
    name = imap_utf7.decode(tail)
    return {
        "name": name,
        "delimiter": delimiter or "/",
        "attributes": [attr.lstrip("\\") for attr in attributes],
    }


def _search_criteria(
    unread_only: bool,
    since: str,
    before: str,
    subject: str,
    sender: str,
    recipient: str,
    text: str,
    newer_than_days: int,
) -> list[str]:
    criteria: list[str] = []
    if unread_only:
        criteria += ["UNSEEN"]
    if since:
        criteria += ["SINCE", _imap_date(since)]
    if before:
        criteria += ["BEFORE", _imap_date(before)]
    if newer_than_days > 0:
        criteria += ["SINCE", _imap_date((datetime.now(timezone.utc) - timedelta(days=newer_than_days)).date().isoformat())]
    if subject:
        criteria += ["SUBJECT", _quote_search(subject)]
    if sender:
        criteria += ["FROM", _quote_search(sender)]
    if recipient:
        criteria += ["TO", _quote_search(recipient)]
    if text:
        criteria += ["TEXT", _quote_search(text)]
    if not criteria:
        criteria += ["ALL"]
    return criteria


def _imap_date(value: str) -> str:
    text = (value or "").strip()
    for fmt in ("%Y-%m-%d", "%Y/%m/%d", "%Y%m%d"):
        try:
            return datetime.strptime(text, fmt).strftime("%d-%b-%Y")
        except ValueError:
            continue
    raise MailRequestError(f"invalid date, expected YYYY-MM-DD: {value}")


def _quote_search(value: str) -> str:
    escaped = value.replace("\\", "\\\\").replace('"', '\\"')
    return '"' + escaped + '"'


def _parse_flags(prefix: str) -> list[str]:
    start = prefix.find("FLAGS (")
    if start == -1:
        return []
    start += len("FLAGS (")
    end = prefix.find(")", start)
    if end == -1:
        return []
    return [flag.lstrip("\\") for flag in prefix[start:end].split()]


def _summarize(message: Message, uid: str, flags: list[str]) -> dict[str, object]:
    return {
        "uid": uid,
        "subject": _decode(message.get("Subject")),
        "from": _address_list(message.get("From")),
        "to": _address_list(message.get("To")),
        "cc": _address_list(message.get("Cc")),
        "date": _to_local_iso(message.get("Date")),
        "message_id": (message.get("Message-ID") or "").strip(),
        "seen": "Seen" in flags,
        "flagged": "Flagged" in flags,
        "answered": "Answered" in flags,
        "has_attachments": False,
        "preview": "",
    }


def _parse_message(
    message: Message, uid: str, include_body: bool, max_bytes: int | None
) -> dict[str, object]:
    flags: list[str] = []
    raw_flags = message.get("X-Flags")
    if raw_flags:
        flags = [raw_flags]
    summary = _summarize(message, uid, flags)
    text_body = ""
    html_body = ""
    attachments: list[dict[str, object]] = []
    for part_id, part in _walk_parts(message):
        content_type = part.get_content_type()
        disposition = (part.get_content_disposition() or "").lower()
        if disposition == "attachment" or (part.get_filename() and disposition != "inline"):
            payload = part.get_payload(decode=True) or b""
            attachments.append(
                {
                    "part": part_id,
                    "filename": _attachment_filename(part, part_id),
                    "content_type": content_type,
                    "size": len(payload),
                }
            )
            continue
        if not include_body:
            continue
        if content_type == "text/plain" and not text_body:
            text_body = _part_text(part)
        elif content_type == "text/html" and not html_body:
            html_body = _part_text(part)

    summary["has_attachments"] = bool(attachments)
    summary["preview"] = " ".join(text_body.split())[:200]
    if max_bytes is not None and len(text_body) > max_bytes:
        text_body = text_body[:max_bytes]
    summary["body_text"] = text_body
    summary["body_html"] = html_body
    summary["attachments"] = attachments
    return summary


def _walk_parts(message: Message) -> list[tuple[str, Message]]:
    """遍历 MIME 部件，返回稳定的点分层级 part id（如 ``1``、``1.2``）。"""
    walk = message.walk()
    root = next(walk, None)
    result: list[tuple[str, Message]] = []

    def recurse(current: Message, prefix: str) -> None:
        if current.is_multipart():
            for index, child in enumerate(current.get_payload(), start=1):
                recurse(child, f"{prefix}{index}" if not prefix else f"{prefix}.{index}")
        else:
            result.append((prefix or "1", current))

    if root is not None:
        if root.is_multipart():
            recurse(root, "")
        else:
            result.append(("1", root))
    return result


def _attachment_filename(part: Message, fallback: str) -> str:
    name = _decode(part.get_filename())
    if name:
        return name
    return f"part-{fallback}"


def _part_text(part: Message) -> str:
    payload = part.get_payload(decode=True)
    if payload is None:
        return ""
    charset = part.get_content_charset() or "utf-8"
    try:
        return payload.decode(charset, errors="replace")
    except LookupError:
        return payload.decode("utf-8", errors="replace")


def _address_list(value: str | None) -> list[dict[str, str]]:
    if not value:
        return []
    decoded = _decode(value)
    result: list[dict[str, str]] = []
    for name, address in getaddresses([decoded]):
        if not address:
            continue
        result.append({"name": _decode(name), "address": address})
    return result


def _clean_addresses(values: list[str]) -> list[str]:
    cleaned: list[str] = []
    for value in values:
        address = (value or "").strip()
        if not address:
            continue
        if "@" not in address:
            raise MailRequestError(f"invalid recipient address: {address}")
        cleaned.append(address)
    return cleaned
