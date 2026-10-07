# 统一翻译服务 (Unified Translation Service)

边缘计算层统一翻译网关，对外提供标准化 REST API，内部按优先级自动回退多个翻译服务商。

## 设计标准

- **语言代码**: 严格遵循 ISO 639-1 / BCP 47 (如 `zh-CN`, `en-US`, `ja-JP`)
- **API 风格**: RESTful JSON，与 Sub2API 网关鉴权体系集成
- **回退策略**: 彩云小译 → Google 网页翻译兼容接口 → 腾讯云 → 百度 → 有道 → MyMemory → LibreTranslate；仅尝试已启用的上游

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
| `TRANSLATE_MYMEMORY` | `true` 显式启用 MyMemory，默认关闭 | 否 |
| `TRANSLATE_MYMEMORY_EMAIL` | 自有有效联系邮箱，可选；不自动填入假邮箱 | 否 |
| `TRANSLATE_MYMEMORY_API_KEY` | MyMemory 正式 API Key，可选 | 否 |
| `TRANSLATE_LIBRETRANSLATE_URL` | 自有或授权使用的 LibreTranslate 实例地址，配置后启用 | 否 |
| `TRANSLATE_LIBRETRANSLATE_API_KEY` | 实例要求的 API Key，可选 | 否 |
| `TRANSLATE_HYMT_URL` | 本地 Hy-MT2 llama.cpp 服务根地址，配置后启用 `hymt` | 否 |
| `TRANSLATE_HYMT_API_KEY` | 推理实例要求的 API Key，可选 | 否 |

MyMemory 和自定义 LibreTranslate 使用独立的显式配置，不受 `TRANSLATE_FREE_PROVIDERS=false` 影响。

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

可选字段 `provider` 指定 `baidu`、`mymemory`、`libretranslate` 等已配置上游。
指定服务商后只调用该上游，失败不暗中回退；未知或未启用服务商返回 HTTP 400。
留空则保留自动回退行为，响应 `provider` 始终标识实际返回译文的上游。

```json
{"q":["Hello world"],"source":"en","target":"zh-CN","provider":"mymemory"}
```

## MyMemory 与 LibreTranslate

MyMemory 调用官方 `/get` 接口，每个文本项最多 500 个 UTF-8 字节，必须指定源语言。
自动源语言、HTML、超长文本直接报错，不猜测语言或静默截断。
即使 HTTP 200，也必须校验 `responseStatus` 和 `quotaFinished`，防止把错误说明当译文。
匿名额度由官方控制；有邮箱或密钥时也不承诺不限量。

LibreTranslate 调用 `/translate` 和 `/detect`，支持文本/HTML及自动语言识别。
配置 `zh-CN` 映射到实例的 `zh`，每个输入项单独请求以保留批次边界。
默认不使用不可靠的第三方公共实例；提供独立 Compose 文件部署私网服务：

```sh
docker compose --project-name sub2api --project-directory /opt/sub2api \
  --env-file /opt/sub2api/.env -f /opt/sub2api/deploy/docker-compose.yml \
  -f /opt/sub2api/deploy/docker-compose.libretranslate.yml up -d --no-deps libretranslate
```

实例固定镜像版本与摘要，只加载中英模型，限制 2 CPU/2 GiB，并持久化模型缓存。
不映射公网端口。Go 网关容器与其共享私网，设置：

```dotenv
TRANSLATE_MYMEMORY=true
TRANSLATE_LIBRETRANSLATE_URL=http://libretranslate:5000
```

新服务器须显式启动此可选服务；Go 网关重建不会自动下载模型或启动旁路实例。
当前中英以外的语言可能不支持，指定上游时返回错误，自动模式继续回退。

### Hy-MT2 离线翻译

`provider: "hymt"` 调用本地 Hy-MT2-1.8B，通过 llama.cpp 的
`/v1/chat/completions` 推理。使用官方单轮 user 提示，不添加系统提示；采样参数为
temperature=0.7、top_p=0.6、top_k=20、repeat_penalty=1.05。
指定服务商不会暗中回退到联网服务；省略服务商时保留现有顺序，百度仍优先于新服务。

部署固定版本的 Q8_0 权重（约 1.91 GB），下载脚本验证官方 LFS SHA-256。
首次准备模型和镜像需要联网；准备完成后的翻译只访问本地服务，不自动下载模型。
Apache-2.0 许可证及官方模型说明见 `tencent/Hy-MT2-1.8B-GGUF` 模型仓库。

```sh
sh deploy/download-hymt-model.sh
# 如官方站点不可达，可显式使用镜像下载，仍校验相同权重摘要。
HYMT_MODEL_HUB=https://hf-mirror.com sh deploy/download-hymt-model.sh

docker compose --project-name sub2api --project-directory /opt/sub2api \
  --env-file /opt/sub2api/.env -f /opt/sub2api/deploy/docker-compose.yml \
  -f /opt/sub2api/deploy/docker-compose.hymt.yml up -d --no-deps hymt
```

在 Go 网关私有环境文件中设置 `TRANSLATE_HYMT_URL=http://hymt:8080`，再重建网关。
服务只加入 Docker 私网，不开放宿主机端口，挂载只读模型，限制 3 CPU / 4 GiB。
单推理槽、8192-token 上下文；每项最多 4096 UTF-8 字节，每批最多 16 项，逐项翻译。
推理超时 120 秒/项，输出最多 2048 token，截断或空输出均报错，不返回不完整译文。
仅支持 `text`，不承诺保留 HTML。支持 `zh-CN`/`zh-Hans`、`zh-TW`/`zh-Hant` 等语言别名。
源语言留空或 `auto` 时模型自动翻译，但不伪造 `detected_language`；不提供独立语言检测。
CPU 适合低并发短文本，长文本或多人并发需另行评估 GPU 部署。

```sh
TRANSLATE_HYMT_LIVE_TEST=1 TRANSLATE_HYMT_URL=http://127.0.0.1:18085 \
  go test ./internal/platform/translate -run TestHyMTLive -v
```

上面的本地测试地址需自行建立临时 loopback 访问入口，生产默认不映射端口。

### 天津海光 DCU 部署

天津 `tianjin-ai` 是海光 K100_AI / gfx928，不使用 NVIDIA CUDA 镜像或 `--gpus all`。
`deploy/docker-compose.hymt-tianjin.yml` 复用主机现有海光版 vLLM 0.13.0，
固定本地镜像标签 `hy-vllm:hymt-20261007`，准备时须核对其镜像 ID 与已验证环境一致。
运行前下载 BF16 模型：`download-hymt-hf-model.py` 固定官方仓库 revision，验证
`model.safetensors` 的 SHA-256。此脚本需要 `huggingface_hub`，可在现有推理镜像中运行。
推理容器启用 `HF_HUB_OFFLINE=1` 与 `TRANSFORMERS_OFFLINE=1`，只挂载准备好的本地文件。

默认仅使用第 2 张卡（device 1），显存预算为该卡总量的 12%，上下文 8192 token，
并发最多 2，关闭 CUDA graph 捕获。不得为了新服务停止现有模型或改变主机驱动。
设置独立的 `HYMT_DCU_API_KEY`，服务仅绑定 `127.0.0.1:18086`，不要直接暴露公网。

天津侧 `hymt-tunnel` 与网关侧 `docker-compose.hymt-visitor.yml` 使用独立 FRP STCP 通道，
不新增公网推理监听端口，也不修改现有媒体代理。两端须使用匹配的 FRP 0.70.1，
密钥通过私有环境文件注入；`Dockerfile.hymt-frpc` 使用已校验的静态 `frpc` 二进制构建。
两端显式设置 20 秒心跳及 90 秒超时，防止服务端 180 秒心跳超时导致活跃请求中断。
visitor 只加入网关私网，无宿主机端口映射。配置网关：

```dotenv
TRANSLATE_HYMT_URL=http://hymt-visitor:18086
TRANSLATE_HYMT_API_KEY=<private-instance-key>
```

采样参数同时包含 llama.cpp 的 `repeat_penalty` 和 vLLM 的 `repetition_penalty`，
确保两个引擎使用相同的重复惩罚。切换前必须验证远端真实译文及未授权请求被拒绝；
保留 CPU 实例可供人工回切，但显式 `provider: "hymt"` 不自动切到其他在线服务。

```sh
TRANSLATE_PUBLIC_LIVE_TEST=1 TRANSLATE_LIBRETRANSLATE_URL=http://127.0.0.1:5000 \
  go test ./internal/platform/translate -run TestPublicProvidersLive -v
```

实时测试中的地址应替换为实际可访问的私网实例地址；默认部署并不发布宿主机 5000 端口。

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
