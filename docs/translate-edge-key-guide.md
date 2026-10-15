# 翻译边缘计算 · Key 申请与调用指引

> 面向对象：需要调用「统一翻译服务」（翻译边缘计算接口）的使用方与运维管理员。
> 维护位置：`docs/translate-edge-key-guide.md`；服务实现见 `backend/internal/platform/translate/README.md`。

## 1. 服务概览

「翻译边缘计算」是 Sub2API 网关内置的统一翻译服务（Unified Translation Service）：

- 对外只有一个标准 REST 接口：`POST /api/v1/translate`，**不需要单独部署、也不需要单独开通套餐**；
- 网关内部按优先级在多个翻译上游之间自动回退，调用方只面向一个统一接口和一种响应结构；
- 提供方只读接口：`GET /api/v1/translate/providers`（公开，无需 Key）。

| 入口 | 地址 | 说明 |
|---|---|---|
| 正式/公网 | `https://company-ai.addzero.site` | 与 API Key 鉴权体系共用同一网关 |
| 内网稳定入口 | `http://192.168.31.252:18080` | 同机调试、内网服务互调用 |

两个入口属于同一个网关进程、同一套鉴权与计费，调用方式完全一致。

## 2. 先分清：需要申请哪一种 Key？

这是最容易混淆的一点。翻译链路里可能出现两种 Key，**它们用途不同、申请方不同**：

| Key 类型 | 作用 | 由谁申请 | 是否必需 |
|---|---|---|---|
| **Sub2API API Key** | 调用方访问 `/api/v1/translate` 的访问凭证 | **使用者本人**在控制台自助创建 | **必需** |
| 上游服务商 Key（腾讯云 / 百度 / 有道 / MyMemory / LibreTranslate / Hy-MT2…） | 网关内部调用具体翻译厂商 | **管理员**在后台统一配置 | 可选 |

结论：

- **普通调用方只需要一个 Sub2API API Key，不需要向翻译服务"提交申请"，也不需要自行注册腾讯云/百度翻译账号。**
- 上游服务商 Key 属于服务端运营配置，普通调用方无需接触，也拿不到。
- 未配置任何付费上游时，网关仍会使用免密钥源与自建源（内置彩云小译、MyMemory、自建 LibreTranslate、自建 Hy-MT2 等）完成翻译。

## 3. 如何获取（申请）Sub2API API Key

完全自助，**无需工单、无需人工审核，创建后立即可用**：

1. 打开控制台并登录，例如 `https://company-ai.addzero.site`。
2. 左侧导航进入 **「API 密钥」** 页面。
3. 点击 **「创建密钥」**。
4. 填写 **「密钥名称」**，建议使用能说明用途的名字，如 `translate-bot`、`翻译服务-生产`。
5. 选择 **「分组」**：
   - 分组决定该 Key 绑定的账号池与计费倍率，由管理员预先创建；
   - 翻译接口**不做平台限制**，任何有效分组都可以调用；
   - 若提示没有可选分组（`暂无可用分组`），请联系管理员先把你的账号加入某个分组。
6. 点击创建，**立即复制并妥善保存** `sk-` 开头的密钥。密钥只显示一次，丢失只能重新生成。
7. 直接在请求头里使用，无需等待生效。

> 服务间/管理员调用：也可以使用管理员 API Key，鉴权方式相同（`Authorization: Bearer <admin-key>`）。

### 3.1 调用前请确认 Key 满足以下前提

- Key 未被停用、未过期；
- Key 绑定用户状态正常；
- 所属分组可用，且允许当前用户使用；
- 计费校验通过（余额 / 订阅 / 配额充足）；
- 未触发该 Key 的 IP 白名单限制。

任一条不满足，网关会在鉴权阶段直接拒绝，见第 7 节错误码。

## 4. 调用方式

### 4.1 鉴权头

推荐使用 Bearer；也兼容另外两种写法（`x-api-key`、`x-goog-api-key`）：

```http
Authorization: Bearer <SUB2API_API_KEY>
Content-Type: application/json
```

注意：**Key 放在 URL 查询参数里（`?key=`/`?api_key=`）已被弃用**，会直接返回 `400 api_key_in_query_deprecated`。

### 4.2 请求体

```json
{
  "q": ["Hello world", "How are you?"],
  "source": "en",
  "target": "zh-CN",
  "format": "text",
  "provider": ""
}
```

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `q` | `string[]` | 是 | 待翻译文本列表，必须是非空数组 |
| `target` | `string` | 是 | 目标语言，ISO 639-1 / BCP 47，如 `zh-CN` |
| `source` | `string` | 否 | 源语言；留空表示自动检测 |
| `format` | `string` | 否 | `text`（默认）或 `html` |
| `provider` | `string` | 否 | 指定单一上游（诊断用）；留空则自动回退 |

指定 `provider` 时**只调用该上游，失败不暗中回退**；未启用/未知的 provider 返回 `400`。

### 4.3 请求示例

```bash
curl -X POST https://company-ai.addzero.site/api/v1/translate \
  -H "Authorization: Bearer $SUB2API_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"q":["Hello world"],"source":"en","target":"zh-CN"}'
```

指定上游（诊断）：

```bash
curl -X POST https://company-ai.addzero.site/api/v1/translate \
  -H "Authorization: Bearer $SUB2API_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"q":["Hello world"],"source":"en","target":"zh-CN","provider":"mymemory"}'
```

### 4.4 响应

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "translations": [
      { "text": "你好世界" }
    ],
    "provider": "hymt"
  }
}
```

- `data.provider` 始终标识**实际返回译文的上游**（即使你没有指定 provider）。
- 自动模式下还可能返回 `data.attempts`，逐项说明每个候选上游是 `success` / `failed` / `cooldown`，便于排查。
- `translations[i].detected_language` 只在 `source` 留空（自动检测）时出现。

### 4.5 查看可用上游（无需 Key）

```bash
curl https://company-ai.addzero.site/api/v1/translate/providers
```

返回按当前健康分排序的 `data.providers`，以及 `data.health`（分数、成功率、连续失败、延迟、冷却截止时间）。

## 5. 语言代码

使用 ISO 639-1 / BCP 47：

- 中文 `zh-CN`，英文 `en-US` / `en`，日文 `ja-JP`，韩文 `ko-KR`，法文 `fr-FR` 等；
- 不指定 `source` 时由上游自动检测；
- 部分上游（如 MyMemory、自建实例）只覆盖部分语言对，指定不支持的语言对时会报错；自动模式会继续回退到其他上游。

## 6. 上游清单与免额度参考（了解即可，调用方无需配置）

| 上游 | 标识 | 是否需要 Key | 备注 |
|---|---|---|---|
| MyMemory | `mymemory` | 否（可选 email/api key 提额） | 单条最多 500 字节，必须指定源语言 |
| Hy-MT2（自建） | `hymt` | 自建实例，可选 `TRANSLATE_HYMT_API_KEY` | 离线推理，质量与延迟取决于部署机器 |
| LibreTranslate（自建） | `libretranslate` | 自建实例，可选 API Key | 私网部署，不映射公网端口 |
| 百度翻译 | `baidu` | 是（管理员配置） | 需实名认证，个人版约 100 万字符/月 |
| 腾讯云 TMT | `tencent` | 是（管理员配置） | 需实名认证，约 500 万字符/月 |
| 有道智云 | `youdao` | 是（管理员配置） | 新用户一次性体验金 |
| 彩云小译 | `caiyun` | 否（内置公开令牌） | 仅中/英/日，零配置可用 |
| Google 网页兼容 | `google_web` | 否（默认关闭） | 非正式接口，可能改版/限流 |

> 上表仅解释"为什么有时不需要申请任何上游账号"。具体启用哪些上游由管理员决定。

## 7. 错误码

| HTTP | code | 含义 | 处理建议 |
|---|---|---|---|
| 400 | — | 请求体非法（缺 `q` / `target`），或 provider 未启用/未知 | 检查 JSON 字段 |
| 400 | `api_key_in_query_deprecated` | Key 放在 URL 参数里 | 改用 `Authorization` 头 |
| 401 | `API_KEY_REQUIRED` | 未提供 Key | 补上鉴权头 |
| 401 | `INVALID_API_KEY` | Key 不存在或被改坏 | 重新复制 Key |
| 401 | `API_KEY_DISABLED` | Key 已停用 | 联系管理员或重新创建 |
| 401 | `USER_NOT_FOUND` / `USER_INACTIVE` | 用户不存在/被停用 | 联系管理员 |
| 403 | `ACCESS_DENIED` | 命中 IP 白名单限制 | 联系管理员放行来源 IP |
| 403 | `API_KEY_EXPIRED` | Key 已过期 | 重新创建或续期 |
| 403 | `GROUP_NOT_ALLOWED` | 分组不再允许当前用户使用 | 联系管理员 |
| 403 | `INSUFFICIENT_BALANCE` | 余额不足 | 充值或更换分组 |
| 403 | `SUBSCRIPTION_NOT_FOUND` | 分组需要订阅但未订阅 | 订阅该分组 |
| 429 | `API_KEY_QUOTA_EXHAUSTED` | Key 配额用尽 | 提额或新建 Key |
| 429 | `INVALID_AUTH_RATE_LIMITED` | 鉴权失败次数过多被限流 | 稍后重试 |
| 502 | `translation_providers_failed` | 所有候选上游都失败 | 看 `data.attempts` 逐项原因，稍后重试或指定其他 provider |
| 503 | `API_KEY_AUTH_OVERLOADED` | 鉴权服务过载 | 稍后重试 |

## 8. 常见问题

**Q：我没有腾讯云/百度账号，能调用翻译接口吗？**
可以。翻译上游由管理员统一配置，网关默认已包含免密钥源与自建源。你只需要一个 Sub2API API Key。

**Q：为什么"申请 Key"没有被审核？**
Sub2API API Key 是自助创建的，创建即生效；需要人工审批的是你的账号是否被加入了某个可用分组，这一步由管理员完成。

**Q：返回 401 但 Key 看起来没问题？**
依次确认：请求头是 `Authorization: Bearer <key>` 而不是查询参数；Key 没有多余空格/换行；没有被停用。

**Q：指定 `provider` 后失败了，为什么不自动换别的上游？**
这是设计行为：`provider` 是诊断开关，只会尝试你指定的那一个。需要自动回退时请留空 `provider`。

**Q：一次能翻译多少条？**
`q` 是数组，可批量提交；但单个上游对单条长度有限制（例如 MyMemory 单条 500 字节），超限会失败或回退。

## 9. 管理员：如何申请与配置上游 Key（可选）

配置入口：后台导航 **「边缘计算」** → 服务目录选择 **翻译** → **上下文（Context）** 标签页。
该页面可开关服务、开关各上游、录入脱敏凭据、设置默认上游，保存后运行时立即读取（凭据只保留在服务端，界面只回显"已配置"）。

| 上游 | 字段 | 对应环境变量 | 申请入口 |
|---|---|---|---|
| 百度翻译 | APP_ID / Secret | `TRANSLATE_BAIDU_APP_ID` / `TRANSLATE_BAIDU_SECRET` | 百度翻译开放平台 → 注册 → 申请「通用文本翻译」（`fanyi-api.baidu.com`） |
| 腾讯云 TMT | SecretId / SecretKey / Region | `TRANSLATE_TENCENT_SECRET_ID` / `TRANSLATE_TENCENT_SECRET_KEY` / `TRANSLATE_TENCENT_REGION` | 腾讯云控制台 → 开通「机器翻译 TMT」→ 访问管理 CAM 创建 API 密钥（需实名认证） |
| 有道智云 | AppKey / AppSecret | `TRANSLATE_YOUDAO_APP_KEY` / `TRANSLATE_YOUDAO_APP_SECRET` | 有道智云控制台 → 创建应用 → 文本翻译（`ai.youdao.com`） |
| MyMemory | Email / API Key（可选） | `TRANSLATE_MYMEMORY_EMAIL` / `TRANSLATE_MYMEMORY_API_KEY` | 官方 `mymemory.translated.net` 申请，仅用于提额 |
| LibreTranslate | Base URL / API Key | `TRANSLATE_LIBRETRANSLATE_URL` / `TRANSLATE_LIBRETRANSLATE_API_KEY` | 自建实例（`deploy/docker-compose.libretranslate.yml`），无需申请 |
| Hy-MT2 | Base URL / API Key | `TRANSLATE_HYMT_URL` / `TRANSLATE_HYMT_API_KEY` | 自建 llama.cpp/vLLM 推理实例，Key 可自定 |
| 彩云小译 | Token（可选） | `TRANSLATE_CAIYUN_TOKEN` | 留空使用内置公开令牌；如需正式令牌向彩云申请 |
| Google 网页兼容 | Proxy URL（可选） | `TRANSLATE_GOOGLE_WEB` / `TRANSLATE_GOOGLE_WEB_PROXY_URL` | 需自行提供可访问 Google 的出网路径，默认关闭 |

也可通过环境变量/部署配置设置，字段与页面一一对应；至少配置一个上游服务即可启动。

## 10. 安全建议

- Key 只放在服务端环境变量或密钥管理系统中，**不要写进前端代码、客户端 App 或公开仓库**；
- 不要把 Key 拼进 URL（既已弃用，也会被日志记录）；
- 按用途拆分 Key（如 `translate-bot`、`翻译-生产`），便于单独轮换与停用；
- 怀疑泄露时立即在「API 密钥」页面停用并重新创建。

## 11. 相关文档

- 服务实现与上游回退细节：`backend/internal/platform/translate/README.md`
- LibreTranslate 自建：`deploy/docker-compose.libretranslate.yml`
- Hy-MT2 相关部署：`deploy/docker-compose.hymt.yml`、`deploy/docker-compose.hymt-tianjin.yml`
- 边缘服务控制台（管理员）：`/admin/vision` → 翻译 → 文档 / 上下文
