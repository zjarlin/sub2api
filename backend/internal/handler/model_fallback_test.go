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
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type fallbackTestUpstream struct {
	service.HTTPUpstream
	models        []string
	bodies        [][]byte
	status        int
	errorResponse string
}

func (u *fallbackTestUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	u.bodies = append(u.bodies, body)
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
		if u.errorResponse != "" {
			response = u.errorResponse
		}
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
				policy := &service.ModelFallbackPolicy{Enabled: true, Tiers: []service.ModelCapabilityTier{
					{Name: "top", Models: []string{"gpt-6-astra"}},
					{Name: "middle", Models: []string{"gpt-5.6-sol"}},
					{Name: "base", Models: []string{"gpt-5.5"}},
				}}
				data, err := json.Marshal(policy)
				require.NoError(t, err)
				settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{service.SettingKeyModelFallbackPolicy: string(data)}}, cfg)
				gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
					service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil)
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
		require.Empty(t, service.DefaultModelFallbackPolicy().Candidates(model))
	}
	h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}
	key := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/responses", nil)
	_, ok := h.nextModelFallback(c, key, "gpt-6-astra", []byte(`{"previous_response_id":"resp_previous"}`), false)
	require.False(t, ok)
	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	_, ok = h.nextModelFallback(c, key, "gpt-6-astra", []byte(`{}`), false)
	require.False(t, ok)
}

func TestFallbackToolsReplayableClientTools(t *testing.T) {
	for _, body := range []string{
		`[{"type":"custom","name":"apply_patch","format":{"type":"grammar","syntax":"lark","definition":"start: /.+/"}}]`,
		`[{"type":"namespace","name":"functions","tools":[{"type":"function","name":"exec"},{"type":"custom","name":"apply_patch"}]}]`,
		`[{"type":"tool_search","execution":"client"}]`,
		`[{"type":"web_search"}]`,
		`[{"type":"web_search_preview_2025_03_11"}]`,
		`[{"type":"namespace","name":"search","tools":[{"type":"web_search_preview"}]}]`,
	} {
		require.True(t, fallbackToolsReplayable(gjson.Parse(body)))
	}
	for _, body := range []string{
		`[{"type":"file_search","vector_store_ids":["vs_private"]}]`,
		`[{"type":"namespace","name":"hosted","tools":[{"type":"code_interpreter"}]}]`,
		`[{"type":"custom","name":"apply_patch"},{"type":"file_search"}]`,
		`[{"type":"tool_search","execution":"server"}]`,
		`[{"type":"tool_search"}]`,
	} {
		require.False(t, fallbackToolsReplayable(gjson.Parse(body)))
	}
}

func TestModelFallbackBlockedReasonPreservesUpstreamError(t *testing.T) {
	for _, tc := range []struct{ body, reason string }{
		{`{"previous_response_id":"private-response-id"}`, "previous_response_id"},
		{`{"conversation":"private-conversation-id"}`, "conversation"},
		{`{"tools":[{"type":"file_search","vector_store_ids":["vs_private"]}]}`, "hosted_tools"},
		{`{"input":[{"type":"web_search_call","id":"private-item-id","status":"completed"}],"tools":[{"type":"web_search"}]}`, "hosted_tool_state"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/responses", nil)
			service.SetOpsUpstreamError(c, 429, "upstream busy", "original detail")
			h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}
			key := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}}
			_, ok := h.nextModelFallback(c, key, "glm-5.3", []byte(tc.body), false)
			require.False(t, ok)
			require.Equal(t, 429, c.GetInt(service.OpsUpstreamStatusCodeKey))
			require.Equal(t, "upstream busy", c.GetString(service.OpsUpstreamErrorMessageKey))
			events := c.MustGet(service.OpsUpstreamErrorsKey).([]*service.OpsUpstreamErrorEvent)
			require.Len(t, events, 1)
			require.Equal(t, tc.reason, events[0].Reason)
			encoded, err := json.Marshal(events)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "private-")
		})
	}
}

func TestModelFallbackReplayAllowsEncryptedReasoning(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/responses", nil)
	key := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}}
	body := []byte(`{"input":[{"type":"reasoning","encrypted_content":"private-ciphertext"},{"type":"message","role":"user","content":"continue"}]}`)
	require.Empty(t, modelFallbackReplayBlockReason(c, key, "glm-5.3", body))
}

func TestModelFallbackReplayDistinguishesDeclarationsFromHostedState(t *testing.T) {
	key := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}}
	for _, body := range []string{
		`{"conversation":null,"input":"continue","tools":[{"type":"web_search"}]}`,
		`{"input":[{"type":"reasoning","summary":[],"encrypted_content":null},{"type":"function_call","name":"shell","call_id":"call_1","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"ok"}],"tools":[{"type":"web_search_preview"},{"type":"function","name":"shell"}]}`,
		`{"input":[{"type":"function_call_output","call_id":"call_1","output":{"type":"web_search_call","business_data":true}}]}`,
		`{"input":[{"type":"additional_tools","tools":[{"type":"web_search_preview_2025_03_11"}]}]}`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/responses", nil)
		require.Empty(t, modelFallbackReplayBlockReason(c, key, "glm-5.3", []byte(body)), body)
	}
	for _, itemType := range []string{"web_search_call", "file_search_call", "code_interpreter_call", "image_generation_call", "mcp_call", "mcp_approval_response", "mcp_approval_request", "mcp_list_tools"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/responses", nil)
		body := []byte(fmt.Sprintf(`{"input":[{"type":%q,"id":"private-item-id"}],"tools":[{"type":"web_search"}]}`, itemType))
		require.Equal(t, "hosted_tool_state", modelFallbackReplayBlockReason(c, key, "glm-5.3", body))
	}
}

func TestModelFallbackAdditionalToolsReplayUsesEffectiveDeclarations(t *testing.T) {
	key := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}}
	for _, tools := range []string{
		`[{"type":"file_search","vector_store_ids":["vs_private"]}]`,
		`[{"type":"namespace","name":"hosted","tools":[{"type":"code_interpreter","container":"private"}]}]`,
		`[{"type":"web_search"},{"type":"mcp","server_url":"https://private.example"}]`,
	} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/responses", nil)
		body := []byte(fmt.Sprintf(`{"tools":[{"type":"web_search"}],"input":[{"type":"additional_tools","tools":[{"type":"function","name":"shell"}]},{"type":"additional_tools","tools":%s}]}`, tools))
		require.Equal(t, "hosted_tools", modelFallbackReplayBlockReason(c, key, "glm-5.3", body))
	}
}
