"""媒体服务商透明中继。

Sub2API 网关在这里集中持有各家素材与文生视频服务商的真实 API Key，对外只
暴露 Sub2API API Key。调用方把各平台的 Base URL 指向网关前缀即可：

    Pexels      -> http://<gateway>/media/relay/pexels
    Pixabay     -> http://<gateway>/media/relay/pixabay
    Coverr      -> http://<gateway>/media/relay/coverr
    OFox        -> http://<gateway>/media/relay/ofox
    秘塔 MiniMax -> http://<gateway>/media/relay/metaso
    MuAPI       -> http://<gateway>/media/relay/muapi
    WaveSpeed   -> http://<gateway>/media/relay/wavespeed
    Seedance    -> http://<gateway>/media/relay/seedance

中继只做「前缀剥离 + 注入上游凭据 + 原样透传」，不做协议转换：各平台仍保留
自己的原生请求与响应格式，因此调用方无需改动解析逻辑。

安全边界：

- 只转发白名单内服务商与固定上游主机，不构成开放代理。
- 上游凭据只从环境变量读取，请求与响应都不回声密钥。
- 入站 Authorization / x-api-key 一律不向上游透传，改为注入服务端凭据，
  避免调用方用自己的网关 Key 冒充上游 Key。

未配置凭据的服务商保持关闭：请求返回 503，`/providers` 也不列出，与
edge-vision / edge-media 既有的「未部署不暴露空端点」约定一致。
"""
from __future__ import annotations

import os
from dataclasses import dataclass
from urllib.parse import parse_qsl, urlencode

import httpx
from fastapi import HTTPException, Request
from fastapi.responses import Response


@dataclass(frozen=True)
class RelayProvider:
    """一个上游服务商的固定中继配置。"""

    key: str
    label: str
    # 上游基地址；中继收到的前缀路径会原样追加在其后。
    base_url: str
    # 持有真实凭据的环境变量名。
    api_key_env: str
    # 注入方式：bearer / raw / x-api-key / query / none。
    auth: str
    # auth=query 时使用的查询参数名。
    auth_query_param: str = "key"
    # 透传给上游的请求头白名单（除 Content-Type 外）。
    forward_headers: tuple[str, ...] = ("Accept",)


# 上游地址固定，不随请求变化；调用方无法改写目标主机。
RELAY_PROVIDERS: dict[str, RelayProvider] = {
    "pexels": RelayProvider(
        key="pexels",
        label="Pexels",
        base_url="https://api.pexels.com",
        api_key_env="PROVIDER_PEXELS_API_KEY",
        # Pexels 要求 Authorization 头直接是 API Key，无 Bearer 前缀。
        auth="raw",
    ),
    "pixabay": RelayProvider(
        key="pixabay",
        label="Pixabay",
        base_url="https://pixabay.com",
        api_key_env="PROVIDER_PIXABAY_API_KEY",
        auth="query",
        auth_query_param="key",
    ),
    "coverr": RelayProvider(
        key="coverr",
        label="Coverr",
        base_url="https://api.coverr.co",
        api_key_env="PROVIDER_COVERR_API_KEY",
        auth="bearer",
    ),
    "ofox": RelayProvider(
        key="ofox",
        label="OFoxAI",
        base_url="https://api.ofox.ai/v1",
        api_key_env="PROVIDER_OFOX_API_KEY",
        auth="bearer",
    ),
    "metaso": RelayProvider(
        key="metaso",
        label="秘塔 MiniMax",
        base_url="https://metaso.cn/api/minimax",
        api_key_env="PROVIDER_METASO_MINIMAX_API_KEY",
        auth="bearer",
    ),
    "muapi": RelayProvider(
        key="muapi",
        label="MuAPI",
        base_url="https://api.muapi.ai/api/v1",
        api_key_env="PROVIDER_MUAPI_API_KEY",
        auth="x-api-key",
    ),
    "wavespeed": RelayProvider(
        key="wavespeed",
        label="WaveSpeed AI",
        base_url="https://api.wavespeed.ai/api/v3",
        api_key_env="PROVIDER_WAVESPEED_API_KEY",
        auth="bearer",
    ),
    "seedance": RelayProvider(
        key="seedance",
        label="Seedance (Ark)",
        base_url="https://ark.cn-beijing.volces.com/api/v3",
        api_key_env="PROVIDER_SEEDANCE_API_KEY",
        auth="bearer",
    ),
}

# 允许覆盖上游基地址的服务商配置前缀（例如自建网关或区域端点）。
BASE_URL_ENV_SUFFIX = "_BASE_URL"

# 中继超时；文生视频是长任务，创建请求本身很快，轮询由调用方发起。
DEFAULT_RELAY_TIMEOUT_SECONDS = 120


def _env(name: str) -> str:
    return os.environ.get(name, "").strip()


def provider_base_url(provider: RelayProvider) -> str:
    """返回生效的上游基地址：环境变量覆盖优先，否则用内置默认值。"""
    override = _env(provider.api_key_env + BASE_URL_ENV_SUFFIX)
    return (override or provider.base_url).rstrip("/")


def is_configured(provider: RelayProvider) -> bool:
    return bool(_env(provider.api_key_env))


def configured_providers() -> list[dict[str, str]]:
    """列出已配置凭据的服务商，供调用方发现可用能力。"""
    return [
        {
            "key": provider.key,
            "label": provider.label,
            "relay_prefix": f"/media/relay/{provider.key}",
        }
        for provider in RELAY_PROVIDERS.values()
        if is_configured(provider)
    ]


def _auth_headers(provider: RelayProvider, api_key: str) -> dict[str, str]:
    if provider.auth == "bearer":
        return {"Authorization": f"Bearer {api_key}"}
    if provider.auth == "raw":
        return {"Authorization": api_key}
    if provider.auth == "x-api-key":
        return {"x-api-key": api_key}
    return {}


def _target_url(provider: RelayProvider, path: str, raw_query: str, api_key: str) -> str:
    base = provider_base_url(provider)
    suffix = path.lstrip("/")
    url = f"{base}/{suffix}" if suffix else base
    if provider.auth == "query":
        # 查询串鉴权：先剥离调用方可能带来的同名参数（那是调用方自己的网关
        # Key），再注入服务端凭据，避免出现两个 key 参数导致上游取错值。
        pairs = [
            (name, value)
            for name, value in parse_qsl(raw_query, keep_blank_values=True)
            if name != provider.auth_query_param
        ]
        pairs.append((provider.auth_query_param, api_key))
        return f"{url}?{urlencode(pairs)}"
    return f"{url}?{raw_query}" if raw_query else url


def _forward_headers(request: Request, provider: RelayProvider, api_key: str) -> dict[str, str]:
    headers: dict[str, str] = {}
    for name in provider.forward_headers:
        value = request.headers.get(name)
        if value:
            headers[name] = value
    content_type = request.headers.get("content-type")
    if content_type:
        headers["Content-Type"] = content_type
    headers.update(_auth_headers(provider, api_key))
    return headers


async def forward(provider_key: str, path: str, request: Request) -> Response:
    """把请求透传到白名单服务商，并原样返回上游响应。"""
    provider = RELAY_PROVIDERS.get(provider_key)
    if provider is None:
        raise HTTPException(status_code=404, detail="unknown relay provider")
    api_key = _env(provider.api_key_env)
    if not api_key:
        raise HTTPException(
            status_code=503,
            detail=f"{provider.label} is not configured on the gateway",
        )
    body = await request.body()
    target = _target_url(provider, path, request.url.query, api_key)
    headers = _forward_headers(request, provider, api_key)
    timeout = httpx.Timeout(DEFAULT_RELAY_TIMEOUT_SECONDS)
    try:
        async with httpx.AsyncClient(timeout=timeout, follow_redirects=False) as client:
            upstream = await client.request(
                request.method,
                target,
                headers=headers,
                content=body if body else None,
            )
    except httpx.HTTPError as exc:
        raise HTTPException(
            status_code=502,
            detail=f"{provider.label} upstream request failed: {type(exc).__name__}",
        ) from exc
    response_headers: dict[str, str] = {}
    content_type = upstream.headers.get("content-type")
    if content_type:
        response_headers["Content-Type"] = content_type
    return Response(
        content=upstream.content,
        status_code=upstream.status_code,
        headers=response_headers,
    )
