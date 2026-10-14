"""Edge Mail - 边缘计算邮件收发服务。

独立部署的 IMAP/SMTP 收发件 API 网关，支持接入 Outlook / Google / QQ 等
主流邮箱。所有账号信息随请求传入，服务不持久化任何凭据或邮件内容。

对外统一经 Sub2API 网关鉴权与计费，暴露在 ``/mail/*``；容器本身不加宿主端口。
"""
from __future__ import annotations

import logging
import os
from typing import Any
from urllib.parse import quote

from fastapi import FastAPI, HTTPException, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse, Response

from app.api import mailbox, send as send_api
from app.config import MailConfig, load_config
from app.mail_client import MailError
from app.providers import UnknownProvider, list_providers

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger("edge-mail")

config: MailConfig = load_config()

app = FastAPI(
    title="Edge Mail",
    description="边缘计算邮件服务：IMAP 收件 + SMTP 发件，支持 Outlook / Google / QQ 等邮箱",
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


@app.exception_handler(MailError)
async def mail_error_handler(request: Request, exc: MailError) -> JSONResponse:
    # 只回传错误类型与人类可读信息，绝不回显凭据。
    return JSONResponse(status_code=exc.status, content={"error": {"type": exc.kind, "message": exc.message}})


@app.exception_handler(UnknownProvider)
async def provider_error_handler(request: Request, exc: UnknownProvider) -> JSONResponse:
    return JSONResponse(
        status_code=400,
        content={
            "error": {
                "type": "invalid_request_error",
                "message": f"unknown provider '{exc.key}'; pass provider explicitly or use a recognized domain",
            }
        },
    )


@app.get("/health")
async def health() -> dict[str, Any]:
    return {
        "status": "ok",
        "service": "edge-mail",
        "version": "1.0.0",
        "capabilities": list(config.capabilities),
        "send_enabled": config.send_enabled,
        "providers": [preset["key"] for preset in list_providers()],
    }


@app.get("/")
async def root() -> dict[str, Any]:
    return {
        "service": "edge-mail",
        "docs": "/docs",
        "endpoints": [
            "/health",
            "/providers",
            "/folders",
            "/messages",
            "/message",
            "/attachment",
            "/message/flags",
            "/send",
        ],
    }


@app.get("/providers")
async def providers() -> dict[str, Any]:
    """列出支持的服务商预设及其 IMAP/SMTP 参数，供前端渲染账号表单。"""
    return {"providers": list_providers()}


@app.post("/folders")
async def folders(body: dict[str, Any]) -> dict[str, Any]:
    return mailbox.list_folders(config, body)


@app.post("/messages")
async def messages(body: dict[str, Any]) -> dict[str, Any]:
    return mailbox.list_messages(config, body)


@app.post("/message")
async def message(body: dict[str, Any]) -> dict[str, Any]:
    return mailbox.get_message(config, body)


@app.post("/attachment")
async def attachment(body: dict[str, Any]) -> Response:
    filename, content_type, payload = mailbox.get_attachment(config, body)
    headers = {
        "Content-Disposition": f"attachment; filename*=UTF-8''{quote(filename)}",
        "Content-Length": str(len(payload)),
    }
    return Response(content=payload, media_type=content_type or "application/octet-stream", headers=headers)


@app.post("/message/flags")
async def message_flags(body: dict[str, Any]) -> dict[str, Any]:
    return mailbox.update_message(config, body)


@app.post("/send")
async def send_route(body: dict[str, Any]) -> dict[str, Any]:
    return send_api.send(config, body)


@app.get("/internal/context", include_in_schema=False)
async def runtime_context() -> dict[str, Any]:
    """仅返回运行参数白名单；网关不转发此路径，只供管理员上下文读取。"""
    return {
        "send_enabled": config.send_enabled,
        "max_fetch": config.max_fetch,
        "max_attachment_bytes": config.max_attachment_bytes,
        "allowed_providers": list(config.allowed_providers),
        "allow_external_recipients": config.allow_external_recipients,
        "connect_timeout_seconds": config.connect_timeout_seconds,
        "operation_timeout_seconds": config.operation_timeout_seconds,
    }


@app.exception_handler(Exception)
async def unhandled_exception_handler(request: Request, exc: Exception) -> JSONResponse:
    logger.exception("未处理异常: %s", exc)
    return JSONResponse(status_code=500, content={"error": {"type": "internal_error", "message": "内部服务错误"}})
