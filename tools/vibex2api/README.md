# VibeX 内置账号平台

适配 RunningHub/VibeX 的网页项目编码会话，向 Sub2API 提供 Chat Completions、客户端函数工具和图片输入。
模型 ID 从账号实际的 `/vc/api/llm-providers` 读取，无预填模型。

## 启动

部署使用 `deploy/docker-compose.builtin-adapters.yml`，设置独立的
`VIBEX_ADAPTER_KEY`。后台配置为 `BUILTIN_ADAPTER_ENABLED=true`、
`BUILTIN_ADAPTER_VIBEX_URL=http://sub2api-vibex:7866`、
`BUILTIN_ADAPTER_VIBEX_KEY`。适配器端口不发布到公网。
升级主服务后执行迁移 `248_add_vibex_platform.sql`。

本地启动：

```sh
V2A_API_KEY=<private-adapter-key> V2A_STATE_FILE=<private-state-path> go run .
```

默认监听 `127.0.0.1:7866`，容器中使用 `V2A_LISTEN=0.0.0.0:7866`。
所有模型、生成和内部账号接口均要求适配器密钥；`/livez` 只检查进程存活。

## 账号接入

1. 添加账号选择 **VibeX**，点击「登录并接入」，打开 RunningHub 登录页。
2. 在 RunningHub 完成登录。把该站点的 `Rh-Accesstoken`，或含 `#rh-sso-token=...`
   的 VibeX 登录返回链接填入密码输入框。
3. 后端请求 `/uc/getUserInfo` 验证身份后原子保存凭证，文件权限 0600。
   登录会话按管理员隔离、十分钟到期，返回值和错误不包含上游凭证。
4. 保存平台账号，自动同步模型目录。选择同步的模型测试文本调用。
5. 编辑账号可以手动刷新钱包余额和 Lite 当日额度，菜单支持重新登录。

本部署的所有 VibeX 路由账号共享一个上游登录身份和专用项目。重新导入同一用户的令牌
会保留专用项目；导入不同用户会切换部署身份。尚未确认官方刷新接口，令牌过期时重新导入。
已有网页业务项目不会被复用。首次生成创建名为 `Sub2API VibeX` 的专用项目并保存 ID。
创建和模型生成可能受账号项目数量、额度及钱包限制，并可能按上游规则产生费用。

## 协议边界

- 支持文本 `messages`（system/developer/user/assistant/tool）、`model`、`stream`、
  `stream_options.include_usage`；文本数组也受支持。
- 支持 `tools` 函数定义、`tool_choice`（none/auto/required/指定函数）、
  `parallel_tool_calls`、助手 `tool_calls` 和匹配的 `tool_call_id` 结果。
  工具使用 JSON 决策桥接，由客户端执行；函数名、参数 Schema 和调用策略均校验，
  无效回复返回错误。支持本地 Schema 引用，拒绝远程引用。
  工具输出校验完成后发送标准 SSE 工具事件；工具结果续聊始终保留输出协议。
- 用户内容支持 `image_url` 数据 URL 和公开 HTTP(S) 地址，最多 8 张、单张 10 MiB、
  3200 万像素，请求体最多 32 MiB。支持 PNG/JPEG/GIF/WebP（动画取首帧）；图片缩放至最长边 1024，
  转 JPEG 上传到专用项目，经平台原生 Read 检视。图片地址不携带账号凭证，
  连接时拒绝本地、私网和保留地址。附件仍保存在上游专用项目中。
- `temperature`、`max_tokens` 等未适配的参数仍返回 400。
- 支持 `response_format` 的 `text`、`json_object`、`json_schema`；JSON 格式通过提示词约束，
  完整输出在返回前校验，流式请求也先缓冲校验再发送。不符合格式返回 502，
  不提供上游原生受约束解码。Schema 支持本地引用，拒绝远程引用；客户端工具调用不受最终答案格式约束。
- 每个请求使用新的上游会话，项目串行使用；发现已有运行中的任务返回 409。
- 连接时用项目元信息执行 `init_root`，等待就绪和空闲状态后创建新会话。
  新会话确认的 `session_id` 可以是 `null`，实际 ID 在提示词启动后分配。
- 支持流式文本、结束、取消和上游用量。失败时取消上游并中止响应，不把部分输出标成成功。
- WebSocket 错误中的已知认证、余额和额度代码分别映射到 401/402/429。
  初始化忙碌、源码回合占用及工作区切换返回 409。
  提示词发送前的网络/5xx 握手失败最多尝试三次；认证、余额和额度失败不重试，
  不自动重发初始化、会话创建或提示词。
  REST GET 查询同样最多尝试三次，维持 45 秒总超时；其他 HTTP 方法不自动重试。
- 上游运行的是编码 Agent，其内置工具行为不等同于调用方工具调用。提示词要求只回答文本，
  但这不能保证上游不执行内置工具；因此必须使用专用项目。
- 新增平台可通过现有 OpenAI 网关及组合路由使用。Responses 函数工具复用现有转换器。
  custom/namespace 工具由通用转换器转换为函数；custom 的 grammar 约束不能在此桥接中保证。
  平台内置搜索、图片生成等服务端工具不属于客户端函数支持范围。
- 网关支持 HTTP `/v1/responses` 和 Responses WebSocket：复用 Responses ↔ Chat
  Completions 转换，WebSocket 的 `response.create` 返回 JSON 事件和用量。
  `previous_response_id` 须引用同一连接的最新回复，通过完整历史重放续聊；未知 ID 返回错误。
  不提供上游原生 Responses
  存储或跨连接历史恢复。工具结果和图片沿用相同的转换、验证与上传链路。

## 验证

```sh
go test -race ./...
go vet ./...
```

测试使用模拟 HTTP/WebSocket 上游，覆盖认证、HTTP 200 中的认证错误、凭证权限及恢复、
模型目录、请求参数拒绝、新会话隔离、流式输出、用量、忙碌项目、余额/额度错误、
工具策略与 Schema、结果历史、大整数精度、图片上传及私网地址拒绝。
真实账号验收仍需验证 RunningHub 登录、账号目录、专用项目创建、WebSocket 消息顺序和生成。
公开前端协议可能变化，模拟测试通过不能替代真实账号验收。

2026-09-28 真实账号 858 的 `free-qwen-3.8-max` 已验证图片识别和客户端文件工具执行、
结果续聊，覆盖公网 Chat、HTTP Responses 和 Responses WebSocket。
WebSocket 的两轮用量记录为 709455、709457，完成事件和输出项使用相同 ID，避免续聊重复工具调用。
图片能力快照只登记实际验证的模型，不根据名称推断其他模型的视觉能力。
发布版本、各阶段验证结果和限制见 [validation.json](validation.json)。
