package service

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// 上游「额度/余额耗尽」的自由文本自动禁用。
//
// 背景：部分 OpenAI 兼容供应商（如 CommandCode）在钱包余额为 0 时返回
//
//	HTTP 400 {"error":{"code":"BAD_REQUEST","type":"invalid_request_error",
//	                   "message":"You have insufficient credits to make this request. ..."}}
//
// 它把「账号没额度」伪装成「客户端请求非法」：code 是 BAD_REQUEST，status 是 400。
// 网关既有的结构化判定（isOpenAIUpstreamAccessStateCode）只认 insufficient_quota /
// insufficient_user_quota 这类 code，且明确拒绝采信自由文本（防止回显用户输入误封账号），
// 而 400 也不在 shouldFailoverUpstreamError 的可切换状态码里。结果是这类错误既不会
// 隔离账号、也不会换号：实测 14 小时内 2 个空钱包账号产出 142 条错误、横跨 62 个模型。
//
// 这里提供一条「内置的临时不可调度规则」，复用已有的 temp_unschedulable 链路
// （匹配 -> SetModelRateLimit(account,model) -> 换号）。它与账号手工配置的
// temp_unschedulable_rules 走同一套机制，区别只在于：内置规则默认启用、不要求
// 账号打开 temp_unschedulable_enabled、无需管理员逐账号配置。
//
// 安全边界（重要）：
//   - 只在 400 上判定；402/403/429/5xx 全部保留既有语义，绝不由本规则接管；
//   - 只在**结构化字段**（error.code / error.type / error.message 等）里匹配，
//     绝不扫描整段 JSON body，避免命中回显的请求内容（既有测试
//     TestQuotaClassificationDoesNotConfuseConcurrencyOrEchoedInput 的约束）；
//   - 采用组合关键词而非单个词，降低误伤（如 "insufficient credits" 而不是裸 "insufficient"）；
//   - 只做**临时**不可调度（可自愈），不调用 handleAuthError，绝不永久禁用账号。
const (
	// upstreamCreditsExhaustedDuration 是额度耗尽后的冷却时长。额度恢复需要人工充值，
	// 因此取一个较长的值避免网关持续空转；但仍是「临时」而非永久。
	upstreamCreditsExhaustedDuration = 30 * time.Minute
	// upstreamCreditsExhaustedReason 是写入 model_rate_limits 的稳定原因标识，
	// 便于面板/日志区分「余额耗尽」与其它临时不可调度。
	upstreamCreditsExhaustedReason = "upstream_credits_exhausted"
	// upstreamCreditsExhaustedRuleIndex 用负数标识内置规则，与账号自定义规则的
	// 非负下标区分开，便于排查时知道命中的是内置规则。
	upstreamCreditsExhaustedRuleIndex = -1
)

// upstreamCreditsExhaustedStatuses 限定可触发内置额度规则的上游状态码。
//
// 有意只取 400：402（Payment required）与 403（含余额文案）已经由既有
// HandleUpstreamError 处理为「停止调度 / 永久禁用」，429/5xx 也有各自的语义。
// 本规则只填补唯一缺口——供应商把额度耗尽伪装成 400 invalid_request_error 自由文本。
// 收窄到 400 可证明不会改变任何既有状态码的行为。
var upstreamCreditsExhaustedStatuses = map[int]struct{}{
	http.StatusBadRequest: {},
}

// upstreamCreditsExhaustedKeywords 是内置规则的匹配词。匹配发生在已抽取的
// 结构化字段文本上（小写），因此这里统一用小写。
// 有意不收录 insufficient_quota / insufficient_user_quota 这类「结构化 code」：
// 它们已由既有 isOpenAIHTTPUpstreamAccessStateError 处理，而作为自由文本出现在
// 形如 "Unknown parameter: insufficient_quota" 的消息里时属于客户端参数错误，
// 收录会导致误判（既有 TestQuotaClassificationDoesNotConfuseConcurrencyOrEchoedInput
// 的约束）。本规则只负责自由文本形态的额度文案。
var upstreamCreditsExhaustedKeywords = []string{
	"insufficient credits",
	"insufficient credit",
	"no credits remaining",
	"no credit remaining",
	"out of credits",
	"credits exhausted",
	"credit balance is exhausted",
	"credit balance exhausted",
	"purchase more credits",
	"not enough credits",
	"insufficient balance",
	"exceeded your current quota",
	"billing hard limit",
	"余额不足",
	"额度不足",
	"欠费",
	"需要充值",
}

// builtinUpstreamCreditsExhaustedRule 暴露内置规则，供匹配与测试复用。
func builtinUpstreamCreditsExhaustedRule() TempUnschedulableRule {
	return TempUnschedulableRule{
		ErrorCode:       0, // 由 upstreamCreditsExhaustedStatuses 单独判定，不使用单一 error_code
		Keywords:        upstreamCreditsExhaustedKeywords,
		DurationMinutes: int(upstreamCreditsExhaustedDuration / time.Minute),
		Description:     "内置：上游账号额度/余额耗尽（自由文本），临时不可调度并换号",
	}
}

// upstreamCreditsExhaustedText 从上游错误体里抽取**仅结构化字段**的文本，
// 有意不触碰 echo / 请求回显等其它字段。
func upstreamCreditsExhaustedText(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	if !json.Valid(body) {
		// 纯文本上游错误（非 JSON）允许整体匹配：这类响应没有可回显的结构化字段，
		// 出现额度文案即代表账号状态。JSON 体绝不整体扫描。
		return strings.ToLower(strings.TrimSpace(string(body)))
	}
	var parts []string
	for _, path := range []string{
		"error.code",
		"error.type",
		"error.message",
		"response.error.code",
		"response.error.type",
		"response.error.message",
		"detail",
		"message",
	} {
		if v := strings.TrimSpace(gjson.GetBytes(body, path).String()); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.ToLower(strings.Join(parts, " "))
}

// isUpstreamCreditsExhaustedError 判断上游响应是否明确表示「账号额度/余额耗尽」。
// 必须在 JSON 场景下由结构化字段支撑，且只在 400 上成立（见 upstreamCreditsExhaustedStatuses）。
func isUpstreamCreditsExhaustedError(statusCode int, body []byte) bool {
	if _, ok := upstreamCreditsExhaustedStatuses[statusCode]; !ok {
		return false
	}
	text := upstreamCreditsExhaustedText(body)
	if text == "" {
		return false
	}
	return textContainsAnyUpstreamCreditKeyword(text)
}

func textContainsAnyUpstreamCreditKeyword(text string) bool {
	for _, kw := range upstreamCreditsExhaustedKeywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}
