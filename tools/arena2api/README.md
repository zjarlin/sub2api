# Arena 文本试接适配器

复用 [Gandhara2077/arena-local-bridge](https://github.com/Gandhara2077/arena-local-bridge)
的浏览器核心，固定提交 `293263b0df6f380f89406c418690df82f7e28d38`。
上游通过带完整性校验的固定版本 tarball 安装，保留其 MIT LICENSE 和 NOTICE。
适配器不运行上游本地操作服务器，不暴露返回密钥的 `/api/status` 或本地 MCP。

## 支持范围

- OpenAI Chat Completions 文本请求、SSE 和非流式 JSON。
- 配置的模型别名对应一段专属 Arena 会话；每段会话只允许一个客户端身份。
- system/developer、用户与 assistant 历史按完整 JSON 上下文传入网页 Agent。
  这是提示文本层面的协议适配，不是 Arena 原生的角色或函数协议，也不能清除已有 Arena 业务历史；必须准备专用新会话。
- 全实例并发 1；忙时返回 503，客户端取消及超时关闭浏览器读取。
- 会话绑定、完成的幂等结果和中断状态保存在私有数据目录中；重启后保持。
- 明确拒绝调用方工具、工具结果、图片、文件、音频、结构化输出和未实现的生成参数。
  未知参数也会返回 400；`tools: null` / `tools: []` 和已知参数的 null 值视为未设置。
  `user`、`metadata`、会话标识只作为传输元数据，不控制生成；`stream_options.include_usage` 可发送，但不提供实测用量。
- Token 用量不可用：响应省略 `usage`，设置 `X-Arena-Usage-Source: unavailable`。
  Sub2API 现有用量日志仍可能显示零计数，该值不代表真实零消耗。此试接渠道不能用于按实测 Token 收费。

上游 `ping` 不进入模型列表；没有登录和会话时服务可启动，但生成返回 503。
`/healthz` 的 configured 状态只说明配置可用，不证明 Arena 当前能生成。

## 本地启动

需要 Node.js 22+。在本目录执行：

```sh
npm ci --ignore-scripts --no-audit --no-fund
npx --no-install playwright install chromium
npm start
```

默认 API 地址：`http://127.0.0.1:7867/v1`。
默认数据目录：`~/.sub2api-arena`。首次启动生成 0600 权限的 `.env`，包含适配器 Bearer 密钥和凭证加密密钥，运行时不会打印其值。
已有上游登录数据可显式通过 `DATA_DIR` 指定目录；已归档会话通过 `ARENA_ARCHIVE_DIR` 指定，只有带模型识别结果和所属账号的归档记录进入目录。
修改目录前应确认它只包含试接使用的专属会话。

可配置 `ARENA_HOST`、`ARENA_PORT`、`ARENA_REQUEST_TIMEOUT_MS`（默认 300000，范围 1000-900000）、`ARENA_AGENT_PROXY`。
禁止多进程共享同一个数据目录或同一段 Arena 会话；若扩容，需要独立目录和独立会话。

### macOS 后台运行

本目录安装依赖后，可由当前用户的 launchd 管理进程：

```sh
arena_data_dir="${DATA_DIR:-$HOME/.sub2api-arena}"
arena_node="$(command -v node)"
mkdir -p -m 700 "$arena_data_dir"
launchctl submit -l site.addzero.sub2api-arena.local \
  -o "$arena_data_dir/service.log" -e "$arena_data_dir/service.err.log" \
  -- /usr/bin/env DATA_DIR="$arena_data_dir" ARENA_HOST=127.0.0.1 ARENA_PORT=7867 \
  "$arena_node" "$PWD/src/index.mjs"
launchctl list site.addzero.sub2api-arena.local
curl --fail http://127.0.0.1:7867/livez
```

此方式保持当前用户登录期间的后台运行；未安装开机自启任务。
停止执行 `launchctl remove site.addzero.sub2api-arena.local`。
更新代码后先停止，再执行上述 submit 命令重新启动。

## 登录和会话配置

先在终端登录，密码输入不会回显，也不需要放进命令参数：

```sh
npm run manage -- login --email your-account@example.com
```

登录凭据加密保存。主服务每次生成前重新读取凭据，所以登录后无需重启。
接近 Cookie 到期时用保存的登录凭据刷新；验证码、账号限制或登录失效需要重新处理真实登录。

在 Arena 网页创建一段专门用于试接的会话，确认它可以回答并记录 URL 中的 UUID。
模型归属须以实际识别结果为准，不能靠模型自行报出的名字确认。
配置专用别名：

```sh
npm run manage -- add-session --model arena-session --session YOUR-SESSION-UUID --email your-account@example.com
npm run manage -- models
npm run manage -- status
```

`models.json` 和归档目录都会在请求时读取，不需要重启。
更换对话、清空对话页或改用其他客户端后，必须配置新的 Arena 会话；原会话不会自动交给新客户端。
中断后的会话状态标为结果不确定，继续生成会返回 409。请检查上游状态并配置新的专属会话，不要删绑定文件后把已有上下文重新发给其他用户。
浏览器断开时，上游内置恢复也不允许再次提交已经开始发送的一轮，避免恢复动作重复执行请求。

## HTTP 接口

| 接口 | 鉴权 | 用途 |
| --- | --- | --- |
| `GET /livez` | 无 | 进程存活；不检查生成 |
| `GET /healthz` | Bearer | 配置就绪状态；未配置返回 503 |
| `GET /v1/models` | Bearer | 已配置别名 / 已识别归档会话 |
| `POST /v1/chat/completions` | Bearer | 文本生成 |

每次生成必须发送稳定的 `X-Arena-Session-Id`，也接受 `X-Codex-Session-Id` 或 `X-Session-Id`。
同一段对话使用同一个身份，多个对话使用不同身份。
`Idempotency-Key` 或 `X-Arena-Idempotency-Key` 标识逻辑请求；重试保留原值，新一轮使用新值。
同一个幂等键换消息会返回 409；没有幂等键时，相同文本再次提问会启动新一轮。
每段会话最多保留 500 份带键的请求结果，达到上限后需配置新会话，不会悄悄删除旧键并重复执行。

流式失败通过 SSE error 返回，不发送成功终止帧或 `[DONE]`；网关会将其视为失败。

## 接入 Sub2API

使用 `sub2api-account.example.json` 的字段创建 `arena` / `apikey` 账号，并分配独立试接分组。
Arena 已有独立平台入口；地址与适配器密钥需显式填写，固定为文本 Chat Completions 和单并发。
原有 `openai` / `apikey` 加 `openai_session_adapter=arena` 的试接账号仍可使用。
创建请求可发到后台 `POST /api/v1/admin/accounts`，共享密钥取本机 `.env`，不要把真实密钥放进版本库。

三个关键 `extra` 设置：

```json
{
  "openai_session_adapter": "arena",
  "openai_responses_mode": "force_chat_completions",
  "model_health_probe_enabled": false
}
```

`openai_session_adapter=arena` 开启此账号专用的身份传递、取消及流内错误处理。
网关将客户端身份与认证 API Key ID 一起哈希，确保不同 Key 使用相同会话名称时仍然隔离。
只传递会话 ID 和幂等键，不传递本地工作目录。静态请求头覆写不能覆盖隔离后的身份。
Responses 的 `prompt_cache_key` 也可作为会话身份。缺少稳定身份时请求返回 400，不用首条消息内容猜测会话。
现有对话页已发送每段对话的身份以及每轮请求的幂等键。

本机 Sub2API 的 Base URL 为 `http://127.0.0.1:7867/v1`；容器主服务访问该地址代表容器自己，需使用同网络下的 `http://sub2api-arena:7867/v1` 或宿主机实际可达地址。
不开放自动生成探测，也不加入公共模型降级链。后台不带会话身份的生成测试会被拒绝；真实生成须通过带身份的专用客户端验证。

更新既有账号时先读取并合并 `extra`，通用账号 PUT 会替换整个对象。

## Docker 部署

从仓库根目录叠加独立配置，设置随机 `ARENA_AGENT_BRIDGE_KEY`：

```sh
docker compose --project-directory . \
  -f deploy/docker-compose.yml -f deploy/docker-compose.arena.yml \
  --profile arena up -d --build sub2api-arena
```

服务不发布公网端口，使用 `arena-data` 命名卷，保存凭据、加密密钥、模型配置和绑定。
可用 `ARENA_STATE_DIR` 替换成私有宿主机目录，需允许 UID 1000 读写。
使用上述相同 Compose 参数执行 `exec -it sub2api-arena npm run manage -- login` 和 `add-session` 完成配置。
252 集群部署可在私有 `.env` 中设置 `SUB2API_ARENA=1`，部署脚本会叠加此服务并保留适配器密钥。
下载 Debian 依赖较慢时，可用 `ARENA_DEBIAN_MIRROR` 指定镜像根地址；APT 仍校验发行版签名。

## 验证

```sh
npm test
```

测试覆盖本地 HTTP 协议、消息保留、跨客户端拒绝、重启后绑定与幂等重放、SSE 结束与失败、串行限制、取消、超时及密钥文件权限。
测试生成器使用模拟上游，不能作为真实 Arena 登录和生成的证据。
配套 Go 定向测试覆盖网关身份隔离、Responses 转换、取消及流内失败；Compose 配置可离线校验。
Docker 存活检查只验证进程可用；真实 Arena 登录和生成需单独验收。
