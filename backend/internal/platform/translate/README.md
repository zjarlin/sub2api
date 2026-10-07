# 统一翻译服务 (Unified Translation Service)

边缘计算层统一翻译网关，对外提供标准化 REST API，内部按优先级自动回退多个翻译服务商。

## 设计标准

- **语言代码**: 严格遵循 ISO 639-1 / BCP 47 (如 `zh-CN`, `en-US`, `ja-JP`)
- **API 风格**: RESTful JSON，与 Sub2API 网关鉴权体系集成
- **回退策略**: 彩云小译 → Google 网页翻译兼容接口 → 腾讯云 → 百度 → 有道

## 环境变量配置

| 变量名 | 说明 | 必填 |
|--------|------|------|
| `TRANSLATE_FREE_PROVIDERS` | 设为 `false` 关闭免密钥源（彩云小译及可选 Google 网页兼容接口），默认开启 | 否 |
| `TRANSLATE_CAIYUN_TOKEN` | 彩云小译令牌，留空使用内置公开令牌 | 否 |
| `TRANSLATE_GOOGLE_WEB` | 设为 `true` 显式开启 Google 网页翻译兼容接口，默认关闭；需同时允许免密钥源 | 否 |
| `TRANSLATE_GOOGLE_WEB_PROXY_URL` | Google 专用 HTTP/HTTPS/SOCKS5 出网代理；未配置时遵循标准 HTTP_PROXY/HTTPS_PROXY | 否 |
| `TRANSLATE_TENCENT_SECRET_ID` | 腾讯云 SecretId | 推荐 |
| `TRANSLATE_TENCENT_SECRET_KEY` | 腾讯云 SecretKey | 推荐 |
| `TRANSLATE_TENCENT_REGION` | 腾讯云区域，默认 `ap-guangzhou` | 否 |
| `TRANSLATE_BAIDU_APP_ID` | 百度翻译 APP ID | 备选 |
| `TRANSLATE_BAIDU_SECRET` | 百度翻译密钥 | 备选 |
| `TRANSLATE_YOUDAO_APP_KEY` | 有道智云 AppKey | 备选 |
| `TRANSLATE_YOUDAO_APP_SECRET` | 有道智云 AppSecret | 备选 |

至少配置一个服务商即可启动。未配置任何服务商时，翻译接口返回错误。

## API 端点

### POST /api/v1/translate

需要 API Key 认证。

**请求体:**
```json
{
  "q": ["Hello world", "How are you?"],
  "source": "en",
  "target": "zh-CN",
  "format": "text"
}
```

**响应:**
```json
{
  "code": 0,
  "data": {
    "translations": [
      {"text": "你好世界"},
      {"text": "你好吗？"}
    ],
    "provider": "tencent"
  }
}
```

### GET /api/v1/translate/providers

公开端点，返回已配置的服务商列表。

**响应:**
```json
{
  "code": 0,
  "data": {
    "providers": ["tencent", "baidu"]
  }
}
```

## 免费额度参考

| 服务商 | 免费额度 | 备注 |
|--------|----------|------|
| 彩云小译 | 免注册（内置公开令牌） | 默认启用，仅中/英/日，零配置即可用 |
| 腾讯云 TMT | 500万字符/月 | 首选，需实名认证 |
| 百度通用翻译 | 100万字符/月 | 个人认证后高级版 |
| 有道智云 | 50元体验金 | 新用户一次性 |

## 扩展新服务商

实现 `Translator` 接口即可：

```go
type Translator interface {
    Translate(ctx context.Context, req *TranslateRequest) (*TranslateResponse, error)
    DetectLanguage(ctx context.Context, text string) (string, error)
    Name() string
}
```

然后在 `aggregator.go` 的 `NewAggregator` 中按优先级注册。

## uTools 聚合翻译分析

2026-10-07 在用户 MacBook 上只读分析官方 `translatehub` 1.3.13 插件。
ASAR SHA-256：`d307216a3b71cf4210a769337de6a61e36b3ff96765c2b518c079bb47984f6bd`。
插件通过 Electron webview 打开腾讯、有道、搜狗、DeepL、微软、彩云、CNKI、Google 网页，
使用 `executeJavaScript` 写入 textarea/contenteditable 并派发 input 事件，没有独立的 uTools 翻译 HTTP 上游。

网关因此按实际来源返回 `google_web`，不伪装成 `utools`。
Google 的兼容端点使用 `client=gtx`、`sl`、`tl`、`dt=t`、`dj=1`、`q`，
逐项解析 `sentences[].trans` 和 `src`，保留批次顺序与自动语言检测。
它是非正式网页兼容接口，可能改版、限流或失效，不承诺免费额度或 SLA。
访问失败、验证码或无翻译响应会返回错误，聚合器继续回退，不尝试绕过校验。
当前服务端直连 Google 超时；经 MacBook 的临时 SOCKS5 通道，curl 返回了真实译文，
Go 适配器首次实测遇到 HTTP 429，不能据此认定稳定可用。
为避免影响现有翻译延迟，此实验性上游默认关闭。
部署环境必须自行提供可访问 Google 的出网路径；临时验证通道不是生产代理。

```sh
TRANSLATE_GOOGLE_WEB_LIVE_TEST=1 TRANSLATE_GOOGLE_WEB_PROXY_URL=socks5h://127.0.0.1:19087 \
  go test ./internal/platform/translate -run TestGoogleWebLive -v
```
