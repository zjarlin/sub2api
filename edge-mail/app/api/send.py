"""发件端点服务层。"""
from __future__ import annotations

import base64
from typing import Any

from app.config import MailConfig
from app.api.mailbox import account_from_body
from app.mail_client import MailRequestError, send_message


def _as_list(value: Any) -> list[str]:
    if value is None:
        return []
    if isinstance(value, str):
        return [item.strip() for item in value.split(",") if item.strip()]
    if isinstance(value, list):
        return [str(item).strip() for item in value if str(item).strip()]
    raise MailRequestError("recipients must be a string or a list of strings")


def _attachments(body: dict[str, Any]) -> list[tuple[str, str, bytes]]:
    raw = body.get("attachments") or []
    if not isinstance(raw, list):
        raise MailRequestError("attachments must be a list")
    result: list[tuple[str, str, bytes]] = []
    for item in raw:
        if not isinstance(item, dict):
            raise MailRequestError("each attachment must be an object")
        filename = str(item.get("filename") or "").strip()
        if not filename:
            raise MailRequestError("attachment filename is required")
        content_type = str(item.get("content_type") or "application/octet-stream").strip()
        encoded = str(item.get("content_base64") or "").strip()
        if not encoded:
            raise MailRequestError(f"attachment '{filename}' is missing content_base64")
        try:
            payload = base64.b64decode(encoded, validate=True)
        except (ValueError, TypeError) as exc:
            raise MailRequestError(f"attachment '{filename}' content_base64 is invalid") from exc
        result.append((filename, content_type, payload))
    return result


def send(config: MailConfig, body: dict[str, Any]) -> dict[str, Any]:
    if not config.send_enabled:
        raise MailRequestError("sending is disabled on this gateway")
    account = account_from_body(body, config)

    to = _as_list(body.get("to"))
    cc = _as_list(body.get("cc"))
    bcc = _as_list(body.get("bcc"))
    if not to:
        raise MailRequestError("to is required")

    if not config.allow_external_recipients:
        allowed = {account.address.lower()}
        for address in to + cc + bcc:
            if address.lower() not in allowed:
                raise MailRequestError(
                    "external recipients are disabled; only sending to the account itself is allowed"
                )

    subject = str(body.get("subject") or "")
    text_body = str(body.get("body") or body.get("text") or "")
    html_body = str(body.get("html") or "")
    if not text_body and not html_body:
        raise MailRequestError("body is required")

    result = send_message(
        account=account,
        to=to,
        subject=subject,
        body=text_body,
        html=html_body,
        cc=cc,
        bcc=bcc,
        reply_to=str(body.get("reply_to") or ""),
        attachments=_attachments(body),
        timeout=config.operation_timeout_seconds,
    )
    result["provider"] = account.provider.key
    return result
