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

type rejectedInputFailoverUpstream struct {
	service.HTTPUpstream
	ids    []int64
	models []string
	bodies [][]byte
}

func (u *rejectedInputFailoverUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.ids = append(u.ids, id)
	u.models = append(u.models, gjson.GetBytes(body, "model").String())
	u.bodies = append(u.bodies, body)
	req.Body = io.NopCloser(strings.NewReader(string(body)))
	status := http.StatusBadRequest
	response := `{"error":{"message":"Invalid input","param":"input","type":"invalid_request_error"}}`
	if id == 844 {
		status = http.StatusTooManyRequests
		response = `{"error":{"code":"rate_limit_exceeded","type":"api_error","message":"quota exhausted"}}`
	} else if gjson.GetBytes(body, "input.0.type").String() == "message" {
		return (&fallbackTestUpstream{}).Do(req, "", id, 1)
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(response))}, nil
}

// 首个账号额度耗尽后，备用透传账号的 input 兼容重试必须保留原任务并支持 SSE。
func TestResponsesInputCompatibilityAfterRateLimitFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_%t", stream), func(t *testing.T) {
			cfg := &config.Config{RunMode: config.RunModeSimple}
			repo := &grokCredentialHandlerRepo{}
			for i, model := range []string{"cn:deepseek-v4.1-flash", "deepseek/deepseek-v4.1-flash"} {
				repo.accounts = append(repo.accounts, service.Account{
					ID: []int64{844, 851}[i], Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
					Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: i + 1,
					Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{model: model}},
					Extra:       map[string]any{"openai_passthrough": true},
				})
			}
			repo.accounts[0].Platform = service.PlatformWorkbuddy
			repo.accounts[0].Extra = map[string]any{"openai_responses_supported": false}
			settings := service.NewSettingService(&contentModerationHandlerSettingRepo{values: map[string]string{
				service.SettingKeyModelAliases: `{"groups":[{"canonical":"deepseek-v4.1-flash","aliases":["cn:deepseek-v4.1-flash","deepseek/deepseek-v4.1-flash"]}]}`,
			}}, cfg)
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			upstream := &rejectedInputFailoverUpstream{}
			gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil)
			cache := &concurrencyCacheMock{
				acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
				acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
			}
			h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(cache), billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
			key := &service.APIKey{ID: 2, User: &service.User{ID: 3, Status: service.StatusActive}, Group: &service.Group{Platform: service.PlatformOpenAI, Status: service.StatusActive}}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyAPIKey), key)
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
			})
			router.Use(middleware.GlobalModelAliases(settings))
			router.POST("/v1/responses", h.Responses)
			body := fmt.Sprintf(`{"model":"deepseek-v4.1-flash","stream":%t,"input":[{"type":"agent_message","content":[{"type":"input_text","text":"Task:\n"},{"type":"encrypted_content","encrypted_content":"Reply OK."}]}]}`, stream)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), "ok")
			require.NotContains(t, response.Body.String(), "Invalid input")
			require.Equal(t, []int64{844, 851, 851}, upstream.ids)
			require.Equal(t, []string{"cn:deepseek-v4.1-flash", "deepseek/deepseek-v4.1-flash", "deepseek/deepseek-v4.1-flash"}, upstream.models)
			require.Equal(t, "agent_message", gjson.GetBytes(upstream.bodies[1], "input.0.type").String())
			require.Equal(t, "Task:\nReply OK.", gjson.GetBytes(upstream.bodies[2], "input.0.content.0.text").String())
			require.Empty(t, response.Header().Get("X-Sub2api-Fallback-Model"))
		})
	}
}
