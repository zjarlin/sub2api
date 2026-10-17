# 请求错误批次 df1ce4bf89e4 评估报告

## 范围与结论

- 评估日期：2026-10-14（Asia/Shanghai）；worktree：`/tmp/sub2api-ops-fix/2026-10-13-df1ce4bf89e4`；分支：`ops-fix/df1ce4bf89e4`；基线：`d8a3ede75`。目录名日期不代表本次评估日期。
- 正文提供完整调用链的 8 条错误：**A=0、B=7、C=1**。触发信息的累计92条、78个签名是采集窗口统计，不是正文8条的分类分母；聚合中其他ID不计入上述8条，不能把同一ID重复计数。
- 首条命令读取worktree根目录AGENTS.md，文件不存在；随后只读读取共享源码根目录AGENTS.md。遵守用户及该文件的部署禁令，未操作共享检出的Git状态，未修改生产配置、容器、数据库或错误resolved状态，未push。
- 本次证据：用户提供的错误快照、已有分支提交内报告、当前基线代码。未再次查询生产数据库或扫描历史容器日志；既有报告中的数据库查询及运行健康结果属于历史证据，不冒充本次核验结果。

分类判据：A要求请求级证据证明网关自身契约违约，可定位函数/分支并复现；B要求明确上游响应支持账号、配置、上游或客户端原因，目前没有网关损坏请求的证据；C表示关键证据缺失，不能区分平台与环境。owner、source、严重度以及错误出现次数均不能单独证明A。

## 已有分支覆盖

只读枚举本地 `ops-fix/*` refs，使用 `git diff --name-only HEAD...<ref>` 和 `git show <ref>:<report>` 检查已有变更，没有切换或修改其他分支。

`ops-fix/e6095d8ab029` 的报告表已覆盖 **200106、200108、200109、200110、200111、200112、200116、200118** 全部8个ID，并保留200106的C边界。其报告记载当时原始记录核验：200106的正文、network_error_type及耗时为空；其余7条明确insufficient credits。与本批快照一致。本报告复核当前代码并补充本批处理动作，不重复历史排查，也不把“已写报告”解释为故障已恢复。

模型目录合成修复 `0ebe9ea19`（关联分支4e1d6214d054、989bc3f72f03）已经在基线，`git merge-base --is-ancestor 0ebe9ea19 HEAD` 返回0。它解决缺少models端点，不解决余额、504或专用模型的输入契约。Auto引用输入及worktree生命周期修复没有本批请求级关联证据，不移植无关补丁。

## 逐条评估

HTTP顺序为对客/上游；所有正文请求分组为6。

| id | 结论(A\|B\|C) | 一句话根因 | 依据 | 建议动作 |
| --- | --- | --- | --- | --- |
| 200106 | C | 裸504与对客499缺少足够的来源和取消时序证据 | 账号180；`meta/llama-guard-4-12b`；HTTP 499/504；03:59:21；消息、正文、耗时为空；调用链仅有failover/504；request_id=`8b18dcba-6140-43bf-9aab-6d0760020b53` | 按下方Q1补504产生组件、原始响应和客户端/网关deadline、取消时点；不能直接判为供应商超时或网关缺陷 |
| 200108 | B | 账号851上游余额不足 | `meta/muse-spark-1.1`；HTTP 400/400；04:00:25；上游BAD_REQUEST/invalid_request_error明确“You have insufficient credits”；request_id=`7dd0a0a1-3c3b-4916-a336-7f333502829d` | 账号所有者核验并充值，或暂停无余额候选、补有余额渠道；充值后用支持的模型验证 |
| 200109 | B | 同一账号余额不足影响另一个模型 | 账号851；`meta/muse-spark-1.2`；HTTP 400/400；04:00:27；同一明确余额错误；request_id=`bfb3b403-e007-418d-9356-85a9eadf0b5b` | 与200108合并处理账号余额，无需逐模型改请求转换 |
| 200110 | B | 同一账号余额不足影响contributor模型 | 账号851；`meta/muse-spark-1.2-contributor`；HTTP 400/400；04:00:29；同一明确余额错误；request_id=`791e68e1-67e0-4f38-baff-26410f4c4974` | 与200108合并处理，核验恢复后的模型权限 |
| 200111 | B | 同一账号余额不足影响1.3模型 | 账号851；`meta/muse-spark-1.3`；HTTP 400/400；04:00:30；同一明确余额错误；request_id=`86049a51-eaa9-4812-8329-e7d8e7a60769` | 与200108合并处理，补有余额候选 |
| 200112 | B | 同一账号余额不足影响1.3-contributor模型 | 账号851；`meta/muse-spark-1.3-contributor`；HTTP 400/400；04:00:32；同一明确余额错误；request_id=`a8d27eed-804c-44af-9fa3-21e24d7974a0` | 与200108合并处理，充值后验证 |
| 200116 | B | 上游500回退后，最终渠道账号851余额不足 | `mimo-v2.5`；04:00:39；账号872/`cline-pass/mimo-v2.5`先返回500、empty response content并记录failover；账号851/`xiaomi/mimo-v2.5`后返回400/insufficient credits；request_id=`06c28824-1ac1-4401-881b-7ab605fd612e` | 先处理851余额，另由872供应商核查请求`cNQNrNMxRRnxYtsHhHGvPzDCtsulJYnT`的空响应；提供健康替代渠道 |
| 200118 | B | 账号851上游余额不足，与模型别名转换无直接关联证据 | `mimo-v2.5-pro`→`xiaomi/mimo-v2.5-pro`；HTTP 400/400；04:00:42；同一明确余额错误；request_id=`5cc9283e-8002-445a-8a08-2b1401942d88` | 与200108合并处理；当前没有证据要求修改模型映射 |

## 代码边界与为何不改代码

- `backend/internal/service/openai_gateway_upstream_errors.go` 的 `shouldFailoverUpstreamError` 对5xx返回true，`shouldFailoverOpenAIUpstreamResponse`进一步处理有明确语义的错误；200106、200116的链中确有failover。单条尝试或最终400不足以证明尚有合规候选被遗漏。
- 同文件 `handleCompatErrorResponse` 读取上游错误、应用配置的错误透传规则、记录上游事件并执行现有错误处理；`backend/internal/service/openai_gateway_chat_completions.go` 的 `handleChatCompletionsErrorResponse` 使用该通用路径。400/invalid_request_error不是请求格式错误的充分证据，具体正文在本批明确为供应商余额不足。
- `backend/internal/handler/failover_loop.go` 的 `failoverClientGone` 在请求context已结束且响应未提交时将状态标记499。因此499和调用链中的504可属于不同阶段，不能仅凭两码不一致判A，也不能由最终499反推具体断开时间。
- 网关代码不能补充供应商额度。把所有400强制改成重试、将BAD_REQUEST永久封禁账号、静默改写请求或对专用模型伪造文本成功，都缺少本批证据与契约依据。若后续证明仍有合规健康候选而平台漏选，或网关错误改写合法请求，应改判A并针对该分支补测试。

**A类补丁摘要：无。** 没有达到A判据的记录；没有业务代码变更或新增测试。既有测试用于核验当前错误映射及取消边界，不证明历史请求已重放或上游已恢复。

## B类环境问题清单

1. 账号851（zjarlin_commandcode）余额不足：7条终态错误横跨Muse Spark与MiMo，优先处理账号级余额。由所有者核对上游账单/余额和套餐权限，再充值或调整调度候选；本次未执行这些配置动作。不自动将错误resolved。
2. 账号872（ClinePass）在200116中返回500空响应：这是已记录的前序上游失败，最终失败为851余额不足。向供应商提供其request ID、脱敏请求与当时日志，核查空响应；不能从这一条认定所有ClinePass模型不可用。
3. 恢复后先对正确端点及支持的模型进行受控验证，再恢复候选。不要直接批量遍历专用模型做通用Chat测试，也不要把模型目录可列出等同于请求可调用。

## C类补证：200106（Q1）

不重复执行既有报告的数据库查询；下一轮需取得此前缺失的请求级证据，而非再读同一空记录。

1. 按request_id `8b18dcba-6140-43bf-9aab-6d0760020b53` 查询2026-10-14 03:58:00—04:01:00（Asia/Shanghai，对应2026-10-13 19:58:00—20:01:00 UTC）的客户端、入口代理、网关及账号180供应商日志。取入站时间、每次尝试起止、failover选择结果、取消原因、context deadline、响应提交状态，以及504具体产生组件和原始头/正文。日志保留窗口不足时，请从历史日志存储查询。
2. 只读补查记录（不查询凭据）：

```sql
SELECT id, request_id, account_id, created_at, status_code,
       upstream_status_code, upstream_error_message, upstream_error_detail,
       upstream_errors, network_error_type, duration_ms,
       inbound_endpoint, upstream_endpoint
FROM ops_error_logs
WHERE id = 200106;
```

3. 取得账号180的脱敏base URL、模型映射和超时设置、原始脱敏请求及客户端deadline；在隔离fixture中分别模拟上游504、客户端先取消、网关deadline耗尽，记录事件顺序及响应。若平台在有效deadline前错误取消或遗漏合规候选，定位具体handler/scheduler分支改判A；若供应商明确超时或客户端主动取消，改判B。尚未执行复现，不凭空创建取消时序。

## 聚合签名评估边界

78个聚合签名包含正文以外记录；只有归一化消息而缺少这些记录的状态、账号和完整链，不能声称已逐条验证窗口内92条事务。既有e6095d8ab029报告已覆盖部分相同ID，可引用其请求级证据；其他记录需先取得原始错误再定最终A/B/C。对本批正文8条的结论不作扩张。

| 聚合现象 | 当前判据及处理方向 |
| --- | --- |
| 跨模型insufficient credits | 明确余额文案支持B；不要把所有额外ID自动归到账号851，需逐条核对账号。与对应账号所有者合并处理余额 |
| 404/No endpoints found、embedding/蛋白/解析等模型 | 核对实时目录、base URL、任务端点和模型输入能力；配置/上游不支持为B，若平台错误路由需证据后改A。目录合成已在基线 |
| 429、503/temporarily unavailable、403/access forbidden | 优先核验供应商额度、容量及访问权限；单凭归一化文本不能断定网关调度遗漏 |
| Upstream request failed、HTTP/2 stream failed、空api_error | C方向：取原始Forward错误、unwrap链、完整响应及请求/取消时序，不能据泛化消息修代码 |
| nemotron-parse拒绝字符串、DeepSeek Web拒绝max_tokens、decision-model URL错误 | 对比入站与转发请求；客户端或通用测试不符合能力为B，网关引入错误为A。目前本批没有相应完整请求体 |

## 验证记录

- 只读本地分支报告核对：e6095d8ab029覆盖本批8/8 ID；当前分支为ops-fix/df1ce4bf89e4；目录合成提交为当前基线祖先。
- 现有边界测试命令：`cd backend && GOMAXPROCS=2 go test -tags unit -p 1 ./internal/service ./internal/handler -run 'TestHandleErrorResponse_Deterministic400|TestHandleErrorResponse_NonDeterministicStatusesKeepGeneric502|TestFailoverClientGone|TestHandleFailoverError_ContextCanceled' -count=1`。PASS：service 0.251s、handler 0.092s；命令退出码0。
- 提交前验证报告逐条表8行、8个唯一ID、A0/B7/C1；验证用户指定路径与worktree报告字节一致，并运行 `git diff --cached --check`。PASS：8个唯一ID、A0/B7/C1；双路径内容一致；暂存差异无空白错误。

## 交付

报告在当前worktree `tools/ops-auto-delegate/reports/df1ce4bf89e4.md` 强制加入版本控制并提交。同内容写入用户指定共享报告路径；仅写该报告文件，不操作共享检出的Git状态。无代码部署理由。下一步由账号所有者处理B清单，日志所有者提供Q1证据；合并或发布由用户决定，若后续需部署验证，须明确授权后走TeamCity `sub2api / Deploy 252 Cluster` 或指定cluster脚本。
