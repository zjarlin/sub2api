"""服务商中继的离线测试：不访问真实上游，只校验前缀、鉴权与白名单。"""
from __future__ import annotations

import os
from pathlib import Path

import httpx
import pytest
from fastapi.testclient import TestClient

BASE_DIR = Path(__file__).resolve().parents[1]
os.environ.setdefault("MEDIA_DATA_DIR", "/tmp/edge-media-test-data")
os.environ.setdefault("MEDIA_DUBBING_COMMAND", "")
os.environ.setdefault("MEDIA_VIDEO_UPSTREAM_URL", "")
os.environ.setdefault("MEDIA_TTS_ENABLED", "false")
os.environ.setdefault("MEDIA_VIDEO_GENERATION_ENABLED", "false")

from app.main import app  # noqa: E402
from app import relay  # noqa: E402


@pytest.fixture()
def client():
    with TestClient(app) as test_client:
        yield test_client


@pytest.fixture()
def captured(monkeypatch: pytest.MonkeyPatch):
    """替换 httpx.AsyncClient，捕获中继真正发出的请求。"""
    calls: list[dict[str, object]] = []

    class FakeResponse:
        def __init__(self) -> None:
            self.status_code = 200
            self.content = b'{"ok":true}'
            self.headers = {"content-type": "application/json"}

    class FakeAsyncClient:
        def __init__(self, **_: object) -> None:
            pass

        async def __aenter__(self):
            return self

        async def __aexit__(self, *_: object) -> None:
            return None

        async def request(self, method, url, *, headers=None, content=None):
            calls.append({"method": method, "url": url, "headers": headers or {}, "content": content})
            return FakeResponse()

    monkeypatch.setattr(relay.httpx, "AsyncClient", FakeAsyncClient)
    return calls


def _configure(monkeypatch: pytest.MonkeyPatch, **values: str) -> None:
    for env_name, value in values.items():
        monkeypatch.setenv(env_name, value)


def test_providers_endpoint_lists_only_configured(monkeypatch: pytest.MonkeyPatch, client: TestClient):
    _configure(monkeypatch, PROVIDER_PEXELS_API_KEY="pexels-secret")
    for provider in relay.RELAY_PROVIDERS.values():
        if provider.api_key_env != "PROVIDER_PEXELS_API_KEY":
            monkeypatch.delenv(provider.api_key_env, raising=False)
    response = client.get("/relay/providers")
    assert response.status_code == 200
    keys = [item["key"] for item in response.json()["providers"]]
    assert keys == ["pexels"]
    assert "pexels-secret" not in response.text


def test_unconfigured_provider_returns_503(monkeypatch: pytest.MonkeyPatch, client: TestClient):
    for provider in relay.RELAY_PROVIDERS.values():
        monkeypatch.delenv(provider.api_key_env, raising=False)
    response = client.get("/relay/pexels/v1/videos/search?query=cat")
    assert response.status_code == 503


def test_unknown_provider_returns_404(monkeypatch: pytest.MonkeyPatch, client: TestClient):
    response = client.get("/relay/not-a-provider/anything")
    assert response.status_code == 404


def test_pexels_relay_uses_raw_authorization_and_keeps_path(
    monkeypatch: pytest.MonkeyPatch, client: TestClient, captured
):
    _configure(monkeypatch, PROVIDER_PEXELS_API_KEY="pexels-secret")
    response = client.get("/relay/pexels/v1/videos/search?query=cat&per_page=20")
    assert response.status_code == 200
    call = captured[-1]
    assert call["url"] == "https://api.pexels.com/v1/videos/search?query=cat&per_page=20"
    # Pexels 要求 Authorization 直接是 Key，不能带 Bearer 前缀。
    assert call["headers"]["Authorization"] == "pexels-secret"


def test_pixabay_relay_injects_query_key(monkeypatch: pytest.MonkeyPatch, client: TestClient, captured):
    _configure(monkeypatch, PROVIDER_PIXABAY_API_KEY="pixabay-secret")
    response = client.get("/relay/pixabay/api/videos/?q=cat")
    assert response.status_code == 200
    call = captured[-1]
    assert "key=pixabay-secret" in call["url"]
    assert "q=cat" in call["url"]
    assert "Authorization" not in call["headers"]


def test_pixabay_relay_drops_caller_supplied_key(
    monkeypatch: pytest.MonkeyPatch, client: TestClient, captured
):
    """调用方自带的 key 参数必须被剥离，否则上游会取到网关 Key。"""
    _configure(monkeypatch, PROVIDER_PIXABAY_API_KEY="pixabay-secret")
    response = client.get("/relay/pixabay/api/videos/?q=cat&key=sub2api-user-key")
    assert response.status_code == 200
    call = captured[-1]
    assert call["url"].count("key=") == 1
    assert "key=pixabay-secret" in call["url"]
    assert "sub2api-user-key" not in call["url"]


def test_coverr_relay_uses_bearer(monkeypatch: pytest.MonkeyPatch, client: TestClient, captured):
    _configure(monkeypatch, PROVIDER_COVERR_API_KEY="coverr-secret")
    response = client.get("/relay/coverr/videos?query=cat")
    assert response.status_code == 200
    call = captured[-1]
    assert call["headers"]["Authorization"] == "Bearer coverr-secret"


def test_ofox_relay_posts_body_and_bearer(monkeypatch: pytest.MonkeyPatch, client: TestClient, captured):
    _configure(monkeypatch, PROVIDER_OFOX_API_KEY="ofox-secret")
    response = client.post("/relay/ofox/videos", json={"prompt": "a cat"})
    assert response.status_code == 200
    call = captured[-1]
    assert call["url"] == "https://api.ofox.ai/v1/videos"
    assert call["headers"]["Authorization"] == "Bearer ofox-secret"
    assert call["content"] == b'{"prompt":"a cat"}'


def test_muapi_relay_uses_x_api_key(monkeypatch: pytest.MonkeyPatch, client: TestClient, captured):
    _configure(monkeypatch, PROVIDER_MUAPI_API_KEY="muapi-secret")
    response = client.get("/relay/muapi/predictions/abc/result")
    assert response.status_code == 200
    call = captured[-1]
    assert call["url"] == "https://api.muapi.ai/api/v1/predictions/abc/result"
    assert call["headers"]["x-api-key"] == "muapi-secret"


def test_inbound_gateway_key_is_not_forwarded_upstream(
    monkeypatch: pytest.MonkeyPatch, client: TestClient, captured
):
    """调用方带的是 Sub2API Key，绝不能冒充上游 Key 传给服务商。"""
    _configure(monkeypatch, PROVIDER_OFOX_API_KEY="ofox-secret")
    response = client.post(
        "/relay/ofox/videos",
        json={"prompt": "a cat"},
        headers={"Authorization": "Bearer sub2api-user-key", "x-api-key": "sub2api-user-key"},
    )
    assert response.status_code == 200
    call = captured[-1]
    assert call["headers"]["Authorization"] == "Bearer ofox-secret"
    assert "sub2api-user-key" not in str(call["headers"])


def test_base_url_override_is_honored(monkeypatch: pytest.MonkeyPatch, client: TestClient, captured):
    _configure(
        monkeypatch,
        PROVIDER_SEEDANCE_API_KEY="seedance-secret",
        PROVIDER_SEEDANCE_API_KEY_BASE_URL="http://ark.internal:9000/api/v3",
    )
    response = client.post("/relay/seedance/contents/generations/tasks", json={"model": "m"})
    assert response.status_code == 200
    call = captured[-1]
    assert call["url"] == "http://ark.internal:9000/api/v3/contents/generations/tasks"
