# 统一翻译服务 (Unified Translation Service)

边缘计算层统一翻译网关，对外提供标准化 REST API，内部按优先级自动回退多个翻译服务商。

## 设计标准

- **语言代码**: 严格遵循 ISO 639-1 / BCP 47 (如 `zh-CN`, `en-US`, `ja-JP`)
- **API 风格**: RESTful JSON，与 Sub2API 网关鉴权体系集成
- **回退策略**: 腾讯云 → 百度 → 有道（按免费额度和稳定性排序）

## 环境变量配置

| 变量名 | 说明 | 必填 |
|--------|------|------|
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
