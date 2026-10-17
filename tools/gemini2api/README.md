# Gemini 文本问答适配器

使用 AI Studio 的 Gemini API Key，通过 Google 官方 OpenAI 兼容端点生成文本。
提供 `/v1/chat/completions`、`/v1/responses` 和 `/v1/models`，以现有 **OpenAI API Key**
账号接入 Sub2API，可绑定平台为 OpenAI 的 **codex 分组**，无需新增平台或数据库迁移。
适配器密钥与 Gemini 上游密钥独立；Sub2API 账号只保存适配器密钥。

这是 API Key 接入，不包含 Gemini 网页登录或 Google One OAuth。

## 启动配置

Python 3.6+，无第三方依赖；容器使用 Python 3.12。

```sh
export G2A_API_KEY='<独立适配器密钥>'
export GEMINI_API_KEY='<AI Studio API Key>'
export G2A_MODELS='gemini-2.5-flash'
python3 server.py
```

需要走出口代理时，设置 `G2A_UPSTREAM_PROXY=http://<代理地址>:<端口>`。
适配器不读取系统 HTTP_PROXY/HTTPS_PROXY；代理仅用于访问 Gemini。

默认监听 `127.0.0.1:7877`。模型列表必须显式配置，使用自己账号实际可调用的模型 ID；
模型示例不代表账号拥有权限。`G2A_LISTEN` 可调整监听地址。
生产部署走仓库内置适配器约定：在 `/opt/sub2api/.env` 设置 `SUB2API_GEMINI=1`、
`GEMINI_API_KEY` 和 `GEMINI_MODELS` 后，通过 TeamCity `sub2api / Deploy 252 Cluster`
或 `deploy/cluster/deploy-252.sh` 启用统一的 `deploy/docker-compose.adapters.yml` 里
`gemini` profile。
部署脚本会自动生成 `GEMINI_ADAPTER_KEY`，适配器容器名为 `sub2api-gemini`，
主服务通过 `http://sub2api-gemini:7877` 访问，不发布宿主端口。
默认上游代理为 `http://sub2api-openai-egress:7890`，可用 `G2A_UPSTREAM_PROXY` 覆盖。
任何生产部署仍遵守仓库根目录 AGENTS.md 的集群部署约束。

## Sub2API 账号与 codex 分组

按 `sub2api-account.example.json` 配置账号：

1. 平台选择 OpenAI，类型选择 API Key，名称填 Gemini Q&A。
2. Base URL 填 Sub2API 能访问的适配器地址，例如 `http://sub2api-gemini:7877`。
3. API Key 填 `G2A_API_KEY`，绑定 codex 分组。示例 group id 6，导入前确认当前环境。
4. 模型映射仅放已配置的 Gemini ID，避免普通 GPT 编码请求被调度到问答账号。
5. Responses 支持设为 true、模式设为 force_responses，关闭上游 Responses WebSocket。
6. 使用 codex 分组的用户 Key 调用 `/v1/responses`，model 选择映射中的 Gemini ID。
   若分组启用了固定 Codex 模型目录，还须把该 ID 加入分组可见目录。

账号示例供创建接口/后台配置参考，不是批量导入导出格式。没有自动写数据库、恢复
已删除的 Gemini 账号、绑定生产分组或部署容器。

```sh
curl "$SUB2API_URL/v1/responses" \
  -H "Authorization: Bearer $CODEX_GROUP_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"gemini-2.5-flash","input":"简要解释什么是 MQTT","stream":true}'
```

## 协议边界

- 只支持 system/developer/user/assistant 文本及文本数组，可提交完整多轮历史。
- 支持 instructions、temperature、top_p、token 上限和用量返回。
- 工具、工具结果、图片、音频、结构化输出及多候选回答返回 400。
- 不存储 Responses 历史，previous_response_id/conversation/background 返回 400；
  续聊要提交完整文本历史。没有 `/responses/compact` 或适配器 WebSocket 端点。
- `stream=true` 是**缓冲式 SSE**：等待 Gemini 完整生成后发送文本与终止事件，
  不是逐 token 实时输出。Responses 输出项 ID 与完成事件一致；达到 token 上限发送
  `response.incomplete`；上游失败在开始 SSE 前返回错误，不伪装成 completed。
- Codex 编码回合通常带工具定义，因此会被这个问答账号拒绝。它适合通过 codex 分组
  发起无工具问答，并不提供 Codex 文件编辑、命令执行或 Agent 编码能力。
- 未支持的普通可选字段（例如 reasoning、metadata、store）不会发往 Gemini。
  Codex 的纯文本 format/verbosity 选项可被接收，verbosity 不发往 Gemini。
- 所有模型/生成接口要求适配器 Bearer 密钥，只有 `/livez` 公开。
  上游连接不重试、不跟随重定向；错误正文不直接透传，访问日志不记录内容或凭证。

## 验证

```sh
python3 -m unittest -v
```

测试通过真实本地 HTTP 请求和模拟 Gemini 上游检查鉴权、模型目录、多轮转换、
用量、Chat/Responses SSE、终止事件、token 截断和失败边界。
独立适配器部署及 codex 分组端到端验收需在账号切换后完成。

## 部署接入

2026-10-12 起，适配器不再维护独立 `GeminiQA` TeamCity 项目；`GeminiQA_Deploy252`
构建配置和 `teamcity-build.json` 均已移除。克隆仓库后，按内置适配器方式部署：

```sh
# /opt/sub2api/.env
SUB2API_GEMINI=1
GEMINI_API_KEY=<Google AI Studio API Key>
GEMINI_MODELS=gemini-2.5-flash
```

然后通过 TeamCity `sub2api / Deploy 252 Cluster`，或在已授权生产部署时执行
`deploy/cluster/deploy-252.sh`。脚本会按 `SUB2API_GEMINI=1` 启用统一 overlay 的
`gemini` profile、生成
`GEMINI_ADAPTER_KEY`、构建并启动 `sub2api-gemini`。不要恢复独立 GeminiQA 项目或
独立部署脚本。

源码验证：

```sh
bash deploy/tests/adapter-registry-test.sh
python3 -m unittest -v
```

codex 分组端到端验收需使用真实 Gemini API Key、Sub2API 用户 Key 和账号命中记录确认。
凭证只应保存在数据库、`.env` 或部署目录中，不进入 Git、命令行参数或构建日志。
