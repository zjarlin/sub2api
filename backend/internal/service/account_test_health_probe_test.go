//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type healthProbeRecordingRepo struct {
	AccountRepository
	successes []ModelHealthObservation
	failures  []ModelHealthObservation
}

func (r *healthProbeRecordingRepo) RecordAccountModelHealthSuccess(_ context.Context, id int64, model string, at time.Time) error {
	r.successes = append(r.successes, ModelHealthObservation{AccountID: id, Model: model, CheckedAt: at})
	return nil
}

func (r *healthProbeRecordingRepo) RecordAccountModelHealthFailure(_ context.Context, id int64, model string, at time.Time) error {
	r.failures = append(r.failures, ModelHealthObservation{AccountID: id, Model: model, CheckedAt: at})
	return nil
}

func TestAccountHealthProbeUsesOneMinimalNativeRequest(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, model, promptPath, budgetPath string
		response                                      func() *http.Response
	}{
		{"chat", APIProtocolChatCompletions, "deepseek-v4-flash", "messages.0.content", "max_tokens", adaptiveCNChatTestResponse},
		{"chat-reasoning", APIProtocolChatCompletions, "o3-mini", "messages.0.content", "max_completion_tokens", adaptiveCNChatTestResponse},
		{"anthropic", APIProtocolAnthropic, "minimax-m3", "messages.0.content", "max_tokens", adaptiveCNAnthropicTestResponse},
		{"responses", APIProtocolResponses, "grok-4", "input.0.content.0.text", "max_output_tokens", adaptiveCNResponsesTestResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := openCodeGoTestAccount(901)
			account.Credentials["api_protocol"] = tc.protocol
			if tc.protocol == APIProtocolAnthropic {
				account.Credentials["base_url"] = "https://opencode.ai/zen/go"
			}
			svc, upstream := adaptiveCNAccountTestService(account, tc.response())
			repo := &healthProbeRecordingRepo{AccountRepository: svc.accountRepo}
			svc.accountRepo = repo

			result, err := svc.RunTestBackground(context.Background(), account.ID, tc.model, AccountTestOptions{HealthProbe: true})

			require.NoError(t, err)
			require.Equal(t, "success", result.Status)
			require.Len(t, upstream.requests, 1)
			body := upstream.bodies[0]
			require.Equal(t, accountHealthProbePrompt, gjson.GetBytes(body, tc.promptPath).String())
			require.EqualValues(t, 256, gjson.GetBytes(body, tc.budgetPath).Int())
			if tc.budgetPath == "max_completion_tokens" {
				require.False(t, gjson.GetBytes(body, "max_tokens").Exists())
			}
			for _, field := range []string{"system", "instructions", "metadata", "tools", "temperature"} {
				require.False(t, gjson.GetBytes(body, field).Exists(), field)
			}
			require.Len(t, repo.successes, 1)
			require.Equal(t, account.ID, repo.successes[0].AccountID)
			require.Equal(t, tc.model, repo.successes[0].Model)
			require.Empty(t, repo.failures)
		})
	}
}

func TestAccountHealthProbeAdaptiveDoesNotRepeatOtherProtocols(t *testing.T) {
	account := adaptiveCNAccountTestAccount(902, PlatformDeepseek)
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())

	result, err := svc.RunTestBackground(context.Background(), account.ID, "deepseek-v4-flash", AccountTestOptions{HealthProbe: true})

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "http://chat.example/v1/chat/completions", upstream.requests[0].URL.String())
}

func TestAccountHealthProbeDoesNotMarkEmptyOrTruncatedStreamsHealthy(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, body string
	}{
		{"chat-empty", APIProtocolChatCompletions, "data: [DONE]\n\n"},
		{"anthropic-empty", APIProtocolAnthropic, "data: {\"type\":\"message_stop\"}\n\n"},
		{"responses-empty", APIProtocolResponses, "data: {\"type\":\"response.completed\"}\n\n"},
		{"anthropic-truncated", APIProtocolAnthropic, "data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"OK\"}}\n\n"},
		{"chat-error", APIProtocolChatCompletions, "data: {\"error\":{\"message\":\"quota exceeded\"}}\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account := openCodeGoTestAccount(903)
			account.Credentials["api_protocol"] = tc.protocol
			if tc.protocol == APIProtocolAnthropic {
				account.Credentials["base_url"] = "https://opencode.ai/zen/go"
			}
			response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body))}
			svc, upstream := adaptiveCNAccountTestService(account, response)
			repo := &healthProbeRecordingRepo{AccountRepository: svc.accountRepo}
			svc.accountRepo = repo

			result, err := svc.RunTestBackground(context.Background(), account.ID, "test-model", AccountTestOptions{HealthProbe: true})

			require.NoError(t, err)
			require.Equal(t, "failed", result.Status)
			require.Len(t, upstream.requests, 1)
			require.Empty(t, repo.successes)
			require.Len(t, repo.failures, 1)
		})
	}
}

func TestAccountHealthProbeVibexOmitsUnsupportedBudget(t *testing.T) {
	account := adaptiveCNAccountTestAccount(904, PlatformVibex)
	account.Credentials["api_protocol"] = APIProtocolChatCompletions
	account.Credentials["base_url"] = "http://chat.example/v1"
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())

	result, err := svc.RunTestBackground(context.Background(), account.ID, "claude-sonnet-4-6", AccountTestOptions{HealthProbe: true})

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, accountHealthProbePrompt, gjson.GetBytes(upstream.bodies[0], "messages.0.content").String())
	require.False(t, gjson.GetBytes(upstream.bodies[0], "max_tokens").Exists())
	require.False(t, gjson.GetBytes(upstream.bodies[0], "max_completion_tokens").Exists())
}

func TestAccountHealthProbeGeminiUsesBoundedTextRequest(t *testing.T) {
	account := adaptiveCNAccountTestAccount(905, PlatformGemini)
	response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"OK\"}]},\"finishReason\":\"STOP\"}]}\n\n"))}
	svc, upstream := adaptiveCNAccountTestService(account, response)

	result, err := svc.RunTestBackground(context.Background(), account.ID, "gemini-2.5-flash", AccountTestOptions{HealthProbe: true})

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Len(t, upstream.requests, 1)
	body := upstream.bodies[0]
	require.Equal(t, accountHealthProbePrompt, gjson.GetBytes(body, "contents.0.parts.0.text").String())
	require.EqualValues(t, 256, gjson.GetBytes(body, "generationConfig.maxOutputTokens").Int())
	require.False(t, gjson.GetBytes(body, "systemInstruction").Exists())
}

func TestAccountHealthProbeOAuthRetainsNativeRequestContract(t *testing.T) {
	account := adaptiveCNAccountTestAccount(906, PlatformOpenAI)
	account.Type = AccountTypeOAuth
	account.Credentials["access_token"] = "fixture-oauth"
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNResponsesTestResponse())

	result, err := svc.RunTestBackground(context.Background(), account.ID, "gpt-5.4", AccountTestOptions{HealthProbe: true})

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, chatgptCodexAPIURL, upstream.requests[0].URL.String())
	body := upstream.bodies[0]
	require.True(t, gjson.GetBytes(body, "input").IsArray())
	require.Equal(t, accountHealthProbePrompt, gjson.GetBytes(body, "input.0.content.0.text").String())
	require.NotEmpty(t, gjson.GetBytes(body, "instructions").String())
	require.False(t, gjson.GetBytes(body, "store").Bool())
	require.False(t, gjson.GetBytes(body, "max_output_tokens").Exists())
}

func TestAccountHealthProbeGrokRetainsSupportedFields(t *testing.T) {
	account := adaptiveCNAccountTestAccount(907, PlatformGrok)
	account.Type = AccountTypeOAuth
	account.Credentials["access_token"] = "fixture-grok"
	account.Credentials["refresh_token"] = "fixture-refresh"
	account.Credentials["expires_at"] = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNResponsesTestResponse())
	svc.grokTokenProvider = NewGrokTokenProvider(svc.accountRepo, nil)

	result, err := svc.RunTestBackground(context.Background(), account.ID, "grok-4.3", AccountTestOptions{HealthProbe: true})

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Len(t, upstream.requests, 1)
	body := upstream.bodies[0]
	require.Equal(t, accountHealthProbePrompt, gjson.GetBytes(body, "input").String())
	require.False(t, gjson.GetBytes(body, "max_output_tokens").Exists())
	require.False(t, gjson.GetBytes(body, "instructions").Exists())
}

func TestAccountHealthProbeGeminiRejectsThoughtOnlyAndTruncatedOutput(t *testing.T) {
	for _, body := range []string{
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"thinking\",\"thought\":true}]},\"finishReason\":\"MAX_TOKENS\"}]}\n\n",
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"OK\"}]}}]}\n\n",
	} {
		account := adaptiveCNAccountTestAccount(908, PlatformGemini)
		response := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
		svc, _ := adaptiveCNAccountTestService(account, response)
		repo := &healthProbeRecordingRepo{AccountRepository: svc.accountRepo}
		svc.accountRepo = repo

		result, err := svc.RunTestBackground(context.Background(), account.ID, "gemini-2.5-flash", AccountTestOptions{HealthProbe: true})

		require.NoError(t, err)
		require.Equal(t, "failed", result.Status)
		require.Empty(t, repo.successes)
		require.Len(t, repo.failures, 1)
	}
}
