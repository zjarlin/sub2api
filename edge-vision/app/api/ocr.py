"""
OCR 文字识别端点（RapidOCR / ONNX Runtime 实现，离线）。

RapidOCR 随 pip 包携带 PP-OCRv4 det/rec/cls 三个 ONNX 模型，
无需额外下载，适配纯 CPU 离线部署。
"""
from __future__ import annotations

import time

import numpy as np
from fastapi import UploadFile, HTTPException
from fastapi.responses import JSONResponse

from app import models
from app.runtime import read_image


async def ocr_endpoint(
    image: UploadFile,
    use_text_detection: bool = True,
    use_text_recognition: bool = True,
):
    if not image.content_type or not image.content_type.startswith("image/"):
        raise HTTPException(status_code=400, detail="只支持图片上传")

    image_bytes = await image.read()
    img = read_image(image_bytes)
    orig_h, orig_w = img.shape[:2]

    start = time.monotonic()
    try:
        engine = models.get_ocr()
        # RapidOCR 接受 BGR ndarray
        result, _ = engine(
            img,
            use_det=use_text_detection,
            use_cls=True,
            use_rec=use_text_recognition,
        )

        text_regions = []
        texts = []
        if result:
            for item in result:
                # RapidOCR 返回 [box(4x2), text, score]
                box, text, score = item[0], item[1], item[2]
                xs = [float(p[0]) for p in box]
                ys = [float(p[1]) for p in box]
                x1, x2 = min(xs), max(xs)
                y1, y2 = min(ys), max(ys)
                text_regions.append({
                    "x": x1,
                    "y": y1,
                    "width": x2 - x1,
                    "height": y2 - y1,
                    "text": text,
                    "confidence": float(score),
                    "box": [[float(p[0]), float(p[1])] for p in box],
                })
                texts.append(text)
    except ImportError as e:
        raise HTTPException(status_code=503, detail=f"OCR 引擎不可用: {e}")
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=500, detail=f"OCR 推理失败: {e}")

    elapsed_ms = (time.monotonic() - start) * 1000
    return JSONResponse(
        content={
            "image_width": orig_w,
            "image_height": orig_h,
            "text_regions": text_regions,
            "full_text": "\n".join(texts),
            "elapsed_ms": elapsed_ms,
        }
    )
