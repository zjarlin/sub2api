# 请求错误批次 25aeef9a83db 评估

- 评估日期：2026-10-14（Asia/Shanghai）。当前分支 `ops-fix/25aeef9a83db`，基线 `d8a3ede75`。
- 根目录 AGENTS.md 缺失：首先尝试读取，随后只读读取用户指定共享源码根目录 AGENTS.md，遵守全部生产操作禁令；未操作共享检出的 Git 状态。
- 八条明细：**A0 / B6 / C2**。48个签名展开为52个唯一ID（含八条明细）：**A0 / B46 / C6**。两种统计不可相加。触发信息中的“错误条数8”是截取明细，与累计52条的展示范围不同。
- 只读数据库核验52/52原始记录，查询时均 resolved=false、is_business_limited=false；未更新错误状态或账号配置。当前健康检查不证明历史模型请求已经恢复。

## 判据与已有覆盖

A：原始请求/响应和复现证明网关自身逻辑违反契约，可以定位文件、函数和逻辑分支并以测试验证。B：原始上游响应明确外部账号、配额、能力、端点或服务故障，没有证据证明网关引入错误；应处理账号、上游或请求。C：缺少区分平台和外部问题所需的原始响应、传输阶段或请求时序。error_owner、严重度、归一化消息不能独立作为判据。

- 优先读取 `ops-fix/1064a56d83ae`（`65da08969`）报告：已覆盖本批八条明细，以及部分聚合ID；其中199690/199693/199739/199740保守判C。本次沿用有证据的评估并补查其余聚合ID，不重复实现补丁。
- 同时核对 `ops-fix/085e75968967` 和 `ops-fix/e2de02c7a392` 的历史报告。历史批次报告的计数/结论不能替代本批原始记录，通用Upstream request failed应以本次查到的原始错误为准。
- `ops-fix/4e1d6214d054` / `989bc3f72f03` 的模型目录修复 `0ebe9ea19` 已在基线；合成目录不保证每个专用模型支持Chat端点。Auto引用输入分支（73473e2b15b4/944f0d9e15ec等）没有本批对应证据。worktree生命周期分支不处理本批上游响应，不引入无关改动。

## 逐条评估

正文八条以“明细”标注；其余为签名聚合补证。HTTP为“对客/上游”；每行附request_id，便于追溯。

| id | 结论(A/B/C) | 一句话根因 | 依据 | 建议动作 |
| --- | --- | --- | --- | --- |
| 199658（明细） | B | Novita自有key限流并报告服务过载 | 账号881；模型`inclusionai/ling-3.1-flash`；HTTP 429/429；原始429；is_byok=true；limit_source=upstream_provider_account；raw=server overload；request_id=`1d442381-689c-42ba-88b6-f5fdb9299d21` | 检查自有key限额/供应商容量，退避、补健康候选或由账号所有者调整BYOK；网关已记录failover，不能解除供应商限流 |
| 199689（明细） | B | 上游账号余额不足 | 账号851；模型`gpt-5.6-terra`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`d81294ee-c973-446a-a83a-45aac5625e1c` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199690（明细） | C | 裸504及499缺失足以定位超时来源的证据 | 账号180；模型`meta/llama-3.2-90b-vision-instruct`；HTTP 499/504；调用链仅failover/504；原始正文、耗时和网络错误类型为空；request_id=`9342a74e-5b4e-4e7a-ab9d-6e45cd18e9f3` | 按Q1补连接/首字节/总耗时、504来源和取消时点，再区分上游超时、代理或网关deadline |
| 199693（明细） | C | 裸504及499缺失足以定位超时来源的证据 | 账号180；模型`meta/llama-guard-4-12b`；HTTP 499/504；调用链仅failover/504；原始正文、耗时和网络错误类型为空；request_id=`740194f4-295f-40d3-914d-401cac7e3d51` | 按Q1补连接/首字节/总耗时、504来源和取消时点，再区分上游超时、代理或网关deadline |
| 199695（明细） | B | NVIDIA推理worker内部失败 | 账号180；模型`meta/muse-glimmer-30b`；HTTP 499/500；原始500/nvcf-worker-service/Internal error while making inference request；499终态另需Q1核对；request_id=`3b632981-7593-4935-8a2e-6ed3cba089e3` | 向NVIDIA报障并补健康候选；网关代码不能修复推理worker，关联客户端取消时序 |
| 199696（明细） | B | 上游账号余额不足 | 账号851；模型`meta/muse-spark-1.1`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`fb78852a-9b05-4a84-aca9-6a42e0bebde2` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199702（明细） | B | 上游账号余额不足 | 账号851；模型`mimo-v2.6-pro-ultraspeed`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`e30ec6a0-fa01-4c17-82f9-4bb39cb18d0b` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199735（明细） | B | NVIDIA推理worker内部失败 | 账号180；模型`nvidia/ai-synthetic-video-detector`；HTTP 502/500；原始500/nvcf-worker-service/Internal error while making inference request；对客502，调用链failover；request_id=`1298588f-cb84-44bd-a575-755fb0d709bd` | 向NVIDIA报障，核对任务输入与真实接口，补健康候选；修改网关不能恢复供应商推理进程 |
| 199737 | B | 模型或当前调用端点在该账号不可用 | 账号180；模型`nvidia/embed-qa-4`；HTTP 404/404；原始404：Upstream error: 404；入站/chat/completions，上游/v1/chat/completions；request_id=`011fc2b1-b7b9-4ef3-bc03-53cb53d8ae61` | 核对base URL、模型映射、实时目录和专用任务端点；目录列出模型不保证普通Chat可调用，移除失效/错端点候选 |
| 199738 | B | 模型或当前调用端点在该账号不可用 | 账号180；模型`nvidia/esmfold`；HTTP 404/404；原始404：Upstream error: 404；入站/chat/completions，上游/v1/chat/completions；request_id=`ed5ce26e-b5f3-4a58-b5f3-e76a91196c71` | 核对base URL、模型映射、实时目录和专用任务端点；目录列出模型不保证普通Chat可调用，移除失效/错端点候选 |
| 199739 | C | 裸504及499缺失足以定位超时来源的证据 | 账号180；模型`nvidia/ising-calibration-1.5-31b`；HTTP 499/504；调用链仅failover/504；原始正文、耗时和网络错误类型为空；request_id=`e2298ca1-a458-4c39-86f5-ec894cc559c3` | 按Q1补连接/首字节/总耗时、504来源和取消时点，再区分上游超时、代理或网关deadline |
| 199740 | C | 裸504及499缺失足以定位超时来源的证据 | 账号180；模型`nvidia/llama-3.1-nemoguard-8b-content-safety`；HTTP 499/504；调用链仅failover/504；原始正文、耗时和网络错误类型为空；request_id=`9ec43a35-a85e-48e0-a167-bd0e7641bf06` | 按Q1补连接/首字节/总耗时、504来源和取消时点，再区分上游超时、代理或网关deadline |
| 199741 | B | 供应商TensorRT-LLM推理出现CUDA非法内存访问 | 账号180；模型`nvidia/llama-3.1-nemoguard-8b-topic-control`；HTTP 502/500；原始500含cudaMemcpyAsync/illegal memory access及供应商推理栈；request_id=`54d39310-4a21-46b6-9198-70bc82eb90ad` | 向NVIDIA报障，附推理事务chat-9f6d8fd742634533afabb42088275274；暂停故障候选或补健康渠道 |
| 199747 | B | 模型或当前调用端点在该账号不可用 | 账号180；模型`nvidia/llama-3.2-nemoretriever-1b-vlm-embed-v1`；HTTP 404/404；原始404：Upstream error: 404；入站/chat/completions，上游/v1/chat/completions；request_id=`2e5d3b91-e352-42ce-8866-6d5cd0fe8c33` | 核对base URL、模型映射、实时目录和专用任务端点；目录列出模型不保证普通Chat可调用，移除失效/错端点候选 |
| 199748 | B | 模型或当前调用端点在该账号不可用 | 账号180；模型`nvidia/llama-3.2-nv-embedqa-1b-v1`；HTTP 404/404；原始404：Upstream error: 404；入站/chat/completions，上游/v1/chat/completions；request_id=`aba8cb72-80ea-459c-bef9-0c5f90e20e76` | 核对base URL、模型映射、实时目录和专用任务端点；目录列出模型不保证普通Chat可调用，移除失效/错端点候选 |
| 199751 | B | 模型或当前调用端点在该账号不可用 | 账号180；模型`nvidia/llama-nemotron-embed-1b-v2`；HTTP 404/404；原始404：Upstream error: 404；入站/chat/completions，上游/v1/chat/completions；request_id=`26e5183b-e6da-47e3-8daa-ab4a5e800502` | 核对base URL、模型映射、实时目录和专用任务端点；目录列出模型不保证普通Chat可调用，移除失效/错端点候选 |
| 199752 | B | 模型或当前调用端点在该账号不可用 | 账号180；模型`nvidia/llama-nemotron-embed-vl-1b-v2`；HTTP 404/404；原始404：Upstream error: 404；入站/chat/completions，上游/v1/chat/completions；request_id=`100ed29a-fe42-4b6c-88d7-ac877adf1cdb` | 核对base URL、模型映射、实时目录和专用任务端点；目录列出模型不保证普通Chat可调用，移除失效/错端点候选 |
| 199756 | B | 模型或当前调用端点在该账号不可用 | 账号180；模型`nvidia/nemotron-3-embed-1b`；HTTP 404/404；原始404：Upstream error: 404；入站/chat/completions，上游/v1/chat/completions；request_id=`e0f62848-ac36-4140-a404-65b34d1af054` | 核对base URL、模型映射、实时目录和专用任务端点；目录列出模型不保证普通Chat可调用，移除失效/错端点候选 |
| 199758 | B | 供应商worker并发容量已满 | 账号180；模型`nvidia/nemotron-3-nano-omni-30b-a3b-reasoning`；HTTP 502/503；原始503/ResourceExhausted: Worker local total request limit reached (16/16)；request_id=`3d80177b-0c9e-4c49-af7c-4d25ec89c763` | 降低并发、退避并补健康渠道，向供应商核实容量；网关不能扩大worker限额 |
| 199759 | B | 上游账号余额不足 | 账号851；模型`gpt-5.6-terra`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`02a6eed4-d020-4b67-8df0-fd66f23e129e` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199760 | B | 上游账号余额不足 | 账号851；模型`nvidia/nemotron-3-ultra-550b-a55b`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`831c4d37-f7b8-4197-b888-f08b8f4de964` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199766 | B | 模型拒绝纯文本输入 | 账号180；模型`nvidia/nemotron-parse`；HTTP 400/400；原始400明确Content cannot be a plain string / model does not support text input；request_id=`6b7e9d13-73cf-45f7-99ae-6627bf6e2c7a` | 按模型任务协议提供所需多模态输入，移出通用文本探测目录；Q3核对入站与转发请求，若网关错误扁平化再改判A |
| 199769 | B | 模型或当前调用端点在该账号不可用 | 账号180；模型`nvidia/nv-embed-v1`；HTTP 404/404；原始404：Upstream error: 404；入站/chat/completions，上游/v1/chat/completions；request_id=`e307ad1a-c7e7-4934-8b01-5c82e733eeda` | 核对base URL、模型映射、实时目录和专用任务端点；目录列出模型不保证普通Chat可调用，移除失效/错端点候选 |
| 199770 | B | 模型或当前调用端点在该账号不可用 | 账号180；模型`nvidia/nv-embedcode-7b-v1`；HTTP 404/404；原始404：Upstream error: 404；入站/chat/completions，上游/v1/chat/completions；request_id=`f17cdcf2-0d83-4190-9ca4-bb9241b59371` | 核对base URL、模型映射、实时目录和专用任务端点；目录列出模型不保证普通Chat可调用，移除失效/错端点候选 |
| 199771 | B | 模型或当前调用端点在该账号不可用 | 账号180；模型`nvidia/nv-embedqa-e5-v5`；HTTP 404/404；原始404：Upstream error: 404；入站/chat/completions，上游/v1/chat/completions；request_id=`668c8423-a2ad-4771-9c16-b1126acb5a31` | 核对base URL、模型映射、实时目录和专用任务端点；目录列出模型不保证普通Chat可调用，移除失效/错端点候选 |
| 199772 | B | 模型或当前调用端点在该账号不可用 | 账号180；模型`nvidia/nv-embedqa-mistral-7b-v2`；HTTP 404/404；原始404：Upstream error: 404；入站/chat/completions，上游/v1/chat/completions；request_id=`04988cea-4cc5-47f8-86db-5433f42c3b97` | 核对base URL、模型映射、实时目录和专用任务端点；目录列出模型不保证普通Chat可调用，移除失效/错端点候选 |
| 199773 | B | 模型或当前调用端点在该账号不可用 | 账号180；模型`nvidia/nvclip`；HTTP 404/404；原始404：Upstream error: 404；入站/chat/completions，上游/v1/chat/completions；request_id=`64a087c4-0db3-431a-ad8b-da7b01790189` | 核对base URL、模型映射、实时目录和专用任务端点；目录列出模型不保证普通Chat可调用，移除失效/错端点候选 |
| 199791 | B | 上游账号余额不足 | 账号851；模型`poolside/laguna-xs-2.1`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`f601371f-2e58-443f-aeaf-006a42432873` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199792 | B | 免费共享池被供应商限流 | 账号881；模型`poolside/laguna-xs-2.1:free`；HTTP 429/429；原始429；is_byok=false；limit_source=upstream_provider_shared_pool；request_id=`4e55200e-cf4e-42b9-8788-2b5f44a3837c` | 退避、补可用供应商或由账号所有者配置自有key；不能把共享池限流当余额耗尽 |
| 199793 | B | 公司vLLM渠道返回服务不可用 | 账号310；模型`q3-emb`；HTTP 502/503；原始503/Service temporarily unavailable；调用链failover；上游事务4d0c6e6b-1c02-4313-aef7-8ee0a2cbb594；request_id=`31fc1814-c517-4207-8cd7-c3ad984ac74a` | 上游维护者检查vLLM健康/模型加载与任务端点，补健康渠道；无证据证明网关制造503 |
| 199799 | C | 通用502缺失原始上游状态、正文及调用链 | 账号238；模型`qwen2.5:7b`；HTTP 502/未记录；upstream_status_code、detail、upstream_errors、network_error_type均为空；request_id=`7e1f7418-424c-4806-885b-ada374cf37eb` | 按Q2补账号238转发URL、传输错误、上游原始响应及适配器日志，核对模型/端点；不凭provider标签定位根因 |
| 199804 | B | token-plan月度配额耗尽 | 账号883；模型`qwen3.6-flash`；HTTP 429/429；原始429/Throttling.AllocationQuota；上游称11-08 16:00:00 UTC重置（正文未写年份）；request_id=`02419d59-0a47-4532-875e-a2c1a462230c` | 账号所有者核对套餐和重置日期、升级/补有额度账号或暂停无配额候选；不靠短间隔重试恢复月配额 |
| 199805 | B | 上游账号余额不足 | 账号851；模型`qwen3.6-max-preview`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`c834e367-1787-4611-a162-19284cfec786` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199807 | B | 上游账号余额不足 | 账号851；模型`qwen3.7-flash`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`6a279cab-fe89-4bdf-866c-603a203274fd` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199808 | B | 上游账号余额不足 | 账号851；模型`qwen3.7-max`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`4e3fc1ea-5bf4-4b04-b032-ad7b9fa3770c` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199809 | B | 上游账号余额不足 | 账号851；模型`qwen3.7-plus`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`a00de04e-301b-43fb-b33b-72c50215ff74` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199810 | B | 上游账号余额不足 | 账号851；模型`qwen3.8-27b`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`90de8359-3e84-4576-a978-05766b8978d9` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199812 | B | VibeX渠道不可达 | 账号858；模型`qwen3.8-flash-next`；HTTP 499/502；原始502/upstream_unavailable/VibeX is unreachable；对客499需另核对取消时序；request_id=`40ed4f11-72c7-40df-a075-16c881949667` | 排查VibeX网络和服务健康，补健康渠道；按Q1核对499，不无证据修改网关转发 |
| 199813 | B | 上游账号余额不足 | 账号851；模型`qwen3.8-max`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`f6aa15eb-6575-407f-a16a-ce5050073a26` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199815 | B | 上游账号余额不足 | 账号851；模型`qwen3.8-omni-flash`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`3fa0e495-5a6d-4b55-812c-76a40262b220` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199823 | B | 模型或当前调用端点在该账号不可用 | 账号180；模型`snowflake/arctic-embed-l`；HTTP 404/404；原始404：Upstream error: 404；入站/chat/completions，上游/v1/chat/completions；request_id=`6b669094-9d45-4fca-9fa8-733faf959d40` | 核对base URL、模型映射、实时目录和专用任务端点；目录列出模型不保证普通Chat可调用，移除失效/错端点候选 |
| 199825 | B | 供应商结束Pixel Canary预览 | 账号851；模型`stealth/pixel-canary`；HTTP 502/403；原始403/FORBIDDEN明确preview ended on September 30th（未写年份）；request_id=`ecaae334-9883-4e33-a65b-a3ade2e1400e` | 账号/目录所有者移除退役模型，改用当前可用模型；网关代码不能恢复供应商已结束的预览 |
| 199826 | B | 模型或当前调用端点在该账号不可用 | 账号253；模型`stealth/space-bunny-alpha`；HTTP 404/404；原始404：No endpoints found for stealth/space-bunny-alpha.；入站/chat/completions，上游/v1/responses；request_id=`97b79530-b212-4154-8f75-d3fae26ec5de` | 核对base URL、模型映射、实时目录和专用任务端点；目录列出模型不保证普通Chat可调用，移除失效/错端点候选 |
| 199829 | B | 上游账号余额不足 | 账号851；模型`stepfun/Step-3.5-Flash`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`9dc127d8-4bf1-4f46-907d-3e825a5298d2` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199830 | B | 上游账号余额不足 | 账号850；模型`stepfun/Step-3.7-Flash`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`913de2d1-f183-4132-8c07-00c9c552685d` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199831 | B | 上游账号余额不足 | 账号850；模型`stepfun/Step-5-Preview`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`b9eb9d8d-f830-40ea-86a0-d7f6d9f0e8f1` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199833 | B | 上游账号余额不足 | 账号850；模型`tencent/hy3-paid`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`abbcd05e-7d2d-401d-9fb5-5a9803b5c9b1` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199834 | B | 上游账号余额不足 | 账号850；模型`thinkingmachines/inkling`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`db8ebc01-fe67-40c4-aa6e-d685712224ef` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199835 | B | 上游账号余额不足 | 账号850；模型`thinkingmachines/inkling-small`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`75b73ed6-34af-4427-ac54-7c7da6715ebf` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199839 | B | 上游账号余额不足 | 账号850；模型`gpt-5.6-luna`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`e8d46da1-300a-4242-9cc9-f1b86dbbeee4` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199841 | B | 上游账号余额不足 | 账号850；模型`deepseek-v4.1-flash`；HTTP 400/400；原始400/BAD_REQUEST/invalid_request_error明确insufficient credits；request_id=`9a561543-d224-44bc-9f6c-8d0360dfe9f2` | 账号所有者核实充值、暂停无余额候选或补有余额账号；代码不能创造余额，通用BAD_REQUEST不应整体封禁 |
| 199842 | C | 通用502缺失原始上游状态、正文及调用链 | 账号238；模型`qwen2.5:7b`；HTTP 502/未记录；upstream_status_code、detail、upstream_errors、network_error_type均为空；request_id=`24479b8a-eb38-4ef1-839f-d6c7fb912014` | 按Q2补账号238转发URL、传输错误、上游原始响应及适配器日志，核对模型/端点；不凭provider标签定位根因 |

## 平台逻辑与A类补丁摘要

本批没有已证实A类，未改运行时代码，未新增无对应缺陷的测试。只新增本报告。
- `backend/internal/service/openai_gateway_upstream_errors.go:isOpenAIUpstreamAccessStateError` 只读取确定的结构化账号状态code，`BAD_REQUEST`不表示配额；自由文本可能包含回显内容。已有明确quota code会进入账号禁用与换号路径，不能将全部400扩展成配额封禁。余额错误重复出现需要账号配置所有者处理；并不证明这条保守规则有缺陷。
- 同文件 `shouldFailoverOpenAIUpstreamResponse` 与 `backend/internal/service/openai_gateway_passthrough.go:shouldFailoverOpenAIPassthroughResponse` 提供限流/服务错误换号路径。本批多个调用链已记录failover，不能仅凭最终失败认定漏换号；还需当时候选资格和请求上下文。
- `backend/internal/handler/failover_loop.go:failoverClientGone` 在请求context已取消且响应未提交时记录499并终止换号。499与500/504可以是不同时间点的结果；不能将499直接解释为上游返回499。199695/199812已有明确上游故障，按B处理，同时保留取消时序核验。

## C类补证与复现

Q1（199690/199693/199739/199740；附核199695/199812）：按表中request_id查询集中历史日志，关联客户端deadline/断开时点、网关context取消、连接/首字节/总耗时、failover起止、首末SSE帧、响应是否已提交。取得504原始响应头/正文、转发URL及代理/egress事务。核对账号180当时模型映射与端点和合规健康候选。供应商确认是否开始推理。若发现网关提前取消或遗漏合规候选，再定位handler/scheduler分支修复。
Q2（199799/199842）：取得账号238的脱敏配置（base URL/模型映射/超时，不含key）、原始上游响应、dial/TLS/读超时错误及渠道服务日志。以相同模型、端点和脱敏body在隔离环境复现；只有通用502和provider标签不足以认定Ollama/网络/网关哪一方有错。
Q3（199766及专用任务模型）：对照脱敏客户端入站与实际转发body，确认纯字符串来自客户端/通用探测还是网关改写。客户端确实发送文本则改输入；若网关损坏合法多模态结构，应改判A并补复现测试。

只读数据库补证示例（未读取密钥）：

```sql
SELECT id, request_id, account_id, created_at, status_code,
       upstream_status_code, upstream_error_message, upstream_error_detail,
       upstream_errors, network_error_type, duration_ms, inbound_endpoint, upstream_endpoint
FROM ops_error_logs
WHERE id IN (199690,199693,199739,199740,199799,199842);
```

## B类环境问题清单

1. 账号850/851：余额不足，充值、暂停无余额路由或补充有额度账号。不要通过改代码生成余额或将通用400永久封禁。
2. 账号881：Novita BYOK限流与Poolside免费共享池限流要分别处理；账号883：月度配额耗尽，需套餐/额度动作。
3. 账号180：专用模型在Chat端点返回404或拒绝文本，整理分组6/探测模型目录与实际任务接口；worker 500/CUDA错误及503并发满向NVIDIA报障并补健康渠道。
4. 账号310 vLLM服务不可用、858 VibeX不可达：由相应服务所有者检查健康、加载、网络和容量。
5. 账号851 Pixel Canary预览已结束、账号253 space-bunny无endpoint：移除退役/失效候选或选择当前有效模型。

## 验证记录

- 只读SQL先查information_schema.columns，再按本表52个明确ID读取原始错误/调用链、端点及状态；另补读199799/199842的error_body/source/owner/created_at。使用 `docker exec -e PGOPTIONS='-c default_transaction_read_only=on -c statement_timeout=15000' ... psql -X -At -v ON_ERROR_STOP=1`。52/52存在，未执行数据库写入。
- `docker logs --since 2026-10-13T23:35:00+08:00 --until 2026-10-14T01:40:00+08:00 sub2api-sub2api-1` 后以六条C的request_id过滤：0行。此前也按明细request_id查询，未匹配。本地无匹配不证明请求不存在，需集中历史日志。
- `curl --max-time 10 -sS http://127.0.0.1:18080/health`：`{"status":"ok"}`；`docker ps`：主容器和gateway healthy，仅证明核验时入口/容器健康。
- `cd backend && GOMAXPROCS=2 go test -tags unit -p 1 ./internal/handler -run 'TestFailoverClientGone|TestHandleFailoverError_ContextCanceled' -count=1`：PASS（0.074s），验证取消时停止failover与499边界。
- `cd backend && GOMAXPROCS=2 go test -tags unit -p 1 ./internal/service -run 'TestQuotaExhaustedDisablesOnFirstFailure|TestQuotaClassificationDoesNotConfuseConcurrencyOrEchoedInput|TestOpenAIHTTPAccessStateDoesNotTrustBadRequestMessage|TestOpenAIUpstreamAccessStateDoesNotScanEchoedJSON' -count=1`：PASS（0.309s），验证明确quota code禁用换号、并发及回显文本不会误封账号。
- 以上测试验证当前基线的既有边界，不是历史事务完整重放，也不证明供应商恢复。
- 报告校验：52个唯一ID，A0/B46/C6；明细8条A0/B6/C2；`git diff --cached --check`通过。

## 交付与下一步

当前worktree的 `tools/ops-auto-delegate/reports/25aeef9a83db.md` 使用 `git add -f` 随当前分支提交；同内容复制至用户明确要求的共享报告路径 `/opt/cloud-dev/zjarlin/aio/workspace/zjarlin/sub2api-chat-playground/tools/ops-auto-delegate/reports/25aeef9a83db.md`。只写该授权文件，不操作共享检出的git状态。
由用户/账号配置所有者处理B类清单，并补六条C的Q1/Q2证据。未push、合并、部署、重启、重建或替换容器，未改生产Compose/环境变量。目前无代码部署理由；以后确认A并需发布验证时，先由用户决定合并和明确授权，再走TeamCity `sub2api / Deploy 252 Cluster` 或 `/opt/sub2api/deploy/cluster/deploy-252.sh`。
