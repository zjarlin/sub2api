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

type exhaustionHTTPUpstream struct {
	service.HTTPUpstream
	attempts []string
	success  string
}

func (u *exhaustionHTTPUpstream) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	attempt := fmt.Sprintf("%d/%s", id, gjson.GetBytes(body, "model").String())
	u.attempts = append(u.attempts, attempt)
	req.Body = io.NopCloser(strings.NewReader(string(body)))
	upstream := &fallbackTestUpstream{}
	if attempt != u.success {
		upstream.status = http.StatusServiceUnavailable
	}
	return upstream.Do(req, proxy, id, concurrency)
}

// 真实 handler 链路覆盖同账号降级、并发溢出和明确模型请求，防止单测绕过中间件。
func TestExhaustionHTTPAccountModelsBeforeOrdinaryPool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"responses", "chat/completions", "messages"} {
		for _, stream := range []bool{false, true} {
			for _, scenario := range []struct {
				name, requested, success string
				busy                     bool
				want                     []string
			}{
				{"first", "auto", "1/glm-5.3", false, []string{"1/glm-5.3"}},
				{"sibling", "auto", "1/qwen3.8-max", false, []string{"1/glm-5.3", "1/qwen3.8-max"}},
				{"ordinary", "auto", "2/glm-5.3", false, []string{"1/glm-5.3", "1/qwen3.8-max", "2/glm-5.3"}},
				{"full", "auto", "2/glm-5.3", true, []string{"2/glm-5.3"}},
				{"explicit", "glm-5.3", "2/glm-5.3", false, []string{"1/glm-5.3", "2/glm-5.3"}},
			} {
				// Auto 目前只在 Responses 与 Chat Completions 入口提供。
				if endpoint == "messages" && scenario.requested == "auto" {
					continue
				}
				t.Run(fmt.Sprintf("%s/%t/%s", endpoint, stream, scenario.name), func(t *testing.T) {
					accounts := make([]service.Account, 2)
					for i := range accounts {
						accounts[i] = service.Account{ID: int64(i + 1), GroupIDs: []int64{71}, Platform: service.PlatformOpenAI,
							Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1 - i,
							Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{"glm-5.3": "glm-5.3", "qwen3.8-max": "qwen3.8-max"}},
							Extra:       map[string]any{"openai_passthrough": true}}
					}
					accounts[0].Extra[service.AccountExhaustionExtraKey] = service.AccountExhaustionPolicy{Enabled: true, Models: []string{"glm-5.3", "qwen3.8-max"}}
					cfg := &config.Config{RunMode: config.RunModeSimple}
					billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
					t.Cleanup(billing.Stop)
					acquired := []int64{}
					concurrency := service.NewConcurrencyService(&concurrencyCacheMock{
						acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
						acquireAccountSlotFn: func(_ context.Context, id int64, _ int, _ string) (bool, error) {
							acquired = append(acquired, id)
							return !(scenario.busy && id == 1), nil
						},
					})
					upstream := &exhaustionHTTPUpstream{success: scenario.success}
					gateway := service.NewOpenAIGatewayService(&grokCredentialHandlerRepo{accounts: accounts}, nil, nil, nil, nil, nil, nil, cfg, nil, concurrency,
						service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
					h := NewOpenAIGatewayHandler(gateway, concurrency, billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
					auto := newAutoModelTestHandler(accounts)
					group := &service.Group{ID: 71, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowMessagesDispatch: true}
					key := &service.APIKey{ID: 2, GroupID: &group.ID, Group: group, User: &service.User{ID: 3, Status: service.StatusActive}}
					router := gin.New()
					router.Use(func(c *gin.Context) {
						c.Set(string(middleware.ContextKeyAPIKey), key)
						c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
					}, auto.AutoModelMiddleware(nil))
					router.POST("/v1/responses", h.Responses)
					router.POST("/v1/chat/completions", h.ChatCompletions)
					router.POST("/v1/messages", h.Messages)
					body := fmt.Sprintf(`{"model":%q,"input":"hello","messages":[{"role":"user","content":"hello"}],"max_tokens":32,"stream":%t}`, scenario.requested, stream)
					recorder := httptest.NewRecorder()
					router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(body)))
					require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
					require.Equal(t, scenario.want, upstream.attempts)
					if scenario.busy {
						require.Equal(t, []int64{1, 2}, acquired, "满并发账号不按模型重复抢槽或等待")
					}
				})
			}
		}
	}
}

func TestExhaustionFallbackStopsAfterOutput(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":"hi"}`))
	c.Request = c.Request.WithContext(service.WithAccountExhaustionRoute(c.Request.Context(), 1, "glm"))
	c.Writer.Write([]byte("data: already-started\n\n"))
	h := &OpenAIGatewayHandler{gatewayService: &service.OpenAIGatewayService{}}
	group := &service.Group{ID: 71, Platform: service.PlatformOpenAI}
	seedAutoModelFallback(c, "glm", []autoModelRouteCandidate{{accountID: 1, model: "glm"}, {accountID: 1, model: "qwen"}})
	_, ok := h.nextModelFallback(c, &service.APIKey{Group: group, GroupID: &group.ID}, "glm", []byte(`{"input":"hi"}`), false)
	require.False(t, ok)
	require.Equal(t, int64(1), service.AccountExhaustionTarget(c.Request.Context()))
}

func TestExhaustionOwnedAccountCannotGrantPriority(t *testing.T) {
	input := map[string]any{service.AccountExhaustionExtraKey: service.AccountExhaustionPolicy{Enabled: true, Models: []string{"glm"}}}
	require.NotContains(t, ownedAccountExtra(input), service.AccountExhaustionExtraKey)
	require.Contains(t, input, service.AccountExhaustionExtraKey)
}
