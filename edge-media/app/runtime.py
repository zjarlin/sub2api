"""媒体文件、配置与子进程/HTTP 上游的共享运行工具。"""
from __future__ import annotations

import json
import os
import shutil
import subprocess
import tempfile
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import httpx

from app.config import MediaConfig


@dataclass
class MediaTask:
    task_id: str
    kind: str
    status: str
    input_path: Path | None = None
    output_path: Path | None = None
    error: str = ""
    metadata: dict[str, Any] | None = None

    def public(self) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "task_id": self.task_id,
            "kind": self.kind,
            "status": self.status,
        }
        if self.error:
            payload["error"] = self.error
        if self.output_path is not None and self.output_path.exists():
            payload["output"] = {
                "path": f"/media/tasks/{self.task_id}/content",
                "bytes": self.output_path.stat().st_size,
            }
        if self.metadata:
            payload["metadata"] = self.metadata
        return payload


def safe_filename(name: str | None, default: str) -> str:
    base = os.path.basename((name or "").strip()) or default
    base = "".join(ch for ch in base if ch.isalnum() or ch in "._- ()[]")
    return base[:180] or default


def ensure_data_dir(cfg: MediaConfig) -> Path:
    cfg.data_dir.mkdir(parents=True, exist_ok=True)
    return cfg.data_dir


def task_dir(cfg: MediaConfig, task_id: str) -> Path:
    return ensure_data_dir(cfg) / "tasks" / task_id


def new_task_id(prefix: str = "media") -> str:
    return f"{prefix}_{uuid.uuid4().hex[:16]}"


def write_upload(cfg: MediaConfig, task_id: str, filename: str, data: bytes) -> Path:
    if len(data) > cfg.max_upload_bytes:
        raise ValueError(f"upload exceeds MEDIA_MAX_UPLOAD_BYTES ({cfg.max_upload_bytes} bytes)")
    directory = task_dir(cfg, task_id)
    directory.mkdir(parents=True, exist_ok=True)
    path = directory / safe_filename(filename, "input.bin")
    path.write_bytes(data)
    return path


async def read_upload(cfg: MediaConfig, task_id: str, upload: "Any", filename: str) -> Path:
    """按上限分块读取上传，避免把超过限制的文件整体读入内存后再拒绝。"""
    directory = task_dir(cfg, task_id)
    directory.mkdir(parents=True, exist_ok=True)
    path = directory / safe_filename(filename, "input.bin")
    total = 0
    with path.open("wb") as handle:
        while True:
            chunk = await upload.read(1024 * 1024)
            if not chunk:
                break
            total += len(chunk)
            if total > cfg.max_upload_bytes:
                handle.close()
                path.unlink(missing_ok=True)
                raise ValueError(
                    f"upload exceeds MEDIA_MAX_UPLOAD_BYTES ({cfg.max_upload_bytes} bytes)"
                )
            handle.write(chunk)
    return path


def write_task_state(cfg: MediaConfig, task: "MediaTask") -> None:
    """把任务状态落到任务目录，使 /tasks/{id} 在进程重启后仍能读到最终结果。"""
    directory = task_dir(cfg, task.task_id)
    directory.mkdir(parents=True, exist_ok=True)
    state = task.public()
    (directory / "task.json").write_text(json_text(state), encoding="utf-8")


def read_task_state(cfg: MediaConfig, task_id: str) -> dict[str, Any] | None:
    path = task_dir(cfg, task_id) / "task.json"
    if not path.is_file():
        return None
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except ValueError:
        return None
    return value if isinstance(value, dict) else None


def require_file(path: Path | None, name: str) -> Path:
    if path is None or not path.is_file():
        raise ValueError(f"{name} is not configured or does not exist")
    return path


def run_command(
    command: str,
    *,
    env: dict[str, str] | None = None,
    timeout: int = 3600,
    cwd: Path | None = None,
) -> subprocess.CompletedProcess[str]:
    if not command.strip():
        raise ValueError("command is not configured")
    merged_env = os.environ.copy()
    if env:
        merged_env.update(env)
    return subprocess.run(
        command,
        shell=True,
        check=False,
        capture_output=True,
        text=True,
        timeout=timeout,
        cwd=str(cwd) if cwd else None,
        env=merged_env,
    )


async def post_json_upstream(
    url: str,
    body: dict[str, Any],
    *,
    timeout: int,
    api_key: str = "",
) -> httpx.Response:
    headers = {"Content-Type": "application/json"}
    if api_key:
        headers["Authorization"] = f"Bearer {api_key}"
    async with httpx.AsyncClient(timeout=timeout) as client:
        return await client.post(url, json=body, headers=headers)


async def post_bytes_upstream(
    url: str,
    body: bytes,
    *,
    content_type: str,
    timeout: int,
    headers: dict[str, str] | None = None,
) -> httpx.Response:
    request_headers = {"Content-Type": content_type}
    if headers:
        request_headers.update(headers)
    async with httpx.AsyncClient(timeout=timeout) as client:
        return await client.post(url, content=body, headers=request_headers)


def copy_or_move(src: Path, dst: Path) -> Path:
    dst.parent.mkdir(parents=True, exist_ok=True)
    if src.resolve() == dst.resolve():
        return dst
    shutil.copy2(src, dst)
    return dst


def json_text(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))
