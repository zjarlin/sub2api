# HTTP Request Handlers

`model_fallback.go` owns the bounded GPT text-model downgrade policy shared by
Responses and Chat Completions. Account retries run before model downgrades;
stateful response IDs and responses with semantic output cannot be replayed.

`concurrency_retry.go` waits for busy upstreams only after other candidates are
exhausted, reopening only capacity exclusions for at most three two-second rounds.

## `auto` 自动选模

OpenAI 和 Composite 分组绑定可调度的 JEV (`typesafe/jev`) 或 Laya (`laya`)
决策账号，并且有可用文本模型时，`GET /v1/models`、`/models` 和 Codex 的
`?client_version=...` 模型清单会列出虚拟模型 `auto`。OpenAI 分组只选择本组
OpenAI 文本模型；Composite 分组可在多个 OpenAI 兼容平台之间选择。启用分组模型
白名单时须允许 `auto`；获准后它可从分组账号目录选择内部候选，即使这些候选没有
逐个列入公开白名单。

客户端以 `model: "auto"` 调用 `/v1/responses`、`/v1/chat/completions`、对应的
根路径别名或 Codex 的 `/backend-api/codex/responses`。Responses POST 子路径
也使用相同逻辑。网关提取截断后的文本、工具名和推理强度作为决策上下文，优先调用 Laya；
Laya 不可用或返回无效决策时尝试 JEV，明确的 4xx 拒绝不会回退。选择必须属于
当前候选集合；只有一个候选时直接使用。网关将 `model` 改为选中的真实模型后
继续执行原有调度和生成用量记录，并通过 `X-Sub2API-Selected-Model` 响应头
告知实际模型。System One 决策调用沿用 `/v1/systemone` 的零计费量用量记录。

Responses WebSocket 与 `/v1/messages` 暂不使用此虚拟模型。
