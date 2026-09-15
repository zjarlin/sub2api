# Auto Router 只读模型健康接口

`GET /api/v1/router/models/health`，使用独立 `Authorization: Bearer` Key。
服务端仅保存 `ROUTER_METRICS_KEY_SHA256`，`ROUTER_METRICS_GROUP_ID` 固定统计分组；缺少配置时关闭访问。
Key 不授予管理 API 或模型调用权限。只输出该组最近 90 分钟的模型请求成功/失败数、成功率、平均首 Token 延迟与覆盖时间。
复用监控 V2 聚合表；原始模型名不受仪表盘展示分档影响，不暴露用户、账号、提示词或上游密钥。

成功率 = success_requests / (success_requests + error_requests)，零样本返回 null。
这衡量网关请求是否成功，不是任务完成正确率；忽略错误配置不会把失败变成成功。
服务端缓存 60 秒，客户端必须判断数据时间和样本量，不得将未知当成 100% 成功。
