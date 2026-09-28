//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestQoderAccountTestUsesOAuthTokenAndSelectedMode(t *testing.T) {
	for _, mode := range []string{AccountTestModeQoderCommitMessage, AccountTestModeDefault} {
		t.Run(mode, func(t *testing.T) {
			account := &Account{
				ID: 855, Platform: PlatformQoder, Type: AccountTypeOAuth, Concurrency: 1,
				Credentials: map[string]any{
					"access_token": "device-token", "api_protocol": APIProtocolChatCompletions,
					"model_mapping": map[string]any{"qwen3.8-max": "qmodel_38max"},
				},
			}
			svc, upstream := adaptiveCNAccountTestService(account, adaptiveCNChatTestResponse())
			ctx, recorder := newTestContext()
			err := svc.TestAccountConnection(ctx, account.ID, "qwen3.8-max", "", mode)
			require.NoError(t, err)
			require.Len(t, upstream.requests, 1)
			req := upstream.requests[0]
			require.Equal(t, QoderChatCompletionsURL(), req.URL.String())
			require.Equal(t, "Bearer device-token", req.Header.Get("Authorization"))
			require.Equal(t, "qmodel_38max", gjson.GetBytes(upstream.lastBody, "model").String())
			require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
			if mode == AccountTestModeQoderCommitMessage {
				require.NotEmpty(t, req.Header.Get("X-Request-ID"))
				require.NotEmpty(t, req.Header.Get("X-Session-ID"))
				require.Equal(t, "qoder-ide", gjson.GetBytes(upstream.lastBody, "metadata.context.client_type").String())
				require.Contains(t, gjson.GetBytes(upstream.lastBody, "messages.1.content").String(), QoderCommitMessageSampleDiff)
			}
		})
	}
}

func TestQoderAccountTestSurfacesUpstreamQuotaError(t *testing.T) {
	account := &Account{ID: 855, Platform: PlatformQoder, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "device-token"}}
	svc, upstream := adaptiveCNAccountTestService(account, newJSONResponse(402, `{"code":116,"error":"quota exceeded"}`))
	ctx, recorder := newTestContext()
	err := svc.TestAccountConnection(ctx, account.ID, "qmodel_38max", "", AccountTestModeQoderCommitMessage)
	require.Error(t, err)
	require.Len(t, upstream.requests, 1)
	require.Contains(t, recorder.Body.String(), "returned 402")
	require.Contains(t, recorder.Body.String(), "quota exceeded")
	require.NotContains(t, recorder.Body.String(), "No API key available")
	require.NotContains(t, recorder.Body.String(), `"type":"test_complete"`)
}
