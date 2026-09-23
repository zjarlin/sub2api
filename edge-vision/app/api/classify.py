"""
YOLO 图像分类端点（ONNX Runtime 实现）。
"""
from __future__ import annotations

import time

import cv2
import numpy as np
from fastapi import UploadFile, HTTPException
from fastapi.responses import JSONResponse

from app import models
from app.runtime import read_image, parse_names


async def classify_endpoint(image: UploadFile, top_k: int = 5):
    if not image.content_type or not image.content_type.startswith("image/"):
        raise HTTPException(status_code=400, detail="只支持图片上传")

    image_bytes = await image.read()
    img = read_image(image_bytes)
    orig_h, orig_w = img.shape[:2]

    start = time.monotonic()
    try:
        session = models.get_cls_session()
        names = parse_names(session.get_modelmeta().custom_metadata_map)

        resized = cv2.resize(img, (224, 224), interpolation=cv2.INTER_LINEAR)
        rgb = resized[:, :, ::-1].astype(np.float32) / 255.0
        tensor = np.ascontiguousarray(rgb.transpose(2, 0, 1)[None])

        outputs = session.run(None, {session.get_inputs()[0].name: tensor})
        logits = np.squeeze(outputs[0])
        # softmax
        exp = np.exp(logits - logits.max())
        probs = exp / exp.sum()

        top_idx = probs.argsort()[::-1][:top_k]
        predictions = [
            {
                "class_id": int(i),
                "class_name": names.get(int(i), f"imagenet_{int(i)}"),
                "confidence": float(probs[i]),
            }
            for i in top_idx
        ]
    except FileNotFoundError as e:
        raise HTTPException(status_code=503, detail=str(e))
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=500, detail=f"分类推理失败: {e}")

    elapsed_ms = (time.monotonic() - start) * 1000
    return JSONResponse(
        content={
            "image_width": orig_w,
            "image_height": orig_h,
            "predictions": predictions,
            "elapsed_ms": elapsed_ms,
        }
    )
