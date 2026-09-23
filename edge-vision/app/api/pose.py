"""
YOLO 人体姿态估计端点（ONNX Runtime 实现）。

输出 17 个 COCO 关键点：nose, left_eye, right_eye, left_ear, right_ear,
left_shoulder, right_shoulder, left_elbow, right_elbow, left_wrist, right_wrist,
left_hip, right_hip, left_knee, right_knee, left_ankle, right_ankle
"""
from __future__ import annotations

import time

import numpy as np
from fastapi import UploadFile, HTTPException
from fastapi.responses import JSONResponse

from app import models
from app.runtime import read_image

KEYPOINT_NAMES = [
    "nose", "left_eye", "right_eye", "left_ear", "right_ear",
    "left_shoulder", "right_shoulder", "left_elbow", "right_elbow",
    "left_wrist", "right_wrist", "left_hip", "right_hip",
    "left_knee", "right_knee", "left_ankle", "right_ankle",
]

# YOLOv8-pose 骨架连接（关键点索引对）
SKELETON = [
    [16, 14], [14, 12], [17, 15], [15, 13], [12, 13], [6, 12], [7, 13],
    [6, 7], [6, 8], [7, 9], [8, 10], [9, 11], [2, 3], [1, 2], [1, 3],
    [2, 4], [3, 5], [4, 6], [5, 7],
]


async def pose_endpoint(
    image: UploadFile,
    confidence_threshold: float = 0.5,
    iou_threshold: float = 0.45,
    max_detections: int = 100,
    keypoint_threshold: float = 0.25,
):
    if not image.content_type or not image.content_type.startswith("image/"):
        raise HTTPException(status_code=400, detail="只支持图片上传")

    image_bytes = await image.read()
    img = read_image(image_bytes)
    orig_h, orig_w = img.shape[:2]

    start = time.monotonic()
    try:
        session = models.get_pose_session()
        tensor, ratio, pad = models.preprocess(img, imgsz=640)
        outputs = session.run(None, {session.get_inputs()[0].name: tensor})

        # output0: [1, 56, 8400] -> [8400, 56]
        pred = np.squeeze(outputs[0], axis=0).T
        boxes_xywh = pred[:, :4]
        person_conf = pred[:, 4]
        kpts = pred[:, 5:56].reshape(-1, 17, 3)  # x, y, conf

        mask = person_conf >= confidence_threshold
        boxes_xywh = boxes_xywh[mask]
        person_conf = person_conf[mask]
        kpts = kpts[mask]

        poses = []
        if len(boxes_xywh) > 0:
            boxes = models.xywh2xyxy(boxes_xywh)
            keep = models.nms(boxes, person_conf, iou_threshold)[:max_detections]
            boxes = models.scale_boxes(boxes[keep], ratio, pad, (orig_h, orig_w))
            person_conf = person_conf[keep]
            kpts = kpts[keep]

            for i in range(len(boxes)):
                x1, y1, x2, y2 = boxes[i]
                keypoints = []
                for j in range(17):
                    kx, ky, kc = kpts[i, j]
                    # 关键点坐标同样从 letterbox 空间还原
                    ox = (kx - pad[0]) / ratio
                    oy = (ky - pad[1]) / ratio
                    keypoints.append({
                        "name": KEYPOINT_NAMES[j],
                        "x": float(np.clip(ox, 0, orig_w)),
                        "y": float(np.clip(oy, 0, orig_h)),
                        "confidence": float(kc),
                        "visible": bool(kc >= keypoint_threshold),
                    })

                poses.append({
                    "confidence": float(person_conf[i]),
                    "bbox": [float(x1), float(y1), float(x2), float(y2)],
                    "keypoints": keypoints,
                    "skeleton": SKELETON,
                })
    except FileNotFoundError as e:
        raise HTTPException(status_code=503, detail=str(e))
    except Exception as e:  # noqa: BLE001
        raise HTTPException(status_code=500, detail=f"姿态估计推理失败: {e}")

    elapsed_ms = (time.monotonic() - start) * 1000
    return JSONResponse(
        content={
            "image_width": orig_w,
            "image_height": orig_h,
            "poses": poses,
            "elapsed_ms": elapsed_ms,
        }
    )
