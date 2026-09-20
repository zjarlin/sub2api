# Application Services

OpenAI API Key 账号的模型调度资格由上游模型目录或显式模型配置确定。
目录缺失且配置为空时，不参与具体模型的调度；同步失败不推断为支持全部模型。
透传只控制请求转发方式：仍校验模型资格，配置中的映射键作为调度白名单，
请求模型名保持原样。已过期目录保留已知模型的支持证据，目录外模型需显式配置。
普通调度、粘性会话、故障切换、自动恢复和模型可用性诊断共用该判断。
原生 OAuth 非透传账号继续使用其现有的平台模型规则。

`account_recovery.go` selects temporary recovery candidates only after the normal
pool is exhausted. A closed scheduling switch always excludes an account, and
automatic recovery never enables that switch.

`reasoning_replay.go` preserves exact DeepSeek Responses reasoning content in
API-key-scoped cache references. Only an explicit missing-reasoning rejection
allows a bounded, observable non-thinking retry for unrecoverable old history.

DeepSeek 官方地址下的旧 OpenAI API Key 账号也应用原生 Responses 无状态与工具结果适配：
保留调用 ID 和结果内容，将非字符串工具结果编码为字符串，图片移到后续用户消息，
并行结果之间的开发者通知移到结果批次之后。普通 OpenAI 兼容账号不应用该转换。
上游明确以 `unsupported_value` 拒绝 `reasoning.effort=none` 时，才移除该值并沿用
原模型的默认推理做有界重试；其他合法推理强度保持透传。OpenAI API Key 账号的明确
余额不足 403 按支付失败停止调度，恢复余额后需通过正常账号恢复流程重新启用。

对明确标记 `upstream_error`、无字段定位且只提供已知通用失败文案的 HTTP 400，
已确认 Responses 支持的普通 API Key 转发先通过 Chat Completions 兼容桥尝试一次；
兼容桥仍失败、透传或未确认协议能力时，使用现有重试预算换到支持同一模型的其他账号，
不在原账号无限重试。两条路径共用严格的通用错误识别，明确的参数、上下文和策略错误保持原处理。
首个模型输出前的故障转移会扣除排队、Responses 和 compact 的心跳字节；
已有正文或工具调用输出后不再重放请求。

`upstream_concurrency.go` 识别 HTTP / SSE 中明确的账号并发拒绝，以及已知的
`service_busy` 容量提示。失败计入账号与模型的调度错误率，短暂冷却后恢复，
不会删除模型支持信息或触发账号健康封禁。优先尝试支持同一模型的其他账号，
候选耗尽后才进行有界等待；余额不足和普通配额限流继续使用各自的保护策略。
本地账号队列满或抢槽超时同样先排除该账号重选，不记为上游调用失败；
用户级并发限制不参与换号，避免绕过用户配额。

Agnes Responses forwarding lowers namespace-only tool declarations through the
shared client-tool adapter, including history and tool choice. JSON and SSE
responses restore the original namespace/name; native namespace routes keep
their existing behavior.
Text-only assistant output messages are replayed as input messages (`role` and
string `content`) for Agnes. This avoids its output-schema validation during
tool-result replay; reasoning, tool calls, and multimodal parts are unchanged.

DeepSeek 原生 Responses 和 OpenAI 兼容账号按最终上游模型识别 remote
compaction v2，将压缩请求转换为摘要回合，再输出单个 `compaction` 项。
摘要使用独立密钥域的 AES-GCM 加密，放入标准 `encrypted_content` 字段；
后续请求在通用历史清理之前还原摘要，支持仅保留标准字段的 Codex 客户端。
该状态依赖部署的 JWT secret，轮换密钥后不能解密旧压缩项。空摘要、未完成的
响应和无法解密的压缩项均明确报错，不作为成功压缩返回。

Responses 视觉辅助由 `vision_fallback.go` 和 `vision_fallback_policy.go` 提供：
仅用当前分组内已确认支持视觉的账号描述图片，随后继续调用原模型；能力目录、
HTTP/WebSocket 转发、缓存和辅助计费同步生效。配置和客户端刷新方式见
[视觉辅助说明](VISION_FALLBACK.md)。
