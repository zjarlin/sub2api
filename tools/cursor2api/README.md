# Cursor SDK 文本适配器

使用官方 [`@cursor/sdk`](https://cursor.com/docs/api/sdk/typescript)，固定版本 `1.0.32`。
Sub2API 通过独立 `cursor` 平台接入本地 SDK 文本生成。
需要 Cursor Dashboard 创建的 API Key，且账号应有相应 SDK 和模型权限。

## 支持范围

- OpenAI Chat Completions 文本请求，支持流式 SSE 和非流式 JSON。
- `/v1/models` 动态读取该账号的真实模型目录，同时暴露 SDK 参数变体。
  没有静态默认模型。后台同步模型后可以配置白名单和别名映射。
- `reasoning_effort` 仅在模型目录暴露对应参数时映射，其他值返回 400。
- 每次生成创建独立临时目录与 `JsonlLocalAgentStore`，完成后删除；账号和请求之间不复用 Agent 会话。
- SDK 在隔离的 worker 中运行。超时、断连或关闭服务时终止 worker，父进程清理请求目录；
  没有取消参数的模型列表和 Agent 创建阶段也受请求超时限制。
- `system`、`developer`、`user` 和 `assistant` 历史作为完整 JSON 提示发送。
  这是文本上下文适配，SDK 不提供原生 OpenAI 角色语义。
- 禁用 SDK 内置工具、MCP 和本机设置层。不提供文件、shell、网络工具或调用方工具。
- 明确拒绝图片、音频、工具结果、结构化输出及无 SDK 映射的参数，例如
  `temperature`、`top_p`、`max_tokens`、`max_completion_tokens` 和 `stop`。
- 用量存在时使用 SDK 返回的实测 token 数。SDK 不返回用量时省略 `usage`，
  不进行估算；此时 Sub2API 用量日志的零计数不代表没有上游消耗。
- 失败保持错误状态；流内失败发送 `error`，不追加成功结束帧或 `[DONE]`。
- 主网关可将不含上述限制参数的 Responses 文本请求转换为 Chat Completions。
  Anthropic Messages 必填的 `max_tokens` 没有 SDK 映射，暂不支持该协议。
  不支持 SDK 原生 Responses、多模态、工具调用或远程压缩。

## 密钥

| 密钥 | 保存位置 | HTTP 请求头 |
| --- | --- | --- |
| Cursor Dashboard API Key | 账号 `credentials.api_key` | `Authorization: Bearer ...` |
| 部署内部共享密钥 | `CURSOR_ADAPTER_KEY` / `builtin_adapter.cursor_key` | `X-Sub2API-Adapter-Key` |

共享密钥不能替代账号 Key。主服务只向配置的内置 Cursor 地址发送共享密钥，
自定义外部 `base_url` 不会收到它。独立外部适配器所需的准入头应在账号请求头覆写中配置。

## 本地运行

需要 Node.js `>=22.13`。在本目录安装依赖并启动：

```sh
npm ci --ignore-scripts --no-audit --no-fund
export CURSOR_ADAPTER_KEY="$(openssl rand -hex 32)"
npm start
```

默认监听 `127.0.0.1:7868`，API 前缀 `/v1`。可配置：

| 环境变量 | 默认值 |
| --- | --- |
| `CURSOR_HOST` | `127.0.0.1` |
| `CURSOR_PORT` | `7868` |
| `CURSOR_REQUEST_TIMEOUT_MS` | `300000` |
| `CURSOR_MAX_CONCURRENT` | `4` |
| `CURSOR_MAX_BODY_BYTES` | `4194304` |

`GET /livez` 不需要密钥，只验证进程存活；`GET /healthz` 需要共享密钥，
同样不证明账号可生成。`GET /v1/models` 和 `POST /v1/chat/completions` 需要两种密钥。
服务不持久化账号 Key，但为了 SDK 调用会在进程内存中使用它。

```sh
npm test
```

测试使用 SDK mock，不访问真实 Cursor 账号。

## Docker 部署

主服务必须先更新为包含 Cursor 支持的代码或镜像。迁移 `256_add_cursor_platform.sql`
随新版主服务启动执行，允许 Cursor 配额与 Composite 路由。

标准 Compose 在仓库根目录叠加 `deploy/docker-compose.cursor.yml`，并在私有 `.env`
配置 `CURSOR_ADAPTER_KEY`。按部署原有方式配置主服务镜像，构建并启动：

```sh
docker compose --project-directory . --env-file .env \
  -f deploy/docker-compose.yml -f deploy/docker-compose.cursor.yml \
  up -d --build sub2api-cursor sub2api
```

镜像以非 root 用户运行，根文件系统只读，临时会话写入 `/tmp`，不发布宿主机端口。
主服务等待适配器进程健康后启动；该健康检查不消耗 Cursor 额度。

252 集群使用现有 `deploy/cluster/deploy-252.sh`：在 `/opt/sub2api/.env` 中设置
`SUB2API_CURSOR=1`。脚本自动生成并复用内部共享密钥、构建 sidecar、等待进程健康，
再执行主服务灰度发布。无需在部署环境放置真实 Cursor 账号 Key。

## 添加账号

在管理页面或用户账号页面选择 **Cursor**，填写 **Cursor Dashboard API Key**。
内置模式下 Base URL 可留空，协议固定为 Chat Completions。
同步模型并选择白名单，再绑定 Cursor 分组或开启兼容 OpenAI 分组的混合调度。
空模型的连接测试优先使用配置的模型或已同步目录，首次测试会先读取实时模型目录。

API 创建请求示例见 `sub2api-account.example.json`。独立部署主服务时配置
`BUILTIN_ADAPTER_ENABLED=true`、`BUILTIN_ADAPTER_CURSOR_URL` 和
`BUILTIN_ADAPTER_CURSOR_KEY`；共享密钥必须与 sidecar 的 `CURSOR_ADAPTER_KEY` 相同。
