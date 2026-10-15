package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOpenAIUpstreamDegenerationErrorContract 固化三处耦合：
//  1. error 文本必须以 OpenAIUpstreamDegenerationCode 开头——handler 的
//     openAIForwardErrorAlreadyCommunicated 白名单按该前缀判断终止事件已写出；
//  2. 账号健康熔断器必须把它判为可归因失败（否则退化在账号维度完全不可见）；
//  3. 状态码 502，与 ops_error_logs 里该事件的 status_code 口径一致。
func TestOpenAIUpstreamDegenerationErrorContract(t *testing.T) {
	err := newOpenAIUpstreamDegenerationError("Upstream output degenerated into a repeated loop")

	require.Equal(t,
		"upstream_degenerate_repetition: Upstream output degenerated into a repeated loop",
		err.Error())
	require.True(t, strings.HasPrefix(err.Error(), OpenAIUpstreamDegenerationCode+":"))

	status, body, eligible := classifyOpenAIAPIKeyHealthFailure(err)
	require.True(t, eligible, "退化熔断必须计入账号健康，否则账号不会被降权")
	require.Equal(t, 502, status)
	require.Equal(t, "Upstream output degenerated into a repeated loop", string(body))
}

// TestOpenAIUpstreamDegenerationErrorIsNotFailover 说明为什么不能复用它当
// UpstreamFailoverError：熔断发生在流中途，语义输出已写给客户端，无法透明换号；
// 一旦被 handler 当成 failover，客户端已看到的错误码会被通用文案覆盖。
func TestOpenAIUpstreamDegenerationErrorIsNotFailover(t *testing.T) {
	err := newOpenAIUpstreamDegenerationError("boom")

	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover), "退化熔断不得伪装成可 failover 错误")
}

// TestRecordDegenerationAbortBlocksAccountModel 覆盖账号+模型级短期隔离：
// 单次熔断只累计 streak（不立刻拉黑，避免偶发抖动误伤），连续 3 次后调度必须
// 跳过该账号+模型组合，从而不再反复复读。
func TestRecordDegenerationAbortBlocksAccountModel(t *testing.T) {
	account := &Account{
		ID:       42,
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "sk-test",
		},
	}
	// 传入的是"请求模型"（与 Forward 里的 originalModel 同源），由
	// recordDegenerationAbort 内部换算成与调度侧一致的 canonical 键。
	const requestedModel = "deepseek-v4.1-flash"
	require.NotEmpty(t, canonicalOpenAIAccountSchedulingModel(account, requestedModel))

	svc := &OpenAIGatewayService{}
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, requestedModel), "未发生熔断时不应阻塞")

	svc.recordDegenerationAbort(account, requestedModel)
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, requestedModel),
		"首次熔断只累计 streak，不立即阻塞")

	svc.recordDegenerationAbort(account, requestedModel)
	svc.recordDegenerationAbort(account, requestedModel)
	require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(account, requestedModel),
		"连续 3 次熔断后必须跳过该账号+模型")

	// 隔离是账号+模型维度的：同账号的其它模型不受影响。
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.1-codex"))
}

// TestRecordDegenerationAbortNilSafety 保证异常入参不会 panic（上游字段缺失时
// account 可能为 nil）。
func TestRecordDegenerationAbortNilSafety(t *testing.T) {
	svc := &OpenAIGatewayService{}
	require.NotPanics(t, func() {
		svc.recordDegenerationAbort(nil, "deepseek-v4.1-flash")
		(&OpenAIGatewayService{}).recordDegenerationAbort(&Account{ID: 1}, "")
	})
}
