# translate 虚拟模型

POST `/v1/responses`，使用现有 Bearer API Key。分组白名单需要允许 `translate`。
配置离线 HYMT 或 LibreTranslate 后，OpenAI/Composite 模型目录会包含 `translate`。
每次操作读取当前翻译配置，只使用离线出口，不回退公网供应商。

```json
{
  "model": "translate",
  "input": "The translation service is available.",
  "metadata": {"target_language": "zh", "source_language": "en"},
  "store": false,
  "stream": false
}
```

默认目标语言为 zh，源语言默认自动检测。支持字符串 input 和仅包含 user/input_text 的消息数组，最多 16 段，每段最多 4096 UTF-8 字节。输出是一个 assistant 文本消息，多段译文以空行分隔。
`stream: true` 返回 Responses SSE 事件；离线推理完成后发送译文，并非模型逐 token 推送。
响应头 `X-Translation-Provider` 标识实际出口。失败返回 OpenAI error 对象及脱敏 attempts。
不支持工具、图片、instructions、会话续接、后台任务或服务端存储；翻译方向使用 metadata 明确指定。
离线服务不因连续失败进入冷却，客户端断开仍取消推理请求。
