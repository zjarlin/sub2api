//go:build unit

package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestApplyModelSystemPromptAddsUserInputProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	policy := &service.ModelSystemPromptPolicy{Entries: []service.ModelSystemPromptEntry{
		{Model: "deepseek-v4.1-flash", Prompt: "Reply in Chinese."},
	}}
	c.Request = c.Request.WithContext(service.WithModelSystemPrompts(c.Request.Context(), policy))
	body := []byte(`{"model":"deepseek-v4.1-flash","instructions":"You are Codex.","input":[{"role":"user","content":"hi"}],"tools":[{"type":"function","name":"request_user_input"}]}`)

	updated := applyModelSystemPrompt(c, "deepseek-v4.1-flash", body)
	require.Contains(t, gjson.GetBytes(updated, "instructions").String(), "Reply in Chinese.")
	require.Contains(t, gjson.GetBytes(updated, "instructions").String(), "request-user-input-protocol")
}

func TestApplyModelSystemPromptDoesNotDuplicateUserInputProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	body := []byte(`{"model":"deepseek-v4.1-flash","instructions":"You are Codex.","input":[{"role":"user","content":"hi"}],"tools":[{"type":"function","name":"request_user_input"}]}`)

	first := applyModelSystemPrompt(c, "deepseek-v4.1-flash", body)
	second := applyModelSystemPrompt(c, "deepseek-v4.1-flash", first)
	require.Equal(t, 1, strings.Count(gjson.GetBytes(second, "instructions").String(), "request-user-input-protocol"))
}

func TestApplyModelSystemPromptSkipsGPTUserInputProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	body := []byte(`{"model":"gpt-6-astra","input":"hi","tools":[{"type":"function","name":"request_user_input"}]}`)

	updated := applyModelSystemPrompt(c, "gpt-6-astra", body)
	require.Equal(t, body, updated)
}
