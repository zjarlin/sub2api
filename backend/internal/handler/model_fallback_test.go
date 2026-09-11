//go:build unit

package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type fallbackTestUpstream struct {
	service.HTTPUpstream
	models []string
	status int
}

func (u *fallbackTestUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	model := gjson.GetBytes(body, "model").String()
	u.models = append(u.models, model)
	status := http.StatusOK
	response := fmt.Sprintf(`{"id":"resp_test","object":"response","status":"completed","model":%q,"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`, model)
	if strings.Contains(req.URL.Path, "chat/completions") {
		response = fmt.Sprintf(`{"id":"chatcmpl_test","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`, model)
	}
	if model != "gpt-5.5" && u.status >= 400 {
		status = u.status
		response = `{"error":{"message":"provider unavailable","type":"upstream_error"}}`
	}
	contentType := "application/json"
	if status == http.StatusOK && gjson.GetBytes(body, "stream").Bool() {
		contentType = "text/event-stream"
		response = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":" + response + "}\n\n"
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}

func TestGPTModelFallbackHTTP(t *testing.T) {
	for _, tc := range []struct {
		endpoint string
		stream   bool
	}{{"responses", false}, {"responses", true}, {"chat/completions", false}, {"chat/completions", true}} {
		endpoint := tc.endpoint
		for _, status := range []int{0, http.StatusOK, http.StatusServiceUnavailable, http.StatusBadRequest} {
			t.Run(fmt.Sprintf("%s/%t/%d", endpoint, tc.stream, status), func(t *testing.T) {
				mapping := map[string]any{"gpt-5.5": "gpt-5.5"}
				if status != 0 {
					mapping["gpt-6-astra"] = "gpt-6-astra"
					mapping["gpt-5.6-sol"] = "gpt-5.6-sol"
				}
				extra := map[string]any{"openai_passthrough": true}
				if status == 0 {
					extra[service.UnsupportedModelsExtraKey] = map[string]any{
						"gpt-6-astra": map[string]any{"status_code": float64(404)},
						"gpt-5.6-sol": map[string]any{"status_code": float64(404)},
					}
				}
				repo := &grokCredentialHandlerRepo{accounts: []service.Account{{
					ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, Concurrency: 1,
					Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": mapping},
					Extra:       extra,
				}}}
				cfg := &config.Config{RunMode: config.RunModeSimple}
				cfg.Gateway.MaxAccountSwitches = 0
				billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				defer billing.Stop()
				upstream := &fallbackTestUpstream{status: status}
				gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
					service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
				cache := &concurrencyCacheMock{
					acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
					acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
				}
				h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(cache), billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
				h.maxAccountSwitches = 0
				key := &service.APIKey{ID: 2, User: &service.User{ID: 3, Status: service.StatusActive}, Group: &service.Group{Platform: service.PlatformOpenAI, Status: service.StatusActive}}
				router := gin.New()
				router.Use(func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyAPIKey), key)
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
				})
				router.POST("/responses", h.Responses)
				router.POST("/chat/completions", h.ChatCompletions)
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/"+endpoint, strings.NewReader(fmt.Sprintf(`{"model":"gpt-6-astra","input":"hi","messages":[{"role":"user","content":"hi"}],"stream":%t}`, tc.stream)))
				request.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(response, request)
				if status == http.StatusBadRequest {
					require.Equal(t, []string{"gpt-6-astra"}, upstream.models)
					require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
					return
				}
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				if status == http.StatusOK {
					require.Equal(t, []string{"gpt-6-astra"}, upstream.models)
					require.Empty(t, response.Header().Get("X-Sub2api-Fallback-Model"))
					return
				}
				require.Equal(t, "gpt-5.5", response.Header().Get("X-Sub2api-Fallback-Model"))
				require.Equal(t, "gpt-6-astra", response.Header().Get("X-Sub2api-Requested-Model"))
				require.Contains(t, response.Body.String(), "gpt-5.5")
				if status == 0 {
					require.Equal(t, []string{"gpt-5.5"}, upstream.models)
				} else {
					require.Equal(t, []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.5"}, upstream.models)
				}
			})
		}
	}
}

func TestGPTFallbackGuards(t *testing.T) {
	for _, model := range []string{"gpt-image-2", "gpt-5.5", "agnes-2.0-flash", "gpt-6-unknown"} {
		require.Empty(t, nextGPTFallbackModel(model))
	}
	h := &OpenAIGatewayHandler{}
	key := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/responses", nil)
	_, ok := h.nextGPTFallback(c, key, "gpt-6-astra", []byte(`{"previous_response_id":"resp_previous"}`), false)
	require.False(t, ok)
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	_, ok = h.nextGPTFallback(c, key, "gpt-6-astra", []byte(`{}`), false)
	require.False(t, ok)
}
