# 2026-09-28 模型路由与 OpenCode Go 用量核查

## OpenCode Go 856

官方来源：<https://opencode.ai/docs/go/>。本次读取日期为 2026-09-28。
按模型单价计算美元用量，再除以各模型月额度；5 小时、周、月分别为月额度的 20%、50%、100%。不同模型共享折算后的订阅窗口。请求数示例不是固定次数套餐。

| 模型 | 成功请求 | 官方价格复算费用 | 折合月额度 |
| --- | ---: | ---: | ---: |
| Qwen3.8 Max | 52 | $2.706342 | 18.042% |
| DeepSeek V4.1 Flash | 64 | $0.279147 | 0.465% |
| DeepSeek V4 Pro | 12 | $0.159948 | 1.066% |
| DeepSeek V4 Flash | 3 | $0.052019 | 0.173% |
| Kimi K3 | 2 | $0.054346 | 0.362% |
| GLM-5.3-Flash | 1 | $0.002609 | 0.004% |
| 合计 | 134 | $3.254411 | 20.114% |

上述成功请求均处于同一 5 小时窗口。DeepSeek 请求发生在工作日 UTC 01:00-04:00 峰时，使用两倍价格。
复算得到 5 小时 100.569%、周 40.228%、月 20.114%；上游返回 100%、40%、20%，5 小时恢复时间为 2026-09-28 14:33:25 Asia/Shanghai。
上游管理页还包含不一定写入本站 usage_logs 的直接调用或管理员测试，单模型明细不应要求逐条完全相同。

本站 Qwen 的历史 52 次成功请求被记为零费用。修复为新增精确模型价格和账号 856 专用统计价格，历史余额不追扣。
账号统计规则使用请求固定时间计算 DeepSeek 峰谷倍率，长上下文区间按官方明确单价保存。

## 降级条件

错误 180131 来自账号 851 CommandCode 的周额度，和 OpenCode Go 的 5 小时额度是两个不同限制。
CommandCode 的错误含 `RATE_LIMITED`、`rate_limit_error` 和 RFC3339 重置时间，原解析器未识别，因而只使用短暂冷却。
修复后读取其明确恢复时间 2026-10-03 15:29:00 Asia/Shanghai。

旧配置将 DeepSeek V4 Pro 放在 Flash 的上级，较低档请求不会向上选择。当前配置将 V4 Pro、V4 Flash、V4.1 Flash 放在运维“主力编码”档。
这是故障转移顺序，不代表公开评测分数相同。同档耗尽仍继续尝试更低档，不跨分组、不截断输入、不伪造模型响应。

Codex 的客户端 custom 工具可以重放；声明 `execution=client` 的 tool_search 可以重放。托管工具、服务端搜索、会话引用、加密推理及已输出实质内容继续受重放检查约束。
原错误未留存完整请求体，因此不能从历史记录断言某一种工具就是该次请求的唯一阻断原因。

2026-09-28 13:26 的生产回放使用流式 `/v1/responses`、客户端 custom 工具和客户端 tool_search：请求 `deepseek-v4.1-flash`，响应头显示降级为 `deepseek-v4-pro`，SSE 正常结束于 `response.completed`。用量记录 704549 确认实际账号为 848，上游模型 `DeepSeek-V4-Pro`。此回放验证的是当前请求形态；历史 #180131 的原始请求体未留存，不能据此推定它与回放完全相同。

## 目录配置

同步当前启用账号的上游目录，保留现有账号映射，补齐精确同义词。
能力字段采用 models.dev 的同一精确版本作为参考，已验证的账号能力优先保留；未知版本不合并，未公开能力不补猜。
图片、向量、专用任务模型不进入文本档位；未能找到可靠能力声明的模型保留目录，标记待核实。
本次盘点 179 个规范模型 ID，线上降级策略覆盖 86 个文本候选（4/17/25/7/33），其余保持未分档或专用模型状态；有参考能力元数据的 8 个账号已更新。170 条渠道定价包含新增的 26 条 OpenCode Go 模型价，856 的账号统计规则覆盖 35 个型号。档位是运维故障转移顺序，不是官方能力排名；目录中的参考上下文和工具能力不等于逐型号实测通过。

以下清单为本次核查快照；目录可调度不代表每个模型都已逐一实测成功。

| 模型 ID | 运维档位或状态 | 账号 | 上下文 | 最大输出 | 输入能力 | 能力参考 |
| --- | --- | --- | ---: | ---: | --- | --- |
| 01-ai/yi-large | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| adept/fuyu-8b | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| ai21labs/jamba-1.5-large-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| aisingapore/sea-lion-7b-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| aquila | 未公开能力，待核实 | 848 | 未知 | 未知 | 未知 | 账号声明或未知 |
| bge-m3 | 未公开能力，待核实 | 238 | 未知 | 未知 | 未知 | 账号声明或未知 |
| bigcode/starcoder2-15b | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| codex-auto-review | 未公开能力，待核实 | 847, 820 | 未知 | 未知 | 未知 | 账号声明或未知 |
| custom_model_gpt-5 | 未公开能力，待核实 | 848 | 未知 | 未知 | 未知 | 账号声明或未知 |
| custom_model_gpt-6 | 未公开能力，待核实 | 848 | 未知 | 未知 | 未知 | 账号声明或未知 |
| databricks/dbrx-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| deepseek-ai/deepseek-coder-6.7b-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| deepseek-flash | 补充文本候选 | 856 | 1000000 | 393216 | text, image | deepseek |
| deepseek-v4-flash | 主力编码 | 851, 856, 848 | 1000000 | 384000 | text | opencode-go |
| deepseek-v4-flash-vision-exp | 通用编码 | 851, 856 | 1000000 | 384000 | text, image | opencode-go |
| deepseek-v4-pro | 主力编码 | 851, 856, 848 | 1000000 | 384000 | text | opencode-go |
| deepseek-v4.1-flash | 主力编码 | 851, 856 | 1000000 | 384000 | text, image | opencode-go |
| doubao-auto | 轻量备用 | 834 | 未知 | 未知 | 未知 | 账号声明或未知 |
| doubao-pro | 轻量备用 | 834 | 未知 | 未知 | 未知 | 账号声明或未知 |
| Doubao-Seed-2.0-Code | 未公开能力，待核实 | 848 | 未知 | 未知 | 未知 | 账号声明或未知 |
| Doubao-Seed-2.1-Pro | 未公开能力，待核实 | 848 | 未知 | 未知 | 未知 | 账号声明或未知 |
| Doubao-Seed-2.1-Turbo | 未公开能力，待核实 | 848 | 未知 | 未知 | 未知 | 账号声明或未知 |
| Doubao-Seed-Evolving | 未公开能力，待核实 | 848 | 未知 | 未知 | 未知 | 账号声明或未知 |
| explore_sub_agent_v2 | 未公开能力，待核实 | 848 | 未知 | 未知 | 未知 | 账号声明或未知 |
| glm | 未公开能力，待核实 | 857 | 未知 | 未知 | 未知 | 账号声明或未知 |
| glm-5 | 通用编码 | 851, 848 | 202752 | 32768 | text | opencode-go |
| glm-5-turbo | 未公开能力，待核实 | 848 | 未知 | 未知 | 未知 | 账号声明或未知 |
| glm-5.1 | 通用编码 | 851, 856 | 202752 | 32768 | text | opencode-go |
| glm-5.2 | 通用编码 | 851, 856 | 1000000 | 131072 | text | opencode-go |
| glm-5.2-fast | 未公开能力，待核实 | 851 | 未知 | 未知 | 未知 | 账号声明或未知 |
| glm-5.3 | 主力编码 | 851, 856, 854 | 1000000 | 131072 | text | opencode-go |
| glm-5.3-flash | 主力编码 | 851, 856, 854 | 1000000 | 131072 | text, image | opencode-go |
| glm-5.3-flashx | 补充文本候选 | 851 | 1048576 | 131072 | text, image | openrouter |
| glm-flash | 未公开能力，待核实 | 857 | 未知 | 未知 | 未知 | 账号声明或未知 |
| glm-vision | 未公开能力，待核实 | 857 | 未知 | 未知 | 未知 | 账号声明或未知 |
| google/codegemma-1.1-7b | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| google/codegemma-7b | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| google/deplot | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| google/diffusiongemma-26b-a4b-it | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| google/gemini-3.1-flash-lite | 补充文本候选 | 851 | 1048576 | 65536 | text, image | openrouter |
| google/gemini-3.5-flash | 补充文本候选 | 851 | 1048576 | 65536 | text, image | openrouter |
| google/gemini-3.5-flash-lite | 补充文本候选 | 851 | 1048576 | 65536 | text, image | openrouter |
| google/gemini-3.6-flash | 补充文本候选 | 851 | 1048576 | 65536 | text, image | openrouter |
| google/gemini-3.8-flash | 补充文本候选 | 851 | 1048576 | 65536 | text, image | openrouter |
| google/gemma-2b | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| google/gemma-3-12b-it | 补充文本候选 | 180 | 131072 | 16384 | text, image | openrouter |
| google/gemma-3-4b-it | 补充文本候选 | 180 | 131072 | 16384 | text, image | openrouter |
| google/gemma-4-31b-it | 补充文本候选 | 180 | 262144 | 16384 | image, text | openrouter |
| google/recurrentgemma-2b | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| gpt-5.2 | 通用编码 | 820 | 400000 | 128000 | text, image | opencode |
| gpt-5.3-codex | 主力编码 | 851 | 400000 | 128000 | text, image | opencode |
| gpt-5.3-codex-spark | 通用编码 | 820 | 128000 | 128000 | text | opencode |
| gpt-5.4 | 主力编码 | 820, 851 | 1050000 | 128000 | text, image | opencode |
| gpt-5.4-mini | 通用编码 | 820, 851 | 400000 | 128000 | text, image | opencode |
| gpt-5.5 | 主力编码 | 847, 820, 851 | 1050000 | 128000 | text, image | opencode |
| gpt-5.6 | 主力编码 | 847, 820 | 1050000 | 128000 | text, image | openai |
| gpt-5.6-luna | 通用编码 | 820, 851, 856 | 1050000 | 128000 | text, image | opencode-go |
| gpt-5.6-sol | 旗舰 | 847, 820, 851 | 1050000 | 128000 | text, image | opencode |
| gpt-5.6-terra | 主力编码 | 847, 820, 851 | 1050000 | 128000 | text, image | opencode |
| gpt-6 | 旗舰 | 820 | 未知 | 未知 | 未知 | 账号声明或未知 |
| gpt-6-astra | 旗舰 | 847, 820 | 1050000 | 128000 | text, image | opencode |
| gpt-6-luna | 通用编码 | 851, 856 | 1050000 | 128000 | text, image | opencode-go |
| gpt-6-sol | 旗舰 | 820, 851 | 1050000 | 128000 | text, image | opencode |
| gpt-image-1 | 专用模型，不套用文本档位 | 820 | 0 | 0 | text, image | openai |
| gpt-image-1.5 | 专用模型，不套用文本档位 | 820 | 0 | 0 | text, image | openai |
| gpt-image-2 | 专用模型，不套用文本档位 | 820 | 0 | 0 | text, image | openai |
| gpt-image-2.5-flare | 专用模型，不套用文本档位 | 820 | 未知 | 未知 | 未知 | 账号声明或未知 |
| gpt-image-2.5-sunburst | 专用模型，不套用文本档位 | 820 | 未知 | 未知 | 未知 | 账号声明或未知 |
| grok-4.5 | 主力编码 | 851 | 500000 | 500000 | text, image | opencode-go |
| grok-4.6 | 主力编码 | 851, 856 | 500000 | 500000 | text, image | opencode-go |
| grok-4.7 | 主力编码 | 851, 856 | 500000 | 500000 | text, image | opencode-go |
| hy3 | 通用编码 | 856 | 256000 | 128000 | text | opencode-go |
| hy4-preview | 通用编码 | 856 | 1024000 | 64000 | text | opencode-go |
| ibm/granite-3.0-3b-a800m-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| ibm/granite-3.0-8b-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| ibm/granite-34b-code-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| ibm/granite-8b-code-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| kimi-k2.5 | 通用编码 | 851 | 262144 | 65536 | text, image | opencode-go |
| kimi-k2.6 | 通用编码 | 851, 180, 856, 848 | 262144 | 65536 | text, image | opencode-go |
| kimi-k2.7-code | 通用编码 | 851, 856, 848 | 262144 | 262144 | text, image | opencode-go |
| kimi-k2.7-code-highspeed | 补充文本候选 | 851 | 262144 | 262144 | text, image | moonshotai |
| kimi-k3 | 主力编码 | 851, 180, 856 | 1048576 | 131072 | text, image | opencode-go |
| longcat-2.0 | 通用编码 | 856 | 1000000 | 131072 | text | opencode-go |
| longcat-2.5-preview-free | 补充文本候选 | 856 | 1000000 | 131072 | text, image | opencode-go |
| meta/codellama-70b | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| meta/llama-3.2-11b-vision-instruct | 专用模型，不套用文本档位 | 180 | 128000 | 4096 | text, image | nvidia |
| meta/llama-3.2-90b-vision-instruct | 专用模型，不套用文本档位 | 180 | 128000 | 8192 | text, image | nvidia |
| meta/llama-guard-4-12b | 专用模型，不套用文本档位 | 180 | 128000 | 16384 | text, image | nvidia |
| meta/llama2-70b | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| meta/muse-glimmer-30b | 补充文本候选 | 180 | 131072 | 131072 | text, image | openrouter |
| meta/muse-spark-1.1 | 补充文本候选 | 851 | 1048576 | 943718 | text, image | openrouter |
| meta/muse-spark-1.2 | 补充文本候选 | 851 | 1048576 | 943718 | text, image | openrouter |
| meta/muse-spark-1.2-contributor | 补充文本候选 | 851 | 1048576 | 943718 | text, image | openrouter |
| meta/muse-spark-1.3 | 补充文本候选 | 851 | 1048576 | 943718 | text, image | openrouter |
| meta/muse-spark-1.3-contributor | 补充文本候选 | 851 | 1048576 | 943718 | text, image | openrouter |
| microsoft/kosmos-2 | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| microsoft/phi-3-vision-128k-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| microsoft/phi-3.5-moe-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| mimo-v2.5 | 通用编码 | 851, 856 | 1000000 | 128000 | text, image | opencode-go |
| mimo-v2.5-pro | 主力编码 | 851, 856 | 1048576 | 128000 | text | opencode-go |
| mimo-v2.6-flash | 通用编码 | 851, 856 | 1048576 | 131072 | text, image | opencode-go |
| mimo-v2.6-pro | 主力编码 | 851, 856 | 1048576 | 131072 | text, image | opencode-go |
| mimo-v2.6-pro-ultraspeed | 补充文本候选 | 851 | 1048576 | 131072 | text, image | openrouter |
| minimax-m2.5 | 通用编码 | 851, 856 | 204800 | 65536 | text | opencode-go |
| minimax-m2.7 | 通用编码 | 856 | 204800 | 131072 | text | opencode-go |
| minimax-m3 | 通用编码 | 857, 851, 856, 848 | 1000000 | 131072 | text, image | opencode-go |
| mistralai/codestral-22b-instruct-v0.1 | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| mistralai/mistral-7b-instruct-v0.3 | 补充文本候选 | 180 | 65536 | 65536 | text | nvidia |
| mistralai/mistral-large | 补充文本候选 | 180 | 128000 | 102400 | text | openrouter |
| mistralai/mistral-large-2-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| mistralai/mistral-nemotron | 补充文本候选 | 180 | 128000 | 8192 | text | nvidia |
| mistralai/mixtral-8x22b-v0.1 | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| muse-spark-1.2-contributor | 轻量备用 | 856 | 1048576 | 131072 | text, image | opencode-go |
| muse-spark-1.3-contributor | 轻量备用 | 856 | 1048576 | 131072 | text, image | opencode-go |
| nv-mistralai/mistral-nemo-12b-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/ai-synthetic-video-detector | 专用模型，不套用文本档位 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/cosmos-reason2-8b | 专用模型，不套用文本档位 | 180 | 131072 | 16384 | text, image | nvidia |
| nvidia/embed-qa-4 | 专用模型，不套用文本档位 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/ising-calibration-1.5-31b | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/llama-3.1-nemoguard-8b-content-safety | 专用模型，不套用文本档位 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/llama-3.1-nemoguard-8b-topic-control | 专用模型，不套用文本档位 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/llama-3.1-nemotron-51b-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/llama-3.1-nemotron-70b-instruct | 补充文本候选 | 180 | 128000 | 8192 | text | nvidia |
| nvidia/llama-3.1-nemotron-safety-guard-8b-v3 | 专用模型，不套用文本档位 | 180 | 128000 | 4096 | text | nvidia |
| nvidia/llama-3.1-nemotron-ultra-253b-v1 | 补充文本候选 | 180 | 128000 | 16384 | text | nvidia |
| nvidia/llama-3.2-nemoretriever-1b-vlm-embed-v1 | 专用模型，不套用文本档位 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/llama-3.2-nv-embedqa-1b-v1 | 专用模型，不套用文本档位 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/llama-nemotron-embed-vl-1b-v2 | 专用模型，不套用文本档位 | 180 | 32768 | 2048 | text, image | nvidia |
| nvidia/llama3-chatqa-1.5-70b | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/mistral-nemo-minitron-8b-8k-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/nemotron-3-embed-1b | 专用模型，不套用文本档位 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/nemotron-3-nano-omni-30b-a3b-reasoning | 专用模型，不套用文本档位 | 180 | 256000 | 65536 | text, image | nvidia |
| nvidia/nemotron-3-super-120b-a12b | 补充文本候选 | 180 | 262144 | 235929 | text | openrouter |
| nvidia/nemotron-3-ultra-550b-a55b | 补充文本候选 | 851, 180 | 262144 | 182520 | text | openrouter |
| nvidia/nemotron-3.5-content-safety | 专用模型，不套用文本档位 | 180 | 131072 | 117964 | text, image | openrouter |
| nvidia/nemotron-3.5-lightning-30b-a3b | 补充文本候选 | 180 | 262144 | 262144 | text | nvidia |
| nvidia/nemotron-4-340b-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/nemotron-4-340b-reward | 专用模型，不套用文本档位 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/nemotron-nano-3-30b-a3b | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/nemotron-parse | 专用模型，不套用文本档位 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/neva-22b | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/nv-embedqa-mistral-7b-v2 | 专用模型，不套用文本档位 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/nvclip | 专用模型，不套用文本档位 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/riva-translate-4b-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/riva-translate-4b-instruct-v1.1 | 专用模型，不套用文本档位 | 180 | 128000 | 4096 | text | nvidia |
| nvidia/riva-translate-4b-instruct-v2 | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| nvidia/vila | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| omen-alpha | 未核实用途，待核实 | 856 | 500000 | 128000 | text, image | opencode-go |
| openai/gpt-oss-20b | 补充文本候选 | 180 | 131072 | 32768 | text | openrouter |
| poolside/laguna-s-2.1-free | 未公开能力，待核实 | 851 | 未知 | 未知 | 未知 | 账号声明或未知 |
| poolside/laguna-xs-2.1 | 补充文本候选 | 180 | 262144 | 32768 | text | openrouter |
| q3-14b | 轻量备用 | 310 | 未知 | 未知 | 未知 | 账号声明或未知 |
| q3-4b | 轻量备用 | 310 | 未知 | 未知 | 未知 | 账号声明或未知 |
| q3-emb | 未公开能力，待核实 | 310 | 未知 | 未知 | 未知 | 账号声明或未知 |
| qwen2.5:7b | 轻量备用 | 238 | 未知 | 未知 | 未知 | 账号声明或未知 |
| qwen3.6-max-preview | 补充文本候选 | 851 | 262144 | 65536 | text | openrouter |
| qwen3.6-plus | 通用编码 | 856 | 1000000 | 65536 | text, image | opencode-go |
| qwen3.7-flash | 补充文本候选 | 851 | 1000000 | 65536 | text, image | openrouter |
| qwen3.7-max | 通用编码 | 851, 856 | 1000000 | 65536 | text | opencode-go |
| qwen3.7-plus | 通用编码 | 851, 856, 848 | 1000000 | 65536 | text, image | opencode-go |
| qwen3.8-27b | 通用编码 | 851 | 1000000 | 131072 | text, image | openrouter |
| qwen3.8-flash | 通用编码 | 856 | 1000000 | 131072 | text, image | opencode-go |
| qwen3.8-max | 主力编码 | 851, 856, 848 | 1000000 | 131072 | text, image | opencode-go |
| qwen3.8-omni-flash | 专用模型，不套用文本档位 | 851 | 1000000 | 131072 | text, image | openrouter |
| sagitta | 未公开能力，待核实 | 848 | 未知 | 未知 | 未知 | 账号声明或未知 |
| seed-code-pro-0430 | 未公开能力，待核实 | 848 | 未知 | 未知 | 未知 | 账号声明或未知 |
| sensenova-6.8-flash-lite | 未公开能力，待核实 | 857 | 未知 | 未知 | 未知 | 账号声明或未知 |
| snowflake/arctic-embed-l | 专用模型，不套用文本档位 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| space-bunny-free | 未核实用途，待核实 | 856 | 1048576 | 524288 | text, image | opencode-go |
| stealth/pixel-canary | 未公开能力，待核实 | 851 | 未知 | 未知 | 未知 | 账号声明或未知 |
| stepfun/Step-3.5-Flash | 补充文本候选 | 851 | 262144 | 65536 | text | openrouter |
| stepfun/Step-3.7-Flash | 补充文本候选 | 851 | 262144 | 230400 | text, image | openrouter |
| stepfun/Step-5-Preview | 未公开能力，待核实 | 851 | 未知 | 未知 | 未知 | 账号声明或未知 |
| tencent/hy3-paid | 未公开能力，待核实 | 851 | 未知 | 未知 | 未知 | 账号声明或未知 |
| writer/palmyra-creative-122b | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| writer/palmyra-fin-70b-32k | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| writer/palmyra-med-70b | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| writer/palmyra-med-70b-32k | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
| zyphra/zamba2-7b-instruct | 未公开能力，待核实 | 180 | 未知 | 未知 | 未知 | 账号声明或未知 |
