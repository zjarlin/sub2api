"""
Edge Vision - 边缘计算视觉服务

独立部署的离线计算机视觉 API 网关，提供：
- YOLO 目标检测 / 实例分割 / 姿态估计 / 图像分类
- OCR 文字识别（中文 / 英文）

所有模型随镜像离线打包，运行时无网络依赖。
"""
from __future__ import annotations

import logging
import os

from fastapi import FastAPI, File, Form, UploadFile
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse

from app.api.detect import detect_endpoint
from app.api.classify import classify_endpoint
from app.api.segment import segment_endpoint
from app.api.pose import pose_endpoint
from app.api.ocr import ocr_endpoint

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger("edge-vision")

# 通过网关访问时，对外路径带 /vision 前缀；root_path 让 Swagger/OpenAPI 生成正确链接。
# 直连容器（无前缀）时 ROOT_PATH 留空即可。
app = FastAPI(
    title="Edge Vision",
    description="离线边缘计算视觉服务：YOLO 检测/分割/姿态/分类 + OCR",
    version="1.0.0",
    root_path=os.environ.get("ROOT_PATH", ""),
)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=False,
    allow_methods=["*"],
    allow_headers=["*"],
)


@app.get("/health")
async def health():
    from app import models

    return {
        "status": "ok",
        "service": "edge-vision",
        "version": "1.0.0",
        "models_loaded": models.are_models_loaded(),
        "capabilities": ["detect", "segment", "pose", "classify", "ocr"],
    }


@app.get("/")
async def root():
    return {
        "service": "edge-vision",
        "docs": "/docs",
        "endpoints": ["/health", "/detect", "/segment", "/pose", "/classify", "/ocr"],
    }


@app.post("/detect")
async def detect_route(
    image: UploadFile = File(...),
    confidence_threshold: float = Form(0.5),
    max_detections: int = Form(100),
    iou_threshold: float = Form(0.45),
):
    return await detect_endpoint(
        image=image,
        confidence_threshold=confidence_threshold,
        max_detections=max_detections,
        iou_threshold=iou_threshold,
    )


@app.post("/segment")
async def segment_route(
    image: UploadFile = File(...),
    confidence_threshold: float = Form(0.5),
    iou_threshold: float = Form(0.45),
    max_detections: int = Form(100),
    export_masks: bool = Form(True),
):
    return await segment_endpoint(
        image=image,
        confidence_threshold=confidence_threshold,
        iou_threshold=iou_threshold,
        max_detections=max_detections,
        export_masks=export_masks,
    )


@app.post("/pose")
async def pose_route(
    image: UploadFile = File(...),
    confidence_threshold: float = Form(0.5),
    iou_threshold: float = Form(0.45),
    max_detections: int = Form(100),
    keypoint_threshold: float = Form(0.25),
):
    return await pose_endpoint(
        image=image,
        confidence_threshold=confidence_threshold,
        iou_threshold=iou_threshold,
        max_detections=max_detections,
        keypoint_threshold=keypoint_threshold,
    )


@app.post("/classify")
async def classify_route(
    image: UploadFile = File(...),
    top_k: int = Form(5),
):
    return await classify_endpoint(image=image, top_k=top_k)


@app.post("/ocr")
async def ocr_route(
    image: UploadFile = File(...),
    use_text_detection: bool = Form(True),
    use_text_recognition: bool = Form(True),
):
    return await ocr_endpoint(
        image=image,
        use_text_detection=use_text_detection,
        use_text_recognition=use_text_recognition,
    )


@app.exception_handler(Exception)
async def unhandled_exception_handler(request, exc):
    logger.exception("未处理异常: %s", exc)
    return JSONResponse(status_code=500, content={"detail": "内部服务错误"})
