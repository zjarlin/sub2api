#!/usr/bin/env python3
"""应用 GPT-SoVITS 在海光 DCU 上运行所需的源码补丁。

上游 GPT-SoVITS 默认假设 CUDA/NVIDIA。海光 DCU 走 DTK + HIP，torch 的
算子与设备语义大体一致，但有两处会导致推理失败或无声，这个脚本在镜像构建
阶段把它们固化成可重复的补丁：

1. sv.py 的 compute_embedding3 用 CPU 上的 wav 计算 fbank，再把结果喂给已
   搬到 GPU 的 ERes2NetV2，造成 conv2d 权重在 cuda、输入在 cpu。
2. torchaudio shim 的 resample 在 CPU 上重采样后没有搬回原设备，导致
   RefEncoder 的 Linear/Conv 出现 device mismatch。
"""
from __future__ import annotations

import pathlib
import sys

SOVITS_ROOT = pathlib.Path("/opt/GPT-SoVITS")
PATCHES = []


def patch_sv_device() -> None:
    path = SOVITS_ROOT / "GPT_SoVITS" / "sv.py"
    source = path.read_text(encoding="utf-8")
    old = """            sv_emb = self.embedding_model.forward3(feat)
        return sv_emb"""
    new = """            model_device = next(self.embedding_model.parameters()).device
            if feat.device != model_device:
                feat = feat.to(model_device)
            sv_emb = self.embedding_model.forward3(feat)
        return sv_emb"""
    if new in source:
        PATCHES.append("sv.py already patched")
    elif old in source:
        path.write_text(source.replace(old, new), encoding="utf-8")
        PATCHES.append("patched sv.py compute_embedding3 device")
    else:
        print("sv.py anchor not found", file=sys.stderr)
        sys.exit(1)


def main() -> None:
    patch_sv_device()
    for item in PATCHES:
        print(item)


if __name__ == "__main__":
    main()
