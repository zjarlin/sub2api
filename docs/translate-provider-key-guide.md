# 翻译上游服务商 Key 领取指引（腾讯云 / 百度 / 有道 / MyMemory 等）

> 面向对象：需要在 Sub2API 后台为「统一翻译服务」配置上游凭据的**管理员**。
> 这不是调用方要看的文档；调用方只需要 Sub2API API Key，见 [`translate-edge-key-guide.md`](./translate-edge-key-guide.md)。
>
> 配置入口：后台 **「边缘计算」** → 服务目录选择 **翻译** → **上下文（Context）** 标签页，
> 每项凭据同时对应一个环境变量（下表已列出），两种方式等价。
>
> ⚠️ 各厂商控制台菜单会调整；具体以官网当前页面为准，本文给出的是可用的官方入口和字段含义。

## 0. 总览：谁需要领、领什么

| 上游 | 需要领的东西 | 是否必须 | 免费额度 | 领取难度 |
|---|---|---|---|---|
| 腾讯云 TMT | 开通机器翻译服务 + `SecretId`/`SecretKey` | 推荐（首选付费源） | 文本翻译 **500 万字符/月** | 低（需实名） |
| 百度翻译开放平台 | `APP ID` + `密钥` | 备选 | 个人认证后约 **100 万字符/月** | 低（需实名） |
| 有道智云 | `AppKey`（应用ID）+ `AppSecret`（应用密钥） | 备选 | **50 元体验金**（一次性） | 低 |
| MyMemory | 免注册即可用；可选 email / API Key 提额 | 否 | 匿名 5,000 字符/天；填 email 50,000/天 | 极低 |
| 彩云小译 | 免注册（网关内置令牌）；可选正式 Token | 否 | 免注册可用 | 极低 |
| LibreTranslate | 自建实例（自己设 Key） | 否 | 自建，无第三方申请 | 中（要部署） |
| Hy-MT2 | 自建推理实例（自己设 Key） | 否 | 自建，无第三方申请 | 中（要部署） |
| Google 网页兼容 | 无需 Key（仅需出网代理） | 否（默认关闭） | 无 SLA | — |

**顺序建议**：腾讯云 TMT → 百度翻译 → 有道智云；免密钥的 MyMemory / 彩云 / 自建源可先用着，零成本验证链路。

## 1. 腾讯云机器翻译 TMT（推荐）

### 1.1 前置
- 注册腾讯云账号并完成**实名认证**（个人或企业均可）。
- 账号可正常访问 `console.cloud.tencent.com`。

### 1.2 开通机器翻译服务
1. 访问机器翻译控制台：<https://console.cloud.tencent.com/tmt>
2. 首次进入会提示**开通服务**，按引导开通（免费，按量计费）。
3. 开通后系统会按规则发放**文本翻译免费资源包**。

### 1.3 领取 API 密钥（SecretId / SecretKey）
1. 访问 **访问管理 CAM → API 密钥管理**：<https://console.cloud.tencent.com/cam/capi>
   （官方文档：主账号访问密钥管理 <https://cloud.tencent.com/document/product/598/40488>）
2. 点击**新建密钥**，得到一对 `SecretId`（公钥）与 `SecretKey`（私钥）。
3. **强烈建议**不要直接用主账号密钥：创建子用户，按最小权限授予机器翻译相关策略
   （如 `QcloudTMTFullAccess`），使用子用户的密钥。
4. `SecretKey` 只在创建时可见一次，请立即复制保存。

### 1.4 填到系统
| 后台字段 | 环境变量 | 值 |
|---|---|---|
| SecretId | `TRANSLATE_TENCENT_SECRET_ID` | 上面的 SecretId |
| SecretKey | `TRANSLATE_TENCENT_SECRET_KEY` | 上面的 SecretKey |
| Region | `TRANSLATE_TENCENT_REGION` | 留空默认 `ap-guangzhou` |

### 1.5 免费额度与计费（官方口径）
- 文本翻译：**每月 500 万字符**免费，以免费资源包形式配送；首次开通当天即发放，之后每月 1 日发放，**仅当月有效**。
- 扣费顺序：**免费额度 > 预付费资源包 > 后付费**。
- **后付费默认关闭**（要到机器翻译控制台 → 系统设置里手动开启）；关闭后付费时，资源包耗尽会**自动停服**而非自动转后付费。
- 语种识别不计费；调用失败不计费；1 个汉字/字母/标点/空格各计 1 个字符。
- 参考页：计费概述 <https://cloud.tencent.com/document/product/551/35017>

## 2. 百度翻译开放平台

### 2.1 前置
- 注册百度账号并登录百度翻译开放平台：<https://fanyi-api.baidu.com/>
- 完成**开发者实名认证**（认证等级决定可用版本与额度；未认证额度受限）。

### 2.2 申请「通用文本翻译」
1. 登录后进入**管理控制台**：<https://fanyi-api.baidu.com/manage/developer>
2. 在「总览 / 我的服务」中申请开通**通用文本翻译**（个人开发者可选标准版/高级版）。
3. 开通后在同一页面可看到 **APP ID** 与 **密钥**（Secret Key）。
4. APP ID 与密钥是调用凭据，请勿公开。

### 2.3 填到系统
| 后台字段 | 环境变量 |
|---|---|
| APP_ID | `TRANSLATE_BAIDU_APP_ID` |
| Secret | `TRANSLATE_BAIDU_SECRET` |

### 2.4 额度
- 个人认证后标准版约 **100 万字符/月**（免费），高级版更多且可申请提额。
- 官方接入文档：<https://fanyi-api.baidu.com/doc/21>

## 3. 有道智云

### 3.1 前置
- 注册有道智云账号：<https://ai.youdao.com/>

### 3.2 领取应用ID与应用密钥
按官方**新手指南**操作：<https://ai.youdao.com/doc.s#guide>

1. 注册并登录账号。
2. 进入**控制台**：<https://ai.youdao.com/console/>
3. **创建应用**，并在应用中**绑定「文本翻译」服务**（未绑定服务会报 110 错误）。
4. 在**应用管理**中查看该应用的 **应用ID（AppKey）** 与 **应用密钥（AppSecret）**。
5. 平台向每个账户赠送 **50 元体验金**，用于接入前测试。

### 3.3 填到系统
| 后台字段 | 环境变量 |
|---|---|
| AppKey | `TRANSLATE_YOUDAO_APP_KEY` |
| AppSecret | `TRANSLATE_YOUDAO_APP_SECRET` |

### 3.4 备注
- 有道要求请求携带 `signType=v3` 的 SHA-256 签名，网关适配器已实现，无需你手工签名。
- 官方 API 文档：<https://ai.youdao.com/DOCSIRMA/html/trans/api/wbfy/index.html>
- 体验金用尽后按官网报价计费。

## 4. MyMemory（免注册，可选提额）

- **不需要申请 Key 即可用**（有限额）。
- 官方额度（文档 <https://mymemory.translated.net/doc/usagelimits.php>）：
  - 匿名使用：**5,000 字符/天**；
  - 提供有效邮箱（请求 `de` 参数）：**50,000 字符/天**；
  - CAT 工具类项目可申请白名单：**150,000 字符/天**。
- 可选 Key：官方 API 文档 <https://mymemory.translated.net/doc/spec.php> 提供 key 获取方式
  （页面上的 "Get your key here"，或 `keygen` 接口用用户名/密码生成）。
- 限制：**单条文本最多 500 UTF-8 字节**，且**必须指定源语言**（不支持自动检测、HTML、超长文本）。
- 填到系统（都可选）：

| 后台字段 | 环境变量 |
|---|---|
| 联系邮箱 | `TRANSLATE_MYMEMORY_EMAIL` |
| API Key | `TRANSLATE_MYMEMORY_API_KEY` |

## 5. 彩云小译

- **免注册免密钥**：网关内置公开令牌，`TRANSLATE_FREE_PROVIDERS` 保持默认开启即可用，仅覆盖中/英/日。
- 如需正式 Token（更高稳定性）：登录彩云开放平台 <https://dashboard.caiyunapp.com/> 申请，填入
  `TRANSLATE_CAIYUN_TOKEN`。

## 6. 自建源：LibreTranslate / Hy-MT2

这两类**不需要向任何厂商申请 Key**，Key 由你自己设定并同时写在两处：

- LibreTranslate：按 `deploy/docker-compose.libretranslate.yml` 部署私网实例，
  设置 `TRANSLATE_LIBRETRANSLATE_URL`；如实例要求鉴权，再设 `TRANSLATE_LIBRETRANSLATE_API_KEY`。
- Hy-MT2（本地 llama.cpp / vLLM）：设置 `TRANSLATE_HYMT_URL`；如需鉴权再设 `TRANSLATE_HYMT_API_KEY`，
  并与推理实例启动参数里的 Key 保持一致。

## 7. 填好之后怎么验证

1. 在「上下文」页保存（凭据只存在服务端，界面只回显"已配置"）。
2. 用当前 Key 查上游列表，确认目标上游已注册：
   ```bash
   curl https://company-ai.addzero.site/api/v1/translate/providers
   ```
3. 指定该上游做一次真实翻译（**指定 provider 不会自动回退**，能直接暴露配置错误）：
   ```bash
   curl -X POST https://company-ai.addzero.site/api/v1/translate \
     -H "Authorization: Bearer $SUB2API_API_KEY" \
     -H "Content-Type: application/json" \
     -d '{"q":["Hello world"],"source":"en","target":"zh-CN","provider":"tencent"}'
   ```
4. 返回 `data.provider` 应为 `tencent` 且 `translations[].text` 是真实译文；
   返回 `400`（未启用/未知 provider）或 `502`（该上游失败）时按错误信息排查。

## 8. 常见坑

- **密钥填错位置**：腾讯云要的是 CAM 的 `SecretId/SecretKey`，不是控制台登录密码，也不是某个子产品的 AppId。
- **未开通服务**：腾讯云只创建密钥、没开通机器翻译，会直接报未授权错误。
- **百度未实名**：不认证额度很低甚至无法开通高级版。
- **有道未绑定服务**：应用没绑定「文本翻译」会报 110。
- **腾讯云资源包过期作废**：免费资源包仅当月有效；后付费默认关闭，用尽会停服。
- **主账号密钥风险**：优先用子账号 + 最小权限策略，降低泄露影响面。
- **泄露处理**：先在厂商控制台禁用/轮换该密钥，再更新后台配置；后台凭据留空提交表示"保持原值不变"。

## 9. 相关文档

- 调用方 Key 申请与调用指引：`docs/translate-edge-key-guide.md`
- 服务实现与上游回退细节：`backend/internal/platform/translate/README.md`
- LibreTranslate 自建：`deploy/docker-compose.libretranslate.yml`
- Hy-MT2 部署：`deploy/docker-compose.hymt.yml`、`deploy/docker-compose.hymt-tianjin.yml`；网关侧接入链路见 `deploy/hymt-relay/README.md`
