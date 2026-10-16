# 边缘天气服务（多源聚合）

`/api/v1/weather` 是一个统一天气入口：调用方只描述「地点 + 需要的字段」，
服务在多个**免密钥公开数据源**之间做能力匹配、健康排序与自动回退。
抽象与翻译服务的 `Aggregator` 保持一致，新增数据源只需实现 `Provider`。

## 接口

### POST /api/v1/weather

需要 API Key 认证（`Authorization: Bearer $SUB2API_KEY`）。

```json
{
  "location": "洛阳",
  "days": 3,
  "history": true,
  "start": "2026-01-01",
  "end": "2026-01-31",
  "provider": "openmeteo"
}
```

| 字段 | 说明 |
|------|------|
| `location` | 必填。城市名、行政区划编码，或 `纬度,经度`。也可用顶层 `latitude`/`longitude`。 |
| `days` | 预报天数，1–16，默认 3。 |
| `history` | 为 `true` 时同时返回 `history` 日线。 |
| `start` / `end` | 历史区间，`YYYY-MM-DD`；省略时取最近一个完整自然月。 |
| `provider` | 指定单一数据源，跳过自动回退（用于诊断）。 |

响应（`data`）：

```json
{
  "provider": "openmeteo",
  "resolved": "中国 河南省 洛阳",
  "current": {"observed_at": "2026-10-16T16:20", "temperature_c": 18.6, "condition": "多云"},
  "forecast": [{"date": "2026-10-16", "week": "周五", "high_c": 22, "low_c": 15}],
  "history": [{"date": "2026-01-01", "week": "周四", "high_c": 3, "low_c": -4, "condition": "多云~晴"}],
  "attempts": [{"provider": "wttr", "status": "failed", "latency_ms": 730}],
  "attribution": "Open-Meteo (CC BY 4.0)"
}
```

全部候选失败时返回 HTTP 502，`reason=weather_providers_failed`，并在 `data.attempts`
给出每个候选的 `success` / `failed` / `skipped` / `cooldown` 与有界的脱敏 `reason`。

### GET /api/v1/weather/providers

公开只读，返回已注册数据源、能力与当前健康分：

```json
{"code": 0, "data": {"providers": [
  {"name": "openmeteo", "current": true, "forecast": true, "history": true, "score": 100},
  {"name": "tianqi2345", "current": false, "forecast": false, "history": true, "score": 100}
]}}
```

## 内置数据源（全部免密钥）

| 名称 | 覆盖 | 当前 | 预报 | 历史 | 说明 |
|------|------|------|------|------|------|
| `openmeteo` | 全球 | ✅ | ✅ | ✅ | Open-Meteo 公开接口；城市名经 geocoding 解析，历史走 archive。署名 CC BY 4.0。 |
| `wttr` | 全球 | ✅ | ✅ | ❌ | `wttr.in` 的 `format=j1`；支持城市名与 `纬度,经度`。 |
| `chinasky` | 中国 | ✅ | ✅ | ❌ | 中国天气网 `d1.weather.com.cn`，只接受行政区划编码（如洛阳 `101180901`），需 referer。 |
| `tianqi2345` | 中国 | ❌ | ❌ | ✅ | 2345 天气历史页，GBK 编码；只接受 2345 地区编码（如洛阳 `57073`）。 |

`location` 形式不匹配时数据源返回 `ErrLocationUnsupported`，聚合器标记为 `skipped`
**且不计入健康分**，避免「编码 vs 城市名」被误判为上游故障。

## 选择与降级

1. 按 `history` 过滤能力：历史请求只保留支持历史的数据源。
2. 在同层内按健康分降序排列，分数相同保留配置顺序（默认 `openmeteo, wttr, chinasky, tianqi2345`）。
3. 每个候选最多 10 秒；错误、空响应都会被判失败并继续回退。
4. 健康分 `max(0, 100 × (成功数+4)/(尝试数+4) − 15 × 连续失败数 − min(20, EWMA 延迟毫秒/500))`，
   反映可用性与速度，不代表数据精度；进程内保存，重启或换配置后重新学习，不跨副本共享。
5. 连续失败 3 次后冷却 30 秒；指定 `provider` 的诊断请求不受冷却限制。

## 配置

只读环境变量，无密钥项：

```sh
WEATHER_FREE_PROVIDERS=true          # false 时不注册任何内置源
WEATHER_PRIORITY=openmeteo,wttr      # 覆盖默认尝试顺序
```

`gateway.weather.enabled` 控制 `/api/v1/weather` 是否开放；发现端点始终可读。

## 扩展新数据源

```go
type Provider interface {
    Name() string
    Capabilities() Capabilities
    Fetch(ctx context.Context, req *WeatherRequest) (*WeatherResponse, error)
}
```

在 `aggregator.go` 的 `NewAggregator` 中注册，并补一条 `defaultPriority`。
需要密钥的商业源（OpenWeatherMap 等）不进这个免密钥抽象，应按 provider 配置模式另加。

## 验证

```sh
go test ./internal/platform/weather/...            # 单元测试：解析、回退、冷却、能力过滤
WEATHER_LIVE_TEST=1 go test ./internal/platform/weather -run TestProvidersLive -v   # 打真实上游
```
