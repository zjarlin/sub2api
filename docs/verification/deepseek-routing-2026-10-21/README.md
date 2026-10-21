# DeepSeek 路由探测（2026-10-21）

从 252 的真实账号凭据直接调用上游 Chat Completions（最小流式请求、最多 64 tokens），代理账号使用其配置代理。成功要求收到内容并正常结束。未使用或保存管理员 JWT/账号密钥。

共探测 40 个相关 API Key 账号；下表是当次观测，余额、限流、无渠道、未配价格、网络与普通 404 均不能判为不支持。只把明确的模型格式/不存在响应列为“不支持”。重复检查请使用账号测试接口；结果导入工具接受同样的 JSON 结构。

| 账号 | Base URL | 1017 | deepseek-flash | deepseek-v4.1-flash |
| --- | --- | --- | --- | --- |
| #180 | https://integrate.api.nvidia.com | 暂不可用 / 404 | 暂不可用 / 404 | 暂不可用 / 404 |
| #181 | https://integrate.api.nvidia.com | 暂不可用 / 404 | 暂不可用 / 404 | 暂不可用 / 404 |
| #198 | https://integrate.api.nvidia.com | 暂不可用 / 404 | 暂不可用 / 404 | 暂不可用 / 404 |
| #253 | https://openrouter.ai/api | 暂不可用 / 402 | 暂不可用 / 400 | 暂不可用 / 402 |
| #283 | https://api.deepseek.com | 暂不可用 / 401 | 暂不可用 / 401 | 未确定 / 网络 |
| #832 | https://api.r4.codes/v1 | 明确不支持 / 404 | 明确不支持 / 404 | 成功 / 200 |
| #837 | https://aaawinn.xyz | 暂不可用 / 503 | 暂不可用 / 503 | 暂不可用 / 503 |
| #838 | https://opc.fiime.cn/api/model-service/v1 | 暂不可用 / 403 | 暂不可用 / 403 | 暂不可用 / 403 |
| #839 | https://cavoti.com | 明确不支持 / 404 | 未确定 / 网络 | 暂不可用 / 402 |
| #841 | https://api.chatanywhere.tech | 暂不可用 / 403 | 暂不可用 / 403 | 成功 / 200 |
| #844 | http://sub2api-workbuddy:7863/v1 | 限流 / 429 | 限流 / 429 | 限流 / 429 |
| #848 | http://sub2api-traework:7864/v1 | 明确不支持 / 400 | 明确不支持 / 400 | 明确不支持 / 400 |
| #850 | https://api.commandcode.ai/provider/v1 | 暂不可用 / 400 | 暂不可用 / 400 | 暂不可用 / 400 |
| #851 | https://api.commandcode.ai/provider/v1 | 暂不可用 / 400 | 暂不可用 / 400 | 暂不可用 / 400 |
| #856 | https://opencode.ai/zen/go/v1 | 暂不可用 / 400 | 暂不可用 / 400 | 暂不可用 / 400 |
| #857 | http://newapi.xiaokedamowang.icu | 暂不可用 / 503 | 暂不可用 / 503 | 暂不可用 / 503 |
| #858 | http://sub2api-vibex:7866/v1 | 明确不支持 / 400 | 明确不支持 / 400 | 明确不支持 / 400 |
| #863 | https://api.cline.bot/api | 暂不可用 / 402 | 明确不支持 / 400 | 明确不支持 / 400 |
| #866 | https://api.cline.bot/api | 成功 / 200 | 明确不支持 / 400 | 明确不支持 / 400 |
| #870 | https://token.sensenova.cn | 明确不支持 / 404 | 成功 / 200 | 暂不可用 / 403 |
| #872 | https://api.cline.bot/api | 成功 / 200 | 明确不支持 / 400 | 明确不支持 / 400 |
| #873 | https://api.cline.bot/api | 成功 / 200 | 明确不支持 / 400 | 明确不支持 / 400 |
| #877 | https://token.xiaorongtech.com/v1 | 暂不可用 / 400 | 暂不可用 / 400 | 暂不可用 / 400 |
| #878 | https://ai.xueia.com | 暂不可用 / 403 | 暂不可用 / 403 | 暂不可用 / 403 |
| #882 | https://opencode.ai/zen/v1 | 暂不可用 / 400 | 暂不可用 / 400 | 暂不可用 / 401 |
| #883 | https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1 | 明确不支持 / 404 | 明确不支持 / 404 | 限流 / 429 |
| #886 | https://8888ok.cc | 暂不可用 / 403 | 暂不可用 / 403 | 暂不可用 / 403 |
| #889 | https://tokenhub.tencentmaas.com | 暂不可用 / 400 | 暂不可用 / 400 | 暂不可用 / 402 |
| #890 | https://tokenhub.tencentmaas.com/plan/v3 | 暂不可用 / 404 | 暂不可用 / 404 | 暂不可用 / 404 |
| #891 | https://tokenhub.tencentmaas.com/plan/v3 | 暂不可用 / 404 | 暂不可用 / 404 | 暂不可用 / 404 |
| #893 | http://sub2api-workbuddy:7863/v1 | 限流 / 429 | 限流 / 429 | 限流 / 429 |
| #898 | http://sub2api-workbuddy:7863/v1 | 限流 / 429 | 限流 / 429 | 限流 / 429 |
| #899 | http://sub2api-traework:7864/v1 | 明确不支持 / 400 | 明确不支持 / 400 | 明确不支持 / 400 |
| #900 | http://sub2api-traework:7864/v1 | 明确不支持 / 400 | 明确不支持 / 400 | 明确不支持 / 400 |
| #902 | http://sub2api-workbuddy:7863/v1 | 限流 / 429 | 限流 / 429 | 限流 / 429 |
| #906 | http://sub2api-workbuddy:7863/v1 | 限流 / 429 | 限流 / 429 | 限流 / 429 |
| #908 | https://api.deepseek.com | 暂不可用 / 402 | 未确定 / 网络 | 暂不可用 / 402 |
| #909 | http://sub2api-workbuddy:7863/v1 | 限流 / 429 | 限流 / 429 | 限流 / 429 |
| #910 | https://www.xiaoyaoapi.com | 暂不可用 / 503 | 暂不可用 / 400 | 成功 / 200 |
| #911 | https://www.tokenav.ai | 暂不可用 / 403 | 暂不可用 / 403 | 暂不可用 / 403 |

Cline #866、#872、#873：`deepseek/deepseek-v4.1-flash-1017` 和 `deepseek/deepseek-v4.1-flash` 均成功；`cline-pass/deepseek-v4.1-flash` 返回 429（月额度耗尽）。#863 的两条按量路线返回 402 余额不足，订阅路线返回 429，因此不能把该号判为“不支持新模型”。

#832、#841、#910 的旧裸 ID 成功；#870 的 `deepseek-flash` 成功。WorkBuddy 各号返回额度 429，本次无法判断最新模型能力。NVIDIA #180、#181、#198 经配置代理仍返回无模型错误信息的 404，保持未知。

机器可筛选明细：`capability-results.json`。例如：

```sh
jq '.accounts[] | select(any(.models[]; .model == "deepseek/deepseek-v4.1-flash-1017" and .result == "supported")) | .id' docs/verification/deepseek-routing-2026-10-21/capability-results.json
```

`tools/import_model_route_probes.py` 默认只展示可导入证据，`--sql` 输出用于审查，`--apply` 才写入指定本地数据库。写入仅更新账号 extra 中的地址绑定模型证据，保留其他模型证据与账号状态；地址不同或已有更新快照时跳过。账号 ID 属于本安装，结果文件禁止在其他安装直接导入。429 等暂态结果不写入能力否定；否定证据 12 小时后失效。

本次没有替换容器。源码回归、直接上游探测与部署后的端到端验收是三项独立证据。

验证：新增树路由/HTTP 同号换叶子/目录根投影与 ETag/优先级及冷却回归；前端组件测试与 i18n、TypeScript、ESLint；275 临时表迁移重复执行与冲突保护；探测导入 SQL 事务回滚验证。扩大回归中的 `TestDeepSeekReasoningReplayForward`、`TestDeepSeekMissingReasoningRetriesOnceWithoutThinking`、`TestCompositeModelCatalogCanonicalizesOrdinaryAndCodexLists`、`TestGatewayCompositeRouteModelsAppearInBothCatalogs` 在未修改的 `167c0bc49` 基线也失败。

证据已写入本安装账号的 `extra.model_route_probes`。现有 #863/#866/#872/#873 仍有旧版写入的账号级冷却（最晚到 11 月 3 日）；本次未部署、未清理这些冷却。部署新版后需把已确认的 Pass 冷却转为叶子级，再恢复已实测成功的按量账号；#863 仍需先补足余额。

管理页的精确叶子测试与 429 隔离回归通过；WebSocket 别名入口也加载树策略。扩大 WebSocket 回归中的 `StoreDisabledFunctionCallOutputAutoAttachPreviousResponseID`、`StoreDisabledToolSearchOutputAutoAttachesPreviousResponseID` 两项续接失败在同一未修改基线也可复现。最终专项回归在 service、handler、repository、middleware 四个包均通过。
