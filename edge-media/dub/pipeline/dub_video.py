#!/usr/bin/env python3
"""视频配音：自动转写或显式时间轴 -> 曼波 TTS -> 对齐 -> ffmpeg 回封。

CLI 从 MEDIA_INPUT / MEDIA_OUTPUT / MEDIA_TASK_DIR / MEDIA_METADATA_JSON 读取任务。
只做同语言配音，不包含翻译、人声分离或口型重建。
"""
from __future__ import annotations

import json
import math
import os
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path

import httpx

MAX_SEGMENTS = 500
MAX_SPEED = 1.6
SAMPLE_RATE = 32000
LANGUAGES = {"zh", "en", "ja", "ko", "yue"}


def env(name: str, default: str = "") -> str:
    return os.environ.get(name, default).strip()


def run(cmd: list[str], **kwargs) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, check=True, capture_output=True, text=True, **kwargs)


def probe(path: Path) -> dict:
    return json.loads(run([
        "ffprobe", "-v", "error", "-show_streams", "-show_format", "-of", "json", str(path),
    ]).stdout)


def probe_duration(path: Path) -> float:
    return float(probe(path)["format"]["duration"])


@dataclass
class Segment:
    start: float
    end: float
    text: str


def validate_segments(items: object, duration: float) -> list[Segment]:
    if not isinstance(items, list) or not 1 <= len(items) <= MAX_SEGMENTS:
        raise ValueError(f"segments must contain 1..{MAX_SEGMENTS} entries")
    segments = []
    previous_end = 0.0
    for index, item in enumerate(items):
        prefix = f"segments[{index}]"
        if not isinstance(item, dict) or set(item) != {"start", "end", "text"}:
            raise ValueError(f"{prefix} requires start, end, text")
        start, end, text = item["start"], item["end"], item["text"]
        if any(isinstance(v, bool) or not isinstance(v, (int, float)) or not math.isfinite(v) for v in (start, end)):
            raise ValueError(f"{prefix} start/end must be finite numbers in seconds")
        if start < 0 or end <= start or end > duration:
            raise ValueError(f"{prefix} requires 0 <= start < end <= video duration ({duration:.3f}s)")
        if start < previous_end:
            raise ValueError(f"{prefix} overlaps the previous segment; keep segments ordered")
        if not isinstance(text, str) or not text.strip() or len(text) > 2000:
            raise ValueError(f"{prefix} text must contain 1..2000 characters")
        segments.append(Segment(float(start), float(end), text.strip()))
        previous_end = end
    return segments


def validate_options(options: object) -> dict:
    if not isinstance(options, dict):
        raise ValueError("options must be a JSON object")
    if set(options) - {"mode", "language", "keep_original_audio", "segments"}:
        raise ValueError("unknown options; supported fields: mode, language, keep_original_audio, segments")
    opts = {"mode": "auto", "language": env("MEDIA_DUB_LANGUAGE", "zh"), "keep_original_audio": False, **options}
    if opts["mode"] not in ("auto", "timeline"):
        raise ValueError("mode must be auto or timeline")
    if not isinstance(opts["language"], str) or opts["language"] not in LANGUAGES:
        raise ValueError("language must be zh, en, ja, ko or yue")
    if not isinstance(opts["keep_original_audio"], bool):
        raise ValueError("keep_original_audio must be a JSON boolean")
    if opts["mode"] == "auto" and "segments" in opts:
        raise ValueError("segments requires mode=timeline")
    return opts


def transcribe(audio: Path, language: str) -> list[Segment]:
    from faster_whisper import WhisperModel

    device = env("MEDIA_DUB_WHISPER_DEVICE", "auto")
    model = WhisperModel(
        env("MEDIA_DUB_WHISPER_MODEL", "large-v3"), device=device,
        compute_type=env("MEDIA_DUB_WHISPER_COMPUTE", "float16" if device != "cpu" else "int8"),
    )
    raw_segments, _ = model.transcribe(
        str(audio), language="zh" if language == "yue" else language, vad_filter=True,
        beam_size=int(env("MEDIA_DUB_WHISPER_BEAM", "5")),
    )
    return [Segment(float(s.start), float(s.end), s.text.strip()) for s in raw_segments if s.text.strip()]


def synthesize(text: str, language: str, output: Path) -> None:
    upstream = env("MEDIA_TTS_UPSTREAM_URL")
    if not upstream:
        raise RuntimeError("MEDIA_TTS_UPSTREAM_URL is required for dubbing")
    params = {"text": text, "text_language": language, "format": "wav"}
    refer, prompt_text = env("MEDIA_TTS_REFER_WAV"), env("MEDIA_TTS_PROMPT_TEXT")
    if refer and prompt_text:
        params.update(refer_wav_path=refer, prompt_text=prompt_text, prompt_language=env("MEDIA_TTS_PROMPT_LANGUAGE", "zh"))
    with httpx.Client(timeout=float(env("MEDIA_TTS_TIMEOUT_SECONDS", "180"))) as client:
        response = client.get(upstream, params=params)
    response.raise_for_status()
    if not response.content:
        raise RuntimeError("TTS upstream returned empty audio")
    output.write_bytes(response.content)


def fit_audio(source: Path, target_seconds: float, output: Path) -> None:
    """短语音补静音，长语音最多加速 1.6 倍；不能容纳时要求用户调整时间轴。"""
    actual = probe_duration(source)
    if not math.isfinite(actual) or actual <= 0:
        raise ValueError("TTS returned invalid audio duration")
    ratio = actual / target_seconds
    if ratio > MAX_SPEED:
        raise ValueError(
            f"speech is too long ({actual:.2f}s) for slot ({target_seconds:.2f}s); "
            f"shorten text or allow at least {actual / MAX_SPEED:.2f}s"
        )
    filters = [f"atempo={max(1, ratio):.8f}", "apad", f"atrim=end_sample={round(target_seconds * SAMPLE_RATE)}"]
    run(["ffmpeg", "-hide_banner", "-y", "-i", str(source), "-ar", str(SAMPLE_RATE), "-ac", "1",
         "-af", f"aresample={SAMPLE_RATE}," + ",".join(filters), str(output)])


def build_track(segments: list[Segment], language: str, workdir: Path, duration: float) -> Path:
    pieces: list[Path] = []
    cursor = 0.0

    def add_silence(seconds: float) -> None:
        if round(seconds * SAMPLE_RATE) <= 0:
            return
        gap = workdir / f"gap_{len(pieces):04d}.wav"
        run(["ffmpeg", "-hide_banner", "-y", "-f", "lavfi", "-i", f"anullsrc=r={SAMPLE_RATE}:cl=mono",
             "-af", f"atrim=end_sample={round(seconds * SAMPLE_RATE)}", str(gap)])
        pieces.append(gap)

    for index, segment in enumerate(segments):
        add_silence(segment.start - cursor)
        raw, fitted = workdir / f"seg_{index:04d}_raw.wav", workdir / f"seg_{index:04d}.wav"
        synthesize(segment.text, language, raw)
        try:
            fit_audio(raw, segment.end - segment.start, fitted)
        except ValueError as exc:
            raise ValueError(f"segments[{index}]: {exc}") from exc
        pieces.append(fitted)
        cursor = segment.end
    add_silence(duration - cursor)
    list_file = workdir / "concat.txt"
    list_file.write_text("".join(f"file '{p.name}'\n" for p in pieces), encoding="utf-8")
    track = workdir / "dub_track.wav"
    run(["ffmpeg", "-hide_banner", "-y", "-f", "concat", "-safe", "0", "-i", str(list_file),
         "-ar", str(SAMPLE_RATE), "-ac", "1", str(track)])
    return track


def mux(video: Path, track: Path, output: Path, keep_original: bool, duration: float) -> None:
    cmd = ["ffmpeg", "-hide_banner", "-y", "-i", str(video), "-i", str(track)]
    if keep_original:
        cmd += ["-filter_complex",
                "[0:a:0]volume=0.15[bg];[1:a:0][bg]amix=inputs=2:duration=first:normalize=0:dropout_transition=0[aout]",
                "-map", "0:v:0", "-map", "[aout]"]
    else:
        cmd += ["-map", "0:v:0", "-map", "1:a:0"]
    # 统一编码便于浏览器播放；尾部静音使最后一句之后的画面也完整保留。
    cmd += ["-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p", "-vf", "pad=ceil(iw/2)*2:ceil(ih/2)*2",
            "-c:a", "aac", "-t", f"{duration:.6f}", "-movflags", "+faststart", str(output)]
    run(cmd)


def dub_video(input_path: Path, output_path: Path, workdir: Path | None = None, options: dict | None = None) -> Path:
    opts = validate_options(options if options is not None else {})
    workdir = Path(workdir or tempfile.mkdtemp(prefix="dub_")).resolve()
    workdir.mkdir(parents=True, exist_ok=True)
    info = probe(input_path)
    videos = [s for s in info["streams"] if s["codec_type"] == "video"]
    if not videos:
        raise ValueError("input must contain a video stream")
    duration = float(videos[0].get("duration") or info["format"]["duration"])
    if not math.isfinite(duration) or duration <= 0:
        raise ValueError("video duration must be positive")
    has_audio = any(s["codec_type"] == "audio" for s in info["streams"])
    if opts["mode"] == "timeline":
        segments = validate_segments(opts.get("segments"), duration)
    else:
        if not has_audio:
            raise ValueError("auto mode requires an audio track; use timeline mode for silent video")
        audio = workdir / "source.wav"
        run(["ffmpeg", "-hide_banner", "-y", "-i", str(input_path), "-vn", "-ac", "1", "-ar", "16000", str(audio)])
        recognized = transcribe(audio, opts["language"])
        if not recognized:
            raise ValueError("no speech detected; use timeline mode with explicit text")
        segments = validate_segments([
            {"start": s.start, "end": min(s.end, duration), "text": s.text} for s in recognized
        ], duration)
    track = build_track(segments, opts["language"], workdir, duration)
    output_path.parent.mkdir(parents=True, exist_ok=True)
    mux(input_path, track, output_path, opts["keep_original_audio"] and has_audio, duration)
    return output_path


def main() -> None:
    metadata = json.loads(env("MEDIA_METADATA_JSON", "{}") or "{}")
    dub_video(Path(env("MEDIA_INPUT")), Path(env("MEDIA_OUTPUT")),
              Path(env("MEDIA_TASK_DIR", tempfile.mkdtemp(prefix="dub_"))), metadata.get("options", metadata))


if __name__ == "__main__":
    try:
        main()
    except (ValueError, subprocess.CalledProcessError) as exc:
        print(str(exc), file=sys.stderr)
        sys.exit(1)
