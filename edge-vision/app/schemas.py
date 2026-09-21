"""
Pydantic 响应模型（用于 OpenAPI 文档描述）。

各端点直接返回 JSONResponse，此处模型仅作为文档与类型参考。
"""
from __future__ import annotations

from typing import Any

from pydantic import BaseModel, Field


class HealthResponse(BaseModel):
    status: str = "ok"
    service: str = "edge-vision"
    version: str = "1.0.0"
    models_loaded: bool = False
    capabilities: list[str] = Field(default_factory=list)


class BBox(BaseModel):
    x: float
    y: float
    width: float
    height: float


class Detection(BaseModel):
    x: float
    y: float
    width: float
    height: float
    confidence: float
    class_id: int
    class_name: str


class DetectResponse(BaseModel):
    image_width: int
    image_height: int
    detections: list[Detection] = Field(default_factory=list)
    elapsed_ms: float


class ClassifyPrediction(BaseModel):
    class_id: int
    class_name: str
    confidence: float


class ClassifyResponse(BaseModel):
    image_width: int
    image_height: int
    predictions: list[ClassifyPrediction] = Field(default_factory=list)
    elapsed_ms: float


class SegmentDetection(Detection):
    area: int | None = None
    polygons: list[list[list[float]]] = Field(default_factory=list)


class SegmentResponse(BaseModel):
    image_width: int
    image_height: int
    detections: list[SegmentDetection] = Field(default_factory=list)
    elapsed_ms: float


class KeyPoint(BaseModel):
    name: str
    x: float
    y: float
    confidence: float
    visible: bool


class Pose(BaseModel):
    confidence: float
    bbox: list[float]
    keypoints: list[KeyPoint] = Field(default_factory=list)
    skeleton: list[list[int]] = Field(default_factory=list)


class PoseResponse(BaseModel):
    image_width: int
    image_height: int
    poses: list[Pose] = Field(default_factory=list)
    elapsed_ms: float


class TextRegion(BaseModel):
    x: float
    y: float
    width: float
    height: float
    text: str
    confidence: float
    box: list[list[float]] = Field(default_factory=list)


class OcrResponse(BaseModel):
    image_width: int
    image_height: int
    text_regions: list[TextRegion] = Field(default_factory=list)
    full_text: str = ""
    elapsed_ms: float
