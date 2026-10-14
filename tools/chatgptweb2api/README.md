# ChatGPT 网页版无登录问答适配器

把 **未登录** 的 `https://chatgpt.com` 网页当作上游，向 Sub2API 暴露 OpenAI 兼容的
`/v1/chat/completions`、`/v1/responses` 与 `/v1/models`，以现有 **OpenAI API Key**
账号接入 codex 分组，**无需新增平台或数据库迁移**。默认经出口代理访问，因此出口 IP
与可访问性由代理决定。

它不是官方 API，也不是账号登录/OAuth：不携带任何 ChatGPT 账号 Cookie，生成完全由匿名
网页完成，模型固定为网页默认模型。

## 为什么需要浏览器

匿名对话不是简单的 JSON 接口。发起一轮需要 `sentinel/chat-requirements` 的
**PoW 挑战** + 一次性 `conduit_token`，会话状态保存在浏览器里；直接 `curl` 站点会拿到
Cloudflare `403`。因此适配器用一次性 Chromium（Go + chromedp，与 `tools/deepseek2api`
同构）完成：打开首页 → 在 composer 输入 → 点击发送按钮 → 轮询结构化的
`#octane-conversation-transcript` JSON 取回助手文本。

## 出口代理

匿名网页会按出口 IP 做风控与限流，因此 **必须经代理** 出网：

    CGW_UPSTREAM_PROXY=http://sub2api-openai-egress:7890

复用现有的 `US egress for OpenAI OAuth` 出口（美国 IP）已实测可用；容器未接入该网络时
用 `CGW_DOCKER_NETWORK` 指定。

## 启动配置

```sh
export CGW_API_KEY='<独立适配器密钥>'
export CGW_MODELS='gpt-web'
export CGW_UPSTREAM_PROXY=http://sub2api-openai-egress:7890
./chatgptweb2api
```

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `CGW_API_KEY` | 必填 | 适配器 Bearer 密钥，Sub2API 账号只保存它 |
| `CGW_MODELS` | 必填 | 逗号分隔的对外模型 ID，例如 `gpt-web` |
| `CGW_LISTEN` | `127.0.0.1:7881` | 监听地址 |
| `CGW_UPSTREAM_PROXY` | 空 | 访问 chatgpt.com 的 HTTP 代理；**建议必填** |
| `CGW_HEADLESS` | `1` | 置 `0` 走有头模式（排障用，需 Xvfb） |
| `CGW_BROWSER_EXECUTABLE` | 空 | 指定 Chromium 路径；空则用系统 `chromium`/`google-chrome` |
| `CGW_USER_AGENT` | Chrome/139 macOS | 伪造 UA |
| `CGW_ANSWER_TIMEOUT` | `180` | 单轮等待答案的最长秒数 |
| `CGW_DEBUG` | 空 | 置 `1` 输出每轮读取的 URL/文本尾部，仅排障 |

适配器不读取系统 `HTTP_PROXY/HTTPS_PROXY`，代理仅用于访问 chatgpt.com。

## Sub2API 账号与 codex 分组

按 `sub2api-account.example.json` 配置账号：

1. 平台选择 OpenAI，类型选择 API Key，名称填 ChatGPT Web。
2. Base URL 填 Sub2API 能访问的适配器地址，例如 `http://chatgptweb2api:7881`。
3. API Key 填 `CGW_API_KEY`，绑定 codex 分组。示例 group id 6，导入前确认当前环境。
4. 模型映射只放 `CGW_MODELS` 中已配置的 ID。
5. Responses 支持设为 true、模式设为 force_responses，关闭上游 Responses WebSocket。
6. 目标分组可见目录需包含该模型 ID。

```sh
curl "$SUB2API_URL/v1/chat/completions" \
  -H "Authorization: Bearer $CODEX_GROUP_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-web","messages":[{"role":"user","content":"简要说明什么是 MQTT"}]}'
```

## 协议边界

- 只支持 system/developer/user/assistant 文本；历史会折成一段提示交给网页。
- 工具、工具结果、图片、音频、结构化输出、多候选返回 400。
- 不存储 Responses 历史；`previous_response_id`/`conversation`/`background` 返回 400，
  续聊要提交完整文本历史。
- `stream=true` 是**缓冲式 SSE**：网页生成完成后才下发文本与终止事件，非逐 token 实时。
- 网页不返回 token 统计，适配器按文本长度估算 `usage`（约 4 字符/token）。这是**近似值**，
  仅为满足网关解析与计费，不代表上游真实用量。
- 并发由单个浏览器实例串行化；匿名对话偶发完全不响应，适配器会在总超时内重开首页重试，
  失败返回 502 明示上游原因，不伪装成 completed。

## 验证

```sh
go test ./...
```

测试使用假浏览器上游，覆盖鉴权、模型目录、提示折叠、Chat/Responses（含流式）与错误边界。

```sh
# 本机端到端（需容器接入出口代理并指定 Chromium）
docker run --rm --network sub2api_sub2api-network \
  -e CGW_API_KEY=test -e CGW_MODELS=gpt-web \
  -e CGW_UPSTREAM_PROXY=http://sub2api-openai-egress:7890 \
  -e CGW_BROWSER_EXECUTABLE=/ms-playwright/chromium-1234/chrome-linux64/chrome \
  -v "$PWD:/app" -w /app mcr.microsoft.com/playwright:v1.62.0-noble \
  bash -lc 'go build -o /tmp/a . && /tmp/a & sleep 3; curl -s localhost:7881/v1/chat/completions -H "Authorization: Bearer test" -H "Content-Type: application/json" -d "{\"model\":\"gpt-web\",\"messages\":[{\"role\":\"user\",\"content\":\"What is 17*23? Reply with only the number.\"}]}"'
```

2026-10-13 实测：经 `sub2api-openai-egress:7890` 匿名提问 `What is 17*23?` 返回 `391`，
`/v1/chat/completions` 与 `/v1/responses` 均通过。

2026-10-14 已在 252 接入：适配器容器 `sub2api-chatgptweb`（镜像 `sub2api-chatgptweb:local`，
网络 `sub2api_sub2api-network`，回环端口 7881），Sub2API 账号 892 `ChatGPT Web`
（OpenAI / API Key / codex 分组 6）。经网关 codex 分组实测：非流式与流式 `/v1/responses`
返回 `completed`，`/v1/chat/completions` 正常，`usage_logs` 记录命中账号 892。
适配器密钥保存在 `/opt/sub2api` 之外的私有位置，不进入 Git。

## 限制与风险

- 匿名网页按出口 IP 风控；**高并发/高频请求可能被限流或拦截**，这是上游策略而非适配器缺陷。
- 网页 DOM/协议随时可能变化，选择器与 `octane-conversation-transcript` 结构需随之更新。
- 仅适合低频、无工具的纯问答；不适合 Codex 编码回合（带工具定义会被 400 拒绝）。
- 不保证长期可用：这是对免费网页的自动化利用，与任何官方条款、配额无关。
