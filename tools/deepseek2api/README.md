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
验证 UID 与原账号一致，再原子保存新 token 和设备信息，并重试当前对话一次。
同一账号的并发请求共用一次登录；自动登录最多等待 90 秒，失败后暂停重试 1 分钟。
额度、限流和其他错误不会触发重新登录。验证码、密码错误等无法自动完成的情况会返回
HTTP 503 `deepseek_relogin_failed`，提示在账号设置中重新登录，避免暂时的登录失败
将 Sub2API 账号永久停用。旧账号未保存密码时返回 HTTP 401 `deepseek_login_required`。

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

可选的 `deploy/docker-compose.deepseek-web.yml` 包含服务和持久化卷。标准部署
在私有 `.env` 中设置 `SUB2API_DEEPSEEK_WEB=1` 后自动叠加该文件、生成共享密钥并
启动适配器；手工运行 Compose 时需自行叠加文件并设置共享密钥。
适配器在未导入账号前依然能通过 `/livez`，但模型请求会返回登录所需错误。
本地验证：在此目录执行 `go test -race ./...`。
