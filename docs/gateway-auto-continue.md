# 网关自动续跑与方案裁决

HTTP `/v1/responses` 的 Agent 请求在助手停在“请选 1/2/3”“等待继续”“已定位但尚未修复”时，由网关判断并继续同一个请求。DeepSeek 原生 Responses 与经 Chat Completions/Anthropic 上游适配的 Responses 请求共用入口。无需客户端自动发送“继续”。

System One 判断原始任务授权、剩余工作和决策来源，默认模型为 `typesafe/jev`，可通过 `decision_model` 改为 `laya`。用户已确认的设计覆盖当前问题时沿用设计；已经定位且用户要求解决的明确缺陷直接修复、验证。新的方案取舍、决策接口不可用或判断不确定时交给最高档裁决者，最高档来源为后台 `model_fallback_policy.tiers[0]`，不硬编码模型名，不向低档降级。有明确且有效的推荐时优先推荐，否则裁决者选最佳方案；“全部都做”不作为默认最优答案。

判断请求复用本进程的 `/v1/systemone` 入口，以原始已鉴权的分组 Key 调用，沿用账号调度、显式 `base_url`、上游密钥、并发限制和调用记录，不读取旧的 `gateway.laya.url` 或直连本机 `edge-laya`。本机目的地址固定由服务监听端口构造，不信任客户端 Host。跨机 Laya 配在 System One 账号中，例如 `base_url=https://ai.addzero.site/v1` 配合该上游的分组 Key；请求内容按 System One 契约透传。返回的实际模型写入裁决记录，JEV 回退到 Laya 时不冒充 JEV 结果。

纯文字选项和 `request_user_input` / `request_user_input_async` 的选择题都支持。结构化问题必须有完整选项，用户秘密、必要业务信息、用户明确要求确认及真实授权边界保留人工输入。网关用开发者消息标记自动决策来源，不伪造用户新授权。

每次决策先通过现有 `auto_model_routes.operation.decision` 落库，按 API Key 和会话隔离，可通过现有路由观察接口查询。记录包含编号、来源、裁决模型、选项、方案、依据和时间；`selected` 与操作类型 `auto_continue_decision` 表示已选定，不宣称修复完成。执行模型继续实施；修复是否完成以最终执行结果为准。没有持久化记录时不自动续跑。

客户端声明异步追问工具时，最终响应附带 `request_user_input_async` 裁决报告卡片，用户可纠正方案，当前执行工具照常发给客户端。没有异步追问工具时报告作为助手文本显示，不替换成阻塞追问。报告工具由网关生成；其反馈在后续请求中归一成正常用户消息，避免 `previous_response_id` 链出现不属于上游历史的工具结果。

每个 HTTP 请求最多续跑两轮，可配置为一到三轮。内部主模型回合和最高档裁决调用通过已有辅助用量队列独立记录，最终回合正常计费，不把内部输出 token 伪装成最终响应的用量。System One 调用复用原生入口的调用记录；Laya 不生成输出 token，JEV 返回真实用量。决策接口故障时先升交最高档；最高档裁决、审计或额外转发失败时保留原回复；内部失败写日志和响应头 `X-Sub2API-Auto-Continue`。

为避免向客户端先发送已代答的选择题或重复响应生命周期，小型终态回复在判断前暂存；遇到实际执行工具立即恢复流式输出。缓冲上限 256 KiB，等待超过 15 秒后的输出恢复普通透传。客户端接收最终真实响应 ID，流式结果只包含一次响应生命周期。

启用条件是请求声明了执行工具，并提供可判断的完整对话。普通无工具问答、后台任务、压缩请求、不透明的 `previous_response_id` / `conversation` 请求及 WebSocket 入站不会自动续跑；这些路径没有足够的原始任务上下文或不同的流生命周期。直接 `/v1/chat/completions` 入站也不进入此 Responses 策略。

配置在 `gateway.auto_continue`，默认开启。环境变量：`GATEWAY_AUTO_CONTINUE_ENABLED`、`GATEWAY_AUTO_CONTINUE_DECISION_MODEL`、`GATEWAY_AUTO_CONTINUE_MAX_ROUNDS`、`GATEWAY_AUTO_CONTINUE_TIMEOUT_SECONDS`、`GATEWAY_AUTO_CONTINUE_MIN_CONFIDENCE`、`GATEWAY_AUTO_CONTINUE_JUDGE_TIMEOUT_SECONDS`。本地测试使用假上游和假原生 System One 入口，不修改线上容器。概率、`confidence` 和 `answer_confidence` 中已提供的值都必须达到置信阈值，不用概率覆盖更低的上游置信度。
