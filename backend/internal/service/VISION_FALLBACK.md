# Responses 与 Chat Completions 视觉辅助

网关在纯文本模型收到图片时，先调用当前 API Key 分组内的视觉模型获取描述，
再把图片所在内容块替换为描述，交给原模型继续回答。借鉴
[deepseek-eyes](https://github.com/hawkongz/deepseek-eyes/blob/master/zh-CN/README.md)
的图片转描述思路，实现不依赖客户端安装 skill。

## 客户端为什么在发送前报错

Responses 的 `input_image` 是图片输入结构，不是模型能力协商。
[标准 Models API](https://developers.openai.com/api/reference/resources/models/methods/list)
的模型对象不规定视觉能力字段。Codex 使用自己的模型目录：

- 自定义供应商：`GET {base_url}/models?client_version=...`。
- ChatGPT 模式：`GET /backend-api/codex/models`。
- 配置了 `model_catalog_json` 时，使用本地目录文件。

目录中的 `input_modalities` 会转换为客户端 `inputModalities`。
本机检查到的客户端代码在 `inputModalities.includes("image") === false` 时，
直接显示 `composer.imageInputsUnsupported`，中文正是“此模型不支持图像输入。请尝试其他模型”。
因此这个提示出现时，图片还没有提交给网关。

本次核对位置为 ChatGPT.app 内的 Codex 客户端资源：
`webview/assets/app-primary-44ec287874b7.js` 和 `webview/assets/zh-CN-cd93ae89e5cf.js`。
文件哈希会随客户端更新变化。

## 配置与生效条件

```yaml
gateway:
  vision_fallback:
    enabled: true
    model: ""
    candidate_timeout_seconds: 60
    timeout_seconds: 120
```

环境变量为 `GATEWAY_VISION_FALLBACK_ENABLED` 和 `GATEWAY_VISION_FALLBACK_MODEL`。
超时分别通过 `GATEWAY_VISION_FALLBACK_CANDIDATE_TIMEOUT_SECONDS` 和
`GATEWAY_VISION_FALLBACK_TIMEOUT_SECONDS` 配置；正数按配置生效，未配置或非正数使用默认预算。
默认开启；`model` 留空按账号优先级、账号 ID、模型名稳定选择，也可以优先选择
分组内已经配置的某个公开模型名。首选不可用或失败时自动尝试其他原生视觉候选。

管理员也可以在“系统设置 → 网关服务 → 视觉助手与自动降级”中保存运行时策略。
保存后写入 `vision_fallback_policy` 设置并立即用于后续 HTTP 请求、模型目录和下一个
WebSocket 回合；它覆盖旧环境变量中的开关、首选模型及两个超时。策略包含有序模型列表、
是否允许列表外视觉模型、单助手超时和整次辅助超时。空列表配合“允许列表外模型”保持旧的
自动发现行为。删除或尚未保存该设置时才回退到环境变量默认值；损坏的已保存 JSON 会被
报告为配置错误，不会静默恢复默认策略。

辅助模型必须满足：

- 属于原请求 API Key 的分组，账号有效、可调度且配额未耗尽，并遵守分组的隐私设置要求。
- 为 OpenAI/Grok/Kimi/Zhipu/DeepSeek 平台的 API Key 账号，使用 Responses 或 Chat Completions 协议。
- 模型来自账号映射或已同步的模型快照，明确支持 `image`；也接受现有官方 OpenAI/Grok 能力规则。
- 不是生图模型；不凭模型名猜测第三方接口能力，不为发现能力额外发送付费探测。

只有存在这样的辅助模型，网关才给兼容 Responses 转发的待辅助目录项增加
`input_modalities: [text, image]`。最终目录重新计算 ETag；关闭辅助功能或移除
可用辅助账号后会恢复原始声明。复合分组按 Responses 路由确定目标平台。
已确认原生视觉能力的模型直接透传图片，不调用辅助模型。
明确声明纯文本或缺少能力元数据的对话模型统一使用辅助，包括 GPT、GPT OSS、
GLM、Qwen、Kimi、豆包及其他兼容模型，不再按家族豁免。判断依据为最终映射到的
上游模型。补齐账号的原生 `input_modalities` 元数据后，支持图像的模型自动恢复直传。
未知模型没有可用助手时，目录保持原声明，
直接提交的请求沿用原转发行为；不会把辅助生成的目录声明当成原生能力证据。

更新网关后，客户端必须刷新模型目录。如果配置了 `model_catalog_json`，需要由
模型同步工具重新生成该文件，并让客户端重新加载。仅更新服务端无法使已经加载的
本地静态目录立刻改变。本功能不修改本机 Codex 配置。

## 请求和计费行为

HTTP Responses、Chat Completions（均支持 JSON/SSE）及 Responses WebSocket 都在原模型转发前转换图片。
Responses 处理 `input` 内的消息 `content`、`function_call_output.output` 和 `custom_tool_call_output.output`；
Chat Completions 处理 `messages[].content` 内的 `image_url`，替换为该协议的 `text` 内容块。
Chat 入口在原生 Chat、Responses 和 Anthropic 分流前完成辅助，避免直通分支遗漏。
保留角色、顺序、调用 ID、工具定义、工具参数、推理参数及原模型，不重建整份请求。
辅助结果在独立缓冲区中处理，不会作为辅助模型的回复发给客户端。

- 支持 HTTPS 图片 URL 和 base64 data URL，不在网关下载图片。
- 跨供应商的 `file_id` 无法共享，已知纯文本模型收到这类图片会明确返回 400。
- 历史截图不按累计张数拒绝；逐张原位转换，不丢弃旧图或最新截图。
  默认整个辅助阶段最多 120 秒，单个助手最多 60 秒；父请求的更短截止时间仍然有效。
  非流式助手需要生成完成才返回响应头，超时后继续尝试下一候选，整次预算不会按图片数重置。
  每张描述最多 4096 输出 token、32 KiB，缓存命中的历史截图不重复请求助手。
- `original` 细节模式转换为 `high`；辅助后的模型不宣称原生 `original` 支持。
- 图片及其文字是待观察资料，辅助提示要求忠实转录，不执行图中指令。
- 摘要按 API Key、分组、辅助账号、模型、图片和附带文本隔离缓存 10 分钟，最多 128 条。
  不缓存原图；相同内容的重放和主账号换号可复用摘要。
- 辅助调用使用已有凭据、代理和转发实现，遵守账号并发槽，独立记录实际用量、模型、账号和计费。
  每次真实辅助调用分配独立的 `vision_helper:` 计费 ID，避免继承父请求 ID 导致主用量被去重拒绝。
  主模型失败时已发生的辅助用量仍然记录。缓存命中不再次计辅助费用。
- 辅助失败、空描述、未完成响应或超时会尝试下一候选；全部失败才返回错误。
  原生能力未知且没有辅助模型时保留原转发行为。
- 对已知纯文本模型，没有助手、加载候选失败、容量不足及无效摘要均保留为可换号错误，
  由外层完成账号/模型回退后统一输出，不提前写入 JSON，也不把助手故障归因到主账号。
  已配置视觉模型不代表当前可用：限流、冷却或配额耗尽会将其排除。
  此时保留原始图片请求供重试，不能删除图片后转发；图片工具的纯图片结果否则会变成空数组。
  终止性的图片格式错误仍返回 400，并标记响应已提交，避免外层追加 `response.failed`。
  若排队或 compact 心跳已提交 SSE，则按 Responses/Chat 协议写入单个流内错误。
  助手返回的实际 HTTP 状态和限流响应头保留给监控与调度，不将未写出的 401/429 一律记成 502。
  网关预算到期记为 504 与明确的助手超时信息，避免被通用 502 描述掩盖。
- 额外延迟与费用来自辅助视觉调用；关闭此配置可恢复原行为。

文字描述无法无损保留图片中的全部信息，精确像素操作仍应选用原生视觉模型。

## 验证

重点测试在 `backend/internal/service/vision_fallback_test.go`、`vision_fallback_chat_test.go`、
`vision_fallback_recovery_test.go` 与 `backend/internal/handler/openai_vision_error_response_test.go`，覆盖能力目录与 ETag、
分组隔离、原生视觉直通、多图与工具输出、JSON/SSE 主模型响应、独立辅助用量、
缓存、取消、输入边界和失败处理。WebSocket 接入点位于 `openai_ws_forwarder_ingress.go`，
在发送上游之前进行转换；透传路径的后续回合辅助调用独立取得并发容量。
对话模型缺少能力快照时，测试同时检查客户端目录放行与实际主模型收到图片描述，
并覆盖没有助手、原生视觉、GPT 别名、复合路由别名和 WebSocket 后续轮次。

2026-09-16 本地验证：service、handler、config、routes 的相关定向回归共通过
264 个测试及子测试事件，启用 `-race`，无竞态报告；`go build ./...` 和
`git diff --check` 通过。当时使用本地模拟上游和真实 WebSocket 连接。
后续又补充无显式映射的模型发现、父子请求计费 ID 隔离回归，均通过 `-race` 验证。

## 2026-09-16 线上验收

首次部署到 `192.168.31.252:18080` 的镜像为
`zjarlin/sub2api:0.1.183-custom-8279fd4f4-vision-v2-20260916`。
发布包以原有 DeepSeek compaction 部署源码为基础，只叠加本功能文件，保留原前端和数据库结构。
分组 6 使用已通过真实图片请求确认的 `gpt-5.6-luna` 作为辅助，
通过 `GATEWAY_VISION_FALLBACK_MODEL` 固定，能力保存于所属账号的现有模型元数据字段。

两张用户截图分别通过 `deepseek-v4.1-flash` Responses SSE 请求验收，
最终事件均为 `response.completed`，输出模型仍为 `deepseek-v4.1-flash`；
最新弹窗截图正确转录出标题、提示文案和按钮文字，耗时约 8.3 秒。
最终用量记录为辅助 `626122`（账号 831 / `gpt-5.6-luna`）与主调用
`626123`（账号 832 / `deepseek-v4.1-flash`），两者使用不同计费 ID，均落库成功。
LAN 与公网 `/v1/models?client_version=...` 均返回 DeepSeek 的 `[text, image]`。

本机 Codex Buddy 同步器另外修复了已有目录覆盖新视觉能力的问题：
网关明确的输入能力优先，其他元数据优先级保持不变，后台同步运行文件同步更新。
73 项测试通过、2 项跳过；真实同步后再次同步 `changed=false`。
`codex debug models` 已确认可加载 DeepSeek 的图像能力。正在运行的桌面客户端
仍需重新加载模型目录；本次未以桌面重启后的实际发送操作作为验收证据。

## 2026-09-16 监控修复

分组 6 的第三方 OpenAI 账号 831 仅保存了 `gpt-5.6-luna` 的视觉能力元数据。
网关曾把其他 GPT 模型的未知能力误判为纯文本，先调用辅助模型，导致四次
`The vision helper could not describe the image` 502。对 `gpt-5.6-sol` 和
`gpt-6-astra` 分别发送真实图片后，两者均直接完成转录，辅助用量没有出现。
现仅在主账号明确为纯文本时调用辅助；能力未知时按原样转发图片。

已把这两个经真实 Responses 图片请求验证的型号写入账号 831 的能力元数据，
来源标记为 `verified-responses-image`，并在目录层用当前可调度账号的验证结果
补正能力声明。未经该验证的账号继续遵守原有目录交集规则。账号元数据更新前的
备份保存在服务端 `backups/vision-capability-831-followup-20260916.json`。

该轮镜像为 `zjarlin/sub2api:0.1.183-custom-8279fd4f4-vision-catalog-20260916`。
隔离发布源码对 service、handler、config、routes 的相关测试启用 `-race` 通过，
Linux amd64 构建及健康检查通过。内网、公网 `/v1/models?client_version=0.114.0`
均返回这两个 GPT 型号及 `deepseek-v4.1-flash` 的 `[text, image]`；本机
Codex Buddy 静态目录同步后也一致。DeepSeek 图片请求用量 `626757` 为辅助账号
831 的 `vision_helper:`，`626758` 为主账号 832，回复正确转录截图。
此外，价格哈希远程拉取超时时继续使用已加载价格并记录 WARN，避免将可用服务的
临时网络故障记为 ERROR。

## 2026-09-17 非 GPT 模型扩展

该轮镜像为 `zjarlin/sub2api:0.1.183-custom-8279fd4f4-general-vision-20260917`。
未知能力的非 GPT 模型在有分组内原生视觉助手时同样接入图片描述流程；目录生成
与 HTTP/WebSocket 请求复用相同判断。复合路由使用解析后的上游模型，避免公开别名
与真实型号不一致时漏报能力。已有原生图像能力优先，辅助模型的选择规则没有放宽。

隔离源码的 service、handler、config、routes 相关 `-race` 回归通过，Linux amd64
构建通过。前端由原部署提交重新构建，主入口资源仍为 `index-D2B9LLM7.js`。
公网与本机同步目录中的 GLM、Qwen、Kimi、Q3、豆包等型号均为 `[text, image]`。

使用用户提供的截图执行真实 Responses SSE 请求：`q3-4b` 约 5.8 秒、`doubao-auto`
约 10.0 秒完成中文提示转录，最终事件均为 `response.completed`，模型名保持原值。
Q3 主调用用量 `627150`、辅助用量 `627149`；豆包主调用 `627152`、辅助 `627151`。
两次辅助均为账号 831 的 `gpt-5.6-luna`，主调用与辅助使用独立计费 ID。
本机 Codex Buddy 目录同步后核对一致；未通过重启正在运行的桌面客户端进行 UI 验收。

## GPT 已验证能力与新增账号

账号 831 的 `gpt-6-astra` 已通过图片请求验证，但新增同型号账号缺少能力快照时，
原目录规则会把该型号降回纯文本。现在目录保留分组内的验证证据：最终上游 GPT
型号相同的未知账号可以继承已验证的图像能力；已验证账号临时不可调度也不会丢失
能力声明。明确的纯文本元数据仍优先，不跨不同上游型号或不同分组传播证据。
辅助模型候选仍必须自身具有原生视觉证据，不使用这种目录推断挑选助手。

回归覆盖新增未知账号后的目录与 ETag、原账号临时不可调度、明确负面能力元数据、
不同型号的公开别名。GPT 图片仍原生转发，不引入额外的辅助调用。

已发布 `zjarlin/sub2api:0.1.183-custom-8279fd4f4-astra-vision-20260917`，以当时
线上 `account-features-20260917` 镜像对应源码为基础，仅修改目录、能力策略及测试。
service、handler、config、routes 的相关定向 `-race` 检查通过，Linux amd64 构建
和公网健康检查通过。实际 Astra 截图请求 4.8 秒完成；修复后内网、公网和本机目录
均为 `[text, image]`，再次运行 Codex Buddy 同步为 `Up to date`。

## 2026-09-18 所有对话模型统一辅助

移除未知 GPT 型号的豁免，补齐 `gpt-5.6`、`gpt-6`、`gpt-reserve`、
`openai/gpt-oss-20b` 等漏项。候选复用模型目录背后的账号映射、已同步模型及
原生能力元数据；不会将辅助后生成的 `image` 声明作为原生能力再次选入，避免递归。
首选模型改为排序偏好，单个助手失败或超过 30 秒后继续尝试其他候选，整个辅助
阶段仍最多 90 秒。已明确支持原生视觉的账号继续直传。

发布镜像 `zjarlin/sub2api:0.1.183-custom-8279fd4f4-all-model-vision-20260918`，
以线上 `test-responsive-20260917` 源码为基础，只叠加视觉策略及候选失败切换。
service、handler、config、routes 的视觉及目录定向 `-race` 测试通过。
内网与公网 `/v1/models?client_version=0.114.0` 的 34 个目录项全部包含 `image`。
上述四个漏项的真实截图 Responses SSE 请求均收到 `response.completed`；
独立辅助用量为 `631755`、`631760`，助手是账号 831 的 `gpt-5.6-luna`。
后续相同图片可复用摘要。按四个验收请求 ID 查询监控错误记录为空。

本机安装的旧版 `codex-buddy` CLI 合并目录时优先保留旧能力；现有
`~/.codex/model-sync/runtime.mjs` 已包含远端能力覆盖逻辑。使用该运行时同步后，
本地 35 项全部包含 `image`（包含本机额外目录项）。正在运行的客户端仍可能需要
重新加载目录；本次未重启桌面应用。

## 2026-09-22 助手不可用与错误响应修复

排查时分组 6 的原视觉账号 831 已停用，账号 180、283 曾处于冷却；新账号 846
只有模型 ID 快照，缺少原生视觉能力记录。用三色图片直连验证 846 的 `gpt-5.6-luna`
成功后，仅为该型号补入 `[text, image]`，并通过调度 outbox 刷新快照。
修改前备份为 252 上的 `backups/vision-capability-846-20260922-before.json`。

实际网关 `q3-4b` 图片请求在 6.8 秒返回 `response.completed`、`status: completed`，
回答“蓝色、黄色、品红色”，辅助用量已按账号 846 独立记录。
同一时点 `deepseek-v4.1-flash` 原主账号均不可调度，验收请求在选择主账号时返回 503，
尚未进入视觉辅助；这不作为该型号恢复成功的证据，也不自动启用已停调账号。

代码补齐了助手不可用时的换号错误，并修复终止错误先写 JSON、外层再追加 SSE 的问题。
这些代码变更已通过 service/handler 定向回归，随后随 `3046e594f3fe` 发布。

## 2026-09-22 助手超时修复

11:00 与 11:01 的线上诊断确认账号 846 的 Luna 助手在返回响应头前触发
`context deadline exceeded`，命中原来固定的 30 秒期限，单图和双图请求均受影响。
预算现改为可配置的单助手 60 秒、整次辅助 120 秒；超时保留换号语义并明确记为 504。
回归覆盖默认值、环境变量、父请求期限、总预算限制及超时后成功切换备用助手。

对账号 846 的 Astra 使用合成仪表盘实测，正式助手提示在 9.5 秒完成描述。
仅追加已验证的 `gpt-6-astra` 原生图片能力，保留其他模型元数据；调度刷新后，
网关 Astra 请求在 4.1 秒完成，正确读取 `ORBIT-7319` 和 25 行传感器。
更新前账号配置备份为 252 上的
`backups/vision-astra-846-2026-09-22T03-09-40-009Z.json`。

## 2026-09-23 运行时策略导致助手池耗尽

请求 `84dbd04f-cd10-4420-bf3e-72cc40dec899` 属于分组 6，主模型为
`deepseek-v4.1-flash`。当时保存的运行时策略使用单助手 15 秒、总计 45 秒，
覆盖了代码与环境变量中的默认预算。账号 831 的助手连续超时触发 10 分钟账号熔断，
其他已登记助手也有失败或不可调度，最终在调用主模型前返回没有原生视觉助手的 503。

通过管理 API 将运行时预算恢复为单助手 60 秒、总计 120 秒；助手顺序为
`gpt-6-astra`、`gpt-5.6-luna`、`meta/llama-3.2-11b-vision-instruct`，
仍允许列表外模型作为后备。没有启用已停用账号、移除模型隔离或关闭健康熔断。
修改前策略保存在 252 的 `backups/vision-policy-20260923-before.json`。

使用合成仪表盘调用原入口 `/responses`，主模型保持 `deepseek-v4.1-flash`：

- 单张用户图片请求 `9e9f0459-d5af-4c0a-a787-53eea8dad267`，13.70 秒完成，
  正确读取 `VISION-6237`、10 个传感器、首末行温度及色块顺序。
- 工具结果双图请求 `8a6ded7b-a01c-4271-a9d8-599bc3802670`，73.13 秒完成，
  正确读取 `VISION-6237` 和 `ORBIT-9271`、各自行数及首末行温度。
  期间主调用遇到每分钟 15 次的上游 429，网关已恢复，监控最终状态为 200。

两次 SSE 均以 `response.completed` 结束，没有流内失败事件。该修复只更新运行时
策略，无需重新部署；上游真实不可用或超过新预算时仍会按既有规则报错，不丢弃图片。
