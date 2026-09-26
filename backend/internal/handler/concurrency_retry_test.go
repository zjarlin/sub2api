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
)

const capacityBody = `{"error":{"code":"TOO_MANY_REQUESTS","type":"billing_error","message":"In-flight request cap reached (1). Retry shortly."}}`

type capacityUpstream struct {
	service.HTTPUpstream
	ids []int64
}

func (u *capacityUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	u.ids = append(u.ids, id)
	if len(u.ids) == 1 {
		return &http.Response{StatusCode: 429, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(capacityBody))}, nil
	}
	return (&fallbackTestUpstream{}).Do(req, "", id, 1)
}

func TestUpstreamConcurrencyHTTPRecovery(t *testing.T) {
	for _, endpoint := range []string{"responses", "chat/completions"} {
		for _, count := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/%d_accounts", endpoint, count), func(t *testing.T) {
				repo := &grokCredentialHandlerRepo{}
				for id := 1; id <= count; id++ {
					repo.accounts = append(repo.accounts, service.Account{ID: int64(id), Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
						Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: id,
						Credentials: map[string]any{"api_key": "test-only", "base_url": "https://upstream.example", "model_mapping": map[string]any{"deepseek-v4.1-flash": "deepseek-v4.1-flash"}},
						Extra:       map[string]any{"openai_passthrough": true}})
				}
				cfg := &config.Config{RunMode: config.RunModeSimple}
				cfg.Gateway.MaxAccountSwitches = 10
				billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				defer billing.Stop()
				upstream := &capacityUpstream{}
				gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
				cache := &concurrencyCacheMock{acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }, acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }}
				h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(cache), billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
				key := &service.APIKey{ID: 2, User: &service.User{ID: 3, Status: service.StatusActive}, Group: &service.Group{Platform: service.PlatformOpenAI, Status: service.StatusActive}}
				router := gin.New()
				router.Use(func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyAPIKey), key)
					c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 3, Concurrency: 1})
				})
				router.POST("/responses", h.Responses)
				router.POST("/chat/completions", h.ChatCompletions)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest("POST", "/"+endpoint, strings.NewReader(`{"model":"deepseek-v4.1-flash","input":"hi","messages":[{"role":"user","content":"hi"}],"stream":false}`)))
				require.Equal(t, 200, response.Code, response.Body.String())
				require.Equal(t, []int64{1, int64(count)}, upstream.ids)
				require.Empty(t, repo.setErrorIDs)
				require.Empty(t, repo.setTempIDs)
			})
		}
	}
}

func TestUpstreamConcurrencyRetryBoundaries(t *testing.T) {
	r := concurrencyRetry{}
	err := &service.UpstreamFailoverError{StatusCode: 429, ResponseBody: []byte(capacityBody)}
	r.record(1, err)
	excluded := map[int64]struct{}{1: {}, 2: {}}
	for i := 0; i < 3; i++ {
		require.True(t, r.retry(context.Background(), excluded))
		require.NotContains(t, excluded, int64(1))
		require.Contains(t, excluded, int64(2))
		excluded[1] = struct{}{}
	}
	require.False(t, r.retry(context.Background(), excluded))
	r = concurrencyRetry{}
	r.record(1, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, r.retry(ctx, excluded))
	require.Contains(t, excluded, int64(1))
	r.record(1, &service.UpstreamFailoverError{StatusCode: 401})
	require.False(t, r.retry(context.Background(), excluded), "later credential failures must not be reopened")
}

func TestLocalCapacityRetryAndAccountSwitchBudget(t *testing.T) {
	retry := concurrencyRetry{}
	retry.record(832, openAILocalCapacityFailover())
	excluded := map[int64]struct{}{832: {}, 837: {}}
	require.True(t, retry.retry(context.Background(), excluded))
	require.NotContains(t, excluded, int64(832))
	require.Contains(t, excluded, int64(837), "RPM 限流账号不能因本地并发恢复而重新放行")

	account := &service.Account{Platform: service.PlatformOpenAI}
	budget := openAIAccountSwitchBudget{limit: 1}
	for range 10 {
		require.False(t, budget.exhausted(account, &service.UpstreamFailoverError{StatusCode: 429}))
	}
	require.False(t, budget.exhausted(account, &service.UpstreamFailoverError{StatusCode: 502}))
	require.True(t, budget.exhausted(account, &service.UpstreamFailoverError{StatusCode: 502}))
	require.False(t, tryRemainingOpenAIAccounts(account, &service.UpstreamFailoverError{StatusCode: 429, NextAccountAction: service.NextAccountStop}))
	require.False(t, tryRemainingOpenAIAccounts(account, &service.UpstreamFailoverError{StatusCode: 429, RequestScopedTransient: true}))
	require.False(t, tryRemainingOpenAIAccounts(&service.Account{Platform: service.PlatformGrok}, &service.UpstreamFailoverError{StatusCode: 429}))
	require.False(t, tryRemainingOpenAIAccounts(&service.Account{Platform: service.PlatformAnthropic}, &service.UpstreamFailoverError{StatusCode: 429}))
	workbuddy := &service.Account{Platform: service.PlatformWorkbuddy}
	require.True(t, tryRemainingOpenAIAccounts(workbuddy, &service.UpstreamFailoverError{StatusCode: 429}),
		"OpenAI-compatible accounts in an OpenAI group must continue account failover on 429")
}

func TestMixedSchedulingAccount429ContinuesFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(6)
	repo := &grokCredentialHandlerRepo{accounts: []service.Account{
		{
			ID: 844, Name: "workbuddy-limited", Platform: service.PlatformWorkbuddy, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
			Credentials:   map[string]any{"api_key": "first", "base_url": "https://first.example", "model_mapping": map[string]any{"deepseek-v4.1-flash": "cn:deepseek-v4.1-flash"}},
			AccountGroups: []service.AccountGroup{{GroupID: groupID}},
		},
		{
			ID: 849, Name: "zcode-healthy", Platform: service.PlatformZcode, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 2,
			Credentials:   map[string]any{"api_key": "second", "base_url": "https://second.example", "model_mapping": map[string]any{"deepseek-v4.1-flash": "cn:deepseek-v4.1-flash"}},
			AccountGroups: []service.AccountGroup{{GroupID: groupID}},
		},
	}}
	upstream := &grokCredentialHandlerUpstream{rateLimitIDs: map[int64]bool{844: true}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Gateway.MaxAccountSwitches = 1
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	defer billing.Stop()
	gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	cache := &concurrencyCacheMock{
		acquireUserSlotFn:    func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	}
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(cache), billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
	key := &service.APIKey{
		ID: 1, GroupID: &groupID,
		User:  &service.User{ID: 4, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), key)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.User.ID, Concurrency: 1})
	})
	router.POST("/responses", h.Responses)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/responses", strings.NewReader(`{"model":"deepseek-v4.1-flash","input":"hi","stream":false}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, []int64{844, 849}, upstream.accountHits())
}
