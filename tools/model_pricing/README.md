# 模型价格快照与生效记录

本目录记录 2026-09-16 对 Sub2API 现有模型的定价调整。`prices.json` 是逐模型报价、来源及适用分组的审计快照，金额为 USD；Token 字段按单个 Token 存储，界面展示每百万 Token 时乘 1,000,000。

## 定价口径

- 对管理端候选列表与实际 `/v1/models` 中的其余 176 个模型名设置价格，豆包两项按次价保持不变。没有新增可调度账号或修改账号映射。
- `official`：41 项官方报价。`market_quote`：45 项 OpenRouter 标准付费路由报价快照，不代表所有供应商统一价格。
- `catalog_reference`：5 项沿用已部署目录参考值，包括未确认真实模型身份的 `gpt-6`、`gpt-reserve` 两个别名，以及 3 项 GPT Image 模型。图片模型文本输出设为零，图片输出仍独立计费。
- `site_reference`：85 项没有直接公开报价的别名、开源或本地模型，采用明确标注的本站参考价，不声称是该模型官方价格或实际成本。
- 本站参考档以 Gemma 3 4B、Qwen3 14B、Llama 3.1 70B、Nemotron 3 Ultra 的公开付费报价为基准；无法确定规模的模型采用通用 14B 档。模型规模不等同于能力或运行成本。
- 向量模型输入参考 `text-embedding-3-small` 的 $0.02/百万 Token；输出不收费。ESMFold、NVCLIP、视频检测器缺乏可比 Token 价格，设置本站 $0.001/成功请求基础服务价。

## 原生配置位置

采用 Sub2API 原生渠道模型价表，按请求模型名匹配，关闭模型限制且不设置映射。分组 6、7、19、20 各自使用独立价表；分组 8 更新原有 `cc` 渠道的价格，保留其设置及分组关系。账号倍率、调度优先级、分组倍率、用户专属折扣均保留。

分组 6、21 的豆包按次价卡仍优先于渠道价表。视频通过分组 20 的 `video_model_prices` 设置每个模型、每种分辨率的每秒价；旧版本渠道表单不接受 `video` 模式，因此不伪装成按次计费。

模型基础费用再乘原有分组或用户专属倍率。例如 `gpt-5.6-sol` 标准基础价输入 $4、输出 $20/百万 Token，分组 6 默认 0.3 倍后为 $1.2/$6；用户专属倍率继续优先生效。

## 已知计费边界

- GPT 6 Astra、5.6/5.5/5.4 与 Gemini Pro 配置长上下文整单阶梯；Grok 在 200,000 Token 起进入加倍档。保留现有长上下文开关。
- Claude 缓存写入继续使用内置的 5 分钟/1 小时独立价格，避免单个自定义字段将两个时长误设成同价。
- Grok Image 2.0 采用 medium 档本站价（1K $0.06、2K $0.08）；现有尺寸字段不区分 low/medium。
- `deepseek-flash` 采用官方峰谷价：低峰输入 $0.15、输出 $0.6、缓存命中 $0.003/百万 Token；周一到周五 UTC 01:00–04:00、06:00–10:00 乘 2。其他 DeepSeek 版本/前缀名采用各自 OpenRouter 标准路由报价，避免把第三方部署一律认定为官方别名。
- 报价不额外复制供应商限时促销、缓存存储、搜索工具、图片/视频输入附加费。媒体输出按原生图片 Token、张数或视频秒数计费。
- 数据库 Token 价格保留小数点后 12 位，写入前已舍入；图片输出单价字段保留 8 位。审计表记录实际保存价格。
- 配置价格不会使已禁用、无账号、协议不支持的模型自动可用。没有为验价启用这些账号。
- 这些是固定快照；后续厂商变价需要更新价表，不会被默认价格目录自动覆盖。

## 来源

- [OpenAI 标准报价](https://developers.openai.com/api/docs/pricing)
- [Google Gemini 报价](https://ai.google.dev/gemini-api/docs/pricing)
- [Anthropic 报价](https://platform.claude.com/docs/en/about-claude/pricing)
- [xAI 报价](https://docs.x.ai/developers/pricing)
- [DeepSeek 报价](https://api-docs.deepseek.com/quick_start/pricing/)
- [OpenRouter 实时模型价目](https://openrouter.ai/api/v1/models)

## 验证与回退

完整原配置备份存放在本机忽略目录 `../desktop_session/runtime/market-pricing-backup-*.json`，以及服务器 `/opt/sub2api/desktop-api/pricing-backups/`，文件权限 0600。应用通过管理 API 完成，逐字段回读价表，并核对非定价配置、账号绑定和用户倍率。

分组更新必须显式携带原有日/周/月限额；无限额用 `-1`，避免旧版更新接口清空限额。回退时删除本次新增的渠道、恢复原渠道价格及视频价表即可；不要重建账号或覆盖用户余额。实际应用及抽样扣费结果记录在 `validation.json`。

本次生效渠道为 7（Gemini）、8（Grok）、9（codex-models）、10（codex），原渠道 2（cc）保留。新增的动态模型补充到渠道 10；分组 21 仍只有豆包价卡。
