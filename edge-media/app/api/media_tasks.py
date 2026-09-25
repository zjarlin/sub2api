"""视频配音、视频生成与通用媒体任务执行。"""
from __future__ import annotations

import asyncio
from pathlib import Path
from typing import Any

from fastapi import HTTPException, UploadFile
from fastapi.responses import FileResponse

from app.config import MediaConfig
from app.runtime import (
    MediaTask,
    json_text,
    new_task_id,
    post_bytes_upstream,
    post_json_upstream,
    read_upload,
    read_task_state,
    run_command,
    task_dir,
    write_task_state,
)


def _command_env(cfg: MediaConfig, task: MediaTask, output: Path) -> dict[str, str]:
    metadata = task.metadata or {}
    return {
        "MEDIA_TASK_ID": task.task_id,
        "MEDIA_TASK_KIND": task.kind,
        "MEDIA_INPUT": str(task.input_path or ""),
        "MEDIA_OUTPUT": str(output),
        "MEDIA_DATA_DIR": str(cfg.data_dir),
        "MEDIA_TASK_DIR": str(task_dir(cfg, task.task_id)),
        "MEDIA_METADATA_JSON": json_text(metadata),
    }


def _validate_video_upload(video: UploadFile) -> None:
    filename = (video.filename or "").lower()
    content_type = (video.content_type or "").lower()
    allowed_suffixes = (".mp4", ".mov", ".mkv", ".webm", ".avi", ".m4v", ".flv", ".ts")
    if not filename.endswith(allowed_suffixes) and not content_type.startswith("video/"):
        raise HTTPException(status_code=400, detail="video upload must be a video file")


def _run_sync_command(cfg: MediaConfig, task: MediaTask, command: str, output: Path) -> None:
    result = run_command(
        command,
        env=_command_env(cfg, task, output),
        timeout=cfg.dubbing_timeout_seconds,
        cwd=task_dir(cfg, task.task_id),
    )
    if result.returncode != 0:
        detail = (result.stderr or result.stdout or "command failed").strip()
        raise RuntimeError(detail[-4000:])
    if not output.exists():
        raise RuntimeError(f"command completed without creating output: {output}")


async def dub_video(cfg: MediaConfig, video: UploadFile, body: dict[str, Any]) -> dict[str, Any]:
    if not cfg.dubbing_enabled:
        raise HTTPException(status_code=503, detail="video dubbing is disabled")
    _validate_video_upload(video)
    task_id = new_task_id("dub")
    try:
        input_path = await read_upload(cfg, task_id, video, video.filename or "input.mp4")
    except ValueError as exc:
        raise HTTPException(status_code=413, detail=str(exc)) from exc
    output = task_dir(cfg, task_id) / "output.mp4"
    task = MediaTask(
        task_id=task_id,
        kind="dubbing",
        status="queued",
        input_path=input_path,
        output_path=output,
        metadata={"filename": input_path.name, "options": body},
    )
    if cfg.dubbing_command:
        task.status = "running"
        try:
            await asyncio.to_thread(_run_sync_command, cfg, task, cfg.dubbing_command, output)
            task.status = "succeeded"
        except RuntimeError as exc:
            task.status = "failed"
            task.error = str(exc)
    elif cfg.video_upstream_url:
        task.status = "running"
        response = await post_bytes_upstream(
            cfg.video_upstream_url.rstrip("/") + "/dub",
            input_path.read_bytes(),
            content_type=video.content_type or "application/octet-stream",
            timeout=cfg.video_timeout_seconds,
            headers={"X-Media-Filename": input_path.name, "X-Media-Task-Id": task_id},
        )
        if response.status_code < 200 or response.status_code >= 300:
            task.status = "failed"
            task.error = response.text[:1000] or "video upstream failed"
        else:
            output.write_bytes(response.content)
            task.status = "succeeded"
    else:
        task.status = "blocked"
        task.error = "configure MEDIA_DUBBING_COMMAND or MEDIA_VIDEO_UPSTREAM_URL"
    write_task_state(cfg, task)
    return task.public()


async def generate_video(cfg: MediaConfig, body: dict[str, Any]) -> dict[str, Any]:
    if not cfg.video_generation_enabled:
        raise HTTPException(status_code=503, detail="video generation is not configured")
    task_id = new_task_id("video")
    directory = task_dir(cfg, task_id)
    directory.mkdir(parents=True, exist_ok=True)
    output = directory / "output.mp4"
    task = MediaTask(
        task_id=task_id,
        kind="video_generation",
        status="queued",
        output_path=output,
        metadata={"options": body},
    )
    if cfg.video_generation_command:
        task.status = "running"
        result = await asyncio.to_thread(
            run_command,
            cfg.video_generation_command,
            _command_env(cfg, task, output),
            cfg.video_generation_timeout_seconds,
            directory,
        )
        if result.returncode != 0:
            task.status = "failed"
            task.error = (result.stderr or result.stdout or "command failed").strip()[-4000:]
        elif not output.exists():
            task.status = "failed"
            task.error = f"command completed without creating output: {output}"
        else:
            task.status = "succeeded"
        write_task_state(cfg, task)
        return task.public()

    task.status = "running"
    response = await post_json_upstream(
        cfg.video_generation_upstream_url.rstrip("/") + "/videos/generations",
        body,
        timeout=cfg.video_generation_timeout_seconds,
    )
    if response.status_code < 200 or response.status_code >= 300:
        task.status = "failed"
        task.error = response.text[:1000] or "video generation upstream failed"
        write_task_state(cfg, task)
        return task.public()
    content_type = response.headers.get("content-type", "")
    if "application/json" in content_type:
        payload = response.json()
        task.status = "succeeded" if payload.get("status") in {None, "succeeded", "completed"} else "running"
        task.metadata = {"upstream": payload}
        write_task_state(cfg, task)
        return task.public()
    output.write_bytes(response.content)
    task.status = "succeeded"
    write_task_state(cfg, task)
    return task.public()


async def transcode_video(cfg: MediaConfig, video: UploadFile, body: dict[str, Any]) -> dict[str, Any]:
    _validate_video_upload(video)
    task_id = new_task_id("transcode")
    try:
        input_path = await read_upload(cfg, task_id, video, video.filename or "input.mp4")
    except ValueError as exc:
        raise HTTPException(status_code=413, detail=str(exc)) from exc
    directory = task_dir(cfg, task_id)
    output = directory / "output.mp4"
    task = MediaTask(
        task_id=task_id,
        kind="transcode",
        status="running",
        input_path=input_path,
        output_path=output,
        metadata={"options": body},
    )
    command = cfg.transcode_command or (
        "ffmpeg -y -i \"$MEDIA_INPUT\" -c:v libx264 -preset veryfast "
        "-c:a aac -movflags +faststart \"$MEDIA_OUTPUT\""
    )
    try:
        await asyncio.to_thread(_run_sync_command, cfg, task, command, output)
    except RuntimeError as exc:
        task.status = "failed"
        task.error = str(exc)
        write_task_state(cfg, task)
        return task.public()
    task.status = "succeeded"
    write_task_state(cfg, task)
    return task.public()


def task_content(cfg: MediaConfig, task_id: str) -> FileResponse:
    directory = task_dir(cfg, task_id)
    for name in ("output.mp4", "output.wav", "output.mp3", "output.webm"):
        path = directory / name
        if path.is_file():
            return FileResponse(path, filename=path.name)
    raise HTTPException(status_code=404, detail="task output not found")


def task_state(cfg: MediaConfig, task_id: str) -> dict[str, Any] | None:
    """读取持久化的任务状态；缺失时按磁盘产物回填，兼容旧任务目录。"""
    state = read_task_state(cfg, task_id)
    if state is not None:
        return state
    directory = task_dir(cfg, task_id)
    if not directory.is_dir():
        return None
    output = directory / "output.mp4"
    return {
        "task_id": task_id,
        "status": "succeeded" if output.exists() else "unknown",
        "output": {"path": f"/media/tasks/{task_id}/content", "bytes": output.stat().st_size}
        if output.exists()
        else None,
    }
