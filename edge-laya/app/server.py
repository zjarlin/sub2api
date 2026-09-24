"""edge-laya 的 HTTP 入口：原生 System One 契约 + OpenAI 兼容面。

两条通道刻意共用一个 `resolve_checkpoint`，保证同一个 `model` 取值在两边解析成
同一个检查点。这是本目录最重要的不变量：兼容面只是协议翻译，不得改变决策语义。

关于 `model` 的取值（与上游 `laya.serve` 的默认别名表有意不同）：

| 取值 | 解析结果 |
|---|---|
| 省略 / 空 | 自动路由（按语言与字符集选检查点） |
| `laya` | 自动路由 —— 这是 `LayaModelID` 公开契约 |
| `laya-english` / `english` | 强制英文检查点 |
| `laya-multilingual` / `multilingual` | 强制多语言检查点 |
| `laya-typed-decisions` / `typed-decisions` | 强制 typed-decisions 工作流 |
| 其它未知值 | 自动路由（兼容 Jev 客户端自带的 `model` 字段） |

上游 `laya.router.normalise_name` 把 `laya` 别名成 `english`，网关又只放行
`model: "laya"`，两者叠加会让中文请求被强制送到读不了中文的英文检查点。
因此这里显式覆盖该别名：`laya` 一律表示「本服务、自动选检查点」，需要锁定
检查点时用显式名字。
"""
from __future__ import annotations

import asyncio
import os
from concurrent.futures import ThreadPoolExecutor
from typing import Any, Dict, Optional

from fastapi import FastAPI, Header, HTTPException, Request

from .openai_compat import create_openai_router

# 公开模型名 → 检查点（None 表示自动路由）。
#
# `laya` 是本服务的公开名，必须自动选路；上游把它别名成 english，这里有意覆盖。
_CHECKPOINT_ALIASES: Dict[str, Optional[str]] = {
    "laya": None,
    "laya-english": "english",
    "laya-multilingual": "multilingual",
    "laya-typed-decisions": "typed-decisions",
    "convaiinnovations/laya": None,
    "convaiinnovations/laya-multilingual": "multilingual",
    "convaiinnovations/laya-typed-decisions": "typed-decisions",
    "english": "english",
    "multilingual": "multilingual",
    "typed-decisions": "typed-decisions",
}

# 本镜像挂载的检查点；未挂载的名字一律拒绝，避免 Router 用默认表去联网下载。
AVAILABLE_CHECKPOINTS = ("english", "multilingual")


def resolve_checkpoint(model: Any) -> Optional[str]:
    """把公开模型名解析成检查点；None 表示自动路由。

    未知名字按自动路由处理：Jev 客户端会在 `model` 里带自己的模型标识
    （例如 `jev-1`），这不是错误，只是不需要锁定 Laya 检查点。
    """
    key = str(model or "").strip()
    if not key:
        return None
    return _CHECKPOINT_ALIASES.get(key.lower(), None)


def ensure_available(checkpoint: Optional[str]) -> Optional[str]:
    if checkpoint is not None and checkpoint not in AVAILABLE_CHECKPOINTS:
        raise HTTPException(
            status_code=400,
            detail=f"checkpoint {checkpoint!r} is not available on this instance; "
                   f"available: {', '.join(AVAILABLE_CHECKPOINTS)}",
        )
    return checkpoint


def create_app(router: Any, api_key: Optional[str] = None) -> FastAPI:
    """构造 edge-laya 应用。

    `router` 需已经 preload 好 `AVAILABLE_CHECKPOINTS`。推理是同步 torch 调用，
    放到单工作线程串行执行，避免阻塞事件循环（否则 `/health` 会一起卡住）。
    """
    if api_key is None:
        api_key = os.environ.get("LAYA_API_KEY") or None

    pool = ThreadPoolExecutor(max_workers=1, thread_name_prefix="laya-infer")
    gate: Optional[asyncio.Lock] = None

    app = FastAPI(title="edge-laya", version="1.0.0")

    def check_auth(authorization: Optional[str]) -> None:
        if not api_key:
            return
        expected = f"Bearer {api_key}"
        if authorization != expected:
            raise HTTPException(status_code=401, detail="invalid or missing bearer token")

    async def predict(state: Any, questions: Dict[str, Any], model: Any) -> Dict[str, Any]:
        nonlocal gate
        checkpoint = ensure_available(resolve_checkpoint(model))
        kwargs: Dict[str, Any] = {}
        if checkpoint:
            kwargs["model"] = checkpoint
        if gate is None:
            gate = asyncio.Lock()
        loop = asyncio.get_running_loop()
        async with gate:
            return await loop.run_in_executor(
                pool, lambda: router.predict(state, questions, **kwargs)
            )

    @app.get("/health")
    async def health() -> Dict[str, Any]:
        return {
            "status": "ok",
            "loaded": list(AVAILABLE_CHECKPOINTS),
            "device": os.environ.get("LAYA_DEVICE", "cpu"),
        }

    @app.post("/v1/systemone")
    async def systemone(
        request: Request,
        authorization: Optional[str] = Header(default=None),
    ) -> Dict[str, Any]:
        check_auth(authorization)
        body = await request.json()
        if not isinstance(body, dict) or "questions" not in body:
            raise HTTPException(
                status_code=400, detail="request body must be an object with a 'questions' field"
            )
        try:
            return await predict(body.get("state"), body["questions"], body.get("model"))
        except HTTPException:
            raise
        except Exception as exc:  # noqa: BLE001 -- 模型/分词器错误按 422 暴露
            raise HTTPException(status_code=422, detail=f"{type(exc).__name__}: {exc}") from exc

    # OpenAI 兼容面复用同一个 predict，保证两条通道解析与推理完全一致。
    app.include_router(
        create_openai_router(predict, available_models=AVAILABLE_CHECKPOINTS)
    )
    return app
