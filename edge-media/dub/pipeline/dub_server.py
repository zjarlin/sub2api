#!/usr/bin/env python3
"""内网 multipart 视频配音接口，完成后返回 MP4。兼容旧版原始字节请求。"""
from __future__ import annotations

import asyncio
import json
import os
import shutil
import subprocess
import tempfile
from pathlib import Path

from fastapi import FastAPI, HTTPException, Request
from fastapi.concurrency import run_in_threadpool
from fastapi.responses import FileResponse
from starlette.background import BackgroundTask
from starlette.datastructures import UploadFile

from dub_video import dub_video, validate_options

app = FastAPI(title="edge-dub")
capacity = asyncio.Semaphore(1)
MAX_UPLOAD_BYTES = int(os.environ.get("MEDIA_MAX_UPLOAD_BYTES", str(512 * 1024 * 1024)))


@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok", "service": "edge-dub"}


async def save_upload(request: Request, directory: Path) -> tuple[Path, dict]:
    if request.headers.get("content-type", "").startswith("multipart/form-data"):
        async with request.form(max_files=1, max_fields=1) as form:
            video = form.get("video")
            if not isinstance(video, UploadFile):
                raise ValueError("video must be an uploaded file")
            options = validate_options(json.loads(str(form.get("options", "{}"))))
            source = directory / ("input" + Path(video.filename or "input.mp4").suffix)
            total = 0
            with source.open("wb") as target:
                while chunk := await video.read(1024 * 1024):
                    total += len(chunk)
                    if total > MAX_UPLOAD_BYTES:
                        raise HTTPException(status_code=413, detail="video exceeds upload limit")
                    target.write(chunk)
    else:
        source, options, total = directory / "input.mp4", {}, 0
        with source.open("wb") as target:
            async for chunk in request.stream():
                total += len(chunk)
                if total > MAX_UPLOAD_BYTES:
                    raise HTTPException(status_code=413, detail="video exceeds upload limit")
                target.write(chunk)
    if not total:
        raise ValueError("video is empty")
    return source, options


@app.post("/dub")
async def handle_dub(request: Request):
    directory = Path(tempfile.mkdtemp(prefix="dub_http_"))
    try:
        source, options = await save_upload(request, directory)
        output = directory / "output.mp4"
        # 一次一个任务，避免多次 ASR 加载挤占内存；耗时工作离开事件循环。
        async with capacity:
            await run_in_threadpool(dub_video, source, output, directory / "work", options)
        if not output.is_file() or not output.stat().st_size:
            raise RuntimeError("dubbing produced no output")
        return FileResponse(output, media_type="video/mp4", filename="output.mp4",
                            background=BackgroundTask(shutil.rmtree, directory, ignore_errors=True))
    except BaseException as exc:
        shutil.rmtree(directory, ignore_errors=True)
        if isinstance(exc, ValueError):
            raise HTTPException(status_code=400, detail=str(exc)) from exc
        if isinstance(exc, subprocess.CalledProcessError):
            raise HTTPException(status_code=502, detail=f"media processing failed: {(exc.stderr or str(exc))[-1500:]}") from exc
        if isinstance(exc, Exception) and not isinstance(exc, HTTPException):
            raise HTTPException(status_code=502, detail=f"dubbing failed: {exc}") from exc
        raise


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=int(os.environ.get("PORT", "18084")))
