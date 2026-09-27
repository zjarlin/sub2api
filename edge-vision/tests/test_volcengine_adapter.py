"""Volcengine Visual Service 兼容层的离线单元测试。"""
from __future__ import annotations

import base64
import io

import numpy as np
import pytest
from fastapi.testclient import TestClient
from PIL import Image

from app.main import app

from app import models
from app.api import volcengine


class _FakeSession:
    def get_modelmeta(self):
        class Meta:
            custom_metadata_map = {"names": "{0: 'person', 1: 'bicycle'}"}

        return Meta()

    def get_inputs(self):
        class Input:
            name = "images"

        return [Input()]

    def run(self, _outputs, _inputs):
        return [np.zeros((1, 84, 8400), dtype=np.float32)]


def _image_base64() -> str:
    image = Image.new("RGB", (32, 24), (255, 255, 255))
    buffer = io.BytesIO()
    image.save(buffer, format="PNG")
    return base64.b64encode(buffer.getvalue()).decode("ascii")


def test_detect_accepts_volcengine_envelope(monkeypatch):
    monkeypatch.setattr(models, "get_detect_session", lambda: _FakeSession())

    async def invoke():
        response = await volcengine.volcengine_route(
            "detect",
            _Request(
                {
                    "Action": "Detect",
                    "Version": "2022-08-31",
                    "ImageBase64": _image_base64(),
                    "Params": {"ConfidenceThreshold": 0.25},
                }
            ),
        )
        return response

    import asyncio

    response = asyncio.run(invoke())
    payload = response.body.decode("utf-8")
    assert response.status_code == 200
    assert '"ResponseMetadata"' in payload
    assert '"RequestId"' in payload
    assert '"Result"' in payload
    assert '"detections"' in payload


def test_http_route_returns_volcengine_envelope(monkeypatch):
    monkeypatch.setattr(models, "get_detect_session", lambda: _FakeSession())
    client = TestClient(app)
    response = client.post(
        "/volcengine/detect",
        json={
            "Action": "Detect",
            "Version": "2022-08-31",
            "ImageBase64": _image_base64(),
            "Params": {"ConfidenceThreshold": 0.25},
        },
    )

    assert response.status_code == 200
    payload = response.json()
    assert payload["ResponseMetadata"]["Action"] == "Detect"
    assert "RequestId" in payload["ResponseMetadata"]
    assert "Result" in payload


def test_rejects_unknown_action():
    import asyncio

    response = asyncio.run(
        volcengine.volcengine_route(
            "detect",
            _Request({"Action": "Unknown", "Version": "2022-08-31", "ImageBase64": _image_base64()}),
        )
    )
    assert response.status_code == 400
    payload = response.body.decode("utf-8")
    assert '"InvalidAction"' in payload


class _Request:
    def __init__(self, payload):
        self._payload = payload

    async def json(self):
        return self._payload
