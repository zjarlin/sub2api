package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSafeUpstreamURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"strips query", "https://api.anthropic.com/v1/messages?beta=true", "https://api.anthropic.com/v1/messages"},
		{"strips fragment", "https://api.openai.com/v1/responses#frag", "https://api.openai.com/v1/responses"},
		{"strips both", "https://host/path?token=secret#x", "https://host/path"},
		{"no query or fragment", "https://host/path", "https://host/path"},
		{"empty string", "", ""},
		{"whitespace only", "  ", ""},
		{"query before fragment", "https://h/p?a=1#f", "https://h/p"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, safeUpstreamURL(tt.input))
		})
	}
}

func TestAppendOpsUpstreamError_UsesRequestBodyBytesFromContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	setOpsUpstreamRequestBody(c, []byte(`{"model":"gpt-5"}`))
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Kind:    "http_error",
		Message: "upstream failed",
	})

	v, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := v.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, `{"model":"gpt-5"}`, events[0].UpstreamRequestBody)
}

func TestAppendOpsUpstreamError_UsesRequestBodyStringFromContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	c.Set(OpsUpstreamRequestBodyKey, `{"model":"gpt-4"}`)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Kind:    "request_error",
		Message: "dial timeout",
	})

	v, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := v.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, `{"model":"gpt-4"}`, events[0].UpstreamRequestBody)
}

func TestAppendOpsUpstreamError_BackfillsSelectedAccountSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	SetOpsSelectedAccount(c, 42, "pool-account-42", "openai")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Kind:    "http_error",
		Message: "upstream failed",
	})

	v, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := v.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, int64(42), events[0].AccountID)
	require.Equal(t, "pool-account-42", events[0].AccountName)
	require.Equal(t, "openai", events[0].Platform)
}

func TestDecorateScheduledAccountClientErrorJSONBody_Admin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set("user_role", RoleAdmin)
	SetOpsSelectedAccount(c, 42, "pool-account-42", PlatformOpenAI)

	body := []byte(`{"error":{"type":"api_error","message":"Service temporarily unavailable"}}`)
	patched := DecorateScheduledAccountClientErrorJSONBody(c, body)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(patched, &parsed))
	errorObj, ok := parsed["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Service temporarily unavailable [scheduled account: pool-account-42]", errorObj["message"])
}

func TestDecorateScheduledAccountClientErrorJSONBody_NonAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	SetOpsSelectedAccount(c, 42, "pool-account-42", PlatformOpenAI)

	body := []byte(`{"error":{"type":"api_error","message":"Service temporarily unavailable"}}`)
	patched := DecorateScheduledAccountClientErrorJSONBody(c, body)

	require.JSONEq(t, string(body), string(patched))
}

func TestAppendOpsUpstreamError_BackfillsModelDiagnostics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)

	ctx := context.WithValue(c.Request.Context(), ctxkey.Model, "gpt-5.5")
	c.Request = c.Request.WithContext(ctx)
	SetOpsModelDiagnostics(c, "gemini-3.5-flash", "gemini-2.5-pro")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Kind:               "http_error",
		UpstreamStatusCode: 429,
		Message:            "quota exhausted",
	})

	v, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := v.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "gpt-5.5", events[0].RequestedModel)
	require.Equal(t, "gemini-2.5-pro", events[0].MappedModel)
}

func TestAppendOpsUpstreamError_ExplicitModelDiagnosticsWin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	SetOpsModelDiagnostics(c, "gpt-5.5", "gemini-2.5-pro")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Kind:           "http_error",
		RequestedModel: "explicit-request",
		MappedModel:    "explicit-upstream",
		Message:        "quota exhausted",
	})

	v, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := v.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "explicit-request", events[0].RequestedModel)
	require.Equal(t, "explicit-upstream", events[0].MappedModel)
}
