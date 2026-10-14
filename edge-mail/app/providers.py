"""邮箱服务商预设。

把 Outlook / Google / QQ 三家（以及可选的网易系）的 IMAP/SMTP 主机、端口和
鉴权方式固化成声明式预置，调用方只需给出 ``provider`` 与邮箱地址，不必记忆
各家主机名和端口差异。

鉴权说明（重要，决定用户怎么填凭据）：

- ``google``  : 必须使用「应用专用密码」（App Password）。Google 已对第三方
  客户端关闭明文密码，且 OAuth2 之外的登录一律要求两步验证 + 应用密码。
  主机 ``imap.gmail.com:993`` / ``smtp.gmail.com:465``。
- ``outlook`` : 同时接受应用密码和 OAuth2。Microsoft 已弃用基本身份验证的
  IMAP，个人账号/无 MFA 企业账号仍可用「应用密码」；企业租户可能要求
  OAuth2，此时服务会返回明确的 ``auth_failed`` 提示改用应用密码或 OAuth。
  主机 ``outlook.office365.com:993`` / ``smtp.office365.com:587``(STARTTLS)。
- ``qq``      : 必须使用「IMAP/SMTP 授权码」，不是 QQ 登录密码；需在
  「设置 → 账户 → POP3/IMAP/SMTP 服务」中开启并生成授权码。
  主机 ``imap.qq.com:993`` / ``smtp.qq.com:465``。

该模块不发起任何网络连接，只做纯数据解析，便于离线单测。
"""
from __future__ import annotations

from dataclasses import asdict, dataclass


@dataclass(frozen=True)
class MailProvider:
    """单个邮箱服务商的连接预设。"""

    key: str
    label: str
    # IMAP 收件
    imap_host: str
    imap_port: int
    imap_ssl: bool
    # SMTP 发件
    smtp_host: str
    smtp_port: int
    # starttls=True 表示先明文连接再 STARTTLS 升级（如 Outlook 587）；
    # False 表示直接用 SMTP_SSL（如 Gmail/QQ 的 465）。
    smtp_starttls: bool
    # 该服务商是否强制要求 OAuth2 才能登录（Google 为 False，因为可用应用密码）。
    auth: str
    # 给用户看的凭据填写提示。
    credential_hint: str
    # 常见域名后缀，用于按邮箱地址推断服务商。
    domains: tuple[str, ...]

    def as_dict(self) -> dict[str, object]:
        return asdict(self)


PROVIDERS: dict[str, MailProvider] = {
    "google": MailProvider(
        key="google",
        label="Google / Gmail",
        imap_host="imap.gmail.com",
        imap_port=993,
        imap_ssl=True,
        smtp_host="smtp.gmail.com",
        smtp_port=465,
        smtp_starttls=False,
        auth="app_password",
        credential_hint="需开启两步验证后生成「应用专用密码」，不能用 Google 账号密码",
        domains=("gmail.com", "googlemail.com"),
    ),
    "outlook": MailProvider(
        key="outlook",
        label="Microsoft Outlook",
        imap_host="outlook.office365.com",
        imap_port=993,
        imap_ssl=True,
        smtp_host="smtp.office365.com",
        smtp_port=587,
        smtp_starttls=True,
        auth="app_password_or_oauth",
        credential_hint="个人账号用「应用密码」；企业租户若被策略禁用基本认证需改用 OAuth2",
        domains=(
            "outlook.com",
            "hotmail.com",
            "live.com",
            "msn.com",
            "office365.com",
            "onmicrosoft.com",
        ),
    ),
    "qq": MailProvider(
        key="qq",
        label="QQ 邮箱",
        imap_host="imap.qq.com",
        imap_port=993,
        imap_ssl=True,
        smtp_host="smtp.qq.com",
        smtp_port=465,
        smtp_starttls=False,
        auth="authorization_code",
        credential_hint="需在 QQ 邮箱设置中开启 IMAP/SMTP 服务并生成「授权码」，不能用登录密码",
        domains=("qq.com", "vip.qq.com", "foxmail.com"),
    ),
    # 网易系在国内同样常用，作为可选扩展提供；同样使用授权码。
    "163": MailProvider(
        key="163",
        label="网易 163 邮箱",
        imap_host="imap.163.com",
        imap_port=993,
        imap_ssl=True,
        smtp_host="smtp.163.com",
        smtp_port=465,
        smtp_starttls=False,
        auth="authorization_code",
        credential_hint="需开启 IMAP/SMTP 服务并使用「授权码」",
        domains=("163.com", "126.com", "yeah.net"),
    ),
}

# 常见域名别名 → 规范服务商 key。
_ALIASES = {
    "gmail": "google",
    "microsoft": "outlook",
    "office365": "outlook",
    "hotmail": "outlook",
    "outlook365": "outlook",
    "mail.qq": "qq",
    "qqmail": "qq",
    "netease": "163",
}


def resolve_provider(provider: str | None, email: str) -> MailProvider:
    """解析服务商预设。

    优先级：显式 ``provider``（含别名） > 邮箱域名自动推断。
    两者都无法命中时抛 ``UnknownProvider``，由上层翻译成 400。
    """
    key = (provider or "").strip().lower()
    if key:
        key = _ALIASES.get(key, key)
        preset = PROVIDERS.get(key)
        if preset is None:
            raise UnknownProvider(key)
        return preset

    address = (email or "").strip().lower()
    if "@" in address:
        domain = address.rsplit("@", 1)[1]
        for preset in PROVIDERS.values():
            for candidate in preset.domains:
                # 精确匹配，或后缀匹配：企业租户常见 contoso.onmicrosoft.com。
                if domain == candidate or domain.endswith("." + candidate):
                    return preset
    raise UnknownProvider(provider or "")


class UnknownProvider(ValueError):
    """未知或无法推断的邮箱服务商。"""

    def __init__(self, key: str):
        self.key = key
        super().__init__(f"unknown mail provider: {key or '(empty)'}")


def list_providers() -> list[dict[str, object]]:
    """返回所有可用的服务商预设，供 ``GET /providers`` 与网关文档使用。"""
    return [preset.as_dict() for preset in PROVIDERS.values()]
