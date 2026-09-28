# VibeX 内置账号平台

适配 RunningHub/VibeX 的网页项目编码会话，向 Sub2API 提供文本 Chat Completions。
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

- 支持文本 `messages`（system/developer/user/assistant）、`model`、`stream`、
  `stream_options.include_usage`；文本数组也受支持。
- 自定义工具、工具结果、图片、temperature、max_tokens 等尚未支持的参数返回 400。
- 每个请求使用新的上游会话，项目串行使用；发现已有运行中的任务返回 409。
- 支持流式文本、结束、取消和上游用量。失败时取消上游并中止响应，不把部分输出标成成功。
- WebSocket 错误中的已知认证、余额和额度代码分别映射到 401/402/429。
- 上游运行的是编码 Agent，其内置工具行为不等同于调用方工具调用。提示词要求只回答文本，
  但这不能保证上游不执行内置工具；因此必须使用专用项目。
- 新增平台可通过现有 OpenAI 网关及组合路由使用。Codex 工具工作流仍不受支持。

## 验证

```sh
go test -race ./...
go vet ./...
```

测试使用模拟 HTTP/WebSocket 上游，覆盖认证、HTTP 200 中的认证错误、凭证权限及恢复、
模型目录、请求参数拒绝、新会话隔离、流式输出、用量、忙碌项目、余额/额度错误。
真实账号验收仍需验证 RunningHub 登录、账号目录、专用项目创建、WebSocket 消息顺序和生成。
公开前端协议可能变化，模拟测试通过不能替代真实账号验收。
