"""edge-media 离线 API 测试，不依赖真实模型或网络。"""
from __future__ import annotations

import os
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

BASE_DIR = Path(__file__).resolve().parents[1]
os.environ.setdefault("MEDIA_DATA_DIR", "/tmp/edge-media-test-data")
os.environ.setdefault("MEDIA_DUBBING_COMMAND", "")
os.environ.setdefault("MEDIA_VIDEO_UPSTREAM_URL", "")
os.environ.setdefault("MEDIA_TTS_ENABLED", "false")
os.environ.setdefault("MEDIA_VIDEO_GENERATION_ENABLED", "false")

from app.main import app, config  # noqa: E402


@pytest.fixture()
def client():
    with TestClient(app) as test_client:
        yield test_client


def test_health_reports_capabilities(client: TestClient):
    response = client.get("/health")
    assert response.status_code == 200
    payload = response.json()
    assert payload["service"] == "edge-media"
    assert "dubbing" in payload["capabilities"]


def test_tts_requires_configured_upstream(client: TestClient):
    response = client.post("/tts", json={"text": "你好"})
    assert response.status_code == 503


def test_tts_validation_happens_before_upstream_check():
    response = None
    with TestClient(app) as test_client:
        response = test_client.post("/tts", json={})
    assert response is not None
    assert response.status_code == 400


def test_video_generation_disabled(client: TestClient):
    response = client.post("/videos/generations", json={"prompt": "a cat"})
    assert response.status_code == 503


def test_transcode_rejects_non_video_extension(client: TestClient):
    response = client.post(
        "/videos/transcode",
        files={"video": ("note.txt", b"not a video", "text/plain")},
        data={"options": "{}"},
    )
    assert response.status_code in {400, 422, 500, 502}


def test_dubbing_without_upstream_reports_blocked_and_persists_state(client: TestClient):
    response = client.post(
        "/videos/dub",
        files={"video": ("clip.mp4", b"\x00\x00\x00\x18ftypmp42", "video/mp4")},
        data={"options": "{}"},
    )
    assert response.status_code == 200
    payload = response.json()
    assert payload["status"] == "blocked"
    assert "MEDIA_DUBBING_COMMAND" in payload["error"]

    status = client.get(f"/tasks/{payload['task_id']}")
    assert status.status_code == 200
    assert status.json()["status"] == "blocked"
    assert status.json()["kind"] == "dubbing"


def test_unknown_task_returns_404(client: TestClient):
    assert client.get("/tasks/dub_missing").status_code == 404
    assert client.get("/tasks/dub_missing/content").status_code == 404


def test_upload_over_limit_is_rejected(client: TestClient):
    import app.main as main_module

    original = main_module.config
    try:
        main_module.config = original.__class__(**{**original.__dict__, "max_upload_bytes": 8})
        response = client.post(
            "/videos/transcode",
            files={"video": ("clip.mp4", b"0123456789", "video/mp4")},
            data={"options": "{}"},
        )
    finally:
        main_module.config = original
    assert response.status_code == 413


def test_transcode_runs_command_and_persists_output():
    """用假的 ffmpeg 命令走通命令执行与任务状态持久化，避免真实转码依赖。"""
    import app.main as main_module

    original = main_module.config
    fake_command = (
        'python3 -c "import os,pathlib;pathlib.Path(os.environ[\'MEDIA_OUTPUT\']).write_bytes(b\'mp4\')"'
    )
    try:
        main_module.config = original.__class__(**{**original.__dict__, "transcode_command": fake_command})
        with TestClient(app) as test_client:
            response = test_client.post(
                "/videos/transcode",
                files={"video": ("clip.mp4", b"\x00\x00\x00\x18ftypmp42", "video/mp4")},
                data={"options": "{}"},
            )
        assert response.status_code == 200, response.text
        payload = response.json()
        assert payload["status"] == "succeeded", payload
        assert payload["output"]["bytes"] == 3
        with TestClient(app) as test_client:
            content = test_client.get(f"/tasks/{payload['task_id']}/content")
        assert content.status_code == 200
        assert content.content == b"mp4"
    finally:
        main_module.config = original
