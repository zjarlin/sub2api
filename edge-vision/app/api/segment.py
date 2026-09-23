"""
YOLO 实例分割端点（ONNX Runtime 实现）。

输出：
- detections：每个实例的 bbox + class + confidence
- masks：每个实例的二值 mask（RLE 压缩，PNG 风格的行游程编码）
- polygons：每个实例 mask 的轮廓多边形（原图坐标）
"""
from __future__ import annotations

import time

import cv2
import numpy as np
from fastapi import UploadFile, HTTPException
from fastapi.responses import JSONResponse

from app import models
from app.runtime import read_image, parse_names


def _mask_to_polygons(mask: np.ndarray) -> list[list[list[float]]]:
    """将二值 mask 转为轮廓多边形列表。"""
    contours, _ = cv2.findContours(
        mask.astype(np.uint8), cv2.RETR_EXTERNAL, cv2.CHAIN_APPROX_SIMPLE
    )
    polygons = []
    for c in contours:
        if len(c) < 3:
            continue
        poly = c.reshape(-1, 2).astype(float).tolist()
        polygons.append(poly)
    return polygons


async def segment_endpoint(
    image: UploadFile,
    confidence_threshold: float = 0.5,
    iou_threshold: float = 0.45,
    max_detections: int = 100,
    export_masks: bool = True,
):
    if not image.content_type or not image.content_type.startswith("image/"):
        raise HTTPException(status_code=400, detail="只支持图片上传")

    image_bytes = await image.read()
    img = read_image(image_bytes)
    orig_h, orig_w = img.shape[:2]

    start = time.monotonic()
    try:
        session = models.get_seg_session()
        names = parse_names(session.get_modelmeta().custom_metadata_map) or {
            i: n for i, n in enumerate(models.COCO_NAMES)
        }

        tensor, ratio, pad = models.preprocess(img, imgsz=640)
        outputs = session.run(None, {session.get_inputs()[0].name: tensor})
        pred = np.squeeze(outputs[0], axis=0).T  # [8400, 116]
        protos = outputs[1][0]  # [32, 160, 160]

        boxes_xywh = pred[:, :4]
        class_scores = pred[:, 4:84]
        coeffs = pred[:, 84:116]  # [8400, 32]

        class_ids = class_scores.argmax(axis=1)
        confidences = class_scores[np.arange(len(class_scores)), class_ids]

        mask = confidences >= confidence_threshold
        boxes_xywh = boxes_xywh[mask]
        confidences = confidences[mask]
        class_ids = class_ids[mask]
        coeffs = coeffs[mask]

        detections = []
        if len(boxes_xywh) > 0:
            boxes = models.xywh2xyxy(boxes_xywh)
            keep = models.nms(boxes, confidences, iou_threshold)[:max_detections]
            boxes = boxes[keep]
            confidences = confidences[keep]
            class_ids = class_ids[keep]
            coeffs = coeffs[keep]

            # 生成 mask：coeffs @ protos -> [n, 160, 160]
            masks = coeffs @ protos.reshape(32, -1)
            masks = masks.reshape(len(keep), 160, 160)
            masks = 1 / (1 + np.exp(-masks))  # sigmoid

            scaled_boxes = models.scale_boxes(boxes.copy(), ratio, pad, (orig_h, orig_w))

            for i in range(len(keep)):
                x1, y1, x2, y2 = scaled_boxes[i]
                det = {
                    "x": float(x1),
                    "y": float(y1),
                    "width": float(x2 - x1),
                    "height": float(y2 - y1),
                    "confidence": float(confidences[i]),
                    "class_id": int(class_ids[i]),
                    "class_name": names.get(int(class_ids[i]), f"class_{int(class_ids[i])}"),
                }

                if export_masks:
                    # mask 位于 160x160 的 letterbox 空间，先放大到 640x640，
                    # 再按原图 letterbox 的 padding 裁剪，最后缩放回原图尺寸。
                    m = masks[i]
                    proto_sz = m.shape[0]  # 160
                    m = cv2.resize(m, (640, 640), interpolation=cv2.INTER_LINEAR)
                    pad_l, pad_t = int(round(pad[0])), int(round(pad[1]))
                    content_w = int(round(orig_w * ratio))
                    content_h = int(round(orig_h * ratio))
                    m = m[pad_t:pad_t + content_h, pad_l:pad_l + content_w]
                    m = cv2.resize(m, (orig_w, orig_h), interpolation=cv2.INTER_LINEAR)
                    binary = (m > 0.5).astype(np.uint8)

                    # 仅在 bbox 区域内保留 mask，减少噪声
                    region = np.zeros_like(binary)
                    rx1, ry1 = max(0, int(x1)), max(0, int(y1))
                    rx2, ry2 = min(orig_w, int(np.ceil(x2))), min(orig_h, int(np.ceil(y2)))
                    region[ry1:ry2, rx1:rx2] = binary[ry1:ry2, rx1:rx2]

                    det["area"] = int(region.sum())
                    det["polygons"] = _mask_to_polygons(region)

                detections.append(det)
    except FileNotFoundError as e:
        raise HTTPException(status_code=503, detail=str(e))
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=500, detail=f"分割推理失败: {e}")

    elapsed_ms = (time.monotonic() - start) * 1000
    return JSONResponse(
        content={
            "image_width": orig_w,
            "image_height": orig_h,
            "detections": detections,
            "elapsed_ms": elapsed_ms,
        }
    )
