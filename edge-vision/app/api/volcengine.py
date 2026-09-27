"""Volcengine Visual Service 兼容层。

火山引擎视觉服务的请求是 POST body 中携带 ``Action`` / ``Version`` 和图片
base64。现有 edge-vision 内部端点继续使用 multipart，本模块只做协议转换，
避免破坏已经在使用的原生接口。
"""
from __future__ import annotations

import base64
import binascii
import json
import uuid
from dataclasses import dataclass
from io import BytesIO
from typing import Any

from fastapi import Request
from fastapi.responses import JSONResponse
from starlette.datastructures import Headers, UploadFile

from app.api.classify import classify_endpoint
from app.api.detect import detect_endpoint
from app.api.ocr import ocr_endpoint
from app.api.pose import pose_endpoint
from app.api.segment import segment_endpoint

VOLCENGINE_VERSION = "2022-08-31"
SUPPORTED_ACTIONS = {
    "Detect": "detect",
    "Segment": "segment",
    "Pose": "pose",
    "Classify": "classify",
    "OCR": "ocr",
}
# 同时兼容火山引擎文档中常见的 CV* Action 命名。
ACTION_ALIASES = {
    "CVDetect": "Detect",
    "CVSegment": "Segment",
    "CVPose": "Pose",
    "CVClassify": "Classify",
    "CVOCR": "OCR",
    "CVProcess": "Detect",
}
SUPPORTED_VERSIONS = {"2022-08-31", "2021-08-31"}


@dataclass
class VolcengineRequest:
    action: str
    version: str
    image_bytes: bytes
    params: dict[str, Any]


def _request_id() -> str:
    return str(uuid.uuid4())


def _error(status_code: int, action: str, code: str, message: str, request_id: str) -> JSONResponse:
    return JSONResponse(
        status_code=status_code,
        content={
            "ResponseMetadata": {
                "RequestId": request_id,
                "Action": action,
                "Version": VOLCENGINE_VERSION,
                "Error": {"Code": code, "Message": message},
            }
        },
    )


def _success(action: str, result: dict[str, Any], request_id: str) -> JSONResponse:
    return JSONResponse(
        content={
            "ResponseMetadata": {
                "RequestId": request_id,
                "Action": action,
                "Version": VOLCENGINE_VERSION,
            },
            "Result": result,
        }
    )


async def _parse_request(request: Request) -> tuple[VolcengineRequest | None, JSONResponse | None]:
    request_id = _request_id()
    try:
        payload = await request.json()
    except (json.JSONDecodeError, UnicodeDecodeError, ValueError):
        return None, _error(400, "", "InvalidParameter", "请求体必须是 JSON", request_id)
    if not isinstance(payload, dict):
        return None, _error(400, "", "InvalidParameter", "请求体必须是 JSON 对象", request_id)

    raw_action = str(payload.get("Action", "")).strip()
    if not raw_action:
        return None, _error(400, "", "MissingParameter", "缺少必填参数 Action", request_id)
    action = ACTION_ALIASES.get(raw_action, raw_action)
    if action not in SUPPORTED_ACTIONS:
        return None, _error(
            400, raw_action, "InvalidAction", f"不支持的 Action: {raw_action}", request_id
        )

    version = str(payload.get("Version", VOLCENGINE_VERSION)).strip()
    if version not in SUPPORTED_VERSIONS:
        return None, _error(400, action, "InvalidVersion", f"不支持的 Version: {version}", request_id)

    encoded = payload.get("ImageBase64") or payload.get("Image") or payload.get("image_base64")
    if not encoded:
        return None, _error(400, action, "MissingParameter", "缺少必填参数 ImageBase64", request_id)
    if not isinstance(encoded, str):
        encoded = json.dumps(encoded)
    if encoded.startswith("data:"):
        _, _, encoded = encoded.partition(",")
    try:
        image_bytes = base64.b64decode(encoded, validate=True)
    except (binascii.Error, ValueError):
        return None, _error(400, action, "InvalidParameter", "ImageBase64 不是合法 base64", request_id)
    if not image_bytes:
        return None, _error(400, action, "InvalidParameter", "ImageBase64 内容为空", request_id)

    params = payload.get("Params") or {}
    if not isinstance(params, dict):
        return None, _error(400, action, "InvalidParameter", "Params 必须是对象", request_id)
    return VolcengineRequest(action=action, version=version, image_bytes=image_bytes, params=params), None


def _upload(content_type: str = "image/jpeg") -> UploadFile:
    if not content_type.startswith("image/"):
        content_type = "image/jpeg"
    return UploadFile(
        file=BytesIO(),
        filename="volcengine-image.jpg",
        headers=Headers({"content-type": content_type}),
    )


async def _invoke(parsed: VolcengineRequest) -> dict[str, Any]:
    action = SUPPORTED_ACTIONS[parsed.action]
    content_type = parsed.params.get("ContentType", "image/jpeg")
    if not isinstance(content_type, str):
        content_type = "image/jpeg"
    upload = _upload(content_type)
    upload.file = BytesIO(parsed.image_bytes)
    params = parsed.params
    if action == "detect":
        return await detect_endpoint(
            upload,
            confidence_threshold=float(params.get("ConfidenceThreshold", 0.5)),
            max_detections=int(params.get("MaxDetections", 100)),
            iou_threshold=float(params.get("IouThreshold", 0.45)),
        )
    if action == "segment":
        return await segment_endpoint(
            upload,
            confidence_threshold=float(params.get("ConfidenceThreshold", 0.5)),
            iou_threshold=float(params.get("IouThreshold", 0.45)),
            max_detections=int(params.get("MaxDetections", 100)),
            export_masks=bool(params.get("ExportMasks", True)),
        )
    if action == "pose":
        return await pose_endpoint(
            upload,
            confidence_threshold=float(params.get("ConfidenceThreshold", 0.5)),
            iou_threshold=float(params.get("IouThreshold", 0.45)),
            max_detections=int(params.get("MaxDetections", 100)),
            keypoint_threshold=float(params.get("KeypointThreshold", 0.25)),
        )
    if action == "classify":
        return await classify_endpoint(upload, top_k=int(params.get("TopK", 5)))
    return await ocr_endpoint(
        upload,
        use_text_detection=bool(params.get("UseTextDetection", True)),
        use_text_recognition=bool(params.get("UseTextRecognition", True)),
    )


async def volcengine_route(endpoint: str, request: Request) -> JSONResponse:
    parsed, failure = await _parse_request(request)
    if failure is not None:
        return failure
    assert parsed is not None
    try:
        raw = await _invoke(parsed)
        if hasattr(raw, "body"):
            body = raw.body
            result = json.loads(body.decode("utf-8"))
        else:
            result = raw
    except Exception as exc:  # noqa: BLE001
        return _error(500, parsed.action, "InternalError", f"视觉推理失败: {exc}", _request_id())
    return _success(parsed.action, result, _request_id())
