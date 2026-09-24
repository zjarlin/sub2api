"""Laya OpenAI 兼容层单元测试。

这些测试不加载真实权重：适配层只依赖注入的 `predict` 可调用对象，因此可以在
无 torch / 无模型的环境里验证协议转换与原生字段透传。

    pytest -q edge-laya/tests/test_openai_compat.py
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

# edge-laya 以 `python -m app` 运行，测试时把该目录加入导入路径。
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from app.openai_compat import (  # noqa: E402
    build_call,
    create_openai_router,
    extract_envelope,
    input_to_text,
)
from app.server import resolve_checkpoint  # noqa: E402

CANNED = {
    "model": "laya-rl-agent",
    "answers": {
        "spam": {
            "type": "choice",
            "choice": "A",
            "probabilities": {"A": 0.97, "B": 0.03},
            "confidence": 0.81,
        }
    },
    "usage": {"input_tokens": 33, "output_tokens": 0},
    "routing": {"model": "english", "reason": "English Latin text"},
}


class Recorder:
    """记录每次 predict 的实参，便于断言透传结果。"""

    def __init__(self, payload=None):
        self.calls = []
        self.payload = payload or CANNED

    def __call__(self, state, questions, model=None, **kwargs):
        self.calls.append({"state": state, "questions": questions, "model": model, **kwargs})
        return self.payload


@pytest.fixture()
def client_factory():
    def build(recorder, available_models=None):
        app = FastAPI()
        app.include_router(create_openai_router(recorder, available_models=available_models))
        return TestClient(app)

    return build


# --------------------------------------------------------------------------- 模型名

def test_resolve_checkpoint_laya_auto_routes():
    """`laya` 与未知模型名必须回落到自动选路（None），不得硬编码 english。

    上游 `laya.router.normalise_name` 把 `laya` 别名成 english，而网关只放行
    `model: "laya"`；若沿用该别名，中文请求会被强制送到读不了中文的英文检查点。
    """
    assert resolve_checkpoint("laya") is None
    assert resolve_checkpoint("gpt-5") is None
    assert resolve_checkpoint("jev-1") is None
    assert resolve_checkpoint(None) is None
    assert resolve_checkpoint("") is None


def test_resolve_checkpoint_explicit_names():
    assert resolve_checkpoint("laya-english") == "english"
    assert resolve_checkpoint("laya-multilingual") == "multilingual"
    assert resolve_checkpoint("laya-typed-decisions") == "typed-decisions"
    assert resolve_checkpoint("MULTILINGUAL") == "multilingual"
    assert resolve_checkpoint("convaiinnovations/laya") is None


# --------------------------------------------------------------------------- 信封解析

def test_extract_envelope_from_dict_and_json_string():
    envelope = {"state": "hello", "questions": {"q": {"type": "noul", "instructions": "?"}}}
    assert extract_envelope(envelope) == ("hello", envelope["questions"], None, None)
    assert extract_envelope(json.dumps(envelope)) == ("hello", envelope["questions"], None, None)


def test_extract_envelope_carries_task_and_lang():
    envelope = {
        "state": "Mein Konto",
        "questions": {"q": {"type": "choice", "instructions": "?", "criteria": {"A": "a"}}},
        "task": "typed-decisions",
        "lang": "de",
    }
    assert extract_envelope(envelope) == ("Mein Konto", envelope["questions"], "typed-decisions", "de")


def test_extract_envelope_none_for_plain_text():
    assert extract_envelope("just some text") is None
    assert extract_envelope(12345) is None


def test_input_to_text_handles_input_items():
    assert input_to_text("plain") == "plain"
    assert input_to_text([{"role": "user", "content": "hi"}]) == "hi"
    assert input_to_text([{"content": [{"type": "input_text", "text": "parts"}]}]) == "parts"


# --------------------------------------------------------------------------- build_call

def test_build_call_keeps_native_state_object():
    """state 是对象时必须原样透传，不能被串成 Python repr。"""
    state = {"from": "a@b.com", "body": "charged twice"}
    questions = {"dept": {"type": "choice", "instructions": "?", "criteria": {"A": "a"}}}
    call = build_call("laya", state, questions, None, None)
    assert call["state"] == state
    assert call["questions"] == questions
    # build_call 只做协议翻译，不解析模型：原样带给统一的 predict。
    assert call["model"] == "laya"


def test_build_call_does_not_force_english_when_model_omitted():
    call = build_call(None, "text", {}, None, None)
    assert call["model"] is None
    assert call["questions"] == {
        "sentiment": {
            "type": "choice",
            "instructions": "What is the sentiment of this text?",
            "criteria": {
                "positive": "positive or approving",
                "negative": "negative or disapproving",
            },
        }
    }


def test_build_call_parses_envelope_from_raw_input():
    envelope = {
        "state": "You won a free iPhone",
        "questions": {"spam": {"type": "choice", "instructions": "?", "criteria": {"A": "spam"}}},
    }
    call = build_call("laya", None, None, None, None, raw_input=envelope, fallback_text="")
    assert call["state"] == "You won a free iPhone"
    assert "spam" in call["questions"]


def test_build_call_inner_envelope_task_lang_do_not_override_explicit():
    envelope = {"state": "s", "questions": {"q": {"type": "noul", "instructions": "?"}},
                "task": "inner", "lang": "de"}
    call = build_call("laya", None, None, "outer", "fr", raw_input=envelope)
    assert call["task"] == "outer"
    assert call["lang"] == "fr"


# --------------------------------------------------------------------------- HTTP 面

def test_chat_completions_passes_native_object(client_factory):
    recorder = Recorder()
    client = client_factory(recorder)
    envelope = {
        "state": {"body": "We were billed twice."},
        "questions": {"dept": {"type": "choice", "instructions": "?", "criteria": {"billing": "refunds"}}},
    }
    resp = client.post(
        "/v1/chat/completions",
        json={"model": "laya", "messages": [{"role": "user", "content": envelope}]},
    )
    assert resp.status_code == 200
    body = resp.json()
    assert body["object"] == "chat.completion"
    assert body["choices"][0]["finish_reason"] == "stop"
    # 决策模型不生成 token：completion_tokens 必须为 0。
    assert body["usage"] == {"prompt_tokens": 33, "completion_tokens": 0, "total_tokens": 33}
    assert json.loads(body["choices"][0]["message"]["content"])["answers"]["spam"]["choice"] == "A"

    call = recorder.calls[0]
    assert call["state"] == {"body": "We were billed twice."}
    # 客户端值原样透传，由统一的 resolve_checkpoint 决定是否自动选路。
    assert call["model"] == "laya"


def test_chat_completions_forces_checkpoint(client_factory):
    recorder = Recorder()
    client = client_factory(recorder)
    resp = client.post(
        "/v1/chat/completions",
        json={
            "model": "laya-multilingual",
            "messages": [{"role": "user", "content": "{\"state\":\"s\",\"questions\":{\"q\":{\"type\":\"noul\",\"instructions\":\"?\"}}}"}],
        },
    )
    assert resp.status_code == 200
    assert recorder.calls[0]["model"] == "laya-multilingual"  # 原样透传，由 server 解析


def test_responses_accepts_object_input(client_factory):
    recorder = Recorder()
    client = client_factory(recorder)
    envelope = {
        "state": "You won a free iPhone",
        "questions": {"spam": {"type": "choice", "instructions": "Is it spam?", "criteria": {"A": "spam"}}},
    }
    resp = client.post("/v1/responses", json={"model": "laya", "input": envelope, "stream": False})
    assert resp.status_code == 200
    body = resp.json()
    assert body["object"] == "response"
    assert body["status"] == "completed"
    assert body["usage"] == {"input_tokens": 33, "output_tokens": 0, "total_tokens": 33}
    assert recorder.calls[0]["state"] == "You won a free iPhone"


def test_responses_forwards_task_and_lang(client_factory):
    recorder = Recorder()
    client = client_factory(recorder)
    resp = client.post(
        "/v1/responses",
        json={
            "model": "laya",
            "input": {"state": "Mein Konto", "questions": {"q": {"type": "noul", "instructions": "?"}}},
            "task": "typed-decisions",
            "lang": "de",
        },
    )
    assert resp.status_code == 200
    assert recorder.calls[0]["task"] == "typed-decisions"
    assert recorder.calls[0]["lang"] == "de"


def test_chat_completions_streams_terminal_done(client_factory):
    recorder = Recorder()
    client = client_factory(recorder)
    with client.stream(
        "POST",
        "/v1/chat/completions",
        json={"model": "laya", "messages": [{"role": "user", "content": "hello"}], "stream": True},
    ) as resp:
        assert resp.status_code == 200
        payload = "".join(resp.iter_text())
    assert "chat.completion.chunk" in payload
    assert "finish_reason" in payload
    assert payload.rstrip().endswith("data: [DONE]")


def test_responses_streams_terminal_completed(client_factory):
    recorder = Recorder()
    client = client_factory(recorder)
    with client.stream(
        "POST",
        "/v1/responses",
        json={"model": "laya", "input": "hello", "stream": True},
    ) as resp:
        assert resp.status_code == 200
        payload = "".join(resp.iter_text())
    assert "response.created" in payload
    assert "response.completed" in payload


def test_models_endpoint_lists_only_mounted_checkpoints(client_factory):
    """默认只挂载 english + multilingual，不得宣称支持未提供的 typed-decisions。"""
    client = client_factory(Recorder())
    body = client.get("/v1/models").json()
    ids = {item["id"] for item in body["data"]}
    assert ids == {"laya", "laya-english", "laya-multilingual"}


def test_models_endpoint_includes_typed_decisions_when_mounted(client_factory):
    client = client_factory(Recorder(), available_models=("english", "multilingual", "typed-decisions"))
    ids = {item["id"] for item in client.get("/v1/models").json()["data"]}
    assert ids == {"laya", "laya-english", "laya-multilingual", "laya-typed-decisions"}


def test_models_endpoint_excludes_unmounted_checkpoint(client_factory):
    """未挂载的检查点不得出现在 /v1/models 里。"""
    client = client_factory(Recorder())
    ids = {item["id"] for item in client.get("/v1/models").json()["data"]}
    assert "laya-typed-decisions" not in ids


def test_predict_failure_surfaces_422(client_factory):
    def boom(state, questions, **kwargs):
        raise ValueError("bad tokenizer")

    app = FastAPI()
    app.include_router(create_openai_router(boom))
    client = TestClient(app)
    resp = client.post("/v1/responses", json={"model": "laya", "input": "hello"})
    assert resp.status_code == 422


# ------------------------------------------------- 两条通道必须解析出同一个检查点

class _FakeRouter:
    """server.create_app 只依赖 `router.predict`，这里按该契约替身。"""

    def __init__(self, seen):
        self._seen = seen

    def predict(self, state, questions, model=None, **kwargs):
        self._seen["last"] = model
        return CANNED


def _edge_laya_client(recorder):
    """按 server.create_app 的真实接线构造应用（含原生 + 兼容两面）。"""
    from app.server import create_app

    saw = {}
    app = create_app(_FakeRouter(saw))
    return TestClient(app), saw


def test_both_faces_resolve_laya_identically():
    """同一个 model='laya' 在两条通道上必须解析成同一个检查点。

    上游 `normalise_name` 把 `laya` 别名成 english；若沿用，中文会被送到读不了
    中文的英文检查点。这里锁定「laya = 自动选路」这一公开契约。
    """
    from app.server import resolve_checkpoint

    assert resolve_checkpoint("laya") is None

    recorder = Recorder()
    client, saw = _edge_laya_client(recorder)

    native = client.post(
        "/v1/systemone",
        json={"model": "laya", "state": "s", "questions": {"q": {"type": "noul", "instructions": "?"}}},
    )
    assert native.status_code == 200
    native_checkpoint = saw["last"]

    compat = client.post(
        "/v1/responses",
        json={"model": "laya", "input": {"state": "s", "questions": {"q": {"type": "noul", "instructions": "?"}}}},
    )
    assert compat.status_code == 200
    compat_checkpoint = saw["last"]

    # 两条通道必须交给 Router 同一个检查点；`laya` 一律是 None（自动选路）。
    assert native_checkpoint is None
    assert compat_checkpoint is None


def test_both_faces_agree_on_explicit_checkpoint():
    recorder = Recorder()
    client, saw = _edge_laya_client(recorder)

    client.post(
        "/v1/systemone",
        json={"model": "laya-multilingual", "state": "s",
              "questions": {"q": {"type": "noul", "instructions": "?"}}},
    )
    native_checkpoint = saw["last"]

    client.post(
        "/v1/responses",
        json={"model": "laya-multilingual",
              "input": {"state": "s", "questions": {"q": {"type": "noul", "instructions": "?"}}}},
    )
    assert saw["last"] == native_checkpoint == "multilingual"


def test_systemone_rejects_unmounted_checkpoint():
    recorder = Recorder()
    client, _ = _edge_laya_client(recorder)
    resp = client.post(
        "/v1/systemone",
        json={"model": "laya-typed-decisions", "state": "s",
              "questions": {"q": {"type": "noul", "instructions": "?"}}},
    )
    assert resp.status_code == 400
    assert "typed-decisions" in resp.json()["detail"]


def test_systemone_requires_questions_field():
    recorder = Recorder()
    client, _ = _edge_laya_client(recorder)
    resp = client.post("/v1/systemone", json={"model": "laya", "state": "s"})
    assert resp.status_code == 400


def test_health_reports_loaded_checkpoints():
    recorder = Recorder()
    client, _ = _edge_laya_client(recorder)
    body = client.get("/health").json()
    assert body["status"] == "ok"
    assert body["loaded"] == ["english", "multilingual"]
