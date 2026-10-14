"""edge-mail 运行配置。

所有连接参数都来自调用方请求里的账号信息（邮箱地址 + 凭据 + 服务商），
服务本身不保存任何账号；这里只配置运行期的安全与限额边界，避免服务在
未设防的情况下被当作开放代理使用。
"""
from __future__ import annotations

import os
from dataclasses import dataclass, field


def _bool(name: str, default: bool = False) -> bool:
    value = os.environ.get(name)
    if value is None:
        return default
    return value.strip().lower() in {"1", "true", "yes", "on"}


def _int(name: str, default: int) -> int:
    value = os.environ.get(name)
    if value is None or not value.strip():
        return default
    try:
        return int(value)
    except ValueError:
        return default


def _csv(name: str) -> tuple[str, ...]:
    raw = os.environ.get(name, "").strip()
    if not raw:
        return ()
    return tuple(item.strip().lower() for item in raw.split(",") if item.strip())


@dataclass(frozen=True)
class MailConfig:
    # 是否允许发送邮件。生产上若无发信需求可关闭，只保留收件能力。
    send_enabled: bool
    # 单次拉取的邮件数量上限，防止一次性拉空整个邮箱。
    max_fetch: int
    # 单个附件下载大小上限（字节）。
    max_attachment_bytes: int
    # 允许接入的服务商白名单；为空表示允许 providers 中所有预设。
    allowed_providers: tuple[str, ...]
    # 建连与单次操作超时（秒）。
    connect_timeout_seconds: int
    operation_timeout_seconds: int
    # 是否允许向任意地址发信。默认允许；置 false 时只允许发给自己（同邮箱）。
    allow_external_recipients: bool
    # 发件人显示名，缺省时回落到邮箱地址。
    default_from_name: str
    capabilities: tuple[str, ...] = field(default_factory=tuple)


def load_config() -> MailConfig:
    capabilities = ["receive", "folders"]
    send_enabled = _bool("MAIL_SEND_ENABLED", True)
    if send_enabled:
        capabilities.append("send")
    return MailConfig(
        send_enabled=send_enabled,
        max_fetch=max(1, min(_int("MAIL_MAX_FETCH", 50), 500)),
        max_attachment_bytes=_int("MAIL_MAX_ATTACHMENT_BYTES", 25 * 1024 * 1024),
        allowed_providers=_csv("MAIL_ALLOWED_PROVIDERS"),
        connect_timeout_seconds=_int("MAIL_CONNECT_TIMEOUT_SECONDS", 20),
        operation_timeout_seconds=_int("MAIL_OPERATION_TIMEOUT_SECONDS", 60),
        allow_external_recipients=_bool("MAIL_ALLOW_EXTERNAL_RECIPIENTS", True),
        default_from_name=os.environ.get("MAIL_DEFAULT_FROM_NAME", "").strip(),
        capabilities=tuple(capabilities),
    )
