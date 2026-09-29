# Auto 路由观测

`auto` 请求携带 Codex 原生 `X-Codex-Turn-Metadata: {"thread_id":"...","turn_id":"..."}`。优先使用其中的 `thread_id` 关联当前线程，避免子代理误用根会话；旧版本可使用 `session_id` 请求头或元数据里的 `session_id`。会话必须是 UUID，回合标识长度为 1 到 128。缺失或无效元数据仍正常执行请求，只是不生成会话卡片记录。

`GET /v1/auto/routes?session_id=<uuid>&run_id=<turn-id>` 使用原 API Key 鉴权，返回 `{"object":"list","data":[...]}`，按请求开始时间倒序。`run_id` 可省略，以读取会话最近的请求；指定时只读取这一轮。另一 Key、会话或回合返回空列表。每次查询最多返回 128 条，这不是持久化保留上限，旧回合仍可按 run_id 查询。

`session_id` 对应会话，`run_id` 对应每轮对话，兼容字段 `turn_id` 与 run_id 相同；`request_id` 区分一轮工具循环中的多次 HTTP 模型请求。其他字段为 `requested_model`、`selected_model`、可选 `resolved_model`、有序 `attempted_models`、`state`、`started_at` 和 `updated_at`。时间戳为 Unix 毫秒，不记录对话正文、上游账号或凭据。元数据读取支持原生 turn_id，并兼容显式 run_id；不会生成一个假的轮次 ID 关联未知会话。

`selected` 表示当前仍在尝试，`responding` 表示已经观察到响应模型，`completed` 要求观察到正常完成信号；HTTP/业务错误为 `failed`，不完整响应或无正常终止信号为 `interrupted`。`selected_model` 是初选公开模型，`resolved_model` 来自实际响应的模型字段，回退路径记录在 `attempted_models`。缺少上游实际模型字段时，客户端只能展示尝试模型，不能据此推断未知的内部路由。

最新请求还返回完整 `candidates` 计划；每项含 `model`、`platform`、可选 `aliases`、`eligible`，可执行项带从 1 开始的 `order`，排除项带 `reason`。原因包括 `not_text_generation`（专用模型）、`auto_policy_excluded`（黑名单或最高档）、`protocol_not_supported`（当前文本入口不支持）、`no_compatible_account`（账号未就绪、模型不支持或请求能力不符）。这是请求开始时的快照，真实尝试另外记录，不把候选资格当作成功证明。

计划按内容摘要 `plan_id` 去重存储，同 Key/会话内相同计划复用。历史请求保留摘要，但每次查询只给最新请求附带完整清单，避免 128 条请求重复传输数百个模型；按 run_id 查询可取回历史回合的计划。客户端支持超过 128 次模型尝试，并在卡片详情中按顺序显示和搜索全部候选；不会把候选数量误当作完成次数。

观察器透传原始 JSON/SSE 字节，缓冲单条 JSON 或 SSE 行最多 2 MiB。迁移 `252_auto_model_routes.sql` 创建 PostgreSQL 表 `auto_model_routes`（命中上下文）和 `auto_model_route_plans`（去重候选计划），不依赖 Redis，不设七天自动过期。索引覆盖 API Key、会话、回合及时间；物理删除 Key 时级联删除记录与计划。已有 Redis 临时记录不自动迁移。

写入复用有界后台任务池，使用独立的短超时，不传播到模型响应。队列满或停止时沿用必达任务的同步短超时回退；数据库异常记告警，原始响应继续透传。每次状态保存携带递增 revision，SQL 只接受更高版本，避免乱序覆盖终态。进程中途退出或数据库不可用时可能留下未完成或缺失记录，因此记录失败不等于模型失败，`selected`/`responding` 也不表示完成。

该接口是 Sub2API 扩展，不是 OpenAI Responses 自定义渲染协议。Codex Buddy 通过独立 Host 元数据通道绘制 `Auto routed` 卡片，不把路由提示伪装成模型生成的消息。需同时升级网关和客户端；原生 CLI 不自动渲染此卡片。
