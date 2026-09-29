//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type autoVisionAccountRepo struct {
	grokCredentialHandlerRepo
}

func (r *autoVisionAccountRepo) ListSchedulableByGroupID(ctx context.Context, _ int64) ([]service.Account, error) {
	return r.ListSchedulableByPlatform(ctx, service.PlatformOpenAI)
}

type autoVisionUpstream struct {
	service.HTTPUpstream
	t      *testing.T
	models []string
}

func (u *autoVisionUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	require.NoError(u.t, err)
	model := gjson.GetBytes(body, "model").String()
	u.models = append(u.models, model)
	if model == "vision-model" {
		require.Contains(u.t, string(body), "data:image/png;base64,AAAA")
	} else {
		require.NotContains(u.t, string(body), "data:image")
		require.Contains(u.t, string(body), "The image contains a red square.")
	}
	response := fmt.Sprintf(`{"id":"chatcmpl_vision","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"The image contains a red square."},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`, model)
	contentType := "application/json"
	if gjson.GetBytes(body, "stream").Bool() {
		contentType = "text/event-stream"
		response = fmt.Sprintf("data: {\"id\":\"chatcmpl_vision\",\"object\":\"chat.completion.chunk\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"The image contains a red square.\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chatcmpl_vision\",\"object\":\"chat.completion.chunk\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", model, model)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}

func TestAutoModelVisionHTTPUsesHelperBeforeTextModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		endpoint   string
		stream     bool
		toolOutput bool
	}{
		{endpoint: "/v1/responses"},
		{endpoint: "/v1/responses", stream: true},
		{endpoint: "/v1/responses", toolOutput: true, stream: true},
		{endpoint: "/v1/chat/completions"},
		{endpoint: "/v1/chat/completions", stream: true},
	} {
		t.Run(fmt.Sprintf("%s/stream=%t/tool=%t", tc.endpoint, tc.stream, tc.toolOutput), func(t *testing.T) {
			accounts := autoModelTestAccounts()[:1]
			primary := &accounts[0]
			primary.Type, primary.Concurrency = service.AccountTypeAPIKey, 1
			primary.Credentials = map[string]any{"api_key": "test-key", "base_url": "https://upstream.example", "model_mapping": map[string]any{"deepseek-v4.1-flash": "deepseek-v4.1-flash"}}
			primary.Extra = map[string]any{openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions)}
			primary.SetUpstreamModelMetadataSnapshot(service.UpstreamModelMetadataSnapshot{Models: map[string]service.UpstreamModelMetadata{
				"deepseek-v4.1-flash": {ID: "deepseek-v4.1-flash", InputModalities: []string{"text"}},
			}})
			helper := *primary
			helper.ID, helper.Extra = 2, map[string]any{openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions)}
			helper.Credentials = map[string]any{"api_key": "helper-key", "base_url": "https://upstream.example", "model_mapping": map[string]any{"vision-model": "vision-model"}}
			helper.SetUpstreamModelMetadataSnapshot(service.UpstreamModelMetadataSnapshot{Models: map[string]service.UpstreamModelMetadata{
				"vision-model": {ID: "vision-model", InputModalities: []string{"text", "image"}},
			}})
			accounts = append(accounts, helper)
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Gateway.VisionFallback.Enabled = true
			settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{}}, cfg)
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			defer billing.Stop()
			upstream := &autoVisionUpstream{t: t}
			repo := &autoVisionAccountRepo{grokCredentialHandlerRepo{accounts: accounts}}
			gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
				service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil)
			concurrency := service.NewConcurrencyService(&concurrencyCacheMock{
				acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
				acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
			})
			openAI := NewOpenAIGatewayHandler(gateway, concurrency, billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
			h := newAutoModelTestHandler(accounts)
			h.gatewayService = service.NewGatewayService(&autoModelAccountRepoStub{gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{71: accounts}}},
				nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, settings, nil, nil, nil, nil, nil, nil)
			group := &service.Group{ID: 71, Platform: service.PlatformOpenAI, Status: service.StatusActive}
			key := &service.APIKey{ID: 2, GroupID: &group.ID, Group: group, User: &service.User{ID: 3, Status: service.StatusActive}}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
			}, h.AutoModelMiddleware(nil))
			router.POST("/v1/responses", openAI.Responses)
			router.POST("/v1/chat/completions", openAI.ChatCompletions)
			payload := map[string]any{"model": "auto", "stream": tc.stream}
			if tc.toolOutput {
				payload["input"] = []any{
					map[string]any{"type": "function_call", "name": "view_image", "call_id": "c1", "arguments": "{}"},
					map[string]any{"type": "function_call_output", "call_id": "c1", "output": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AAAA"}}},
				}
			} else if tc.endpoint == "/v1/responses" {
				payload["input"] = []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64,AAAA"}}}}
			} else {
				payload["messages"] = []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAAA"}}}}}
			}
			body, err := json.Marshal(payload)
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, tc.endpoint, strings.NewReader(string(body))))
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Equal(t, []string{"vision-model", "deepseek-v4.1-flash"}, upstream.models)
			require.Equal(t, "deepseek-v4.1-flash", recorder.Header().Get("X-Sub2API-Selected-Model"))
			require.Contains(t, recorder.Body.String(), "The image contains a red square.")
		})
	}
}
