# DeepSeek 网页适配器

仓库内的 DeepSeek 网页版文本对话适配器。它通过 DeepSeek 网页会话访问
`chat.deepseek.com/api/v0`，向 Sub2API 提供 Chat Completions 接口。
与官方付费 API Key 的 `deepseek` 平台分开，使用 `deepseek_web` 平台。

## 登录

在「添加账号」中选择 DeepSeek 网页版，打开官方登录页，完成登录后导入浏览器的
Bearer token 和该账号对应的 `device_id`。适配器验证 `/users/current` 和
`/users/auth_token/check_device` 后，将凭据保存在 `DEEPSEEK_WEB_STATE_FILE`。
每个 DeepSeek 账号应使用自己的真实浏览器设备指纹。

DeepSeek 目前没有向第三方适配器提供 OAuth2 授权码回调接口，因此此流程是
**网页登录后的会话导入**，不是 OAuth2 授权。浏览器同源限制也不允许本系统
直接读取官方登录页的会话。不要把适配器共享密钥或网页 token 放入公开配置。

## 能力与运行

- 模型：`deepseek-web-chat`、`deepseek-web-reasoner`。
- 支持纯文本 Chat Completions；工具调用、文件、图片和其他采样参数会明确拒绝。
- 支持 SSE 响应格式；当前在上游回复完成后发送结果，不提供逐 token 实时转发。
- 运行时下载 DeepSeek PoW WASM。上游更新文件名后可设置 `DEEPSEEK_WEB_WASM_URL`。
- `DEEPSEEK_WEB_ADAPTER_KEY` 为必填共享密钥；`DEEPSEEK_WEB_LISTEN` 默认
  `127.0.0.1:7867`，Compose 中设为 `0.0.0.0:7867`。

可选的 `deploy/docker-compose.deepseek-web.yml` 包含服务和持久化卷；部署时将其
与基础 Compose 文件一起叠加，并在私有环境文件中设置共享密钥。
适配器在未导入账号前依然能通过 `/livez`，但模型请求会返回登录所需错误。
本地验证：在此目录执行 `go test -race ./...`。
