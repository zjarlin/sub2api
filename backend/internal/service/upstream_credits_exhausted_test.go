//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// 真实线上样本：CommandCode 钱包耗尽时返回 400 + invalid_request_error 自由文本。
const upstreamCreditsExhaustedSampleBody = `{"error":{"code":"BAD_REQUEST","message":"You have insufficient credits to make this request. Please purchase more credits to continue using the service.","type":"invalid_request_error"}}`

func TestIsUpstreamCreditsExhaustedError_RecognizesBadRequestQuotaText(t *testing.T) {
	// 唯一缺口：400 伪装成 invalid_request_error 的自由文本额度文案。
	require.True(t, isUpstreamCreditsExhaustedError(http.StatusBadRequest, []byte(upstreamCreditsExhaustedSampleBody)))
}

// 锁定「不改变既有语义」的契约：402/403/429/5xx 一律不由内置规则接管。
func TestIsUpstreamCreditsExhaustedError_DoesNotClaimAlreadyHandledStatuses(t *testing.T) {
	for _, status := range []int{
		http.StatusPaymentRequired,
		http.StatusForbidden,
		http.StatusUnauthorized,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
		http.StatusBadGateway,
		529,
	} {
		require.False(t, isUpstreamCreditsExhaustedError(status, []byte(upstreamCreditsExhaustedSampleBody)),
			"status %d must keep its existing handling", status)
	}
}

func TestIsUpstreamCreditsExhaustedError_IgnoresNonQuotaStatuses(t *testing.T) {
	// 5xx / 429 由既有逻辑处理，内置规则不得介入，避免抢占既有语义。
	for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusTooManyRequests, 529, http.StatusForbidden} {
		require.False(t, isUpstreamCreditsExhaustedError(status, []byte(upstreamCreditsExhaustedSampleBody)),
			"status %d must not be claimed by the credits rule", status)
	}
}

func TestIsUpstreamCreditsExhaustedError_DoesNotScanEchoedJSON(t *testing.T) {
	// 关键回归约束：JSON 体只在结构化字段里匹配，绝不整体扫描，
	// 否则回显的请求内容会把正常请求误判为额度耗尽。
	cases := []string{
		`{"error":{"code":"invalid_request_error","message":"Invalid input"},"echo":{"prompt":"You have insufficient credits"}}`,
		`{"error":{"type":"invalid_request_error","code":"unknown_parameter","message":"Unknown parameter: insufficient_quota"}}`,
		`{"error":{"message":"Invalid input"},"metadata":{"note":"out of credits"}}`,
	}
	for _, body := range cases {
		require.False(t, isUpstreamCreditsExhaustedError(http.StatusBadRequest, []byte(body)), body)
	}
}

func TestIsUpstreamCreditsExhaustedError_DoesNotMatchUnrelatedInsufficientText(t *testing.T) {
	// "insufficient" 单独出现（例如工具消息缺失）不得被误判为额度问题。
	body := []byte(`{"error":{"code":"InvalidParameter","message":"An assistant message with 'tool_calls' must be followed by tool messages (insufficient tool messages following tool_calls message)","type":"BadRequest"}}`)
	require.False(t, isUpstreamCreditsExhaustedError(http.StatusBadRequest, body))
}

func TestIsUpstreamCreditsExhaustedError_MatchesPlainTextBody(t *testing.T) {
	// 非 JSON 上游错误没有可回显的结构化字段，允许整体匹配。
	require.True(t, isUpstreamCreditsExhaustedError(http.StatusBadRequest, []byte("You have insufficient credits")))
}

func TestBuiltinTempUnschedulableRuleMatch_RequiresOpenAICompatibleAccount(t *testing.T) {
	body := []byte(upstreamCreditsExhaustedSampleBody)
	_, ok := builtinTempUnschedulableRuleMatch(&Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}, http.StatusBadRequest, body)
	require.False(t, ok, "non OpenAI-compatible accounts must not use the builtin credits rule")

	_, ok = builtinTempUnschedulableRuleMatch(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, http.StatusBadRequest, body)
	require.True(t, ok)
}

// 内置规则必须绕过账号开关：空钱包账号通常没有配置 temp_unschedulable_enabled。
func TestHandleUpstreamCreditsExhausted_AppliesWithoutAccountOptIn(t *testing.T) {
	repo := &modelNotFoundAccountRepoStub{}
	svc := &RateLimitService{accountRepo: repo}
	account := &Account{
		ID:          851,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		// 有意不设置 temp_unschedulable_enabled / rules
	}

	handled := svc.HandleUpstreamCreditsExhausted(
		context.Background(),
		account,
		http.StatusBadRequest,
		[]byte(upstreamCreditsExhaustedSampleBody),
		"gpt-5.6-terra",
	)

	require.True(t, handled)
	require.Zero(t, repo.tempCalls, "额度耗尽必须做模型级临时不可调度，而不是账号级永久禁用")
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "gpt-5.6-terra", repo.modelRateLimitCalls[0].scope)
	// SetModelRateLimit 的 reason 参数承载完整 TempUnschedState JSON，其中
	// rule_index=-1 标识内置规则、matched_keyword 记录命中的自由文本。
	require.Contains(t, repo.modelRateLimitCalls[0].reason, `"rule_index":-1`)
	require.Contains(t, repo.modelRateLimitCalls[0].reason, `"matched_keyword":"insufficient credits"`)
}

func TestHandleUpstreamCreditsExhausted_IgnoresUnrelatedErrors(t *testing.T) {
	repo := &modelNotFoundAccountRepoStub{}
	svc := &RateLimitService{accountRepo: repo}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive}

	handled := svc.HandleUpstreamCreditsExhausted(
		context.Background(),
		account,
		http.StatusBadRequest,
		[]byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: foo"}}`),
		"gpt-5.4",
	)

	require.False(t, handled)
	require.Empty(t, repo.modelRateLimitCalls)
}

// 内置规则在池模式下同样生效：额度耗尽与池模式的「同账号重试」语义无关。
func TestHandleUpstreamCreditsExhausted_AppliesInPoolMode(t *testing.T) {
	repo := &modelNotFoundAccountRepoStub{}
	svc := &RateLimitService{accountRepo: repo}
	account := &Account{
		ID:       1,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Status:   StatusActive,
		Credentials: map[string]any{
			"pool_mode": true,
		},
	}

	handled := svc.HandleUpstreamCreditsExhausted(
		context.Background(),
		account,
		http.StatusBadRequest,
		[]byte(upstreamCreditsExhaustedSampleBody),
		"gpt-5.4",
	)

	require.True(t, handled)
	require.Len(t, repo.modelRateLimitCalls, 1)
}

// tryTempUnschedulable 是公共链路：即便账号未开启开关，内置规则也应命中。
func TestTryTempUnschedulable_BuiltinCreditsRuleAppliesWithoutOptIn(t *testing.T) {
	repo := &modelNotFoundAccountRepoStub{}
	svc := &RateLimitService{accountRepo: repo}
	account := &Account{ID: 851, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive}

	handled := svc.tryTempUnschedulable(
		context.Background(),
		account,
		http.StatusBadRequest,
		[]byte(upstreamCreditsExhaustedSampleBody),
		"minimax-m2.5",
	)

	require.True(t, handled)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "minimax-m2.5", repo.modelRateLimitCalls[0].scope)
}

// 端到端：验证「决策 -> 隔离」整条链路的接线。
// 1) shouldFailoverOpenAIUpstreamResponse 对该 400 返回 true（触发换号）；
// 2) handleOpenAIAccountUpstreamError 记录**模型级**临时不可调度，而非永久禁用账号。
func TestCreditsExhausted_EndToEndFailsOverAndIsolatesAccountModel(t *testing.T) {
	body := []byte(upstreamCreditsExhaustedSampleBody)
	account := &Account{ID: 851, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	svc := &OpenAIGatewayService{}

	require.True(t, svc.shouldFailoverOpenAIUpstreamResponse(account, http.StatusBadRequest, "You have insufficient credits", body),
		"额度耗尽必须允许换号")
	require.True(t, shouldFailoverOpenAIPassthroughResponse(account, http.StatusBadRequest, body),
		"透传路径同样必须允许换号")

	repo := &modelNotFoundAccountRepoStub{}
	svcWithRepo := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
	disabled := svcWithRepo.handleOpenAIAccountUpstreamError(
		context.Background(), account, http.StatusBadRequest, http.Header{}, body, "gpt-5.6-terra",
	)
	require.True(t, disabled)
	require.Zero(t, repo.tempCalls, "不得把额度耗尽升级为账号级临时不可调度/永久禁用")
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "gpt-5.6-terra", repo.modelRateLimitCalls[0].scope)
}
