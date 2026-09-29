# Auto 候选恢复、决策目录及 Kimi 参数修复

## 现场证据

- 用户截图中的 `5d82594f-81c6-4902-b1e8-a5ea35d90001` 和 `61456767-e200-465b-99f7-6beb419cc9d3` 分别为 20:46、21:05 的 ZCode 861 / glm-5.3 并发 429（`code=3009`），均早于 21:25 的上一轮部署。原请求体未保存，不能事后断定它们的具体重放限制。
- 上一轮部署后，请求 `2ba23261-d2ab-47fe-9594-db0869ccd4ce` 在上游 429 后恢复，最终 HTTP 200、Auto `completed`。
- 请求 `1277996f-0ebf-4789-b399-382edb7d2650` 命中 Cline 863 的 402 `insufficient_credits`，随后明确记录 `model_fallback_blocked: hosted_tools`，最终失败。历史请求未保存实际工具类型，不能宣称已精确重放该请求。
- 22:04 左右的只读快照：自 21:25 起 80 个 Auto 请求，79 completed、1 failed。这是当时观察结果，不是可用性承诺。
- JEV 账号 853 的协议是 System One，显式映射只有 `typesafe/jev`；其共享上游目录含 84 项，`deepseek/deepseek-v4.1-flash` 被全局别名归一为 `deepseek-v4.1-flash` 后错误显示为 JEV 文本候选。目录不代表 JEV 实际执行该模型。

## 行为变化

- Auto 使用本次请求固定的有限候选列表，不递归构造或循环重试模型。繁忙账号无可用替代时，先尝试后续可重放模型；没有后续候选才沿用有界同模型等待。
- `conversation:null` 和无执行历史的 `web_search`、preview 搜索声明不再单独阻断降级；保持完整输入及工具声明。候选还必须能通过原生协议保留搜索，避免 Chat 桥接静默丢失工具。搜索能力拒绝只切换本次候选，不隔离普通函数调用能力。
- `previous_response_id`、真实 conversation、密文推理、资源绑定工具及真实托管工具历史仍保护其状态。函数参数和返回值里的同名业务字段不误判为托管历史。
- Chat → Responses、Chat → Messages、raw Chat 三条流路径暂存 role、空正文/推理、空工具占位和 usage 前导。实际正文、推理、工具 ID/名称/参数一到就正常刷新，不等待完整响应。前导后 429、额度错误或截断能在未提交时继续降级；已输出语义内容不重放。
- 前导缓冲限制为 64 KiB / 1024 项；超限按未提交的上游故障处理。正常空终态保留协议事件顺序与 usage，截断不能伪造成成功。
- NVIDIA 官方 Kimi K3 的原生档位适配：minimal→low、medium→high、xhigh→max；匹配确切模型和 NVIDIA 域名，不增加 token 预算，并遵守真实分组上限。生产 group 6 当前没有设置推理上限。三个协议入口均覆盖；详见 [Kimi 验证](kimi-nvidia-reasoning.md)。
- 其他来源返回用户提供的确切 Kimi 参数拒绝时，Auto 将其识别为能力差异并换候选。普通格式、鉴权和安全拒绝不因此变成无限重试。
- Auto 计划剔除决策平台共享目录污染；JEV 仅保留实际决策模型，Laya 同理。真实文本来源及离线来源的解释仍保留；历史计划不回写。

## 验证范围

本轮不新增供应商推理探测，使用模拟上游覆盖 HTTP 429/503、精确 Kimi 参数错误、流式/非流式、多候选、搜索声明、工具历史、三个流协议、真实输出后失败、缓冲上限及 EOF。通过管道验证真实输出在 EOF 之前刷新，防止退化为全量缓冲。

已有 `TestArenaResponsesStreamFailureDoesNotEmitCompleted` 在原始 scanCCStream 的临时 overlay 上同样失败：它要求零 usage 失败返回非 nil result，而现有服务约定返回 nil。本轮保留其原语义，单独记录基线断言失配，不宣称整仓测试全绿。

## 发布结果

- 2026-09-29 22:14（Asia/Shanghai）最终核对通过：`sub2api-sub2api-17` / `18` 均使用 `sub2api:auto-recovery-20260929` 且 healthy；版本 `fb05dcb5b-ds-auto-relogin-auto-recovery`；网关 `/health` 为 200，Nginx 配置检查通过。
- 隔离源码只叠加本轮 48 个源码/测试/前端文件于前次已部署版本，保留 DeepSeek 自动续登；未带入并行开发的 `ask` 虚拟模型。`model_fallback_policy.go` 仅应用搜索能力条件 hunk。逐文件 SHA256 在 `source-manifest.json`。
- 定向竞态测试通过：service 5.470s、handler 40.364s、repository 1.178s、migrations 1.021s，日志 `final-race-tests.log`。此前扩展测试中的旧搜索声明断言已更新，同时新增真实搜索历史不可重放断言。
- 隔离前端 71/71 测试、类型检查、生产构建通过；191 个嵌入资源与构建产物逐文件 SHA256 一致，后端 embed 构建、镜像构建通过。
- 七天最小探测及迁移 253 同包上线。发布前保存并临时暂停 35 个账号的自动探测单键配置，等待至少 50 秒后启动新副本。旧 Nginx worker 按切流前 PID 等待排空，旧容器退出后才恢复原设置；逐项存在性/值核对通过。新旧版本混跑时恢复脚本会拒绝执行。
- 核对时 `last_probe_at` 新列存在，新增自动占用数为 0；本轮没有重复探测 331 项，没有新增供应商推理验证。正常业务流量和以后到期的周检按原策略运行。
- 发布目录：`/opt/sub2api/releases/20260929T135314Z-auto-recovery`。运行证明 `verification.json`，部署过程 `deployment.log`。前次镜像 `sub2api:auto429-20260929` 保留用于回退；迁移为仅新增可空列，回退旧二进制前必须暂停探测并完全退役新版，避免并发重复。

服务健康和模拟回归不证明每个供应商当前可用。历史现场请求体缺失，本轮未执行额外付费原样重放；不宣称所有现场失败已逐条实测消失。真实输出后的失败、不可移植的服务端状态、所有候选耗尽、客户端取消及策略拒绝仍返回可追踪的真实错误，不能伪造成功。

