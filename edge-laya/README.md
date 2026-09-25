# Laya 离线决策模型

Laya 与 JEV 共用 `POST /v1/systemone`。传 `model: "laya"` 走本地 Laya，`model: "typesafe/jev"` 走 CommandCode。客户端均使用现有 Codex/OpenAI 分组 API Key；网关按模型从账号池选择对应平台。启用内置适配器后需创建 Laya 账号，默认内网上游不要求共享密钥；若为 edge-laya 配置了 `LAYA_API_KEY`，账号侧的 `builtin_adapter.laya_key` 必须与之相同。

## 两个对外面

| 端点 | 协议 | 用途 |
|---|---|---|
| `POST /v1/systemone` | **Laya 原生契约** | 与 JEV 共用 wire protocol，返回 `{model, answers, usage, routing}`。决策能力的真相来源，网关只做原样透传。 |
| `POST /v1/chat/completions` | OpenAI Chat | 兼容面，给现有 OpenAI SDK |
| `POST /v1/responses` | OpenAI Responses | 兼容面，给 Codex 等 Responses 客户端 |
| `GET /v1/models` | OpenAI | 列出 `laya` / `laya-english` / `laya-multilingual` / `laya-typed-decisions` |

两条通道跑在同一个进程、共用同一个 `Router`，`model` 解析与推理语义完全一致。
OpenAI 兼容面只是协议包装，不改变决策语义。

## 原生协议

```bash
curl -X POST https://company-ai.addzero.site/v1/systemone \
  -H "Authorization: Bearer $CODEX_GROUP_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"laya","state":"Payments failed for three days.","questions":{"urgent":{"type":"noul","instructions":"Does this need urgent attention?"}}}'
```

省略 `model` 或传 `laya` 都表示「由 Router 按语言自动选检查点」；这是推荐用法。
需要锁定检查点时传 `laya-english` / `laya-multilingual` / `laya-typed-decisions`。

## OpenAI 兼容面

`{state, questions, task, lang}` 可以放在顶层，也可以放在 `input` / `messages[].content`
里，原样透传给 `Router.predict`：

```bash
curl -X POST https://company-ai.addzero.site/v1/responses \
  -H "Authorization: Bearer $CODEX_GROUP_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "laya",
    "input": {
      "state": {"subject": "Duplicate charge on invoice #4411", "body": "We were billed twice."},
      "questions": {
        "department": {"type": "choice", "instructions": "Which department?", "criteria": {"billing": "refunds", "technical": "bugs"}},
        "urgent": {"type": "noul", "instructions": "Does this need urgent attention?"}
      }
    }
  }'
```

注意 Laya 不生成 token：`completion_tokens` 恒为 0，`total_tokens` 等于输入 token。
SSE 只发一个内容分片后收尾，但会发出合规终态（chat 的 `[DONE]`、responses 的
`response.completed`）。

## 部署

`edge-laya/scripts/prepare-model.sh /opt/sub2api/edge-laya/models` 使用一次性 Python Docker 容器下载固定版本的英文和多语言权重。这个步骤需要联网且可能占用数 GB 空间；准备完成后运行无需外网。将 TeamCity 参数 `env.EDGE_LAYA_ENABLED` 设为 `1` 后，`deploy/cluster/deploy-laya.sh` 会检查权重、构建镜像并在 Sub2API 内网启动容器；启用 `gateway.laya.enabled: true` 后公网网关才会转发。请先完成权重准备和内网健康验证，再打开网关开关。

镜像使用上游固定 commit 的 `laya[serve]`，本地挂载权重供 `Router` 使用。上游 `laya.serve` 提供 `/health` 和 `/v1/systemone`，客户端提供的 `model: "laya"` 由其自动选择检查点。

## 测试

```bash
pytest -q edge-laya/tests/
```

兼容层测试用注入的假 `predict`，不加载真实权重，无需 torch。

## 计费

当前网关不会为 Laya 写入 Sub2API 用量账单；启用前需明确运营计费策略，不能按视觉接口的 `$0.002/次` 宣称已计费。
