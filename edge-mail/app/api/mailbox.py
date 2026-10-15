"""邮件收件操作的服务层：把请求体翻译成 ImapClient 调用。"""
from __future__ import annotations

from typing import Any

from app.config import MailConfig
from app.mail_client import (
    ImapClient,
    MailError,
    MailRequestError,
    build_account,
)
from app.providers import UnknownProvider
from app.providers import list_providers


def account_from_body(body: dict[str, Any], config: MailConfig):
    """从请求体构造账号；统一做服务商白名单与参数校验。"""
    account = build_account(
        email_address=str(body.get("email", "")),
        credential=str(body.get("credential", "")),
        provider=body.get("provider") or None,
        from_name=str(body.get("from_name", "")),
        imap_host=str(body.get("imap_host", "")),
        imap_port=body.get("imap_port") or None,
        smtp_host=str(body.get("smtp_host", "")),
        smtp_port=body.get("smtp_port") or None,
        proxy=str(body.get("proxy", "")),
    )
    if config.allowed_providers and account.provider.key not in config.allowed_providers:
        raise MailRequestError(
            f"provider '{account.provider.key}' is not enabled on this gateway"
        )
    return account


def list_folders(config: MailConfig, body: dict[str, Any]) -> dict[str, Any]:
    account = account_from_body(body, config)
    with ImapClient(account, timeout=config.operation_timeout_seconds) as client:
        folders = client.list_folders()
    return {"account": account.address, "provider": account.provider.key, "folders": folders}


def list_messages(config: MailConfig, body: dict[str, Any]) -> dict[str, Any]:
    account = account_from_body(body, config)
    limit = min(int(body.get("limit") or config.max_fetch), config.max_fetch)
    folder = str(body.get("folder") or "INBOX")
    with ImapClient(account, timeout=config.operation_timeout_seconds) as client:
        messages = client.list_messages(
            folder=folder,
            limit=max(1, limit),
            unread_only=bool(body.get("unread_only")),
            since=str(body.get("since") or ""),
            before=str(body.get("before") or ""),
            subject=str(body.get("subject") or ""),
            sender=str(body.get("from") or ""),
            recipient=str(body.get("to") or ""),
            text=str(body.get("text") or ""),
            newer_than_days=int(body.get("newer_than_days") or 0),
            include_body_preview=bool(body.get("include_body")),
        )
    return {
        "account": account.address,
        "provider": account.provider.key,
        "folder": folder,
        "count": len(messages),
        "messages": messages,
    }


def get_message(config: MailConfig, body: dict[str, Any]) -> dict[str, Any]:
    account = account_from_body(body, config)
    uid = str(body.get("uid") or "").strip()
    if not uid:
        raise MailRequestError("uid is required")
    folder = str(body.get("folder") or "INBOX")
    include_body = body.get("include_body", True)
    with ImapClient(account, timeout=config.operation_timeout_seconds) as client:
        message = client.get_message(folder=folder, uid=uid, include_body=bool(include_body))
    return {
        "account": account.address,
        "provider": account.provider.key,
        "folder": folder,
        "message": message,
    }


def get_attachment(config: MailConfig, body: dict[str, Any]) -> tuple[str, str, bytes]:
    account = account_from_body(body, config)
    uid = str(body.get("uid") or "").strip()
    part = str(body.get("part") or "").strip()
    if not uid or not part:
        raise MailRequestError("uid and part are required")
    folder = str(body.get("folder") or "INBOX")
    with ImapClient(account, timeout=config.operation_timeout_seconds) as client:
        filename, content_type, payload = client.get_attachment(folder=folder, uid=uid, part=part)
    if len(payload) > config.max_attachment_bytes:
        raise MailRequestError(
            f"attachment exceeds the {config.max_attachment_bytes} byte limit"
        )
    return filename, content_type, payload


def update_message(config: MailConfig, body: dict[str, Any]) -> dict[str, Any]:
    account = account_from_body(body, config)
    uid = str(body.get("uid") or "").strip()
    action = str(body.get("action") or "").strip().lower()
    if not uid:
        raise MailRequestError("uid is required")
    folder = str(body.get("folder") or "INBOX")
    with ImapClient(account, timeout=config.operation_timeout_seconds) as client:
        if action == "mark_read":
            client.set_seen(folder, uid, True)
        elif action == "mark_unread":
            client.set_seen(folder, uid, False)
        elif action == "move":
            target = str(body.get("target_folder") or "").strip()
            if not target:
                raise MailRequestError("target_folder is required for move")
            client.move_message(folder, uid, target)
        elif action == "delete":
            client.delete_message(folder, uid)
        else:
            raise MailRequestError(
                "action must be one of: mark_read, mark_unread, move, delete"
            )
    return {"account": account.address, "uid": uid, "action": action, "ok": True}


__all__ = [
    "account_from_body",
    "list_folders",
    "list_messages",
    "get_message",
    "get_attachment",
    "update_message",
    "list_providers",
    "MailError",
    "MailRequestError",
    "UnknownProvider",
]
