"""Laya 的 OpenAI 兼容面。

Laya 原生契约是 `/v1/systemone`：一次前向传播返回校准概率，没有 token 流，
也没有文本生成。本模块只做协议适配，让现有 OpenAI SDK / Codex 客户端无需手写
`{state, questions}` 信封就能调用同一个决策模型，转发语义与 `/v1/systemone` 完全一致：

- `laya` 与未知模型名 → 交给 Router 自动选检查点；
- `laya-english` / `laya-multilingual` → 强制指定检查点；
- 顶层或 `input` / `messages[].content` 里的 `{state, questions, task, lang}`
  原样透传给 `router.predict`，不做二次编码。

返回体里 `completion_tokens` 恒为 0：Laya 不生成 token，将 `prompt_tokens`
同时计入 `total_tokens`，避免把决策调用伪装成聊天计费。
"""
from __future__ import annotations

import asyncio
import inspect
import json
import time
from typing import Any, Callable, Dict, List, Optional

from fastapi import APIRouter, HTTPException
from fastapi.responses import StreamingResponse
from pydantic import BaseModel

_SENTIMENT_FALLBACK: Dict[str, Any] = {
    "sentiment": {
        "type": "choice",
        "instructions": "What is the sentiment of this text?",
        "criteria": {
            "positive": "positive or approving",
            "negative": "negative or disapproving",
        },
    }
}


class Message(BaseModel):
    role: str
    # 纯文本，或 OpenAI 多段 content parts（字符串/对象数组）。
    content: Any = ""


class ChatCompletionRequest(BaseModel):
    model: Optional[str] = None
    messages: List[Message] = []
    stream: Optional[bool] = False
    # Laya 原生字段；出现时优先于 messages 里的信封。
    state: Any = None
    questions: Optional[Dict[str, Any]] = None
    task: Optional[str] = None
    lang: Optional[str] = None


class ResponsesRequest(BaseModel):
    model: Optional[str] = None
    input: Any = None
    stream: Optional[bool] = False
    # Laya 原生字段；出现时优先于 input 里的信封。
    state: Any = None
    questions: Optional[Dict[str, Any]] = None
    task: Optional[str] = None
    lang: Optional[str] = None


def flatten_content(content: Any) -> str:
    """把 message content 收敛成纯文本。"""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts: List[str] = []
        for item in content:
            if isinstance(item, dict):
                text = item.get("text")
                if isinstance(text, str):
                    parts.append(text)
            elif isinstance(item, str):
                parts.append(item)
        return "\n".join(parts)
    if content is None:
        return ""
    if isinstance(content, (dict, list)):
        return json.dumps(content, ensure_ascii=False)
    return str(content)


def messages_to_text(messages: List[Message]) -> str:
    if not messages:
        return ""
    text = flatten_content(messages[-1].content)
    if len(messages) == 1:
        return text
    history = [flatten_content(m.content) for m in messages[:-1]]
    return "\n".join([part for part in history if part] + [text])


def input_to_text(payload: Any) -> str:
    """OpenAI Responses 的 `input` 可以是字符串，也可以是 input item 数组。"""
    if isinstance(payload, str):
        return payload
    if isinstance(payload, list):
        parts: List[str] = []
        for item in payload:
            if isinstance(item, dict):
                if "content" in item:
                    parts.append(flatten_content(item.get("content")))
                elif isinstance(item.get("text"), str):
                    parts.append(item["text"])
            elif isinstance(item, str):
                parts.append(item)
        return "\n".join(part for part in parts if part)
    if payload is None:
        return ""
    if isinstance(payload, (dict, list)):
        return json.dumps(payload, ensure_ascii=False)
    return str(payload)


def extract_envelope(payload: Any):
    """从载荷里取出 Laya 原生 `{state, questions}`。

    接受：已解析的 dict、包含该信封的 JSON 字符串、或裸的单问题对象
    （带 `type` + `criteria`）。取不到时返回 None。
    """
    data = payload
    if isinstance(data, str):
        try:
            parsed = json.loads(data)
        except Exception:
            return None
        if not isinstance(parsed, dict):
            return None
        data = parsed
    if not isinstance(data, dict):
        return None

    questions = data.get("questions")
    state = data.get("state")
    if isinstance(state, (str, dict, list)) and isinstance(questions, dict) and questions:
        return state, questions, data.get("task"), data.get("lang")

    if "type" in data and "criteria" in data:
        question = {k: v for k, v in data.items() if k not in ("state", "task", "lang")}
        return state if state is not None else "", {"q0": question}, data.get("task"), data.get("lang")
    return None


def build_call(
    model: Optional[str],
    native_state: Any,
    native_questions: Optional[Dict[str, Any]],
    task: Optional[str],
    lang: Optional[str],
    raw_input: Any = None,
    fallback_text: str = "",
) -> Dict[str, Any]:
    """把一次请求解析成 `router.predict` 的实参。

    原生字段优先；其次是 input/content 里的信封；最后退化为「文本 + 情感二分类」。
    """
    if native_state is not None and isinstance(native_questions, dict) and native_questions:
        return {
            "state": native_state,
            "questions": native_questions,
            "model": model,
            "task": task,
            "lang": lang,
        }

    for candidate in (native_state, raw_input, fallback_text):
        if candidate is None:
            continue
        extracted = extract_envelope(candidate)
        if extracted is None:
            continue
        state, questions, inner_task, inner_lang = extracted
        return {
            "state": state,
            "questions": questions,
            "model": model,
            "task": task or inner_task,
            "lang": lang or inner_lang,
        }

    state = fallback_text if native_state is None else native_state
    return {
        "state": state if state is not None else "",
        "questions": _SENTIMENT_FALLBACK,
        "model": model,
        "task": task,
        "lang": lang,
    }


def encode_result(result: Dict[str, Any]) -> str:
    return json.dumps(result, ensure_ascii=False, default=str)


def token_usage(result: Dict[str, Any]) -> Dict[str, int]:
    usage = result.get("usage") or {}
    prompt_tokens = int(usage.get("input_tokens") or 0)
    return {"prompt_tokens": prompt_tokens, "completion_tokens": 0, "total_tokens": prompt_tokens}


def _sse(event: Dict[str, Any]) -> str:
    return f"data: {json.dumps(event, ensure_ascii=False, default=str)}\n\n"


def _chat_payload(result: Dict[str, Any], model: str) -> Dict[str, Any]:
    return {
        "id": f"chatcmpl-{_now_ms()}",
        "object": "chat.completion",
        "created": _now_s(),
        "model": model,
        "choices": [
            {
                "index": 0,
                "message": {"role": "assistant", "content": encode_result(result)},
                "finish_reason": "stop",
            }
        ],
        "usage": token_usage(result),
    }


def _response_object(result: Dict[str, Any], model: str, response_id: str, item_id: str) -> Dict[str, Any]:
    text = encode_result(result)
    usage = token_usage(result)
    return {
        "id": response_id,
        "object": "response",
        "created_at": _now_s(),
        "model": model,
        "status": "completed",
        "output": [
            {
                "type": "message",
                "id": item_id,
                "role": "assistant",
                "status": "completed",
                "content": [{"type": "output_text", "text": text}],
            }
        ],
        "output_text": text,
        "usage": {
            "input_tokens": usage["prompt_tokens"],
            "output_tokens": 0,
            "total_tokens": usage["total_tokens"],
        },
    }


def _chat_stream(result: Dict[str, Any], model: str):
    """Laya 单次前向即得到完整结果，因此只发一个内容分片再收尾。"""
    chunk_id = f"chatcmpl-{_now_ms()}"
    created = _now_s()
    text = encode_result(result)
    yield _sse(
        {
            "id": chunk_id,
            "object": "chat.completion.chunk",
            "created": created,
            "model": model,
            "choices": [
                {"index": 0, "delta": {"role": "assistant", "content": text}, "finish_reason": None}
            ],
        }
    )
    yield _sse(
        {
            "id": chunk_id,
            "object": "chat.completion.chunk",
            "created": created,
            "model": model,
            "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
            "usage": token_usage(result),
        }
    )
    yield "data: [DONE]\n\n"


def _responses_stream(result: Dict[str, Any], model: str):
    """发出 OpenAI Responses 生命周期事件，以 response.completed 收尾。"""
    response_id = f"resp_{_now_ms()}"
    item_id = f"item_{_now_ms()}"
    text = encode_result(result)
    created = _now_s()
    yield _sse(
        {
            "type": "response.created",
            "response": {
                "id": response_id,
                "object": "response",
                "created_at": created,
                "model": model,
                "status": "in_progress",
                "output": [],
            },
        }
    )
    yield _sse(
        {
            "type": "response.output_item.added",
            "output_index": 0,
            "item": {
                "type": "message",
                "id": item_id,
                "role": "assistant",
                "status": "in_progress",
                "content": [],
            },
        }
    )
    yield _sse(
        {
            "type": "response.output_text.delta",
            "item_id": item_id,
            "output_index": 0,
            "content_index": 0,
            "delta": text,
        }
    )
    yield _sse(
        {
            "type": "response.output_text.done",
            "item_id": item_id,
            "output_index": 0,
            "content_index": 0,
            "text": text,
        }
    )
    yield _sse(
        {
            "type": "response.output_item.done",
            "output_index": 0,
            "item": {
                "type": "message",
                "id": item_id,
                "role": "assistant",
                "status": "completed",
                "content": [{"type": "output_text", "text": text}],
            },
        }
    )
    yield _sse(
        {
            "type": "response.completed",
            "response": _response_object(result, model, response_id, item_id),
        }
    )


def _now_s() -> int:
    return int(time.time())


def _now_ms() -> int:
    return int(time.time() * 1000)


# edge-laya 只挂载英文与多语言权重；typed-decisions 未随镜像提供，因此默认不对外宣称支持。
DEFAULT_AVAILABLE_MODELS: tuple = ("english", "multilingual")

# 检查点 → 公开模型名。同名别名（english/multilingual）也接受，但只列公开名。
_CHECKPOINT_PUBLIC_NAME: Dict[str, str] = {
    "english": "laya-english",
    "multilingual": "laya-multilingual",
    "typed-decisions": "laya-typed-decisions",
}


def create_openai_router(
    predict: Callable[..., Dict[str, Any]],
    available_models: Optional[tuple] = None,
) -> APIRouter:
    """基于 `router.predict` 构造 OpenAI 兼容路由。

    推理是同步 torch 调用，单次可达数百毫秒，因此放到单工作线程执行，避免阻塞事件循环
    （否则 `/health` 会一起卡住）。单 worker 是有意为之：一台 CPU 或 GPU 上串行前向
    正是这里的预期负载。

    `available_models` 列出本实例实际挂载的检查点。`Router` 会用上游的默认模型表补齐
    未指定的键（其中 `typed-decisions` 指向 Hugging Face），在离线部署里调用它只会
    失败；因此这里只允许已挂载的检查点，未挂载的请求直接给出明确错误，而不是让它去
    尝试联网下载。
    """
    resolved_available = tuple(available_models or DEFAULT_AVAILABLE_MODELS)

    # 推理、串行化与 model 解析都由注入的 predict 统一负责（见 server.py）。
    # 这里只做协议翻译：把客户端 model 原样透传，避免两条通道解析出不同检查点。
    async def run(call: Dict[str, Any]) -> Dict[str, Any]:
        kwargs: Dict[str, Any] = {}
        if call.get("task"):
            kwargs["task"] = call["task"]
        if call.get("lang"):
            kwargs["lang"] = call["lang"]
        try:
            result = predict(call["state"], call["questions"], call.get("model"), **kwargs)
            if inspect.isawaitable(result):
                result = await result
            return result
        except HTTPException:
            raise
        except Exception as exc:  # noqa: BLE001 -- 模型/分词器错误按 422 暴露
            raise HTTPException(status_code=422, detail=f"{type(exc).__name__}: {exc}") from exc

    router = APIRouter()

    @router.get("/v1/models")
    async def list_models() -> Dict[str, Any]:
        now = _now_s()
        # `laya` 是自动选路入口；其余为本实例已挂载的检查点。
        model_ids = ["laya"] + [
            _CHECKPOINT_PUBLIC_NAME[checkpoint]
            for checkpoint in resolved_available
            if checkpoint in _CHECKPOINT_PUBLIC_NAME
        ]
        return {
            "object": "list",
            "data": [
                {"id": model_id, "object": "model", "created": now, "owned_by": "laya"}
                for model_id in model_ids
            ],
        }

    @router.post("/v1/chat/completions")
    async def chat_completions(req: ChatCompletionRequest):
        call = build_call(
            model=req.model,
            native_state=req.state,
            native_questions=req.questions,
            task=req.task,
            lang=req.lang,
            raw_input=req.messages[-1].content if req.messages else None,
            fallback_text=messages_to_text(req.messages),
        )
        result = await run(call)
        label = req.model or "laya"
        if req.stream:
            return StreamingResponse(_chat_stream(result, label), media_type="text/event-stream")
        return _chat_payload(result, label)

    @router.post("/v1/responses")
    async def responses(req: ResponsesRequest):
        call = build_call(
            model=req.model,
            native_state=req.state,
            native_questions=req.questions,
            task=req.task,
            lang=req.lang,
            raw_input=req.input,
            fallback_text=input_to_text(req.input),
        )
        result = await run(call)
        label = req.model or "laya"
        if req.stream:
            return StreamingResponse(
                _responses_stream(result, label), media_type="text/event-stream"
            )
        return _response_object(result, label, f"resp_{_now_ms()}", f"item_{_now_ms()}")

    return router
