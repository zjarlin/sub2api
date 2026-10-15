# Sub2API v0 接入

网关源码在 okm252 的 `/tmp/sub2api-v0-platform`，分支 `feat/v0-platform`，基于部署版本 `996464cf1`。目前尚未部署，未更改线上账号或 Codex MCP 配置。

## 数据流

Codex 调用本机工具 → Sub2API `/v1/v0/*` → v0 官方 API v2 → 本机取回源码 → `~/prototypes/v0-随机目录` 与同名 ZIP。

服务端不保存生成项目；上游 v0 保存 chat。本机保存每次取回的独立快照，不覆盖现有项目。同步接口无需 webhook。返回的 `handle` 是访问会话的凭据，绑定网关 API Key、分组、上游账号及凭据指纹，有效期一年。更换网关签名密钥或上游凭据会使句柄失效。

## 已实现接口

- `POST /v1/v0/chats`：创建原型，响应头 `X-V0-Chat-Handle` 返回网关签发的句柄。
- `POST /v1/v0/chats/{handle}/messages`：继续修改。
- `GET /v1/v0/chats/{handle}`：会话详情。
- `GET /v1/v0/chats/{handle}/files`：读取源码，包括 UTF-8 和 base64 文件。
- `GET /v1/v0/chats/{handle}/files/download`：上游 ZIP 接口。
- `GET /v1/v0/chats/{handle}/preview`：预览信息。
- `GET /v1/v0/chats/{handle}/messages`：消息列表。

不开放团队 chat 列表、hooks 或其他管理接口。请求上限沿用网关；响应最多 64 MiB，超时十分钟。上游固定为 `https://api.v0.dev/v2`。复用账号调度、并发控制和计费资格检查，记录成功生成/修改的调用；没有虚构 token、固定调用单价或额外扣费，上游 v0 的实际消耗仍由 v0 结算。

## 部署后配置

1. 通过标准集群部署流程部署该版本并运行数据库迁移。
2. 管理后台创建 `v0` 平台的 API Key 账号，填写用户提供的 v0 上游密钥；密钥不得写进 Git。
3. 绑定 v0 或 composite 分组，并给客户端创建可访问该分组的网关 API Key。
4. 本机配置 `SUB2API_API_KEY_FILE` 指向权限为 600 的网关密钥文件，或用环境变量 `SUB2API_API_KEY`。这是网关密钥，不是 v0 上游密钥。
5. 进行真实生成、追问、源码取回验收。当前测试未调用付费生成接口。

## 本机 CLI

```bash
export SUB2API_BASE_URL=http://192.168.31.252:18080
export SUB2API_API_KEY_FILE=/absolute/path/to/gateway-key
python3 /Users/zjarlin/.codex/tools/v0-platform/v0_prototype.py v0_create --prompt '生成一个中文设备管理后台' --framework 'Vue 3 + Vite'
```

命令返回 `directory`、`archive`、`files` 和 `handle`。指定 Vue 会作为 v0 的框架指令发送，生成结果还需要实际编译验收。

## 本机 MCP

同一脚本加 `--mcp` 即为 stdio MCP 服务，提供 `v0_create`、`v0_followup` 和 `v0_download`。部署和分组密钥配置完成后，再注册到 Codex，工具调用超时应设置为 900 秒。

MCP 提供工具能力；是否调用由 Codex 根据需求与工具描述决定。当前没有添加 System One 对原型需求的自动分类，也没有把 v0 当成 Responses 模型供应商。

## 验证

- Go 全项目编译。
- v0 凭据校验、签名篡改/过期、接口白名单与迁移测试。
- 前端类型检查、Vite 生产构建、平台目录测试、v0 账号创建表单测试。
- Python 本机产物写入、ZIP、base64、路径越界与未完成生成恢复测试；MCP 初始化与工具发现。

这些是源码与本机工具验证，不代表已部署或完成线上真实生成验收。
