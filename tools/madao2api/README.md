# 码道（华为云 CodeArts 代码智能体 Web 端）适配器

仓库内的码道 Web 适配器。它复用一次**浏览器登录后的华为云会话**（Cookie + cftk 令牌），
经由码道站内 CloudAgent 会话协议访问 `https://devcloud.cn-north-4.huaweicloud.com/chat`，
向 Sub2API 提供 OpenAI 兼容接口。与任何官方华为云模型 API 平台分开，使用独立的 `madao` 平台。

## 为什么是网页会话适配器

码道（CodeArts 代码智能体）是订阅制 Web/IDE 产品，只通过小程序、Web 端与 IDE 插件访问，
**没有公开的 API Key、没有 OpenAI 兼容端点、也没有 OAuth2 授权码接口**。因此本适配器属于
「网页登录后的会话导入」，不是 OAuth2 授权，也不是官方 API 代理。

## 上游协议（已逆向确认）

- 站内根：`https://devcloud.cn-north-4.huaweicloud.com/chat`
- 鉴权：华为云 SSO Cookie（关键 Cookie `devclouddevuibjtcftk`）+ 请求头 `cftk`
- 会话校验：`GET /rest/me`
- 模型目录：`GET /PromptCenterService/v1/agent-center/agents/detail?agent_id=...`（失败回退内置目录）
- 文本对话（与当前网页一致）：
  1. `POST /v1/cloudagent/sessions`，body `{}` → `result.session_id`
  2. `POST /codebaseservice/v1/cloudagent/sessions/{id}/messages`，body `{content, model_id, repos:[]}`，该响应直接返回 SSE
  3. 按 `event: message` 的 `data.content` 转发正文，`event: done` 完成；思考和工具事件不混入正文
  4. `DELETE /v1/cloudagent/sessions/{id}` 清理本次请求创建的临时任务
- 请求统一带 `x-codearts-doer-scenario-type: web`；`/v1/cloudagent/` 请求带 `Agent-Type: CodeBase`，模型目录带 `Agent-Type: AgentCenter`。
- 原 `/kernel/sessions/create` 在当前 Web 部署返回 404，不能用它验证聊天。

## 登录（网页登录导入）

在「添加账号」选择 **码道 CodeArts**，类型 `apikey`，点击「登录码道」：

1. 适配器启动部署内的隔离 Chromium，打开码道登录页；
2. 管理页面轮询并显示浏览器截图；
3. 在截图中用华为云账号完成登录（验证码/二次验证可选）；
4. 适配器导出码道域 Cookie 与 cftk，调用 `GET /rest/me` 校验后落盘到 `MADAO_STATE_FILE`。

会话凭据只发送到此部署的隔离浏览器与站内接口，不经过剪贴板。账号表单无需填写地址与共享密钥
（后端注入）。账内 API Key 是内部共享密钥，不能替代上游凭据。

## 能力与运行

- 模型：从站内目录的 `gpts.models[].model_parameters.model_id` 读取，默认 `GLM-5.2`，另支持别名 `madao`、`madao-code`。
- 支持纯文本 Chat Completions；工具调用、图片与其他采样参数会被明确拒绝（码道 Agent 自带工具）。
- 每个请求创建一个一次性会话，把完整对话拼为单条提示；流式响应按文本增量转发 SSE。
- 环境变量：`MADAO_ADAPTER_KEY`（必填共享密钥）、`MADAO_LISTEN`（默认 `127.0.0.1:7870`）、
  `MADAO_STATE_FILE`（默认 `/app/data/credential.json`）、`MADAO_BASE_URL`、`MADAO_BROWSER_EXECUTABLE`。

## 部署

252 集群在 `.env` 设置 `SUB2API_MADAO=1` 后，`deploy/cluster/deploy-252.sh` 自动叠加
`deploy/docker-compose.madao.yml`、生成并持久化 `MADAO_ADAPTER_KEY` 并启动 `sub2api-madao`
（端口不发布到公网）。后台读取 `BUILTIN_ADAPTER_MADAO_URL` 与 `BUILTIN_ADAPTER_MADAO_KEY`。

## 边界与风险

- 上游为私有 Web 协议，可能随码道前端升级而变动；出现解析失败时需对照新前端 bundle 更新。
- 会话 Cookie 会过期；过期后需在账号设置中重新登录。
- 将自动化用于活动抽奖等行为可能违反码道服务条款；本适配器只用于文本对话。

## 验证

`go test ./...` 验证会话创建、消息 POST 流式响应、非流式聚合、模型目录、命名 SSE 事件及失败清理。
实际验收需要对登录后的账号调用 Sub2API `/api/v1/admin/accounts/{id}/test`，确认 SSE 中出现回复正文和成功结束；HTTP 200 或登录成功都不足以证明聊天可用。
