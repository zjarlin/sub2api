"""
YOLO 目标检测端点（ONNX Runtime 实现）。
"""
from __future__ import annotations

import time

import numpy as np
from fastapi import UploadFile, HTTPException
from fastapi.responses import JSONResponse

from app import models
from app.runtime import read_image, parse_names


async def detect_endpoint(
    image: UploadFile,
    confidence_threshold: float = 0.5,
    max_detections: int = 100,
    iou_threshold: float = 0.45,
):
    if not image.content_type or not image.content_type.startswith("image/"):
        raise HTTPException(status_code=400, detail="只支持图片上传")

    image_bytes = await image.read()
    img = read_image(image_bytes)
    orig_h, orig_w = img.shape[:2]

    start = time.monotonic()
    try:
        session = models.get_detect_session()
        names = parse_names(session.get_modelmeta().custom_metadata_map) or {
            i: n for i, n in enumerate(models.COCO_NAMES)
        }

        tensor, ratio, pad = models.preprocess(img, imgsz=640)
        outputs = session.run(None, {session.get_inputs()[0].name: tensor})
        # output0: [1, 84, 8400] -> [8400, 84]
        pred = np.squeeze(outputs[0], axis=0).T

        boxes_xywh = pred[:, :4]
        class_scores = pred[:, 4:]
        class_ids = class_scores.argmax(axis=1)
        confidences = class_scores[np.arange(len(class_scores)), class_ids]

        mask = confidences >= confidence_threshold
        boxes_xywh = boxes_xywh[mask]
        confidences = confidences[mask]
        class_ids = class_ids[mask]

        detections = []
        if len(boxes_xywh) > 0:
            boxes = models.xywh2xyxy(boxes_xywh)
            keep = models.nms(boxes, confidences, iou_threshold)[:max_detections]
            boxes = models.scale_boxes(boxes[keep], ratio, pad, (orig_h, orig_w))
            confidences = confidences[keep]
            class_ids = class_ids[keep]

            for i in range(len(boxes)):
                x1, y1, x2, y2 = boxes[i]
                detections.append({
                    "x": float(x1),
                    "y": float(y1),
                    "width": float(x2 - x1),
                    "height": float(y2 - y1),
                    "confidence": float(confidences[i]),
                    "class_id": int(class_ids[i]),
                    "class_name": names.get(int(class_ids[i]), f"class_{int(class_ids[i])}"),
                })
    except FileNotFoundError as e:
        raise HTTPException(status_code=503, detail=str(e))
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=500, detail=f"检测推理失败: {e}")

    elapsed_ms = (time.monotonic() - start) * 1000
    return JSONResponse(
        content={
            "image_width": orig_w,
            "image_height": orig_h,
            "detections": detections,
            "elapsed_ms": elapsed_ms,
        }
    )
