# translate 虚拟模型

POST `/v1/responses`，使用现有 Bearer API Key。分组白名单需要允许 `translate`。
配置离线 HYMT 或 LibreTranslate 后，OpenAI/Composite 模型目录会包含 `translate`。
每次操作读取当前翻译配置，固定优先 HYMT，失败或未配置时才使用 LibreTranslate，不回退公网供应商。

```json
{
  "model": "translate",
  "input": "The translation service is available.",
  "metadata": {"target_language": "zh", "source_language": "en"},
  "store": false,
  "stream": false
}
```

不带 `metadata.target_language` 时按输入推断方向：含中日韩文字的输入译成英文，其余译成中文
（中英混排只要带汉字就算中文）。显式 `metadata.target_language` 永远优先，例如
`{"target_language":"fr"}`。源语言默认自动检测。

支持字符串 input 和仅包含 user/input_text 的消息数组，最多 16 段，每段最多 4096 UTF-8 字节。
消息数组只取**最后一条 user 消息**：Codex 会把 developer 消息、`environment_context` 一并注入，
历史轮次不参与翻译。输出是一个 assistant 文本消息，多段译文以空行分隔。

`stream: true` 返回 Responses SSE 事件；离线推理完成后发送译文，并非模型逐 token 推送。
响应头 `X-Translation-Provider` 标识实际出口。失败返回 OpenAI error 对象及脱敏 attempts。

Codex 等客户端必带的 `tools`、`tool_choice`、`instructions`、`include`、`reasoning`、
`prompt_cache_key`、`parallel_tool_calls`、`client_metadata` 会被忽略，不影响翻译。
仍不支持图片（非 `input_text` 内容会被跳过）、`previous_response_id`、`conversation`、
`background` 与 `store: true`。
离线服务不因连续失败进入冷却，客户端断开仍取消推理请求。
