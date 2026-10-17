# OpenAI 兼容视频 API（New API 系中继）

支持把 New API / Sora 风格的中继（如 wuyeai.cn、metaso.cn）接到 OpenAI 平台的
**API Key** 账号上，按它们原生协议透传 `/v1/videos`：

```
POST   /v1/videos                       创建任务（异步，按次计费）
GET    /v1/videos/{task_id}             查询状态
GET    /v1/videos/{task_id}/content     下载视频（支持 Range 断点续传）
```

## 与 Grok / Seedance 的区别

| 能力 | 上游协议 | 账号平台 | 端点 |
| --- | --- | --- | --- |
| Grok Imagine | xAI `/videos/generations`、edits/extensions | Grok | 由 Grok 分组承载 |
| Seedance | Ark `/api/v3/contents/generations/tasks` | OpenAI API Key | 需勾选 Seedance 能力 |
| **OpenAI 视频** | New API `/v1/videos` | OpenAI API Key | 需勾选 OpenAI Video 能力 |

`/v1/videos/generations`、`/videos/generations`、`/videos/edits`、`/videos/extensions`
等是 xAI 专有协议，OpenAI 平台的分组调用它们仍按平台门禁返回 404。OpenAI 视频只走
`/v1/videos`、`/v1/videos/{id}`、`/v1/videos/{id}/content`。

## 配置

1. 新建 **OpenAI** 平台、**API Key** 类型账号，Base URL 填中继的 API 根地址
   （例如 `https://www.wuyeai.cn/v1`；未带 `/v1` 时网关会补 `/v1/videos`），
   API Key 填中继令牌。
2. 在账号的端点能力中勾选 **OpenAI Video (/v1/videos)**。未勾选的账号不会被
   视频请求调度到，避免聊天账号误接视频流量。
3. 按需配置模型映射，例如 `minimax-h3 → Minimax-H3-720p`（中继自己的模型 ID）。
4. 将账号加入 OpenAI 分组，并启用分组的「允许图片生成」媒体权限。
5. 为该模型配置定价：视频按次或按秒都支持。
   - 分组定价里的 **按次（per_request）** 视频模型 → 每次提交扣一次，
     不做时长乘法（与 wuyeai 这类「按次固定价」的中继口径一致）。
   - **按秒（video）** 或分组视频单价表 → 按 `resolution × duration × 次数` 计费，
     时长上限放宽到 30 秒（Grok 仍为 15 秒）。

## 调用

```bash
# 1. 创建（每提交一次计费一次，不要重复提交）
curl "$SUB2API_BASE_URL/v1/videos" \
  -H "Authorization: Bearer $SUB2API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "minimax-h3",
    "prompt": "一只橘猫在雨夜街道上缓慢行走，电影级光影",
    "duration": 8,
    "ratio": "16:9",
    "resolution": "720p",
    "referenceImages": ["https://cdn.example.com/cat.png"]
  }'
# → {"id":"task_xxx","object":"video","status":"queued","progress":0,...}

# 2. 轮询（5~10 秒一次；查询不额外计费，也不会重复计费）
curl "$SUB2API_BASE_URL/v1/videos/task_xxx" \
  -H "Authorization: Bearer $SUB2API_KEY"

# 3. 完成后下载（同一令牌；支持 Range）
curl -L "$SUB2API_BASE_URL/v1/videos/task_xxx/content" \
  -H "Authorization: Bearer $SUB2API_KEY" -o result.mp4
```

请求体除 `model` 按账号映射改写外**原样透传**（`prompt`、`duration`/`seconds`、
`ratio`、`resolution`/`size`、`referenceImages`、`first_frame_image`、
`last_frame_image`、`input_reference` 以及中继新增字段都不做过滤），因此中继的
字段校验语义与直连一致。

## 任务归属与计费

- 创建成功后任务 ID 绑定到接单账号（`group + user + key + task`），后续查询与下载
  只允许同一用户、同一 API Key、同一分组，并且**固定使用原提交账号**。
- 未绑定过的任务 ID 查询返回 404 `Video request not found`，不会把查询转发到别的账号。
- 计费在**创建成功**时结算一次（任务 ID 作为幂等键，`openai-video:` 命名空间）；
  查询、下载不再计费。中继创建失败返回非 2xx 时不计费。
- 分辨率档位归一化到 `480p` / `720p` / `1080p`：中继的 `768p` 计入 720p，
  `2k` 计入 1080p；`size: "1280x720"` 按短边推断档位（仅用于计费，不改请求体）。
