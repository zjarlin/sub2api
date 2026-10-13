package handler

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestDeriveOpenAIForwardAttemptBody_AccountChangeStripsEncryptedReasoning 覆盖
// 方案 A 的核心语义：OpenAI 把 reasoning.encrypted_content 绑定在签发它的上游
// 账号/组织上，因此只要本次尝试落到的账号不是密文所有者（无论双方是否同为
// passthrough、是否同模型），就必须整块剔除这批密文，否则上游必然以
// invalid_encrypted_content 拒绝整包。
func TestDeriveOpenAIForwardAttemptBody_AccountChangeStripsEncryptedReasoning(t *testing.T) {
	h := &OpenAIGatewayHandler{}
	canonical := []byte(kiroReasoningCanonicalBody)

	t.Run("same_family_account_switch_strips", func(t *testing.T) {
		// 两个非 passthrough 账号之间换号：旧逻辑保留密文（同族），新逻辑必须剥离。
		state := &openAIPassthroughFailoverState{}
		first := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(100, false), state)
		require.Equal(t, 1, reasoningItemCount(t, first), "owning account must keep the reasoning item")

		second := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(101, false), state)
		require.Equal(t, 0, reasoningItemCount(t, second), "switching accounts must drop the ciphertext")
		require.NotContains(t, string(second), "ENC_BLOB")
	})

	t.Run("passthrough_family_account_switch_strips", func(t *testing.T) {
		// 两个 passthrough 账号之间换号：旧逻辑在 currentPassthrough 为真时提前返回，
		// 完全不做剥离，是漏判点之一。
		state := &openAIPassthroughFailoverState{}
		first := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(110, true), state)
		require.Equal(t, 1, reasoningItemCount(t, first))

		second := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(111, true), state)
		require.Equal(t, 0, reasoningItemCount(t, second), "passthrough-to-passthrough switch must drop the ciphertext")
		require.NotContains(t, string(second), "ENC_BLOB")
	})

	t.Run("sticky_escape_seeded_owner_strips", func(t *testing.T) {
		// 粘性绑定（上一轮签发密文的账号）为 200，本轮因粘性逃逸调度到 201：
		// 即使这是本请求内第一次尝试，也必须剥离。
		state := &openAIPassthroughFailoverState{reasoningSourceAccountID: 200}
		attempt := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(201, false), state)
		require.Equal(t, 0, reasoningItemCount(t, attempt), "sticky escape onto another account must drop the ciphertext")
		require.NotContains(t, string(attempt), "ENC_BLOB")
	})

	t.Run("sticky_hit_same_account_preserves", func(t *testing.T) {
		// 命中同一账号：保留密文，保住推理连续性与前缀缓存。
		state := &openAIPassthroughFailoverState{reasoningSourceAccountID: 300}
		attempt := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(300, false), state)
		require.Equal(t, 1, reasoningItemCount(t, attempt), "same owning account must preserve reasoning")
		require.Equal(t, "ENC_BLOB", gjson.GetBytes(attempt, "input.1.encrypted_content").String())
	})

	t.Run("unknown_owner_first_attempt_preserves", func(t *testing.T) {
		// 无粘性绑定且本请求尚未尝试任何账号：所有者未知，首个账号即接手，
		// 不应剥离（保留连续性，失败交由响应式恢复兜底）。
		state := &openAIPassthroughFailoverState{}
		attempt := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(400, false), state)
		require.Equal(t, 1, reasoningItemCount(t, attempt))
	})

	t.Run("canonical_body_never_mutated", func(t *testing.T) {
		state := &openAIPassthroughFailoverState{}
		_ = h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(500, false), state)
		_ = h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(501, false), state)
		require.JSONEq(t, kiroReasoningCanonicalBody, string(canonical),
			"canonical forwardBody must never be mutated")
	})
}

// TestDeriveOpenAIForwardAttemptBody_CompactionStripsOnAccountChange 覆盖
// compaction 密文：它与 reasoning.encrypted_content 一样绑定签发账号，换号也必须剥离。
func TestDeriveOpenAIForwardAttemptBody_CompactionStripsOnAccountChange(t *testing.T) {
	h := &OpenAIGatewayHandler{}
	canonical := []byte(`{"model":"gpt-5.1","stream":false,"input":[` +
		`{"type":"message","role":"user","content":"hello"},` +
		`{"id":"cmp_1","type":"compaction","encrypted_content":"CMP_BLOB"}` +
		`]}`)

	state := &openAIPassthroughFailoverState{}
	first := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(600, false), state)
	require.Equal(t, int64(2), gjson.GetBytes(first, "input.#").Int(), "owning account keeps both items")

	second := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(601, false), state)
	require.NotContains(t, string(second), "CMP_BLOB")
	require.Equal(t, int64(0), gjson.GetBytes(second, `input.#(type=="compaction")`).Int())
}

// TestDeriveOpenAIForwardAttemptBody_ModelFallbackReseedsOwner 覆盖降级路径：
// 模型降级时 handler 会重置所有者，降级后首个账号接手并触发剥离。
func TestDeriveOpenAIForwardAttemptBody_ModelFallbackReseedsOwner(t *testing.T) {
	h := &OpenAIGatewayHandler{}
	canonical := []byte(kiroReasoningCanonicalBody)

	state := &openAIPassthroughFailoverState{reasoningSourceAccountID: 700}
	// 模拟降级：handler 侧把 modelFallback 置真并重置所有者。
	state.modelFallback = true
	state.reasoningSourceAccountID = 0

	attempt := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(701, true), state)
	require.Equal(t, 0, reasoningItemCount(t, attempt), "model fallback must strip the ciphertext")
	require.Equal(t, int64(701), state.reasoningSourceAccountID, "fallback attempt becomes the new owner")
}

// TestDeriveOpenAIForwardAttemptBody_RetrySameAccountKeepsOwnership 确认同账号重试
// 不被误剥离（避免把"换号剥离"退化成"每次都剥离"）。
func TestDeriveOpenAIForwardAttemptBody_RetrySameAccountKeepsOwnership(t *testing.T) {
	h := &OpenAIGatewayHandler{}
	canonical := []byte(kiroReasoningCanonicalBody)
	state := &openAIPassthroughFailoverState{}

	account := newOpenAIPassthroughAccount(800, false)
	first := h.deriveOpenAIForwardAttemptBody(nil, canonical, account, state)
	retry := h.deriveOpenAIForwardAttemptBody(nil, canonical, account, state)
	require.Equal(t, 1, reasoningItemCount(t, first))
	require.Equal(t, 1, reasoningItemCount(t, retry), "same-account retry must preserve reasoning")
	require.JSONEq(t, string(first), string(retry))
}
