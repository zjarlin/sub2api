"""Edge Media - 曼波配音、视频配音与视频生成边缘服务。"""
from __future__ import annotations

import logging
import os
from typing import Any

from fastapi import FastAPI, File, Form, HTTPException, UploadFile
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse

from app.api import media_tasks, tts
from app.config import MediaConfig, load_config

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger("edge-media")

config: MediaConfig = load_config()

app = FastAPI(
    title="Edge Media",
    description="边缘媒体服务：曼波/GPT-SoVITS TTS、视频配音与视频生成任务编排",
    version="1.0.0",
    root_path=os.environ.get("ROOT_PATH", ""),
)
app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=False,
    allow_methods=["*"],
    allow_headers=["*"],
)


@app.get("/health")
async def health() -> dict[str, Any]:
    return {
        "status": "ok",
        "service": "edge-media",
        "version": "1.0.0",
        "capabilities": list(config.capabilities),
        "tts_enabled": config.tts_enabled,
        "video_generation_enabled": config.video_generation_enabled,
        "dubbing_enabled": config.dubbing_enabled,
    }


@app.get("/")
async def root() -> dict[str, Any]:
    return {
        "service": "edge-media",
        "docs": "/docs",
        "endpoints": [
            "/health",
            "/tts",
            "/v1/audio/speech",
            "/videos/dub",
            "/videos/generations",
            "/videos/transcode",
            "/tasks/{task_id}/content",
        ],
    }


@app.post("/tts")
@app.post("/v1/audio/speech")
async def tts_route(body: dict[str, Any]):
    return await tts.synthesize(config, body)


@app.post("/videos/dub")
async def dub_route(video: UploadFile = File(...), options: str = Form("{}")):
    try:
        parsed = __import__("json").loads(options or "{}")
    except ValueError as exc:
        raise HTTPException(status_code=400, detail="options must be a JSON object") from exc
    if not isinstance(parsed, dict):
        raise HTTPException(status_code=400, detail="options must be a JSON object")
    return await media_tasks.dub_video(config, video, parsed)


@app.post("/videos/generations")
async def video_generation_route(body: dict[str, Any]):
    return await media_tasks.generate_video(config, body)


@app.post("/videos/transcode")
async def transcode_route(video: UploadFile = File(...), options: str = Form("{}")):
    try:
        parsed = __import__("json").loads(options or "{}")
    except ValueError as exc:
        raise HTTPException(status_code=400, detail="options must be a JSON object") from exc
    if not isinstance(parsed, dict):
        raise HTTPException(status_code=400, detail="options must be a JSON object")
    return await media_tasks.transcode_video(config, video, parsed)


@app.get("/tasks/{task_id}")
async def task_status(task_id: str):
    state = media_tasks.task_state(config, task_id)
    if state is None:
        raise HTTPException(status_code=404, detail="task not found")
    return state


@app.get("/tasks/{task_id}/content")
async def task_content(task_id: str):
    return media_tasks.task_content(config, task_id)


@app.exception_handler(Exception)
async def unhandled_exception_handler(request, exc):
    logger.exception("未处理异常: %s", exc)
    return JSONResponse(status_code=500, content={"detail": "内部服务错误"})
