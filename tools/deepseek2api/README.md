# DeepSeek 网页适配器

仓库内的 DeepSeek 网页版文本对话适配器。它通过 DeepSeek 网页会话访问
`chat.deepseek.com/api/v0`，向 Sub2API 提供 Chat Completions 接口。
与官方付费 API Key 的 `deepseek` 平台分开，使用 `deepseek_web` 平台。

## 登录

在「添加账号」或编辑账号时选择 DeepSeek 网页版，输入邮箱和密码。适配器使用独立
Chromium 登录官方网页，获取 Bearer token 和该浏览器的 `device_id`，再验证
`/users/current` 和 `/users/auth_token/check_device`，将凭据保存在
`DEEPSEEK_WEB_STATE_FILE`。网页要求验证时，可查看登录页面并按提示重新登录。

勾选「失效后自动重新登录」后，只有登录验证成功才会保存邮箱和密码。密码和登录邮箱
使用 AES-GCM 加密，密钥由 `DEEPSEEK_WEB_ADAPTER_KEY` 派生，密文绑定账号 UID；
状态文件权限为 `0600`。接口只返回开关状态，不返回密码或密文。更换适配器密钥后，
需要重新登录才能恢复自动登录。取消勾选并成功登录，会删除该账号已保存的密码。

对话遇到上游 HTTP 401 或 `40003` 鉴权错误时，会使用已保存的账号密码重新登录，
验证 UID 与原账号一致，再原子保存新 token 和设备信息，并在尚未输出时重试当前对话一次。
流式回复已开始后不再续登重放；发生错误时发送 SSE error，不发送成功结束标记。
同一账号的并发请求共用一次登录；自动登录最多等待 90 秒，失败后暂停重试 1 分钟。
额度、限流和其他错误不会触发重新登录。验证码、密码错误等无法自动完成的情况会返回
HTTP 503 `deepseek_relogin_failed`，提示在账号设置中重新登录，避免暂时的登录失败
将 Sub2API 账号永久停用。旧账号未保存密码时返回 HTTP 401 `deepseek_login_required`。

DeepSeek 目前没有向第三方适配器提供 OAuth2 授权码回调接口，因此此流程是
**网页登录后的会话导入**，不是 OAuth2 授权。浏览器同源限制也不允许本系统
直接读取官方登录页的会话。不要把适配器共享密钥或网页 token 放入公开配置。

## 能力与运行

- 模型目录保留 `deepseek-web-chat`（默认不思考）、`deepseek-web-reasoner`（默认思考）。
  接口同时兼容 `deepseek-chat`、`deepseek-reasoner`、`deepseek-v4.1-flash` 别名；
  最后一个默认开启思考。网页实际模型由 DeepSeek 上游决定，别名不用于选择特定模型版本。
- 支持纯文本和函数工具调用。图片、文件、音频、自定义工具与服务端工具会明确拒绝。
- SSE 实时转发正文和 `reasoning_content`，每个增量都 flush；支持
  `stream_options.include_usage`。用量优先使用上游计数，缺失时本地估算，不作为精确计费依据。
- 新旧网页 SSE 协议共用解析器：支持 fragments、旧版 content/thinking_content、
  多行 data、BATCH、显式索引及省略路径/操作的增量。只有真实 FINISHED 才成功结束；
  toast/error、半帧、断流或改写已发文本都会明确失败。
- 思考开关依次按 `reasoning_effort`、`thinking`（布尔或 enabled/disabled 对象）、
  `enable_thinking`、`thinking_enabled` 覆盖，后者优先。
  搜索开关支持 `web_search`、`search_enabled`，后者优先，默认关闭。
- 为兼容常见客户端，接收并校验 `max_tokens`、`max_completion_tokens`、`temperature`、
  `top_p`、`user`、`n=1`。**网页没有采样/输出上限控制接口，这些采样与长度参数不会生效**；
  不能依赖它们限制输出。其他未支持的参数继续返回 400。
- 请求使用网页登录时采集的 User-Agent，并携带匹配的 web/client-hints、Origin/Referer。
  旧凭据没有 User-Agent 时使用兼容浏览器默认值，下一次登录后更新。
- 每次对话创建独立会话并获取一次性 PoW；明确收到 `40301` 且尚无内容时，换新挑战重试一次。
  请求取消会关闭上游响应并清理会话。完整请求最多 10 分钟；HTTP 429 保持限流错误，不触发续登。
- 运行时下载 DeepSeek PoW WASM。上游更新文件名后可设置 `DEEPSEEK_WEB_WASM_URL`。
- `DEEPSEEK_WEB_ADAPTER_KEY` 为必填共享密钥；`DEEPSEEK_WEB_LISTEN` 默认
  `127.0.0.1:7867`，Compose 中设为 `0.0.0.0:7867`。

## 函数工具

`tools` 使用 Chat Completions 的 function 定义，支持 `tool_choice` 的 none/auto/required/
指定函数和 `parallel_tool_calls`。工具 Schema 使用 JSON Schema Draft 2020-12 校验，
支持本地 `$ref`，禁止加载外部引用。网页模型输出原生 DSML，适配器验证函数白名单、
参数类型与调用策略，再转换为标准 `tool_calls` 和 `finish_reason=tool_calls`。

兼容单/双全角竖线 DSML 标记与两种闭合标签；保留字符串参数空白和大整数精度。
不补全、不猜测截断的工具参数。流式请求可提前返回安全正文和思考，但 DSML 标记及其
后的正文暂存到完整验证后才输出，避免把工具标记当文字泄漏给调用方。

调用方执行工具后，携带上一轮 assistant.tool_calls 和匹配的 role=tool/tool_call_id
再次调用。适配器验证结果 ID 一一对应，并将历史调用恢复为 DSML。适配器本身不执行工具。

## 协议参考与验证

协议参考：[zouzhenglu/ds2api](https://github.com/zouzhenglu/ds2api)，核对版本
`5eeebf0d9a616a5dcea53b090bddee315f4ce531` 的 `lib/dsweb.js`、`lib/tools.js`。
复用本项目已有 Go 适配器和登录/PoW 实现，独立实现协议兼容，没有引入上游 Node 服务或复制源码。

在此目录运行 `go test -race ./...`。测试覆盖真实 HTTP 提前出流、取消和会话清理、
中途鉴权错误不重放、PoW 换新、工具 Schema/选择策略、DSML 分块隔离和工具结果续聊。
可通过 `DEEPSEEK_WEB_TEST_WASM_FILE` 指向当前官方 WASM 验证真实 PoW 求解。
测试使用本地模拟上游，不代表已经部署或完成真实账号验收。

服务定义在统一的 `deploy/docker-compose.adapters.yml` 中，带 `deepseek-web` profile。
标准部署在私有 `.env` 中设置 `SUB2API_DEEPSEEK_WEB=1` 后自动启用该 profile、
生成共享密钥并启动适配器；手工运行 Compose 时需叠加该文件、加 `--profile deepseek-web`
并自行设置共享密钥。
适配器在未导入账号前依然能通过 `/livez`，但模型请求会返回登录所需错误。
