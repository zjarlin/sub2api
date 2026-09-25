#!/usr/bin/env python3
"""把视频配音流水线包装成 HTTP 服务，供 edge-media 的 MEDIA_VIDEO_UPSTREAM_URL 调用。

edge-media 把上传视频以原始字节 POST 到 /dub（见 edge-media/app/api/media_tasks.py），
本服务同步跑完 ASR -> 曼波 TTS -> 对齐 -> 回封，直接返回 output.mp4 字节。
"""
from __future__ import annotations

import tempfile
from pathlib import Path

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import FileResponse

from dub_video import dub_video

app = FastAPI(title="edge-dub")


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok", "service": "edge-dub"}


@app.post("/dub")
async def handle_dub(request: Request):
    body = await request.body()
    if not body:
        raise HTTPException(status_code=400, detail="video body is required")
    filename = Path(request.headers.get("X-Media-Filename", "input.mp4")).name or "input.mp4"
    workdir = Path(tempfile.mkdtemp(prefix="dub_http_"))
    input_path = workdir / filename
    input_path.write_bytes(body)
    output_path = workdir / "output.mp4"
    try:
        dub_video(input_path, output_path, workdir=workdir / "work")
    except Exception as exc:  # noqa: BLE001 - 统一转成 502 返回给上游
        raise HTTPException(status_code=502, detail=f"dubbing failed: {exc}") from exc
    if not output_path.is_file():
        raise HTTPException(status_code=502, detail="dubbing produced no output")
    return FileResponse(output_path, media_type="video/mp4", filename="output.mp4")


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=int(__import__("os").environ.get("PORT", "18084")))
