"""
图像解码、ONNX 元数据解析与通用后处理工具。
"""
from __future__ import annotations

from io import BytesIO

import numpy as np
from PIL import Image, ImageOps


def read_image(upload_bytes: bytes) -> np.ndarray:
    """将上传字节解码为 BGR uint8 图像（兼容 EXIF 方向）。"""
    pil = Image.open(BytesIO(upload_bytes))
    pil = ImageOps.exif_transpose(pil)
    if pil.mode not in ("RGB", "L"):
        pil = pil.convert("RGB")
    rgb = np.array(pil.convert("RGB"))
    return rgb[:, :, ::-1].copy()


def parse_names(metadata_map: dict) -> dict[int, str]:
    """解析 ONNX 内嵌的 names 元数据，形如 {0: 'person', 1: 'bicycle'}。"""
    import ast

    raw = metadata_map.get("names", "")
    if not raw:
        return {}
    try:
        parsed = ast.literal_eval(raw)
        if isinstance(parsed, dict):
            return {int(k): str(v) for k, v in parsed.items()}
    except (ValueError, SyntaxError):
        pass
    return {}
