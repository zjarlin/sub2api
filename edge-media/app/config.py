"""edge-media 运行配置。

所有外部模型都通过环境变量接入。默认只开启不需要 GPU 的媒体处理能力，TTS
与视频生成必须显式配置上游命令或 HTTP 地址，避免服务在未准备权重时静默失败。
"""
from __future__ import annotations

import os
from dataclasses import dataclass, field
from pathlib import Path


def _bool(name: str, default: bool = False) -> bool:
    value = os.environ.get(name)
    if value is None:
        return default
    return value.strip().lower() in {"1", "true", "yes", "on"}


def _int(name: str, default: int) -> int:
    value = os.environ.get(name)
    if value is None or not value.strip():
        return default
    try:
        return int(value)
    except ValueError:
        return default


def _path(name: str, default: str = "") -> Path | None:
    value = os.environ.get(name, "").strip()
    if not value:
        return Path(default) if default else None
    return Path(value).expanduser()


@dataclass(frozen=True)
class MediaConfig:
    data_dir: Path
    max_upload_bytes: int
    tts_enabled: bool
    tts_upstream_url: str
    tts_timeout_seconds: int
    tts_refer_wav: Path | None
    tts_prompt_text: str
    tts_prompt_language: str
    tts_default_language: str
    video_enabled: bool
    video_upstream_url: str
    video_timeout_seconds: int
    video_command: str
    video_generation_enabled: bool
    video_generation_upstream_url: str
    video_generation_timeout_seconds: int
    video_generation_command: str
    dubbing_enabled: bool
    dubbing_command: str
    dubbing_timeout_seconds: int
    transcode_command: str
    capabilities: tuple[str, ...] = field(default_factory=tuple)


def load_config() -> MediaConfig:
    data_dir = _path("MEDIA_DATA_DIR", "/data") or Path("/data")
    tts_url = os.environ.get("MEDIA_TTS_UPSTREAM_URL", "").strip().rstrip("/")
    video_url = os.environ.get("MEDIA_VIDEO_UPSTREAM_URL", "").strip().rstrip("/")
    generation_url = os.environ.get("MEDIA_VIDEO_GENERATION_UPSTREAM_URL", "").strip().rstrip("/")
    command = os.environ.get("MEDIA_VIDEO_COMMAND", "").strip()
    generation_command = os.environ.get("MEDIA_VIDEO_GENERATION_COMMAND", "").strip()
    dubbing_command = os.environ.get("MEDIA_DUBBING_COMMAND", "").strip()

    capabilities: list[str] = ["dubbing"]
    if tts_url:
        capabilities.append("tts")
    if video_url or command:
        capabilities.append("video")
    if generation_url or generation_command:
        capabilities.append("video_generation")

    return MediaConfig(
        data_dir=data_dir,
        max_upload_bytes=_int("MEDIA_MAX_UPLOAD_BYTES", 512 * 1024 * 1024),
        tts_enabled=_bool("MEDIA_TTS_ENABLED", bool(tts_url)),
        tts_upstream_url=tts_url,
        tts_timeout_seconds=_int("MEDIA_TTS_TIMEOUT_SECONDS", 180),
        tts_refer_wav=_path("MEDIA_TTS_REFER_WAV"),
        tts_prompt_text=os.environ.get("MEDIA_TTS_PROMPT_TEXT", "").strip(),
        tts_prompt_language=os.environ.get("MEDIA_TTS_PROMPT_LANGUAGE", "zh").strip() or "zh",
        tts_default_language=os.environ.get("MEDIA_TTS_DEFAULT_LANGUAGE", "zh").strip() or "zh",
        video_enabled=_bool("MEDIA_VIDEO_ENABLED", bool(video_url or dubbing_command)),
        video_upstream_url=video_url,
        video_timeout_seconds=_int("MEDIA_VIDEO_TIMEOUT_SECONDS", 3600),
        video_command=command,
        video_generation_enabled=_bool(
            "MEDIA_VIDEO_GENERATION_ENABLED", bool(generation_url or generation_command)
        ),
        video_generation_upstream_url=generation_url,
        video_generation_timeout_seconds=_int("MEDIA_VIDEO_GENERATION_TIMEOUT_SECONDS", 3600),
        video_generation_command=generation_command,
        dubbing_enabled=_bool("MEDIA_DUBBING_ENABLED", True),
        dubbing_command=dubbing_command,
        dubbing_timeout_seconds=_int("MEDIA_DUBBING_TIMEOUT_SECONDS", 7200),
        transcode_command=os.environ.get("MEDIA_TRANSCODE_COMMAND", "").strip(),
        capabilities=tuple(capabilities),
    )
