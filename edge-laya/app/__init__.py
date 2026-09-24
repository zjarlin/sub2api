"""Laya 本地决策服务。

对外提供两条通道：原生 `/v1/systemone`（上游 laya.serve）与 OpenAI 兼容面
`/v1/chat/completions`、`/v1/responses`（`openai_compat`）。
"""
