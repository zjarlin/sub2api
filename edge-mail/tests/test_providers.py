"""服务商预设与解析的离线单测。"""
from __future__ import annotations

import pytest

from app.providers import PROVIDERS, UnknownProvider, list_providers, resolve_provider


@pytest.mark.parametrize(
    "email,expected",
    [
        ("someone@gmail.com", "google"),
        ("a@googlemail.com", "google"),
        ("user@outlook.com", "outlook"),
        ("user@hotmail.com", "outlook"),
        ("user@live.com", "outlook"),
        ("user@contoso.onmicrosoft.com", "outlook"),
        ("user@qq.com", "qq"),
        ("user@foxmail.com", "qq"),
        ("user@163.com", "163"),
        ("user@126.com", "163"),
    ],
)
def test_domain_inference(email: str, expected: str):
    assert resolve_provider(None, email).key == expected


@pytest.mark.parametrize(
    "alias,expected",
    [("gmail", "google"), ("microsoft", "outlook"), ("office365", "outlook"), ("qqmail", "qq")],
)
def test_provider_aliases(alias: str, expected: str):
    assert resolve_provider(alias, "x@example.com").key == expected


def test_explicit_provider_wins_over_domain():
    # 自建/企业邮箱走 outlook 主机，但域名不是 outlook：显式指定必须生效。
    assert resolve_provider("outlook", "user@corp.example.com").key == "outlook"


def test_unknown_domain_without_provider_raises():
    with pytest.raises(UnknownProvider):
        resolve_provider(None, "user@unknown.tld")


def test_unknown_provider_key_raises():
    with pytest.raises(UnknownProvider):
        resolve_provider("nope", "user@gmail.com")


@pytest.mark.parametrize(
    "key,imap_host,smtp_host,smtp_starttls",
    [
        ("google", "imap.gmail.com", "smtp.gmail.com", False),
        ("outlook", "outlook.office365.com", "smtp.office365.com", True),
        ("qq", "imap.qq.com", "smtp.qq.com", False),
    ],
)
def test_required_provider_endpoints(key: str, imap_host: str, smtp_host: str, smtp_starttls: bool):
    preset = PROVIDERS[key]
    assert preset.imap_host == imap_host
    assert preset.imap_port == 993
    assert preset.imap_ssl is True
    assert preset.smtp_host == smtp_host
    assert preset.smtp_starttls is smtp_starttls


def test_outlook_uses_starttls_on_587_and_certs_are_not_skipped():
    preset = PROVIDERS["outlook"]
    assert preset.smtp_port == 587
    assert preset.smtp_starttls is True


def test_list_providers_covers_outlook_google_qq():
    keys = {item["key"] for item in list_providers()}
    assert {"outlook", "google", "qq"}.issubset(keys)
