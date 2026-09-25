#!/usr/bin/env python3
"""开源视频配音流水线：ASR -> 翻译 -> 曼波 TTS -> 时间轴对齐 -> 混音回封。

由 edge-media 的 MEDIA_DUBBING_COMMAND 调用，输入输出通过环境变量传递：

- MEDIA_INPUT      输入视频路径
- MEDIA_OUTPUT     输出视频路径
- MEDIA_TASK_DIR   任务工作目录
- MEDIA_METADATA_JSON  任务 options（JSON）

流程完全基于开源组件：
- ffmpeg 抽音轨 / 拼接 / 回封
- faster-whisper 做语音识别（可选 GPU / CPU）
- GPT-SoVITS（曼波音色）HTTP API 做逐段语音合成，按目标时长做变速对齐

设计取舍：逐段合成再按原时间轴拼接，保证画面与配音同步；翻译层默认关闭，
只做“同语言重配音”，需要翻译时通过 MEDIA_DUB_TRANSLATE_* 接入。
"""
from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass
from pathlib import Path

import httpx


def env(name: str, default: str = "") -> str:
    return os.environ.get(name, default).strip()


def run(cmd: list[str], **kwargs) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, check=True, capture_output=True, text=True, **kwargs)


def probe_duration(path: Path) -> float:
    result = subprocess.run(
        [
            "ffprobe",
            "-v",
            "error",
            "-show_entries",
            "format=duration",
            "-of",
            "default=noprint_wrappers=1:nokey=1",
            str(path),
        ],
        check=True,
        capture_output=True,
        text=True,
    )
    return float(result.stdout.strip())


@dataclass
class Segment:
    start: float
    end: float
    text: str


def transcribe(audio: Path, language: str | None) -> list[Segment]:
    """用 faster-whisper 识别转写，返回按时间排序的分段。"""
    from faster_whisper import WhisperModel

    model_name = env("MEDIA_DUB_WHISPER_MODEL", "large-v3")
    device = env("MEDIA_DUB_WHISPER_DEVICE", "auto")
    compute_type = env("MEDIA_DUB_WHISPER_COMPUTE", "float16" if device != "cpu" else "int8")
    model = WhisperModel(model_name, device=device, compute_type=compute_type)
    raw_segments, info = model.transcribe(
        str(audio),
        language=language or None,
        vad_filter=True,
        beam_size=int(env("MEDIA_DUB_WHISPER_BEAM", "5")),
    )
    segments = [Segment(float(s.start), float(s.end), (s.text or "").strip()) for s in raw_segments]
    return [s for s in segments if s.text]


def synthesize(text: str, language: str, output: Path) -> None:
    """调用曼波 GPT-SoVITS 合成单段语音。"""
    upstream = env("MEDIA_TTS_UPSTREAM_URL")
    if not upstream:
        raise RuntimeError("MEDIA_TTS_UPSTREAM_URL is required for dubbing")
    params = {"text": text, "text_language": language, "format": "wav"}
    refer = env("MEDIA_TTS_REFER_WAV")
    prompt_text = env("MEDIA_TTS_PROMPT_TEXT")
    prompt_language = env("MEDIA_TTS_PROMPT_LANGUAGE", "zh")
    if refer and prompt_text:
        params.update(
            {
                "refer_wav_path": refer,
                "prompt_text": prompt_text,
                "prompt_language": prompt_language,
            }
        )
    timeout = float(env("MEDIA_TTS_TIMEOUT_SECONDS", "180"))
    with httpx.Client(timeout=timeout) as client:
        response = client.get(upstream, params=params)
    response.raise_for_status()
    if not response.content:
        raise RuntimeError("TTS upstream returned empty audio")
    output.write_bytes(response.content)


def fit_audio(source: Path, target_seconds: float, output: Path) -> None:
    """把合成语音贴到目标时长：短了补静音，长了用 atempo 加速（封顶 1.6x）。"""
    actual = probe_duration(source)
    if actual <= 0:
        shutil.copy(source, output)
        return
    ratio = actual / target_seconds if target_seconds > 0 else 1.0
    filters: list[str] = []
    if ratio > 1.02:
        tempo = min(ratio, 1.6)
        filters.append(f"atempo={tempo:.4f}")
    pad = max(target_seconds - actual / (min(ratio, 1.6) if ratio > 1.02 else 1.0), 0.0)
    filter_expr = ",".join(filters + [f"apad=pad_dur={pad:.3f}", "atrim=0:{:.3f}".format(target_seconds)])
    run(
        [
            "ffmpeg",
            "-hide_banner",
            "-y",
            "-i",
            str(source),
            "-af",
            filter_expr,
            "-ar",
            "32000",
            "-ac",
            "1",
            str(output),
        ]
    )


def build_track(segments: list[Segment], language: str, workdir: Path) -> Path:
    """逐段合成并对齐到原始时间轴，拼接成完整配音轨。"""
    pieces: list[Path] = []
    cursor = 0.0
    for index, segment in enumerate(segments):
        target = max(segment.end - segment.start, 0.2)
        if segment.start > cursor + 0.01:
            gap = workdir / f"gap_{index:04d}.wav"
            run(
                [
                    "ffmpeg",
                    "-hide_banner",
                    "-y",
                    "-f",
                    "lavfi",
                    "-i",
                    "anullsrc=r=32000:cl=mono",
                    "-t",
                    f"{segment.start - cursor:.3f}",
                    str(gap),
                ]
            )
            pieces.append(gap)
        raw = workdir / f"seg_{index:04d}_raw.wav"
        fitted = workdir / f"seg_{index:04d}.wav"
        synthesize(segment.text, language, raw)
        fit_audio(raw, target, fitted)
        pieces.append(fitted)
        cursor = segment.start + probe_duration(fitted)
    if not pieces:
        raise RuntimeError("no dubbing segments were produced")
    list_file = workdir / "concat.txt"
    list_file.write_text("".join(f"file '{p.name}'\n" for p in pieces), encoding="utf-8")
    track = workdir / "dub_track.wav"
    run(
        [
            "ffmpeg",
            "-hide_banner",
            "-y",
            "-f",
            "concat",
            "-safe",
            "0",
            "-i",
            str(list_file),
            "-ar",
            "32000",
            "-ac",
            "1",
            str(track),
        ],
        cwd=workdir,
    )
    return track


def mux(video: Path, track: Path, output: Path, keep_original: bool) -> None:
    """把配音轨回封到原视频；默认压低原声而不是完全静音，保留环境音底噪。"""
    if keep_original:
        run(
            [
                "ffmpeg",
                "-hide_banner",
                "-y",
                "-i",
                str(video),
                "-i",
                str(track),
                "-filter_complex",
                "[0:a]volume=0.15[bg];[1:a]volume=1.0[dub];[bg][dub]amix=inputs=2:duration=first:dropout_transition=0[aout]",
                "-map",
                "0:v",
                "-map",
                "[aout]",
                "-c:v",
                "copy",
                "-c:a",
                "aac",
                "-shortest",
                str(output),
            ]
        )
    else:
        run(
            [
                "ffmpeg",
                "-hide_banner",
                "-y",
                "-i",
                str(video),
                "-i",
                str(track),
                "-map",
                "0:v",
                "-map",
                "1:a",
                "-c:v",
                "copy",
                "-c:a",
                "aac",
                "-shortest",
                str(output),
            ]
        )


def dub_video(
    input_path: Path,
    output_path: Path,
    workdir: Path | None = None,
    options: dict | None = None,
) -> Path:
    """执行完整配音流程，返回输出路径。CLI 与 HTTP 服务共用同一入口。"""
    workdir = Path(workdir or tempfile.mkdtemp(prefix="dub_"))
    workdir.mkdir(parents=True, exist_ok=True)
    opts = (options or {}).get("options", options) if isinstance(options, dict) else {}
    opts = opts or {}
    language = str(opts.get("language") or env("MEDIA_DUB_LANGUAGE", "zh"))
    keep_original = str(opts.get("keep_original_audio", env("MEDIA_DUB_KEEP_ORIGINAL", "false"))).lower() in {
        "1",
        "true",
        "yes",
    }
    started = time.time()
    audio = workdir / "source.wav"
    run(
        [
            "ffmpeg",
            "-hide_banner",
            "-y",
            "-i",
            str(input_path),
            "-vn",
            "-ac",
            "1",
            "-ar",
            "16000",
            str(audio),
        ]
    )
    segments = transcribe(audio, language)
    print(f"[dub] transcribed {len(segments)} segments in {time.time() - started:.1f}s", flush=True)
    if not segments:
        raise RuntimeError("no speech detected in input video")
    track = build_track(segments, language, workdir)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    mux(input_path, track, output_path, keep_original)
    print(
        f"[dub] wrote {output_path} ({output_path.stat().st_size} bytes) in {time.time() - started:.1f}s",
        flush=True,
    )
    return output_path


def main() -> None:
    options = json.loads(env("MEDIA_METADATA_JSON", "{}") or "{}")
    dub_video(
        input_path=Path(env("MEDIA_INPUT")),
        output_path=Path(env("MEDIA_OUTPUT")),
        workdir=Path(env("MEDIA_TASK_DIR", tempfile.mkdtemp(prefix="dub_"))),
        options=options,
    )


if __name__ == "__main__":
    try:
        main()
    except subprocess.CalledProcessError as exc:
        print(exc.stderr or exc.stdout or str(exc), file=sys.stderr)
        sys.exit(exc.returncode or 1)
