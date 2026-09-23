"""
Edge Vision 端到端 HTTP 冒烟测试。

默认针对 252 已部署实例；可用 EDGE_VISION_URL 覆盖：

    EDGE_VISION_URL=http://127.0.0.1:18081 pytest -q tests/test_api.py
"""
from __future__ import annotations

import io
import os

import numpy as np
import pytest
import requests
from PIL import Image, ImageDraw

BASE_URL = os.environ.get("EDGE_VISION_URL", "http://192.168.31.252:18082")


def _image_bytes(with_text: bool = False) -> bytes:
    img = Image.new("RGB", (640, 480), (245, 245, 245))
    draw = ImageDraw.Draw(img)
    draw.rectangle([50, 120, 300, 420], fill=(60, 120, 200), outline=(20, 20, 20), width=3)
    if with_text:
        draw.text((60, 40), "EDGE VISION OCR 12345", fill=(0, 0, 0))
    buf = io.BytesIO()
    img.save(buf, format="PNG")
    return buf.getvalue()


def test_health():
    resp = requests.get(f"{BASE_URL}/health", timeout=10)
    assert resp.status_code == 200
    data = resp.json()
    assert data["status"] == "ok"
    assert data["service"] == "edge-vision"
    assert "detect" in data["capabilities"]


def test_detect():
    resp = requests.post(
        f"{BASE_URL}/detect",
        files={"image": ("t.png", _image_bytes(), "image/png")},
        data={"confidence_threshold": "0.25"},
        timeout=60,
    )
    assert resp.status_code == 200
    data = resp.json()
    assert data["image_width"] == 640 and data["image_height"] == 480
    assert isinstance(data["detections"], list)
    assert data["elapsed_ms"] > 0


def test_segment():
    resp = requests.post(
        f"{BASE_URL}/segment",
        files={"image": ("t.png", _image_bytes(), "image/png")},
        data={"confidence_threshold": "0.25"},
        timeout=60,
    )
    assert resp.status_code == 200
    assert isinstance(resp.json()["detections"], list)


def test_pose():
    resp = requests.post(
        f"{BASE_URL}/pose",
        files={"image": ("t.png", _image_bytes(), "image/png")},
        data={"confidence_threshold": "0.25"},
        timeout=60,
    )
    assert resp.status_code == 200
    assert isinstance(resp.json()["poses"], list)


def test_classify():
    resp = requests.post(
        f"{BASE_URL}/classify",
        files={"image": ("t.png", _image_bytes(), "image/png")},
        data={"top_k": "3"},
        timeout=60,
    )
    assert resp.status_code == 200
    assert len(resp.json()["predictions"]) == 3


def test_ocr():
    resp = requests.post(
        f"{BASE_URL}/ocr",
        files={"image": ("t.png", _image_bytes(with_text=True), "image/png")},
        timeout=60,
    )
    assert resp.status_code == 200
    data = resp.json()
    assert "12345" in data["full_text"].replace(" ", "")


def test_rejects_non_image():
    resp = requests.post(
        f"{BASE_URL}/detect",
        files={"image": ("t.txt", b"not an image", "text/plain")},
        timeout=30,
    )
    assert resp.status_code == 400


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))
