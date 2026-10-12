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
本目录 `compose.yml` 是独立适配器配置，未接入生产部署；宿主端口仅绑定回环地址。
配置加入外部网络 `sub2api_sub2api-network`，默认使用现有 `sub2api-openai-egress:7890`。
可用 `G2A_DOCKER_NETWORK` 和 `G2A_UPSTREAM_PROXY` 修改。它只定义 gemini2api 服务，
与主服务 Compose 项目分开；部署项目名使用 `gemini-qa`，须获得明确部署授权。
生产 Sub2API 在容器内运行时，不能用容器自己的 `127.0.0.1` 访问该服务，
需要把适配器接入网关后端所在 Docker 网络，通过服务名访问。
任何生产部署仍遵守仓库根目录 AGENTS.md 的集群部署约束。

## Sub2API 账号与 codex 分组

按 `sub2api-account.example.json` 配置账号：

1. 平台选择 OpenAI，类型选择 API Key，名称填 Gemini Q&A。
2. Base URL 填 Sub2API 能访问的适配器地址，例如 `http://gemini2api:7877`。
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

## 独立部署（不走流水线）

2026-10-12 起，适配器部署**不再使用 TeamCity 流水线**：`GeminiQA` 项目及
`GeminiQA_Deploy252` 构建配置已从 TeamCity 删除，`teamcity-build.json` 也已移除，
避免流水线被重新导入或被自动触发。部署仅在获得明确授权后，由人工在源码目录执行：

    python3 -m unittest -v
    python3 -m py_compile server.py deploy.py verify_runtime.py
    python3 deploy.py

`deploy.py` 读取既有账号 885 的上游凭证并保存权限 0600 的回退记录，生成独立适配器密钥，
构建提交专属镜像并启动无宿主端口的候选容器。候选通过真实 SSE 问答和工具/图片拒绝验收后，
通过管理 API 切换账号 Base URL、密钥和 Responses 模式，同时清除账号代理；
Gemini 上游出口由适配器处理。再使用管理员自己的 codex 分组 Key 验证问答和账号命中记录。
失败则恢复账号原配置并停止候选；不操作主网关容器，旧适配器实例保留用于回退。
凭证只在数据库与部署目录中保存，不进入 Git、命令行参数或构建日志。
