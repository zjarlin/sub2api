# Laya 离线决策模型

Laya 与 JEV 共用 `POST /v1/systemone`。传 `model: "laya"` 走本地 Laya，`model: "typesafe/jev"` 走 CommandCode。客户端均使用现有 Codex/OpenAI 分组 API Key；Laya 无需添加上游账号。

## 部署

`scripts/prepare-model.sh /opt/sub2api/edge-laya/models` 使用一次性 Python Docker 容器下载固定版本的英文和多语言权重。这个步骤需要联网且可能占用数 GB 空间；准备完成后运行无需外网。将 TeamCity 参数 `env.EDGE_LAYA_ENABLED` 设为 `1` 后，`deploy/cluster/deploy-laya.sh` 会检查权重、构建镜像并在 Sub2API 内网启动容器；启用 `gateway.laya.enabled: true` 后公网网关才会转发。请先完成权重准备和内网健康验证，再打开网关开关。

镜像使用上游固定 commit 的 `laya[serve]`，本地挂载权重供 `Router` 使用。上游 `laya.serve` 提供 `/health` 和 `/v1/systemone`，客户端提供的 `model: "laya"` 由其自动选择检查点。

```bash
curl -X POST https://company-ai.addzero.site/v1/systemone \
  -H "Authorization: Bearer $CODEX_GROUP_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"laya","state":"Payments failed for three days.","questions":{"urgent":{"type":"noul","instructions":"Does this need urgent attention?"}}}'
```

当前网关不会为 Laya 写入 Sub2API 用量账单；启用前需明确运营计费策略，不能按视觉接口的 `$0.002/次` 宣称已计费。
