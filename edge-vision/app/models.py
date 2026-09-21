"""
边缘视觉模型加载与推理封装（纯 ONNX Runtime，无 torch 依赖）。

模型全部随镜像离线打包，运行时不会访问网络：
- yolov8n.onnx       : 目标检测
- yolov8n-seg.onnx   : 实例分割
- yolov8n-pose.onnx  : 人体姿态估计
- yolov8n-cls.onnx   : 图像分类
- RapidOCR           : OCR（det/rec/cls 三个 ONNX 模型，随 pip 包离线携带）

所有推理走 onnxruntime CPUExecutionProvider，适配 252 无 GPU 主机。
"""
from __future__ import annotations

import os
import threading
from pathlib import Path
from typing import Optional

import numpy as np
import onnxruntime as ort

MODELS_DIR = Path(os.environ.get("MODEL_DIR", "/models"))

# YOLOv8 COCO 80 类名称
COCO_NAMES = [
    "person", "bicycle", "car", "motorcycle", "airplane", "bus", "train", "truck",
    "boat", "traffic light", "fire hydrant", "stop sign", "parking meter", "bench",
    "bird", "cat", "dog", "horse", "sheep", "cow", "elephant", "bear", "zebra",
    "giraffe", "backpack", "umbrella", "handbag", "tie", "suitcase", "frisbee",
    "skis", "snowboard", "sports ball", "kite", "baseball bat", "baseball glove",
    "skateboard", "surfboard", "tennis racket", "bottle", "wine glass", "cup",
    "fork", "knife", "spoon", "bowl", "banana", "apple", "sandwich", "orange",
    "broccoli", "carrot", "hot dog", "pizza", "donut", "cake", "chair", "couch",
    "potted plant", "bed", "dining table", "toilet", "tv", "laptop", "mouse",
    "remote", "keyboard", "cell phone", "microwave", "oven", "toaster", "sink",
    "refrigerator", "book", "clock", "vase", "scissors", "teddy bear",
    "hair drier", "toothbrush",
]

# ImageNet 1000 类名称（分类模型），从 ultralytics 导出的 ONNX 已内嵌元数据，
# 这里做最小兜底：仅返回类别索引与置信度，类别名以 imagenet_<id> 形式呈现。
IMAGENET_PREFIX = "imagenet_"

_lock = threading.Lock()
_sessions: dict[str, ort.InferenceSession] = {}
_ocr_engine = None


def _session_options() -> ort.SessionOptions:
    opts = ort.SessionOptions()
    opts.intra_op_num_threads = int(os.environ.get("ORT_NUM_THREADS", "4"))
    opts.inter_op_num_threads = 1
    opts.graph_optimization_level = ort.GraphOptimizationLevel.ORT_ENABLE_ALL
    opts.log_severity_level = 3
    return opts


def _load_session(model_file: str) -> ort.InferenceSession:
    """加载 ONNX 会话，带缓存与线程锁。"""
    with _lock:
        if model_file in _sessions:
            return _sessions[model_file]

        path = MODELS_DIR / model_file
        if not path.exists():
            raise FileNotFoundError(f"模型文件缺失: {path}")

        session = ort.InferenceSession(
            str(path),
            sess_options=_session_options(),
            providers=["CPUExecutionProvider"],
        )
        _sessions[model_file] = session
        return session


def get_detect_session() -> ort.InferenceSession:
    return _load_session("yolov8n.onnx")


def get_seg_session() -> ort.InferenceSession:
    return _load_session("yolov8n-seg.onnx")


def get_pose_session() -> ort.InferenceSession:
    return _load_session("yolov8n-pose.onnx")


def get_cls_session() -> ort.InferenceSession:
    return _load_session("yolov8n-cls.onnx")


def get_ocr():
    """懒加载 RapidOCR（纯 ONNX，离线）。"""
    global _ocr_engine
    if _ocr_engine is not None:
        return _ocr_engine

    with _lock:
        if _ocr_engine is not None:
            return _ocr_engine
        from rapidocr_onnxruntime import RapidOCR

        # 纯 CPU 推理；模型随 pip 包内置，无需联网下载
        _ocr_engine = RapidOCR()
        return _ocr_engine


def are_models_loaded() -> bool:
    return len(_sessions) > 0 or _ocr_engine is not None


def letterbox(
    img: np.ndarray,
    new_shape: tuple[int, int] = (640, 640),
    color: tuple[int, int, int] = (114, 114, 114),
) -> tuple[np.ndarray, float, tuple[float, float]]:
    """等比缩放 + 灰边填充到目标尺寸，返回图、缩放比例、padding。"""
    import cv2

    shape = img.shape[:2]  # h, w
    if isinstance(new_shape, int):
        new_shape = (new_shape, new_shape)

    r = min(new_shape[0] / shape[0], new_shape[1] / shape[1])
    new_unpad = (int(round(shape[1] * r)), int(round(shape[0] * r)))
    dw, dh = new_shape[1] - new_unpad[0], new_shape[0] - new_unpad[1]
    dw, dh = dw / 2, dh / 2

    if shape[::-1] != new_unpad:
        img = cv2.resize(img, new_unpad, interpolation=cv2.INTER_LINEAR)

    top, bottom = int(round(dh - 0.1)), int(round(dh + 0.1))
    left, right = int(round(dw - 0.1)), int(round(dw + 0.1))
    img = cv2.copyMakeBorder(
        img, top, bottom, left, right, cv2.BORDER_CONSTANT, value=color
    )
    return img, r, (left, top)


def preprocess(img_bgr: np.ndarray, imgsz: int = 640) -> tuple[np.ndarray, float, tuple[float, float]]:
    """YOLO 标准预处理：letterbox + BGR->RGB + NCHW float32 /255。"""
    padded, ratio, pad = letterbox(img_bgr, (imgsz, imgsz))
    rgb = padded[:, :, ::-1]
    tensor = rgb.transpose(2, 0, 1)[None].astype(np.float32) / 255.0
    return np.ascontiguousarray(tensor), ratio, pad


def xywh2xyxy(boxes: np.ndarray) -> np.ndarray:
    out = boxes.copy()
    out[:, 0] = boxes[:, 0] - boxes[:, 2] / 2
    out[:, 1] = boxes[:, 1] - boxes[:, 3] / 2
    out[:, 2] = boxes[:, 0] + boxes[:, 2] / 2
    out[:, 3] = boxes[:, 1] + boxes[:, 3] / 2
    return out


def nms(boxes: np.ndarray, scores: np.ndarray, iou_thres: float) -> list[int]:
    """标准非极大值抑制，返回保留的索引。"""
    if len(boxes) == 0:
        return []
    x1, y1, x2, y2 = boxes[:, 0], boxes[:, 1], boxes[:, 2], boxes[:, 3]
    areas = np.maximum(0, x2 - x1) * np.maximum(0, y2 - y1)
    order = scores.argsort()[::-1]

    keep: list[int] = []
    while order.size > 0:
        i = int(order[0])
        keep.append(i)
        if order.size == 1:
            break
        xx1 = np.maximum(x1[i], x1[order[1:]])
        yy1 = np.maximum(y1[i], y1[order[1:]])
        xx2 = np.minimum(x2[i], x2[order[1:]])
        yy2 = np.minimum(y2[i], y2[order[1:]])
        w = np.maximum(0.0, xx2 - xx1)
        h = np.maximum(0.0, yy2 - yy1)
        inter = w * h
        iou = inter / (areas[i] + areas[order[1:]] - inter + 1e-9)
        inds = np.where(iou <= iou_thres)[0]
        order = order[inds + 1]
    return keep


def scale_boxes(
    boxes: np.ndarray,
    ratio: float,
    pad: tuple[float, float],
    orig_shape: tuple[int, int],
) -> np.ndarray:
    """将 letterbox 坐标还原到原图坐标。"""
    boxes[:, [0, 2]] -= pad[0]
    boxes[:, [1, 3]] -= pad[1]
    boxes /= ratio
    boxes[:, [0, 2]] = boxes[:, [0, 2]].clip(0, orig_shape[1])
    boxes[:, [1, 3]] = boxes[:, [1, 3]].clip(0, orig_shape[0])
    return boxes
