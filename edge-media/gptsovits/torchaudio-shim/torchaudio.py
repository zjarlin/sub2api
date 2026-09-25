"""torchaudio 兼容层，用 soundfile + librosa 实现 GPT-SoVITS 用到的最小子集。

海光 DTK 镜像自带 torch 但没有 torchaudio，PyPI 上的 torchaudio 与 DTK 版
torch ABI 不兼容。GPT-SoVITS 只用到 load / save / functional.resample /
transforms.Resample / _extension，这里逐一实现，避免引入不兼容的二进制 wheel。

注意：librosa 在 CPU 上重采样，这里必须把结果搬回输入张量所在设备，否则在
GPU 上运行时下游模块会出现 “mat2 is on cuda:0, different from other
tensors on cpu” 的设备不一致错误。
"""
from __future__ import annotations

from pathlib import Path
from typing import Any

import numpy as np
import soundfile as sf
import torch

__version__ = "2.9.0-shim"


def load(
    filepath: str | Path,
    frame_offset: int = 0,
    num_frames: int = -1,
    normalize: bool = True,
    channels_first: bool = True,
    **_: Any,
):
    data, sample_rate = sf.read(str(filepath), dtype="float32", always_2d=True)
    if frame_offset:
        data = data[frame_offset:]
    if num_frames and num_frames > 0:
        data = data[:num_frames]
    tensor = torch.from_numpy(np.ascontiguousarray(data.T if channels_first else data))
    return tensor, sample_rate


def save(filepath: str | Path, src: torch.Tensor, sample_rate: int, **_: Any) -> None:
    array = src.detach().cpu().numpy()
    if array.ndim == 2 and array.shape[0] <= 8:
        array = array.T
    sf.write(str(filepath), array, int(sample_rate))


class _Functional:
    @staticmethod
    def resample(waveform: torch.Tensor, orig_freq: int, new_freq: int, **_: Any) -> torch.Tensor:
        if orig_freq == new_freq:
            return waveform
        import librosa

        device = waveform.device
        array = waveform.detach().cpu().numpy()
        resampled = librosa.resample(array, orig_sr=orig_freq, target_sr=new_freq, axis=-1)
        out = torch.from_numpy(np.ascontiguousarray(resampled)).to(waveform.dtype)
        return out.to(device)

    @staticmethod
    def spectrogram(*args: Any, **kwargs: Any):
        return torch.stft(*args, **kwargs)


functional = _Functional()


class transforms:  # noqa: N801 - 模拟 torchaudio.transforms 命名空间
    class Resample(torch.nn.Module):
        def __init__(self, orig_freq: int, new_freq: int, **_: Any) -> None:
            super().__init__()
            self.orig_freq = orig_freq
            self.new_freq = new_freq

        def forward(self, waveform: torch.Tensor) -> torch.Tensor:
            return functional.resample(waveform, self.orig_freq, self.new_freq)


class _Extension:
    @staticmethod
    def fail_if_no_sox() -> None:
        return None

    @staticmethod
    def fail_if_no_kaldi() -> None:
        return None


_extension = _Extension()
__all__ = ["load", "save", "functional", "transforms", "_extension", "__version__"]
