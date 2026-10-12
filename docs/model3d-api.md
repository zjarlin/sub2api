# Meshy / Tripo / Hi3D 3D 接口

在账号管理中新建「3D 生成」账号，选择 Meshy、Tripo 或 Hi3D：

| 服务商 | 账号凭据 | 支持模式 |
| --- | --- | --- |
| Meshy | API Key | 文生 3D、图生 3D |
| Tripo | API Key | 文生 3D、图生 3D、多视角 |
| Hi3D / Hitem3D | Client ID、Client Secret | 图生 3D、多视角 |

账号存储为 `platform=model3d`、`type=apikey`，通过 `credentials.model3d_provider`
区分服务商。默认地址可以留空，允许管理员配置自建代理地址。
账号绑定的 HTTP/SOCKS 出站代理用于官方接口请求；图片和产物下载保持独立的安全下载策略。
单次接口超时由 `GATEWAY_MODEL3D_TIMEOUT_SECONDS` 控制（默认 60 秒）。
服务商选择按账号/分组优先级进行，旧的全局 provider/priority 配置不参与账号调度。
Hi3D 密钥脱敏返回，编辑时留空保留原值；令牌按账号缓存，凭据更换后失效。

绑定 `model3d` 或 `composite` 分组，开启该分组图片生成权限，再为默认模型配置
显式 `per_request` 价格（允许明确配置 0）。创建前检查余额、Key/用户平台额度；
上游成功受理后通过现有 usage_logs 与幂等扣费流程记录真实账号、模型和分组。
仅收取一次提交费，查询、取消和下载不再扣费；上游失败或取消不会自动退还提交费。

服务总开关仍为 `GATEWAY_MODEL3D_ENABLED=true`。部署级 `MESHY_API_KEY` 等凭据
不再用于线上调用，必须录入账号并绑定分组。旧的部署级任务句柄不能跨账号模式使用。

## 授权方式

当前接入的是三个服务商的官方机器接口，使用以下凭据，无网页会话或用户授权跳转：

- Meshy 在控制台生成 API Key，网关以 `Authorization: Bearer <key>` 调用。
- Tripo 同样在控制台生成 API Key，网关以 Bearer 调用。
- Hi3D 使用 `client_id` + `client_secret` 走 `/open-api/v1/auth/token` 换取访问令牌
  （Basic 认证的机器到机器换取，非用户授权），令牌按 JWT 过期时间缓存并自动刷新。

网关对调用方使用自己的 API Key（`Authorization: Bearer <sub2api key>`）认证，
上游凭据只在服务端使用，不会返回给客户端。因此无需浏览器跳转或回调地址。

## 调用

所有响应使用网关的 `{code,message,data}` 格式。服务商发现无需认证：
`GET /api/v1/3d/providers`。

```bash
curl "$GATEWAY/api/v1/3d/generations" \
  -H "Authorization: Bearer $API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"provider":"meshy","mode":"text-to-3d","prompt":"A small wooden house"}'
```

创建返回 HTTP 202，`data.task_id` 为带签名的网关句柄。使用同一 API Key：

- `GET /api/v1/3d/tasks/{task_id}` 查询进度和产物。
- `GET /api/v1/3d/tasks/{task_id}/assets/{index}` 下载产物。
- `DELETE /api/v1/3d/tasks/{task_id}` 取消或删除任务；Hi3D 不支持取消。
- `GET /api/v1/3d/balance?provider=meshy` 查询上游额度。

图生 3D 使用 `mode:"image-to-3d"` 与 `image_url` 或 `image_base64`。
Hi3D 图片下载禁止内网目标。产物中的 `gateway_path` 可用于带认证的下载。
状态统一为 queued、running、succeeded、failed、cancelled。
句柄默认有效 168 小时，可设置 `GATEWAY_MODEL3D_TASK_HANDLE_TTL_HOURS`。

## 费用与验证边界

创建任务按账号优先级与最近使用时间选号，并占用账号并发槽位；提交后不会自动切换或重试。
网关提交费与服务商积分是两个独立账本。若上游受理后计费存储失败，仍返回 HTTP 202
和任务句柄，`billing_status=pending`，服务端记录错误供运维人工对账（未实现自动补账），客户端不要重新提交生成。
Meshy 文生默认创建 preview，纹理 refine 通过 `options.preview_task_id` 提交（传当前 Key 创建的网关任务句柄，不接受裸上游 ID）。
Hi3D 产物链接有时效，生成成功后应及时下载。

协议依据 Meshy 官方 meshy-cli endpoint contracts、Tripo 官方 Python SDK 0.4.2、
Hi3D 官方认证/提交/查询/余额文档。自动化验证使用本地模拟上游；未做付费真实生成验收。
本次源码集成不部署或重启 252 服务。
