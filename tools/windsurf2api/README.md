# Windsurf（Devin Connect）文本适配器

Windsurf 现在由 Cognition / Devin 云提供模型，Windsurf 桌面端与 CLI 通过
Connect-RPC 访问 `server.codeium.com` 的 `GetChatMessage`。该协议没有官方公开
SDK，本适配器实现了同一套 HTTP + protobuf 线上协议，把 Windsurf 云端模型
转成 OpenAI Chat Completions，供 Sub2API 的 `windsurf` 平台使用。

## 支持范围

- OpenAI Chat Completions 文本请求，支持流式 SSE 和非流式 JSON。
- `/v1/models` 读取该账号实时的模型目录（`GetCliModelConfigs`），
  暴露上游 selector 与其短别名。
- `system`/`developer` 合并为上游系统提示；`user`/`assistant` 作为会话历史。
- `max_tokens`/`max_completion_tokens`、`temperature`、`top_p` 映射到上游
  CompletionConfig。`temperature=0` 会钳到 `0.001`（上游拒绝精确 0）。
- 明确拒绝图片、音频、结构化输出、调用方工具（tools/tool_choice）等无映射字段。
- 用量来自上游 metadata（prompt / completion / cache read / cache write），
  缺失时省略 `usage`，不进行估算。
- 失败保持错误状态；流内失败发送 `error`，不追加成功结束帧或 `[DONE]`。

## 登录与密钥

Windsurf 提供官方 OAuth2 隐式流授权（编辑器 "Provide Authentication
Token" 备份登录用的同一入口）。管理页点击“登录 Windsurf”后，浏览器
打开 `windsurf.com/windsurf/signin` 完成授权，回调会附带
`devin-session-token$<JWT>`，页面标题为 “Provide Authentication Token”。
把该页显示的 Token（例如 `ott$…`）粘回页面即可：适配器会先用
`RegisterUser` 把它换成账号 Key，再校验并写入账号凭据。不需要手动创建或拷贝会话 Key。

| 密钥 | 保存位置 | HTTP 请求头 |
| --- | --- | --- |
| Windsurf 会话 Token | 账号 `credentials.api_key` | `Authorization: Bearer ...` |
| 部署内部共享密钥 | `WINDSURF_ADAPTER_KEY` / `builtin_adapter.windsurf_key` | `X-Sub2API-Adapter-Key` |

会话 Token 形如 `devin-session-token$<JWT>`，也可用其他方式获取后手动填入。
共享密钥不能替代账号 Token；主服务只向配置的内置
Windsurf 地址发送共享密钥，自定义外部 `base_url` 不会收到它。

## 本地运行

需要 Node.js `>=22.13`，无第三方依赖：

```sh
export WINDSURF_ADAPTER_KEY="$(openssl rand -hex 32)"
npm start
```

默认监听 `127.0.0.1:7869`，API 前缀 `/v1`。可配置：

| 环境变量 | 默认值 |
| --- | --- |
| `WINDSURF_HOST` | `127.0.0.1` |
| `WINDSURF_PORT` | `7869` |
| `WINDSURF_REQUEST_TIMEOUT_MS` | `600000` |
| `WINDSURF_MAX_CONCURRENT` | `4` |
| `WINDSURF_MAX_BODY_BYTES` | `4194304` |

`GET /livez` 不需要密钥，只验证进程存活；`GET /healthz` 需要共享密钥，
不证明账号可生成。`GET /v1/models` 和 `POST /v1/chat/completions` 需要两种密钥。

```sh
npm test
```

测试使用 mock 传输层，不访问真实 Windsurf 账号。

## 添加账号

在管理页面或用户账号页面选择 **Windsurf**，完成上方官方浏览器
授权（OAuth2）后创建账号。内置模式下 Base URL 可留空，协议固定为
Chat Completions。同步模型并选择白名单，再绑定 Windsurf 分组或开启兼容 OpenAI
分组的混合调度。

API 创建请求示例见 `sub2api-account.example.json`。独立部署主服务时配置
`BUILTIN_ADAPTER_ENABLED=true`、`BUILTIN_ADAPTER_WINDSURF_URL` 和
`BUILTIN_ADAPTER_WINDSURF_KEY`；共享密钥必须与 sidecar 的
`WINDSURF_ADAPTER_KEY` 相同。
