# 豆包桌面套餐 Docker 适配器

复用本人已登录的豆包桌面会话，以纯 HTTP 调用桌面工作任务，提供 OpenAI Chat Completions 接口，接入本仓库现有 Sub2API。生成服务可在 Linux Docker 中独立运行，不包含豆包桌面、浏览器、AppleScript 或本地工具执行器。不是火山引擎 API。

## 实测依据（2026-09-15）

- 当前客户端 `2.29.10`，桌面应用 `aid=582478`，资源版本 `3.37.0`。
- 只携带登录 Cookie 的早期请求返回 HTTP 200，但 SSE 业务错误为 `710022004`、`verify / slide`。
- 复用本机已有的设备参数与浏览器校验 Cookie 后，独立 Linux 容器请求正常返回助手正文。本轮无需滑动验证码；没有实现验证码自动破解，也未生成或重放 `a_bogus` 签名。
- 首轮初始化参数不完整的请求落入已有默认会话。补充 `sub_conv_firstmet_type=1` 等新会话参数后，ACK 返回全新会话、第 1 条消息、`inner_app_id=582478`、工作模式 `3` 和对应模型键。服务现在逐请求校验这些字段，任何不符立即报错。
- ARM64 Linux 容器内，模型 `9` 的 JSON 回复与模型 `5` 的 SSE 回复均精确等于随机测试标记。252 的 AMD64 Linux 容器也已验证模型 `5` 精确回复。
- 原生桌面发送的早期对照任务也曾成功；当前服务完全不使用桌面自动发送方案。

本轮验证证明了桌面登录态、独立生成、会话隔离和两种兼容输出。尚未验证登录态长期有效期、不同网络出口或高并发；不能承诺以后永远不触发验证。

## 本机准备和运行

在已登录豆包的 Mac 上运行一次导出。当前 Python 使用系统版本，避免本机 Homebrew Python 的已知动态链接问题。

```sh
cd /path/to/sub2api/tools/desktop_session
/usr/bin/python3 prepare.py
docker compose up -d --build
```

当前 Mac 的 Docker 运行环境为 `colima-aio-dev`，Compose 独立命令为 `docker-compose`，可使用 `docker-compose --context colima-aio-dev up -d --build`。

`prepare.py` 将所需 Cookie 和设备参数写入 `runtime/session.json`，创建独立的适配器密钥 `runtime/api_key`。目录权限为 `0700`，文件为 `0600`；两者均被 Git 和 Docker 构建上下文排除。重新准备会刷新会话，保留适配器密钥。镜像使用 Python 3.12，并安装 `requirements.txt` 中的 `jsonschema` 及其依赖完成结构化输出校验。

Linux 部署只需服务代码/镜像及上述两份私密文件；无需复制整个豆包用户目录或 macOS 钥匙串。将私密目录只读挂载到 `/run/secrets`，使容器 UID 可读取它。`compose.yml` 默认仅映射宿主机回环端口 `18089`。

请求体默认上限为 **32 MiB（33554432 字节）**，与 Sub2API 的默认纯文本入口上限一致。原先固定的 64 KiB 限制会使长上下文在适配器入口返回 `413 / request_too_large`。现在可在 `.env` 中设置正整数 `DESKTOP_MAX_REQUEST_BODY_BYTES` 覆盖上限，重新构建并创建容器后生效。直接运行 Python 时使用同名环境变量。上限按完整 JSON 的字节数计算，不截断消息；超过上限仍返回 413，并在错误中提供当前字节上限。上游模型的上下文限制仍独立生效。

请求必须带单个有效的 `Content-Length`，暂不接受 `Transfer-Encoding`；缺失、重复、无效长度或不完整请求体返回 `400 / invalid_request`，不再误报为请求过大。

上游 SSE 的 `FULL_MSG_NOTIFY` 会在 `content_block`、`content` 和 `ext.raw_messages` 三处回传上下文，后两处还包含嵌套 JSON 编码。读取预算按一份请求 JSON、两份再次编码的请求 JSON 及 64 KiB 元数据计算单行上限，总流预留两个最大行及 1 MiB 回复空间。大小超限或非法上游流返回 `502 / invalid_upstream_stream`；早期探测入口保持原有固定预算。

生成请求的单次网络等待默认 **120 秒**（`DESKTOP_UPSTREAM_TIMEOUT_SECONDS`），SSE 读取总时限默认 **600 秒**（`DESKTOP_GENERATION_TIMEOUT_SECONDS`）。总时限在每行返回时检查，正在阻塞的读取最多额外等待一个网络超时。两个设置必须大于 0 且不超过 1800 秒；目录查询保持 20 秒。超时返回 `504 / upstream_timeout`，连接重置、DNS 或不完整 HTTP 返回 `502 / upstream_connection_failed`。不自动重发未确认完成的生成请求。

2026-09-17 实测 `doubao-auto` 把“当前高峰期算力紧张，优先通道暂时繁忙。我们正在优先为你调度资源，请稍后再试。”作为完整助手正文返回。适配器现在精确识别该通知，返回 `503 / upstream_capacity` 和 `Retry-After: 30`，同一模型冷却 30 秒，不把通知当作成功答案、工具格式错误或有效会话结果。冷却不影响另一个模型，也不会偷偷换模型；它不能解决豆包上游自身的资源不足。

部署后的 `doubao-pro` 也复现了同一繁忙通知。Sub2API 在可用上游耗尽时仍可能把外层响应聚合为 502；账号失败明细中的 `upstream_status_code=503` 和 `upstream_error_message` 才是这次容量失败的依据，不能将外层 502 一律解释为适配器崩溃。

生成请求返回 `X-Request-ID`；标准输出记录请求编号、规范模型名、处理阶段、耗时、状态码、异常类型和栈位置，不记录 Cookie、请求正文、助手正文或原始异常文本。可使用 `docker logs sub2api-doubao-desktop` 定位后续错误。

官方客户端的 `buildGeneralAgentConversationAgentTaskParam` 在未指定环境时使用 `CloudVM`，因此工作会话可显示为“云电脑”。本适配器沿用工作模式，不能凭 `local_permissions: []` 或提示词关闭服务端内置工具。外部函数仍通过下文的文本桥接交由调用方执行；这与豆包原生云电脑/本机工具执行链路不同，目前未接通原生自定义工具注册与结果回传。

## API

| 入口 | 行为 |
| --- | --- |
| `GET /health` | 进程状态和是否需要更新验证状态；不代表一次生成成功 |
| `GET /v1/models` | 需 Bearer 密钥，返回两个已验证的桌面工作模型 |
| `POST /v1/chat/completions` | 需 Bearer 密钥，首次创建工作会话，后续按已确认历史增量续接 |

使用 `runtime/api_key` 的内容作为 Bearer token。最小请求：

```json
{
  "model": "doubao-pro",
  "messages": [{"role": "user", "content": "你好"}],
  "stream": false
}
```

对外模型 ID 为 `doubao-auto` 和 `doubao-pro`，分别映射到上游菜单的 `自动`（键 `9`）和 `豆包 2.1 Pro`（键 `5`），工作模式均为 `3`。`/v1/models` 的 `name` 字段保留上游显示名称。服务读取账号的模型菜单，只公开这两个已验证的选项，并检查新会话记录是否采用所选键；这不证明底层模型权重或套餐具体扣费规则。

### 同一会话增量续接

调用方继续发送完整 `messages`，接口形状保持兼容。适配器将归一化后的历史逐条计算 SHA-256，只有历史唯一匹配已确认的上游会话末尾及上一条实际返回的 assistant 消息时，才复用其 `conversation_id`、`section_id` 和消息位置；发送给豆包的内容为新增消息及必要的协议提醒。系统提示词及未变化的工具定义不再重复上传。工具调用编号与函数的对应关系会随工具结果补充，`tool_choice`、输出格式或工具定义变化时显式更新约束。

工具模式每轮保留简短的 `content/tool_calls` 输出协议提醒。实测发现，完全省去该提醒时，“只回复标记”的用户要求可能使模型直接返回正文，导致工具协议校验失败；重复输出协议不代表重复发送完整系统提示词或函数参数 Schema。

- 可传 `X-Desktop-Session`（也接受 `session_id` / `conversation_id`）隔离任务。没有标识时，以完整历史匹配已确认的会话末尾；不凭相同系统提示词或相同首问合并。网关若不透传会话头，仍可通过完整历史匹配续接。
- 编辑历史、压缩历史、切换模型、回到旧分支、过期或找不到已确认会话时，创建新的上游会话并完整初始化。不向已经前进的上游会话追加旧分支内容。
- 含 assistant 历史的相同请求重试复用已确认结果与调用 ID；显式会话标识也支持首轮重试。无标识且不含 assistant 的相同首问始终新建。
- 续聊请求发出前持久化移除旧位置；网络中断、上游错误、格式校验失败后不盲目在不确定的位置重发。下次完整请求重新初始化。
- `DESKTOP_STATE_FILE` 开启持久化。保存历史摘要、会话位置、工具协议和最后一次已校验回复，不保存完整历史或 Cookie。文件权限 `0600`，最多 64 个会话、8 MiB，6 小时过期。刷新桌面会话快照内容会清空关联，避免切换账号后复用旧状态。
- Compose 增加私有可写目录 `DESKTOP_STATE_DIR`；重新运行 `prepare.py` 可创建。旧安装也可单独创建目录、设为运行 UID 所有并添加此变量，无需刷新已有登录态。容器其他目录仍为只读。

这里只减少客户端到豆包的重复上传，不保证上游推理不读取历史，也不改变桌面订阅的计费规则。

### 原生工具协议调查边界

2026-09-17 对当前客户端随附资源及热更新包完成核对：旧任务路径有 `/samantha/tool/init`（`client_id`、`tools[].name/description/input_schema`）及 `client_tool_key`，并有工具心跳、结果回传接口；当前 Office 本地执行链路另使用 `/alice/office/tool_local/chunk_stream` 与 `upload_tool_call_result_v2`，关联设备和 sandbox。

使用独立测试 client ID 注册 `lookup_marker` 得到 HTTP 200、业务码 0 和工具标识，但将该标识及客户端使用的任务字段带入当前 Pro 工作会话后，模型没有获得该函数；实际回复表示工具列表与工具搜索均找不到它。注册成功不能证明当前工作模型支持任意外部工具。因此本版本只启用已实测的会话续接，外部函数继续走明确标注的文本 JSON 桥接，没有声称接通原生工具，没有改用火山方舟 API，也没有让适配器执行桌面内置工具。

2026-09-17 增量续聊修正版已部署为 `sub2api-desktop:20260917-incremental-v2`：本机及 AMD64 镜像各 77 项测试通过，部署的 16 个 Python 文件与本地 SHA-256 一致。通过现有客户端有效凭据完成 Sub2API `/v1/responses` 的 Pro 工具调用、真实随机结果回传、重试复用及第三轮 SSE 回答；三轮同一上游会话，实际上传字节数为 31700 / 1482 / 1245。正式适配器的自动模式 SSE 与 JSON 最终答案也完成三轮验证，字节数为 71973 / 1783 / 1585。详细证据见 `validation.json` 的 `incremental_session_validation`；旧镜像、源码和回滚容器保留。

2026-09-16 已在 252 的 Docker 服务及 Sub2API 同步上述名称：模型列表精确返回这两个 ID，Pro JSON 与自动模式 SSE 均返回完整随机测试标记，响应 `model` 与请求一致；17 项单元测试通过。

当前范围：

- 纯文本。多条 `messages` 作为带角色的 JSON 上下文传入全新任务；不是豆包原生 system-role API，不保持服务端共享历史。
- 支持 `response_format: null`、`{"type":"text"}`、`{"type":"json_object"}` 和 `{"type":"json_schema","json_schema":{"name":"result","schema":{...},"strict":true}}`。JSON 模式使用提示约束及返回前校验，不是上游原生约束解码；不自动重试生成。只去除包裹整个回复的 JSON 代码围栏，不从解释文字中截取 JSON。无效 JSON、重复键、非有限数值、非对象结果或不符合 schema 的回复返回 `502 / invalid_response_format`，JSON 与 SSE 路径一致，不提前发出成功块。
- Schema 使用 JSON Schema draft 2020-12，根类型为 `object`，支持本地 `$defs` / `$ref`，拒绝外部引用及无法解析的引用；`format` 按该标准默认为注解。`strict` 为 true、false 或省略时均校验实际输出，参数本身不会透传给豆包。不合法的格式或 schema 在生成前返回 400。
- `stream=true` 是**缓冲 SSE**：完整校验助手正文和结束状态后返回内容、结束块、`[DONE]`，没有实时首 token。
- 支持 `tools` 中的 function 工具、`tool_choice` 的 `none` / `auto` / `required` / 指定函数，以及 `parallel_tool_calls`。通过文本 JSON 决策协议适配，校验所选名称、函数参数 schema、强制选择和并行数量后，返回标准 `assistant.tool_calls`、JSON 字符串 arguments、独立 call ID 和 `finish_reason=tool_calls`。SSE 调用块含 `index`。不合格模型决策返回 `502 / invalid_tool_calls`，不静默丢弃调用。
- 工具由调用方执行，适配器不运行 shell、Python、浏览器或桌面动作，也不执行模型所列函数。调用方回传 `assistant.tool_calls` 和相同 `tool_call_id` 的 `role=tool` 消息后可继续生成，无需追加虚构的 user 消息。所有并行调用均须有且仅有一个结果；未知、重复或缺失的 ID 在生成前返回 400。支持字符串及纯 text 内容块，不支持附件、图片、custom 工具或 Responses API；为保持 OpenAI 兼容性接受并忽略 `max_completion_tokens`，其它生成参数（如 `temperature`、`max_tokens`）仍会被拒绝。
- `response_format` 仅约束最终答案，不约束 `tool_calls` 的参数对象；函数参数独立按声明的 parameters 校验，strict 为 false 或省略时也会校验。工具桥接不等同于豆包原生工具协议，也不承诺模型每次都能产生有效决策。
- 对话末条可以是 assistant、system、developer、user 或已完成的 tool 结果；空文本末条也可继续。assistant 末条按继续未完成的回答或任务处理，完整保留角色和历史，不伪造 user 消息。单条 system / developer / assistant 消息同样保留角色；未完成的工具调用仍须先返回结果。
- 单账号仅允许一个在途生成；并发请求返回 429，不排队积压。响应读取有大小和超时限制。
- 不伪造 token 用量，不根据 HTTP 200 或流结束信号单独判断成功。
- 若再出现 `710022004`，本进程标记需要验证并停止后续生成重试，返回 503。完成正常验证或刷新桌面登录态后重新执行 `prepare.py`，将新快照同步到服务器；服务在下一次请求时重新加载。没有无需人工介入的验证恢复承诺。
- 断开客户端连接不会实现已验证的上游任务取消；后台读取最多持续到本次有界请求结束。

## 接入已有 Sub2API

使用 OpenAI API Key 账号的现有自定义 `base_url`，无需修改网关 Go 代码或 `.s2plugin` 协议。账号模板见 `sub2api-account.example.json`。

让适配器加入 Sub2API 的现有 Docker 网络：

```sh
SUB2API_NETWORK=sub2api_sub2api-network \
  docker compose -f compose.yml -f compose.sub2api.yml up -d --build
```

账号类型 `openai / apikey`；上游 `http://sub2api-doubao-desktop:8080`；API Key 为适配器密钥；仅启用 `chat_completions`；`extra.openai_responses_mode=force_chat_completions`；并发为 `1`；模型映射为 `doubao-auto→doubao-auto`、`doubao-pro→doubao-pro`，适配器内部解析为菜单键 `9`、`5`。绑定专用实验分组，关闭后台上游计费探测。

252 上的独立容器名为 `sub2api-doubao-desktop`，部署目录 `/opt/sub2api/desktop-api`，镜像 `sub2api-desktop:20260916-continuation`。适配器宿主机入口仅为 `127.0.0.1:18089`，Sub2API 从容器网络访问它。账号 `834` 同时绑定 `6`（codex）与专属分组 `21`（豆包桌面套餐实验-20260915）。实验分组模型列表为 `doubao-auto`、`doubao-pro`，请求和响应的 `model` 使用同一名称。2026-09-16 已将这两个模型配置为按次计费，详见 `pricing.json`；实验分组仍仅授权当前管理员使用。协议验证结果见本目录 `validation.json`。旧网关实验密钥保存在被忽略的 `runtime/sub2api-access.json`，但已失效；不要提交或复制到普通日志。

2026-09-16 模型价格：`doubao-auto` 基础价 **$0.005/次**，`doubao-pro` **$0.010/次**；分组 21 倍率 1，分组 6 保留三折，默认实扣分别 **$0.0015/次、$0.003/次**。用户专属折扣继续覆盖分组默认倍率。配置通过管理 API 保存，两个模型已使用现有管理员 Key 从分组 6 完成真实生成，账单 `624710`、`624711` 均为 `per_request`，零 token 用量下按次金额与上述价格一致；分组 21 完成管理接口和数据库回读验证。未恢复旧实验密钥，未调整账号调度、账号倍率、用户折扣或分组限额。

定价参考 [火山引擎公开价](https://www.volcengine.com/product/doubao/)：Seed 2.1 Turbo 输入/输出为 3/15 元每百万 token，Pro 为 6/30 元。按每次 4000 输入、1500 输出和预算汇率 7 元/美元折算取整；这些是定价假设，不是桌面模型身份确认、实测 token 或实时汇率，也不是官方按次售价。工具调用后继续生成另计一次；不叠加 token 费用，固定单价不随上下文长度变化。原配置备份在被忽略的 `runtime/pricing-backup-20260916T063432Z.json`；恢复时必须同时提交原有日/周/月限额，无限制使用 `-1`，避免管理接口将省略字段或 `null` 解释为其他限额状态。

2026-09-16 长请求修复已部署：线上配置为 33554432 字节；27 项测试在本机和 AMD64 镜像中通过。适配器直连的 Pro JSON 与自动模式 SSE 请求体均为 84841 字节，均返回 HTTP 200、正确模型、完整随机标记及正常结束状态。本轮网关验证返回 `401 / INVALID_API_KEY`；只读核实专用测试密钥已被删除，未恢复或新增密钥。因此上述长请求结果仅证明适配器端到端生成，不能替代本轮网关验证。部署前镜像与源码备份保留，可用于回滚。

2026-09-16 后续 `response_format` 修复已部署：37 项本机及 AMD64 镜像测试通过。候选容器验证了 `text`、`json_schema`，以及请求体 70384 字节的 `json_object` 自动模式 SSE；均返回正确随机标记和正常结束状态。正式容器切换后再次验证 Pro `json_schema` JSON 回复成功。此次同时补齐了三份上下文及嵌套 JSON 转义的 SSE 读取预算。网关专用测试密钥仍返回 `401 / INVALID_API_KEY`，没有本轮网关全链路成功的结论。

2026-09-16 函数工具桥接已部署：55 项本机及 AMD64 镜像测试通过。候选容器完成 Pro JSON 指定单函数、自动模式 SSE 两个并行调用的完整两轮验证；正式容器再次完成 Pro 指定函数调用及结果续聊。随机结果均在收到调用后才由测试客户端生成，模型随后精确返回这些结果，最终输出同时通过 `json_object` 校验。适配器进程运行正常、重启次数为 0，原镜像及源码备份保留。网关旧专用密钥仍返回 `401 / INVALID_API_KEY`；本次没有网关全链路成功的结论。

## 职责和验证

2026-09-16 续写兼容修复已部署：移除末条必须是非空 user 或 tool 的错误限制，保留单条非 user 消息的角色；工具结果完整性校验继续生效。61 项本机及 AMD64 镜像测试通过。候选容器完成 Pro JSON 工具结果后接 assistant 的续写，以及 70482 字节上下文的自动模式 SSE 续写；正式容器完成两个并行函数调用、工具结果及 assistant 末条的 SSE 续写，随机结果均精确匹配。正式进程 running、重启次数 0、health ready；保留上一版本容器与源码备份。旧网关测试密钥仍返回 401，本轮真实生成验证均为适配器直连。

- `session.py`、`native_context.py`、`prepare.py`：Mac 端会话读取、版本核对、私密快照导出。
- `transport.py`：固定豆包域名、拒绝重定向、版本参数及桌面菜单。
- `protocol.py`：工作任务请求和有界 SSE 读取；保留早期脱敏错误探测。
- `reply.py`：会话及模型选择校验、可见助手文本补丁合并、严格完成判定。
- `client.py`：组合上述协议，与部署和 HTTP 框架无关。
- `api.py`：兼容请求范围、JSON/SSE 输出；`server.py`：Bearer 鉴权、串行生成和验证状态。
- `models.py`：请求、输出格式和工具策略模型；`structured_output.py`：格式参数校验、提示约束、JSON 与 schema 校验。
- `messages.py`：文本内容归一化和工具调用历史关联；`tool_policy.py`：工具定义及选择规则；`tool_calls.py`：决策提示、调用校验和响应映射。
- `conversations.py`：已确认会话匹配、增量提示、重试结果复用、状态持久化与过期回收。
- `smoke.py`：通过适配器或专用 Sub2API 密钥执行一次随机标记的真实生成验证。
- `smoke_tools.py`：先取得真实调用，再由测试客户端生成随机工具结果并续聊；覆盖指定函数、并行调用、JSON/SSE 与 response_format 组合，不执行实际外部工具。
- `smoke_conversations.py`：通过运行中服务的 HTTP 接口完成三轮工具往返，按已提交状态核对同一会话、重试结果及每轮上游输入字节数。
- `container_probe.py`：从 stdin 读取快照的独立 Linux 检查；`probe.py`、`history.py` 为早期诊断；`native_probe.applescript` 仅编译过，不是服务依赖。

直连冒烟测试可通过 `DESKTOP_ADAPTER_BASE_URL` 和 `DOUBAO_API_KEY_FILE` 覆盖适配器 URL 与密钥文件路径；`--gateway` 始终读取 `runtime/sub2api-access.json`。

```sh
python3.12 -m venv tools/desktop_session/.venv
tools/desktop_session/.venv/bin/python -m pip install -r tools/desktop_session/requirements.txt
tools/desktop_session/.venv/bin/python -m unittest discover -s tools/desktop_session -p 'test_*.py'
/usr/bin/python3 tools/desktop_session/smoke.py --gateway --model doubao-pro
/usr/bin/python3 tools/desktop_session/smoke.py --gateway --model doubao-auto --stream
/usr/bin/python3 tools/desktop_session/smoke.py --response-format json_schema
/usr/bin/python3 tools/desktop_session/smoke.py --response-format json_object --model doubao-auto --stream --padding-bytes 70000
/usr/bin/python3 tools/desktop_session/smoke_tools.py --model doubao-pro
/usr/bin/python3 tools/desktop_session/smoke_tools.py --model doubao-auto --stream --parallel
/usr/bin/python3 tools/desktop_session/smoke.py --model doubao-auto --stream --tail-role assistant --padding-bytes 70000
/usr/bin/python3 tools/desktop_session/smoke_tools.py --model doubao-pro --assistant-tail
python3 tools/desktop_session/smoke_conversations.py --state-file tools/desktop_session/state/conversations.json
python3 tools/desktop_session/smoke_conversations.py --state-file tools/desktop_session/state/conversations.json --model doubao-auto --stream
python3 tools/desktop_session/smoke_conversations.py --state-file tools/desktop_session/state/conversations.json --plain-text
```

协议主要依据当前客户端随附资源，使用原生日志和实际响应交叉验证。`sub_conv_firstmet_type` 的补充参考 [doubao2api 的客户端实现](https://github.com/wangchuxiaoji-oss/doubao2api/blob/main/doubao2api/client.py)，并在本账号的新桌面会话 ACK 中独立验证。没有运行该项目的服务或浏览器代码。
