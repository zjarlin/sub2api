//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func antigravityHealthProbeFixture(model, body string, status int, upstreamErr error) (*AntigravityGatewayService, *Account, *mockSmartRetryUpstream) {
	var response *http.Response
	if upstreamErr == nil {
		response = &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	}
	upstream := &mockSmartRetryUpstream{
		responses:  []*http.Response{response},
		errors:     []error{upstreamErr},
		repeatLast: true,
	}
	service := &AntigravityGatewayService{tokenProvider: &AntigravityTokenProvider{}, httpUpstream: upstream}
	account := &Account{
		ID: 987, Platform: PlatformAntigravity, Type: AccountTypeUpstream, Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "fixture-token", "project_id": "fixture-project",
			"model_mapping": map[string]any{model: model},
		},
		Extra: map[string]any{"allow_overages": true},
	}
	return service, account, upstream
}

func TestAntigravityHealthProbeUsesOneBoundedNativeRequest(t *testing.T) {
	for _, model := range []string{"gemini-2.5-flash", "claude-sonnet-4-5"} {
		t.Run(model, func(t *testing.T) {
			body := "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"OK\"}]},\"finishReason\":\"STOP\"}]}}\n\n"
			svc, account, upstream := antigravityHealthProbeFixture(model, body, http.StatusOK, nil)

			result, err := svc.TestConnection(context.Background(), account, model, true)

			require.NoError(t, err)
			require.Equal(t, "OK", result.Text)
			require.Equal(t, model, result.MappedModel)
			require.Len(t, upstream.calls, 1)
			request := upstream.requestBodies[0]
			require.Equal(t, "fixture-project", gjson.GetBytes(request, "project").String())
			require.Equal(t, model, gjson.GetBytes(request, "model").String())
			require.Equal(t, accountHealthProbePrompt, gjson.GetBytes(request, "request.contents.0.parts.0.text").String())
			require.EqualValues(t, accountHealthProbeMaxTokens, gjson.GetBytes(request, "request.generationConfig.maxOutputTokens").Int())
			require.True(t, gjson.GetBytes(request, "request.systemInstruction").Exists())
			require.NotContains(t, string(request), "enabledCreditTypes")
		})
	}
}

func TestAntigravityHealthProbeDoesNotRetryOrSpendCredits(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		err        error
	}{
		{"quota", `{"error":{"status":"RESOURCE_EXHAUSTED","message":"QUOTA_EXHAUSTED"}}`, http.StatusTooManyRequests, nil},
		{"capacity", `{"error":{"status":"UNAVAILABLE","message":"MODEL_CAPACITY_EXHAUSTED"}}`, http.StatusServiceUnavailable, nil},
		{"server", `{"error":{"status":"INTERNAL"}}`, http.StatusInternalServerError, nil},
		{"transport", "", 0, errors.New("transport reset")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := "gemini-2.5-flash"
			svc, account, upstream := antigravityHealthProbeFixture(model, tc.body, tc.status, tc.err)
			account.Type = AccountTypeOAuth
			account.Credentials["access_token"] = "fixture-token"

			result, err := svc.TestConnection(context.Background(), account, model, true)

			require.Error(t, err)
			require.Nil(t, result)
			require.Len(t, upstream.calls, 1)
			require.NotContains(t, string(upstream.requestBodies[0]), "enabledCreditTypes")
		})
	}
}

func TestAntigravityHealthProbeRejectsEmptyTruncatedAndFailedResponses(t *testing.T) {
	text := "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"OK\"}]}}]}}\n\n"
	completed := "data: {\"response\":{\"candidates\":[{\"finishReason\":\"STOP\"}]}}\n\n"
	for _, tc := range []struct {
		name, body, errorText string
	}{
		{"empty", completed, "no generated text"},
		{"truncated", text, "before completion"},
		{"done-only", text + "data: [DONE]\n\n", "before completion"},
		{"error-after-text", text + "data: {\"error\":{\"message\":\"quota exceeded\"}}\n\n", "quota exceeded"},
		{"error-after-completion", text + completed + "data: {\"response\":{\"error\":{\"message\":\"quota exceeded\"}}}\n\n", "quota exceeded"},
		{"thought-only", "data: {\"candidates\":[{\"content\":{\"parts\":[{\"thought\":true,\"text\":\"thinking\"}]},\"finishReason\":\"MAX_TOKENS\"}]}\n\n", "no generated text"},
		{"blocked", text + "data: {\"candidates\":[{\"finishReason\":\"SAFETY\"}]}\n\n", "SAFETY"},
		{"malformed", text + "data: {broken}\n\n", "invalid stream data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := "gemini-2.5-flash"
			svc, account, upstream := antigravityHealthProbeFixture(model, tc.body, http.StatusOK, nil)

			result, err := svc.TestConnection(context.Background(), account, model, true)

			require.ErrorContains(t, err, tc.errorText)
			require.Nil(t, result)
			require.Len(t, upstream.calls, 1)
		})
	}
}

func TestAntigravityHealthProbeAcceptsCompleteUnwrappedResponseWithoutTrailingNewline(t *testing.T) {
	text, err := parseAntigravityHealthProbeResponse([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"OK\"}]},\"finishReason\":\"MAX_TOKENS\"}]}"))
	require.NoError(t, err)
	require.Equal(t, "OK", text)
}

func TestAntigravityManualConnectionKeepsExistingProbe(t *testing.T) {
	model := "gemini-2.5-flash"
	body := "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}}\n\n"
	svc, account, upstream := antigravityHealthProbeFixture(model, body, http.StatusOK, nil)

	result, err := svc.TestConnection(context.Background(), account, model)

	require.NoError(t, err)
	require.Equal(t, "hello", result.Text)
	require.Len(t, upstream.calls, 1)
	bodyJSON := upstream.requestBodies[0]
	require.Equal(t, ".", gjson.GetBytes(bodyJSON, "request.contents.0.parts.0.text").String())
	require.EqualValues(t, 1, gjson.GetBytes(bodyJSON, "request.generationConfig.maxOutputTokens").Int())
}
