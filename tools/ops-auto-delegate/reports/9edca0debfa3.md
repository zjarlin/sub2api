# sub2api 请求错误批次评估：9edca0debfa3

- 采集窗口：2026-10-16 最近 180 分钟；范围：resolved=false、is_business_limited=false、status>=400。
- 本批明细：8 条；均为 OpenAI 分组 6 的上游 502。逐条结论：A=0，B=8，C=0。
- 判据：A 仅限网关自身可由代码确定并修复的错误；B 为账号/配置/上游返回或客户端请求问题；C 为缺少 request/route/upstream 原始证据而无法归因。

## 逐条评估

| id | 结论 | 一句话根因 | 依据 | 建议动作 |
|---:|:---:|---|---|---|
| 202789 | B | 上游 `/v1/responses` 直接返回 502，网关仅透传 `Upstream request failed`。 | provider/upstream_http；无调用链、无网关内部错误；同一模型跨账号连续出现。 | 核查账号 893 对应上游健康、原始响应与时间窗；上游报障或临时摘除账号，不改代码。 |
| 202790 | B | 同上，账号 844 的上游不可用。 | 同签名且跨账号复现。 | 核查账号 844 上游日志/配额/线路。 |
| 202791 | B | failover 的 search helper（账号 865）返回 502，未产生已验证搜索结果。 | 调用链明确 `stage=search_helper`、`upstream_status_code=502`；`verifiedSearchFallbackText` 只接受 completed 搜索及有效来源。 | 检查账号 865 的搜索模型/线路及结构化 `web_search_call`/引用响应；恢复可验证来源或关闭该辅助策略。 |
| 202792 | B | 上游 `/v1/responses` 502，账号 902 线路故障或上游暂不可用。 | provider/upstream_http；无内部栈。 | 核查账号 902 上游原始响应与健康。 |
| 202793 | B | 上游 `/v1/responses` 502，账号 844 同线路持续失败。 | 同 202790。 | 同 202790。 |
| 202795 | B | search helper（账号 865）未返回 verified source-backed results。 | 调用链明确 helper 502；代码按设计拒绝无 completed 搜索/有效 URL 的结果，不是误判。 | 修复/替换账号 865 的搜索上游，或补充通过探查的可验证候选。 |
| 202796 | B | 同 202795。 | helper 502，候选 index=1。 | 同 202795。 |
| 202798 | B | 同 202795。 | helper 502，候选 index=1。 | 同 202795。 |

## A 类修复与验证

本批无 A 类，不修改业务代码，也没有可合理新增的测试。现有 `backend/internal/service/auto_model_search.go` 已明确：搜索助手必须返回 completed 的 `web_search_call` 与合法 HTTP(S) 来源，否则返回 502；该保护属于正确的安全/证据约束。

验证：完成静态调用链与现有测试定位；未执行部署、容器重启、Compose 或生产配置修改。

## B 类汇总：环境/账号/上游问题清单

- 账号 893、844、902 的 `deepseek-v4.1-flash` 上游 502：分别按账号核查原始上游响应、线路、配额和健康状态。
- 账号 865 的 `gpt-6-luna` search helper 返回 502/未形成可验证来源：核查搜索模型能力、`web_search_call` 完成状态、`action.sources` 或 `url_citation`，必要时摘除/替换该候选。
- 由于未提供上游原始响应头、完整请求体、线路配置和同时间窗健康探针，无法把这些 502 归因到网关代码；代码修改不成立。

## C 类补证

本批明细均含足够的阶段/来源/账号信息，暂无 C。若要进一步确认具体上游根因，需要补充 request_id 对应的上游原始状态、响应体、路由 URL、账号配置快照及搜索 helper 完整 Responses 响应。
