"""曼波 / GPT-SoVITS 文本转语音适配。"""
from __future__ import annotations

import base64
import json
from pathlib import Path
from typing import Any

import httpx
from fastapi import HTTPException
from fastapi.responses import JSONResponse, Response

from app.config import MediaConfig


def _runes(text: str) -> int:
    return len(text)


def _resolve_text(body: dict[str, Any]) -> str:
    text = body.get("text")
    if text is None:
        text = body.get("input")
    if text is None and isinstance(body.get("messages"), list):
        parts: list[str] = []
        for message in body["messages"]:
            if not isinstance(message, dict):
                continue
            content = message.get("content")
            if isinstance(content, str):
                parts.append(content)
            elif isinstance(content, list):
                for item in content:
                    if isinstance(item, dict) and isinstance(item.get("text"), str):
                        parts.append(item["text"])
        text = "".join(parts)
    value = str(text or "").strip()
    if not value:
        raise HTTPException(status_code=400, detail="text is required")
    if len(value) > 20000:
        raise HTTPException(status_code=413, detail="text is too long (max 20000 characters)")
    return value


def _response_format(body: dict[str, Any]) -> str:
    value = str(body.get("response_format") or body.get("format") or "wav").strip().lower()
    if value not in {"wav", "mp3"}:
        raise HTTPException(status_code=400, detail="response_format must be wav or mp3")
    return value


async def synthesize(cfg: MediaConfig, body: dict[str, Any]) -> Response:
    text = _resolve_text(body)
    response_format = _response_format(body)
    if not cfg.tts_enabled or not cfg.tts_upstream_url:
        raise HTTPException(status_code=503, detail="TTS upstream is not configured")
    language = str(
        body.get("language") or body.get("text_language") or cfg.tts_default_language
    ).strip() or cfg.tts_default_language
    refer_wav = str(body.get("refer_wav_path") or "").strip()
    if not refer_wav and cfg.tts_refer_wav is not None:
        refer_wav = str(cfg.tts_refer_wav)
    params: dict[str, str] = {
        "text": text,
        "text_language": language,
        "format": response_format,
    }
    if refer_wav:
        params["refer_wav_path"] = refer_wav
    prompt_text = str(body.get("prompt_text") or cfg.tts_prompt_text).strip()
    if prompt_text:
        params["prompt_text"] = prompt_text
    prompt_language = str(body.get("prompt_language") or cfg.tts_prompt_language).strip()
    if prompt_language:
        params["prompt_language"] = prompt_language
    speed = body.get("speed")
    if speed is not None:
        params["speed"] = str(speed)

    timeout = httpx.Timeout(cfg.tts_timeout_seconds)
    # GPT-SoVITS api.py 的合成入口是根路径 `GET /?text=...`；edge-media 对外的
    # `/tts` 只是适配层路径，不能原样追加到上游，否则会命中上游的 404。
    upstream_url = cfg.tts_upstream_url
    if upstream_url.rstrip("/").endswith("/tts"):
        upstream_url = upstream_url.rstrip("/")[: -len("/tts")]
    async with httpx.AsyncClient(timeout=timeout) as client:
        upstream = await client.get(upstream_url, params=params)
    if upstream.status_code < 200 or upstream.status_code >= 300:
        detail = upstream.text[:1000] if upstream.text else "TTS upstream failed"
        raise HTTPException(status_code=502, detail=detail)
    if not upstream.content:
        raise HTTPException(status_code=502, detail="TTS upstream returned empty audio")

    content_type = upstream.headers.get("content-type") or "audio/wav"
    if response_format == "mp3" and "audio/mpeg" not in content_type and "audio/mp3" not in content_type:
        # 部分 GPT-SoVITS 分支即使传 format=mp3 仍返回 wav；保持上游字节和真实类型，
        # 由调用方按实际 Content-Type 解码，避免前端拿到错误的扩展名。
        response_format = "wav" if content_type.startswith("audio/wav") else response_format
    if body.get("return_base64"):
        return JSONResponse(
            {
                "data": [{"b64_json": base64.b64encode(upstream.content).decode("ascii")}],
                "format": response_format,
                "characters": _runes(text),
            }
        )
    return Response(content=upstream.content, media_type=content_type)


def validate_refer_wav(path: Path | None) -> None:
    if path is not None and not path.is_file():
        raise ValueError(f"TTS reference audio does not exist: {path}")


def load_preset(path: Path) -> dict[str, Any]:
    data = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(data, dict):
        raise ValueError("preset must be a JSON object")
    return data
