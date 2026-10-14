"""edge-mail HTTP 层测试，不联网。"""
from __future__ import annotations

import os
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

os.environ.setdefault("MAIL_SEND_ENABLED", "true")
os.environ.setdefault("MAIL_ALLOW_EXTERNAL_RECIPIENTS", "true")
os.environ.setdefault("MAIL_ALLOWED_PROVIDERS", "")

from app.main import app  # noqa: E402
from app import mail_client as mc  # noqa: E402
from tests.fake_servers import FakeIMAP, FakeSMTP, build_raw_message  # noqa: E402


@pytest.fixture()
def client():
    with TestClient(app) as test_client:
        yield test_client


def _patch_imap(monkeypatch, fake: FakeIMAP):
    monkeypatch.setattr(mc.imaplib, "IMAP4_SSL", lambda *a, **k: fake)
    monkeypatch.setattr(mc.imaplib, "IMAP4", lambda *a, **k: fake)


def test_health_lists_providers(client: TestClient):
    payload = client.get("/health").json()
    assert payload["service"] == "edge-mail"
    assert {"google", "outlook", "qq"}.issubset(set(payload["providers"]))
    assert "receive" in payload["capabilities"]
    assert "send" in payload["capabilities"]


def test_providers_endpoint_exposes_endpoints(client: TestClient):
    payload = client.get("/providers").json()
    google = next(item for item in payload["providers"] if item["key"] == "google")
    assert google["imap_host"] == "imap.gmail.com"
    assert google["smtp_host"] == "smtp.gmail.com"


def test_unknown_provider_returns_400(client: TestClient):
    response = client.post("/folders", json={"email": "a@unknown.tld", "credential": "x"})
    assert response.status_code == 400
    assert response.json()["error"]["type"] == "invalid_request_error"


def test_missing_credential_returns_400(client: TestClient):
    response = client.post("/folders", json={"email": "a@gmail.com"})
    assert response.status_code == 400


def test_folders_endpoint_returns_decoded_names(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    fake = FakeIMAP(folders=["INBOX", "已发送", "Trash"])
    _patch_imap(monkeypatch, fake)
    response = client.post("/folders", json={"email": "u@qq.com", "credential": "code"})
    assert response.status_code == 200
    names = [folder["name"] for folder in response.json()["folders"]]
    assert "已发送" in names


def test_auth_failure_returns_401(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    fake = FakeIMAP(login_ok=False)
    _patch_imap(monkeypatch, fake)
    response = client.post("/folders", json={"email": "u@gmail.com", "credential": "bad"})
    assert response.status_code == 401
    assert response.json()["error"]["type"] == "auth_failed"


def test_messages_listing(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    fake = FakeIMAP(search_result=["1", "2"])
    fake.payloads = {
        "1": build_raw_message("first", "a@b.com", "me@qq.com", "one"),
        "2": build_raw_message("second", "a@b.com", "me@qq.com", "two"),
    }
    _patch_imap(monkeypatch, fake)
    response = client.post("/messages", json={"email": "u@qq.com", "credential": "c", "limit": 10})
    payload = response.json()
    assert payload["count"] == 2
    assert payload["messages"][0]["uid"] == "2"


def test_message_fetch_requires_uid(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    _patch_imap(monkeypatch, FakeIMAP())
    response = client.post("/message", json={"email": "u@qq.com", "credential": "c"})
    assert response.status_code == 400


def test_attachment_download_sets_disposition(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    fake = FakeIMAP()
    fake.payloads = {
        "4": build_raw_message("file", "a@b.com", "me@qq.com", "body", attachment=b"PDFBODY")
    }
    _patch_imap(monkeypatch, fake)
    message = client.post("/message", json={"email": "u@qq.com", "credential": "c", "uid": "4"}).json()
    part = message["message"]["attachments"][0]["part"]
    response = client.post(
        "/attachment", json={"email": "u@qq.com", "credential": "c", "uid": "4", "part": part}
    )
    assert response.status_code == 200
    assert response.content == b"PDFBODY"
    assert "filename" in response.headers["content-disposition"]


def test_flags_requires_valid_action(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    _patch_imap(monkeypatch, FakeIMAP())
    response = client.post(
        "/message/flags", json={"email": "u@qq.com", "credential": "c", "uid": "1", "action": "explode"}
    )
    assert response.status_code == 400


def test_flags_mark_read(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    fake = FakeIMAP()
    _patch_imap(monkeypatch, fake)
    response = client.post(
        "/message/flags", json={"email": "u@qq.com", "credential": "c", "uid": "1", "action": "mark_read"}
    )
    assert response.status_code == 200
    assert response.json()["ok"] is True


def test_send_validates_body(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    monkeypatch.setattr(mc.smtplib, "SMTP_SSL", FakeSMTP)
    response = client.post(
        "/send", json={"email": "u@qq.com", "credential": "c", "to": ["a@b.com"], "subject": "s"}
    )
    assert response.status_code == 400


def test_send_success(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    FakeSMTP.instances.clear()
    monkeypatch.setattr(mc.smtplib, "SMTP_SSL", FakeSMTP)
    response = client.post(
        "/send",
        json={
            "email": "u@qq.com",
            "credential": "c",
            "to": ["a@b.com"],
            "subject": "你好",
            "body": "正文",
        },
    )
    assert response.status_code == 200
    payload = response.json()
    assert payload["accepted"] is True
    assert payload["provider"] == "qq"


def test_send_disabled_returns_400(client: TestClient, monkeypatch: pytest.MonkeyPatch):
    import app.main as main_module
    from dataclasses import replace

    monkeypatch.setattr(main_module, "config", replace(main_module.config, send_enabled=False))
    monkeypatch.setattr(mc.smtplib, "SMTP_SSL", FakeSMTP)
    response = client.post(
        "/send",
        json={"email": "u@qq.com", "credential": "c", "to": ["a@b.com"], "body": "x"},
    )
    assert response.status_code == 400


def test_internal_context_is_a_safe_snapshot(client: TestClient):
    payload = client.get("/internal/context").json()
    assert "send_enabled" in payload
    assert "credential" not in response_text(payload)


def response_text(payload: dict) -> str:
    import json

    return json.dumps(payload)
