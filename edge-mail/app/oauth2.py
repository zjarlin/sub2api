"""OAuth2 授权：用 refresh_token 换 access_token，并构造 XOAUTH2 SASL 载荷。

只依赖 Python 标准库（urllib / base64），不引入外部依赖，便于离线单测。
edge-mail 不持久化任何凭据：refresh_token / access_token 只存在于单次请求。
"""
from __future__ import annotations

import base64
import json
import urllib.error
import urllib.parse
import urllib.request

from app.providers import MailProvider


class OAuth2Error(Exception):
    """OAuth2 令牌交换或 SASL 构造错误。"""

    pass


def _build_xoauth2(user: str, access_token: str) -> bytes:
    """构造 IMAP/SMTP XOAUTH2 SASL 初始响应。

    规范格式：``user=<address>\\x01auth=Bearer <token>\\x01\\x01``。
    """
    payload = f"user={user}\x01auth=Bearer {access_token}\x01\x01"
    return payload.encode("utf-8")


def build_xoauth2(user: str, access_token: str) -> bytes:
    """对外暴露的 XOAUTH2 载荷构造；纯函数便于单测。"""
    return _build_xoauth2(user, access_token)


def exchange_refresh_token(
    provider: MailProvider,
    refresh_token: str,
    timeout: int = 30,
) -> dict[str, object]:
    """用 refresh_token 换取 access_token。

    使用服务商预设里的 OAuth2 客户端信息。若预设未配置会抛 OAuth2Error。
    返回微软令牌端点响应的 JSON（含 access_token / expire）。
    """
    if provider.auth != "oauth2":
        raise OAuth2Error(f"provider '{provider.key}' does not use oauth2")
    if not provider.oauth2_client_id or not provider.oauth2_token_endpoint:
        raise OAuth2Error(f"provider '{provider.key}' has no oauth2 client configured")

    body = urllib.parse.urlencode({
        "grant_type": "refresh_token",
        "client_id": provider.oauth2_client_id,
        "refresh_token": refresh_token,
        "scope": provider.oauth2_token_scope,
    }).encode("utf-8")
    request = urllib.request.Request(
        provider.oauth2_token_endpoint,
        data=body,
        headers={"Content-Type": "application/x-www-form-urlencoded"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return json.load(response)
    except urllib.error.HTTPError as exc:
        payload = exc.read().decode(errors="replace")
        raise OAuth2Error(
            f"oauth2 token exchange failed: {exc.code} {exc.reason}: {payload[:200]}"
        ) from exc
    except (urllib.error.URLError, OSError) as exc:
        raise OAuth2Error(f"oauth2 token endpoint unreachable: {exc}") from exc


def get_access_token(provider: MailProvider, refresh_token: str, timeout: int = 30) -> str:
    """换取 access_token；结果里没有 access_token 视为失败。"""
    result = exchange_refresh_token(provider, refresh_token, timeout=timeout)
    token = result.get("access_token")
    if not isinstance(token, str) or not token:
        raise OAuth2Error("oauth2 token response missing access_token")
    return token


# 便于测试使用的 XOAUTH2 初始响应可读摘要。
def xoauth2_b64(user: str, access_token: str) -> str:
    """返回 base64 编码的 XOAUTH2 初始响应（用于 SMTP AUTH）。"""
    return base64.b64encode(_build_xoauth2(user, access_token)).decode("ascii")


__all__ = [
    "OAuth2Error",
    "build_xoauth2",
    "exchange_refresh_token",
    "get_access_token",
    "xoauth2_b64",
]
