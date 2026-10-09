# 码道 CodeArts Ask 适配器

将官方 IDE 插件的 **Ask 纯文本对话**协议接入 Sub2API 的独立 `madao` 平台，提供
`/v1/models` 和 `/v1/chat/completions`，支持非流式、流式和完整文本对话历史。

## 协议与授权

- 上游：`https://snap-access.cn-north-4.myhuaweicloud.com`。
- 对话：`POST /v1/chat/chat`，`task: chat`，文本消息采用 `type/content`，
  `enable_code_interpreter: false`，不发送 Agent ID、代码库、附件或客户端工具。
- SSE：只转发 `delta.content` 或旧版累计 `text` 的增量；忽略元数据和推理内容；
  JSON `text: "[DONE]"` 才表示完成。工具事件、上游错误或中断不能作为成功回复返回。
- 身份校验：签名的 `GET /snap-manager/v1/current/user`。
- 模型目录：签名的 `GET /v1/model/builtin`，再查询默认 Ask 专家目录；目录暂时不可用时
  使用内置模型目录。默认模型 `GLM-5.2`，支持别名 `madao`、`madao-code`。
- 签名复用官方 `huaweicloud-sdk-go-v3`，对实际 HTTP 正文和查询参数进行 SDK-HMAC-SHA256 签名。

Ask 使用官方插件的独立 OAuth 授权。网页 Cookie/cftk 不能用于这条接口；旧账号需要重新授权，
不会回退到 CloudAgent 任务。适配器已移除 CloudAgent 会话创建与执行路径。

## 网页登录

在账号页选择「码道 CodeArts」，点击「登录码道」：

1. 部署内的隔离 Chromium 打开官方插件授权页；管理页面显示截图并转发鼠标、键盘输入。
2. 使用华为云账号完成登录、验证码及授权。
3. 隔离浏览器的回环地址接收授权码，适配器通过 PKCE 和 ES256 DPoP 交换临时 IAM 凭据。
4. 验证当前用户身份后，将临时密钥、刷新令牌、PKCE verifier 和 DPoP 私钥原子保存到
   `MADAO_STATE_FILE`（文件权限 `0600`）。请求前自动刷新即将到期的密钥并保存轮换后的令牌。

账号表单无需填写上游密钥或地址，后台注入内部共享密钥。登录凭据不通过剪贴板传递。

## 支持范围

仅支持文本角色 `system/developer/user/assistant`，完整历史拍平后传给 Ask。
工具调用、工具结果与图片被拒绝。部分兼容采样参数允许提交但不影响上游 Ask 行为。

环境变量：

- `MADAO_ADAPTER_KEY`：必填内部共享密钥。
- `MADAO_LISTEN`：默认 `127.0.0.1:7870`。
- `MADAO_STATE_FILE`：默认 `/app/data/credential.json`。
- `MADAO_ASK_BASE_URL`：原生 Ask 上游地址。
- `MADAO_BROWSER_EXECUTABLE`：授权所用 Chromium 路径。

252 通过 TeamCity `Deploy 252 Cluster` 部署。`SUB2API_MADAO=1` 启用
`deploy/docker-compose.madao.yml`，凭据保存在持久化卷，适配器端口不发布到公网。

## 验证

`go test -race ./...` 和 `go vet ./...` 验证签名、Ask 帧解析、HTTP 流式/非流式转换、
OAuth 回调、DPoP、并发刷新、凭据持久化、旧 Cookie 重新授权和错误处理。

设置 `MADAO_E2E_URL` 和 `MADAO_E2E_KEY` 后运行
`go test -run TestAskConversationE2E -v`，可验证真实模型目录、非流式回复、带历史的流式对话、
执行命令请求的文本拒绝，以及客户端工具拒绝。共享密钥应通过环境注入。

实际验收应使用授权后的账号调用 Sub2API `/api/v1/admin/accounts/{id}/test`，
确认出现回复正文及成功的 `test_complete`。登录成功或 HTTP 200 本身不代表对话测试通过。
