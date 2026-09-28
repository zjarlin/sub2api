# 账号管理表单与组件

OpenAI 透传账号仍可编辑模型白名单与映射，映射键用于调度，透传不会改写模型名。
OpenAI API Key 账号没有目录且未配置模型时，不参与模型调度。创建时会尝试同步
上游模型目录；同步失败会显示提示，用户可在编辑界面同步目录或明确配置白名单。

`ModelProbeSettings.vue` 配置自动模型可用性探测，`modelProbePolicy.ts` 负责表单模型和 extra 字段转换。
默认每个账号至少间隔 168 小时、每轮最多发起一次真实测试请求。GPT 系列（含映射目标）不参与自动测试。
真实请求的健康记录会推迟自动测试；关闭探测不会关闭业务调度、模型目录读取或管理员手动测试。

豆包、TRAE Work、WorkBuddy 使用随部署启动的内置 HTTP 适配器。表单隐藏地址和共享密钥，由后端配置注入；保存时固定 Chat Completions 上游，创建后自动同步模型。TRAE Work 并发默认 1，可按已验证的上游容量调整；豆包与 WorkBuddy 保持单并发。

Grok、Kimi、智谱、DeepSeek、MiniMax、OpenCode Go、豆包、TRAE Work、WorkBuddy、Zcode 按协议自动兼容 OpenAI/Codex 分组，不需要写入或开启 `mixed_scheduling`。账号只有在用户显式选择并绑定分组后才参与该分组调度。Antigravity 仍需显式开启混合调度后，才可选择 Anthropic/Gemini 分组。

自动兼容来源的模型 ID 原样转发，不做别名或模型映射。创建这些平台的 API Key 账号后，表单会尝试同步上游模型目录；也可明确选择原模型 ID 白名单。未获得目录且未配置白名单的账号不会参与对应模型调度。

`BuiltinAdapterLogin.vue` 在新建 TRAE / WorkBuddy 账号，或编辑缺少内置适配器连接凭据的账号时提供登录入口。TRAE 手工提交浏览器回调链接，WorkBuddy 国内版自动轮询授权；登录凭证持久化到适配器共享账号池并立即加载，刷新由适配器维护。表单关闭会中止轮询，重新授权会取消旧会话。已有登录账号池可直接创建 Sub2API 路由账号；多个同平台路由账号共用该池。

部署、首次登录和验收边界见 `tools/traework2api/README.sub2api.md`、`tools/workbuddy2api/README.sub2api.md`。

ZCode 使用同一组件选择 Coding Plan / Start Plan 与 BigModel / Z.ai 账号区域；选项在授权
会话创建后固定。整个部署共享一个 ZCode 上游登录与套餐，成功重新授权会替换旧凭据。
Start Plan 目录来自实际套餐权益，登录 JWT 过期后需重新授权；聊天请求依赖可选浏览器验证服务。
切换套餐后应同步相关账号的模型目录。部署和真实调用验收见 `tools/zcode2api/README.sub2api.md`。

VibeX 使用同一登录会话组件导入 RunningHub 令牌，凭证在后台验证后保存到适配器。
`VibexUsage.vue` 在账号编辑页提供手动余额/额度刷新。部署与文本协议边界见
`tools/vibex2api/README.md`；模型从真实目录同步，不使用静态默认模型。

Qoder 测试模型列表优先使用账号已保存的官方目录及展示名，默认候选使用官方模型键。
提交消息测试先于通用供应商分支处理，OAuth 使用设备令牌，并应用账号模型映射。
目录启用状态和实际推理额度是独立信息；上游额度不足会按真实 HTTP 错误显示。
